package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"

	"github.com/hobbyquaker/occulited/internal/addonimage"
)

// The catalogue's addon images (openccu-lite task 100). An entry's manifest declares its icon and
// logo as paths relative to the package root; for the Addons page before an install the check
// fetches them from the addon's repository at the tag the manifest was read at (or the default
// branch, where the manifest came from there), beside the manifest - its directory in the
// repository is the package root there (addon_files/openccu-lite.json declaring
// mosquitto/www/icon.svg: addon_files/mosquitto/www/icon.svg) - into ImagesDir under their content
// hash, with the same rules as the installed ones: at most addonimage.MaxSize bytes, and the type by
// content. A file that is not an image is not kept. Nothing is fetched outside the user's check
// (D-90, B-240): the page and the image routes answer from the files. An adapter manifest (one the
// catalogue carries for a third-party addon) declares images of a package whose repository does not
// carry them beside the manifest, so no image is fetched for it.

var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// fetchImages is the entry's images after this check: the ones the manifest declares, kept where
// the file of the previous check is still right (same path, same tag, still on disk), fetched where
// not. c is this check's result so far (its Manifest and Tag), prev the previous check's entry.
func (s *Service) fetchImages(ctx context.Context, e Entry, c cached, prev cached) map[string]string {
	if s.ImagesDir == "" || c.Manifest == nil || e.Adapter() {
		return nil
	}
	var out map[string]string
	keep := func(kind, hash string) {
		if out == nil {
			out = map[string]string{}
		}
		out[kind] = hash
	}
	for _, kind := range addonimage.Kinds {
		p := addonimage.Declared(c.Manifest.UI, kind)
		if p == "" {
			continue
		}
		if h, ok := prev.Images[kind]; ok && prev.Manifest != nil && prev.Tag == c.Tag && addonimage.Declared(prev.Manifest.UI, kind) == p && s.imageOnDisk(h) {
			keep(kind, h)
			continue
		}
		if ctx.Err() != nil {
			return out
		}
		var b []byte
		var err error
		for _, u := range rawURLs(s.RawGitHub, e.Git, path.Join(path.Dir(e.Manifest), p), c.Tag) {
			b, _, err = s.getETag(ctx, u, "", addonimage.MaxSize)
			if err == nil {
				break
			}
		}
		if err != nil {
			slog.Warn("catalog: an addon image could not be fetched", "git", e.Git, "kind", kind, "path", p, "err", err)
			continue
		}
		if _, ok := addonimage.Sniff(b); !ok {
			slog.Warn("catalog: an addon image is not an image and was not kept", "git", e.Git, "kind", kind, "path", p)
			continue
		}
		h, err := s.storeImage(b)
		if err != nil {
			slog.Warn("catalog: an addon image could not be stored", "git", e.Git, "kind", kind, "err", err)
			continue
		}
		keep(kind, h)
	}
	return out
}

// imageOnDisk says whether the file of a hash is in ImagesDir.
func (s *Service) imageOnDisk(hash string) bool {
	if !hashRe.MatchString(hash) {
		return false
	}
	st, err := os.Stat(filepath.Join(s.ImagesDir, hash))
	return err == nil && st.Mode().IsRegular()
}

// storeImage writes an image under its content hash and answers the hash.
func (s *Service) storeImage(b []byte) (string, error) {
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(s.ImagesDir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(s.ImagesDir, h)
	if s.imageOnDisk(h) {
		return h, nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return h, nil
}

// pruneImagesLocked removes the files of ImagesDir no cached entry names any more. s.mu held.
func (s *Service) pruneImagesLocked() {
	if s.ImagesDir == "" {
		return
	}
	used := map[string]bool{}
	for _, c := range s.cache.Entries {
		for _, h := range c.Images {
			used[h] = true
		}
	}
	entries, err := os.ReadDir(s.ImagesDir)
	if err != nil {
		return
	}
	for _, en := range entries {
		if name := en.Name(); !used[name] && (hashRe.MatchString(name) || filepath.Ext(name) == ".tmp") {
			_ = os.Remove(filepath.Join(s.ImagesDir, name))
		}
	}
}

// Image is a catalogue entry's image of a kind, as the check fetched it: the bytes, which the
// caller types by content (addonimage.Sniff). fs.ErrNotExist when the catalogue knows no such
// addon, the manifest declares no such kind, or the check could not take it.
func (s *Service) Image(id, kind string) ([]byte, error) {
	s.mu.Lock()
	s.loadLocked()
	var hash string
	for _, c := range s.cache.Entries {
		if c.Manifest != nil && c.Manifest.ID == id {
			hash = c.Images[kind]
			break
		}
	}
	dir := s.ImagesDir
	s.mu.Unlock()
	if hash == "" || dir == "" || !hashRe.MatchString(hash) {
		return nil, fmt.Errorf("%s %s: %w", id, kind, fs.ErrNotExist)
	}
	b, err := os.ReadFile(filepath.Join(dir, hash))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%s %s: %w", id, kind, fs.ErrNotExist)
		}
		return nil, err
	}
	if len(b) > addonimage.MaxSize {
		return nil, fmt.Errorf("%s %s: larger than %d bytes", id, kind, addonimage.MaxSize)
	}
	return b, nil
}
