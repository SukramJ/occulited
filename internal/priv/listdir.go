package priv

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// openccu-lite B-253: hmipserver's data directory (/etc/config/crRFD/data, its per-device files and
// the access point's identity and key-exchange files) is the daemon's alone - 0700, its files
// 0600, like rfd's AES device files. occulited needs two things from it and reads neither as its
// own user any more: the names in it (which SGTINs have a .dev file, which modules an .ap file -
// accesspoints.go, hmipexchange.go), and, for the local key mode's snapshot, the module's
// identity files themselves (hmiplocalkey.go). The names come through this operation, which lists
// exactly a directory of Policy.ListDirs and answers names and nothing else; the identity files
// come through "read", admitted by the shapes of Policy.ReadGlobs. Nothing else in that directory
// - the .dev files, the link and meta data - is readable to the daemon.

// opListDir is the directory listing's operation: a directory and nothing else.
const opListDir = "listdir"

// The listing's bound: a directory with more entries than this is answered in part.
const listDirMaxEntries = 10000

// ListDir asks the helper for the names in dir.
func (c Client) ListDir(dir string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := c.call(ctx, request{Op: opListDir, Path: dir})
	if err != nil {
		return nil, err
	}
	return res.Names, nil
}

// ListDir answers the names of the entries in dir, sorted as the directory read gives them, at most
// listDirMaxEntries; dir itself is not followed when it is a link.
func (Local) ListDir(dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if len(out) >= listDirMaxEntries {
			break
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// listDir is the directory listing at the boundary: a directory of Policy.ListDirs, as it is named
// and as its links resolve, and nothing else in the request.
func (s *Server) listDir(req request) response {
	if req.Name != "" || len(req.Args) > 0 || len(req.Data) > 0 || len(req.Stdin) > 0 || len(req.Env) > 0 || req.Dir != "" || req.Src != "" || req.Dst != "" || req.Target != "" || req.Hash != "" || req.AllFiles {
		s.log("helper: refused a directory listing with more than a directory: %s", req.Path)
		return refuse("listdir takes a directory and nothing else")
	}
	if !s.Policy.listDirAllowed(req.Path) {
		s.log("helper: refused the directory listing of %s", req.Path)
		return refuse("directory listing " + req.Path)
	}
	names, err := s.ops().ListDir(req.Path)
	if err != nil {
		return response{Error: err.Error()}
	}
	return response{OK: true, Names: names}
}

// listDirNamed: dir, as given, is one of ListDirs exactly (no path below one, no prefix).
func (p Policy) listDirNamed(dir string) bool {
	rel, ok := p.rel(dir)
	if !ok || strings.Contains(rel, "..") {
		return false
	}
	for _, d := range p.ListDirs {
		if rel == strings.TrimSuffix(d, "/") {
			return true
		}
	}
	return false
}

// listDirAllowed is listDirNamed for the directory as named and as resolved: on the image
// /etc/config is a link to the userfs, so both spellings are on the list.
func (p Policy) listDirAllowed(dir string) bool {
	if !p.listDirNamed(dir) {
		return false
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return false
	}
	resolved := p
	resolved.Root = root
	return resolved.listDirNamed(real)
}
