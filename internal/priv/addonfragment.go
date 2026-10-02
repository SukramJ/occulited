package priv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/addonunit"
)

// occulited B-35: an addon ships its lighttpd fragment as <tree>/etc/lighttpd.conf, and the drop-in
// sync (internal/system/lighttpdropin.go) holds it to the directive allowlist and writes a root copy
// lighttpd reads. Since openccu-lite B-252 a confined addon's files are its own, 0640, and its
// directories 0751: occulited's user can neither open the etc directory nor read the file, so the
// sync after an install refused every fragment as "cannot be reached" and a changed one from an
// update was never taken. The fragment is read through this operation instead: exactly that one
// path of an addon id's shape, opened through os.Root on the addon's tree (a link - the file or a
// directory on the way - that leads out of the tree is refused, never followed), a regular file of
// at most AddonFragmentMax bytes, answered as it is. Nothing else of the tree is readable to the
// daemon through it.

const (
	opAddonFragment = "addon-fragment"
	// AddonFragmentRel is where an addon ships its lighttpd fragment, relative to its tree.
	AddonFragmentRel = "etc/lighttpd.conf"
	// AddonFragmentMax bounds a fragment; a frontend's few blocks are a page or two.
	AddonFragmentMax = 64 << 10
)

// The ways a fragment cannot be taken; a missing one is fs.ErrNotExist.
var (
	ErrFragmentUnreachable = errors.New("the fragment cannot be reached inside the addon's own directory")
	ErrFragmentLink        = errors.New("the fragment is a link that does not lead into the addon's own directory, or cannot be read")
	ErrFragmentNotRegular  = errors.New("the fragment is not a regular file")
	ErrFragmentTooLarge    = fmt.Errorf("the fragment is larger than %d bytes", AddonFragmentMax)
)

// fragmentErrnos name the errors on the wire (response.Errno).
var fragmentErrnos = map[string]error{
	"ENOENT":      fs.ErrNotExist,
	"unreachable": ErrFragmentUnreachable,
	"link":        ErrFragmentLink,
	"notregular":  ErrFragmentNotRegular,
	"toolarge":    ErrFragmentTooLarge,
}

// ReadAddonFragment asks the helper for an addon's lighttpd fragment.
func (c Client) ReadAddonFragment(path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := c.call(ctx, request{Op: opAddonFragment, Path: path})
	if res.Errno != "" {
		if e, ok := fragmentErrnos[res.Errno]; ok {
			return nil, fmt.Errorf("%s: %w", path, e)
		}
	}
	if err != nil {
		return nil, err
	}
	return res.Stdout, nil
}

// ReadAddonFragment reads path, which must be <tree>/etc/lighttpd.conf, through os.Root on <tree>.
// A permission error is returned as it is (fs.ErrPermission), so the daemon knows to ask the helper.
func (Local) ReadAddonFragment(path string) ([]byte, error) {
	tree, ok := strings.CutSuffix(path, "/"+AddonFragmentRel)
	if !ok || tree == "" {
		return nil, fmt.Errorf("%s: not an addon's lighttpd fragment", path)
	}
	r, err := os.OpenRoot(tree)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist) // no tree, no fragment
	}
	defer r.Close()
	if _, err := r.Lstat(AddonFragmentRel); err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%s: %w", path, fs.ErrNotExist)
		case errors.Is(err, fs.ErrPermission):
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", path, ErrFragmentUnreachable)
	}
	f, err := r.Open(AddonFragmentRel)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%s: %w", path, ErrFragmentLink)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrFragmentNotRegular)
	}
	if st.Size() > AddonFragmentMax {
		return nil, fmt.Errorf("%s: %w", path, ErrFragmentTooLarge)
	}
	raw, err := io.ReadAll(io.LimitReader(f, AddonFragmentMax+1))
	if err != nil {
		return nil, fmt.Errorf("the fragment cannot be read: %w", err)
	}
	if len(raw) > AddonFragmentMax {
		return nil, fmt.Errorf("%s: %w", path, ErrFragmentTooLarge)
	}
	return raw, nil
}

// addonFragment is the fragment read at the boundary: one path of the fragment's shape and nothing
// else in the request.
func (s *Server) addonFragment(req request) response {
	if req.Name != "" || len(req.Args) > 0 || len(req.Data) > 0 || len(req.Stdin) > 0 || len(req.Env) > 0 || req.Dir != "" || req.Src != "" || req.Dst != "" || req.Target != "" || req.Hash != "" || req.WWW != "" || req.Recursive {
		s.log("helper: refused an addon fragment read with more than a path: %s", req.Path)
		return refuse("addon-fragment takes a path and nothing else")
	}
	if !s.Policy.addonFragmentAllowed(req.Path) {
		s.log("helper: refused the addon fragment read %s", req.Path)
		return refuse("addon fragment " + req.Path)
	}
	raw, err := s.ops().ReadAddonFragment(filepath.Clean(req.Path))
	if err != nil {
		for code, e := range fragmentErrnos {
			if errors.Is(err, e) {
				return response{Error: err.Error(), Errno: code}
			}
		}
		return response{Error: err.Error()}
	}
	return response{OK: true, Stdout: raw}
}

// addonFragmentAllowed: the path is /usr/local/addons/<id>/etc/lighttpd.conf with an addon id's
// shape, nothing more and nothing less.
func (p Policy) addonFragmentAllowed(path string) bool {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") {
		return false
	}
	id, ok := strings.CutPrefix(rel, AddonHomeDir)
	if !ok {
		return false
	}
	id, ok = strings.CutSuffix(id, "/"+AddonFragmentRel)
	return ok && addonunit.IDRe.MatchString(id)
}
