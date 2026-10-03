package priv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/addonimage"
	"github.com/hobbyquaker/occulited/internal/addonunit"
	"github.com/hobbyquaker/occulited/internal/manifest"
)

// openccu-lite task 100: an installed addon's icon and logo come out of its tree, at the paths its
// stored manifest declares (ui.icon, ui.icon_dark, ui.logo, ui.logo_dark). A confined addon's tree
// is closed to the daemon (openccu-lite B-252), so the read goes through this operation - and the
// operation is as narrow as the fragment read (addonfragment.go): the helper takes the stored
// manifest's path and the tree's path, both of an addon id's shape and of the same id, and a kind;
// it reads the manifest itself (root-owned, written by the install), resolves the kind to the one
// path the manifest declares for it, opens that path through os.Root on the tree (a link out of
// the tree is refused, never followed), takes a regular file of at most addonimage.MaxSize bytes,
// and answers it only when its content is an image (addonimage.Sniff). Nothing else of the tree
// comes out: not a file the manifest does not name, and not a file that is no image however it is
// named - an addon's token file, say, is text and stays where it is.

const opAddonImage = "addon-image"

// AddonPolicyDir is where the addon policies and the stored manifests live, as the box spells it;
// system.AddonPolicyDir is this constant.
const AddonPolicyDir = "/usr/local/etc/config/addon-policy"

// AddonManifestSuffix names the stored manifest beside the policy, <id>.manifest.json.
const AddonManifestSuffix = ".manifest.json"

// The ways an image cannot be taken; an undeclared kind, a missing manifest or a missing file is
// fs.ErrNotExist.
var (
	ErrImageUnreachable = errors.New("the image cannot be reached inside the addon's own directory")
	ErrImageLink        = errors.New("the image is a link that does not lead into the addon's own directory, or cannot be read")
	ErrImageNotRegular  = errors.New("the image is not a regular file")
	ErrImageTooLarge    = fmt.Errorf("the image is larger than %d bytes", addonimage.MaxSize)
	ErrImageNotImage    = errors.New("the file is not an image (SVG, PNG, JPEG, GIF or WebP)")
)

// imageErrnos name the errors on the wire (response.Errno).
var imageErrnos = map[string]error{
	"ENOENT":      fs.ErrNotExist,
	"unreachable": ErrImageUnreachable,
	"link":        ErrImageLink,
	"notregular":  ErrImageNotRegular,
	"toolarge":    ErrImageTooLarge,
	"notimage":    ErrImageNotImage,
}

// ReadAddonImage asks the helper for one of an addon's declared images: manifestPath is the stored
// manifest, tree the addon's directory, kind one of addonimage.Kinds.
func (c Client) ReadAddonImage(manifestPath, tree, kind string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.call(ctx, request{Op: opAddonImage, Path: manifestPath, Dir: tree, Name: kind})
	if res.Errno != "" {
		if e, ok := imageErrnos[res.Errno]; ok {
			return nil, fmt.Errorf("%s %s: %w", tree, kind, e)
		}
	}
	if err != nil {
		return nil, err
	}
	return res.Stdout, nil
}

// ReadAddonImage reads the image the stored manifest at manifestPath declares for kind out of
// tree, through os.Root on tree. A permission error is returned as it is (fs.ErrPermission), so the
// daemon knows to ask the helper.
func (Local) ReadAddonImage(manifestPath, tree, kind string) ([]byte, error) {
	if !slices.Contains(addonimage.Kinds, kind) {
		return nil, fmt.Errorf("%s: %w", kind, fs.ErrNotExist)
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", manifestPath, fs.ErrNotExist)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", manifestPath, fs.ErrNotExist)
	}
	if m.ID != filepath.Base(tree) {
		return nil, fmt.Errorf("%s names %s, not %s: %w", manifestPath, m.ID, filepath.Base(tree), fs.ErrNotExist)
	}
	rel := addonimage.TreeRel(addonimage.Declared(m.UI, kind))
	if rel == "" {
		return nil, fmt.Errorf("%s %s: %w", tree, kind, fs.ErrNotExist)
	}
	r, err := os.OpenRoot(tree)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", tree, fs.ErrNotExist) // no tree, no image
	}
	defer r.Close()
	if _, err := r.Lstat(rel); err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s/%s: %w", tree, rel, fs.ErrNotExist)
		case errors.Is(err, fs.ErrPermission):
			return nil, err
		}
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageUnreachable)
	}
	f, err := r.Open(rel)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageLink)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageNotRegular)
	}
	if st.Size() > addonimage.MaxSize {
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageTooLarge)
	}
	b, err := io.ReadAll(io.LimitReader(f, addonimage.MaxSize+1))
	if err != nil {
		return nil, fmt.Errorf("the image cannot be read: %w", err)
	}
	if len(b) > addonimage.MaxSize {
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageTooLarge)
	}
	if _, ok := addonimage.Sniff(b); !ok {
		return nil, fmt.Errorf("%s/%s: %w", tree, rel, ErrImageNotImage)
	}
	return b, nil
}

// addonImage is the image read at the boundary: the manifest's path, the tree's path and the kind,
// nothing else in the request.
func (s *Server) addonImage(req request) response {
	if len(req.Args) > 0 || len(req.Data) > 0 || len(req.Stdin) > 0 || len(req.Env) > 0 || req.Src != "" || req.Dst != "" || req.Target != "" || req.Hash != "" || req.WWW != "" || req.Recursive {
		s.log("helper: refused an addon image read with more than its three fields: %s", req.Dir)
		return refuse("addon-image takes the manifest, the tree and the kind and nothing else")
	}
	if !s.Policy.addonImageAllowed(req.Path, req.Dir, req.Name) {
		s.log("helper: refused the addon image read %s %s %s", req.Path, req.Dir, req.Name)
		return refuse("addon image " + req.Dir + " " + req.Name)
	}
	raw, err := s.ops().ReadAddonImage(filepath.Clean(req.Path), filepath.Clean(req.Dir), req.Name)
	if err != nil {
		for code, e := range imageErrnos {
			if errors.Is(err, e) {
				return response{Error: err.Error(), Errno: code}
			}
		}
		return response{Error: err.Error()}
	}
	return response{OK: true, Stdout: raw}
}

// addonImageAllowed: the manifest is <AddonPolicyDir>/<id>.manifest.json, the tree
// /usr/local/addons/<id>, both with the same id of an addon id's shape, and the kind is one of the
// four.
func (p Policy) addonImageAllowed(manifestPath, tree, kind string) bool {
	if !slices.Contains(addonimage.Kinds, kind) {
		return false
	}
	mrel, ok := p.rel(manifestPath)
	if !ok || strings.Contains(mrel, "..") {
		return false
	}
	trel, ok := p.rel(tree)
	if !ok || strings.Contains(trel, "..") {
		return false
	}
	id, ok := strings.CutPrefix(trel, AddonHomeDir)
	if !ok || !addonunit.IDRe.MatchString(id) {
		return false
	}
	return mrel == AddonPolicyDir+"/"+id+AddonManifestSuffix
}
