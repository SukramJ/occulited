package priv

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// B-113: the storage hint (task 84) walks /var/log, /run and /usr/local as the occulite user and
// could not see into a directory only its owner reads: RedMatic's npm debug logs are in
// var/npm-cache/_logs, drwx------ addon-redmatic. The helper lists such a directory - the paths,
// sizes and modification times of the log files in it, never a byte of them - and only under
// Policy.LogListDirs.

// opListLogs is the log listing's operation: a directory and the every-file flag, nothing else.
const opListLogs = "listlogs"

// LogFileInfo is one file the log listing found.
type LogFileInfo struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mtime"`
}

// The listing's bounds: the files one answer holds, the entries one walk visits, the levels below
// the directory.
const (
	logListMaxFiles   = 500
	logListMaxEntries = 50000
	logListDepth      = 8
)

// LogFileName is what counts as a log file by its name - *.log, *.log.N and *.log.NN, the rule of
// the fork's lite-log-inventory.sh. The storage hint's scan uses the same function.
func LogFileName(name string) bool {
	if strings.HasSuffix(name, ".log") {
		return true
	}
	i := strings.LastIndex(name, ".log.")
	if i < 0 {
		return false
	}
	n := name[i+len(".log."):]
	if len(n) < 1 || len(n) > 2 {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// ListLogFiles asks the helper for the log files under dir.
func (c Client) ListLogFiles(dir string, allFiles bool) ([]LogFileInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := c.call(ctx, request{Op: opListLogs, Path: dir, AllFiles: allFiles})
	return res.Files, err
}

// ListLogFiles walks dir without following a symlink - neither a linked directory nor a linked
// file is a regular file of the walk - and answers the regular files named like logs (every one
// with allFiles): path, length, modification time. node_modules and npm's _cacache are left out, as
// the storage scan leaves them out.
func (Local) ListLogFiles(dir string, allFiles bool) ([]LogFileInfo, error) {
	dir = filepath.Clean(dir)
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	out := []LogFileInfo{}
	visited := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if visited++; visited > logListMaxEntries || len(out) >= logListMaxFiles {
			return filepath.SkipAll
		}
		if err != nil {
			if d != nil && d.IsDir() && p != dir {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if p != dir && (d.Name() == "node_modules" || d.Name() == "_cacache" || strings.Count(strings.TrimPrefix(p, dir+"/"), "/") >= logListDepth) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !(allFiles || LogFileName(d.Name())) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, LogFileInfo{Path: p, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	return out, nil
}

// listLogs is the log listing at the boundary. A directory and the every-file flag, nothing else;
// the directory under Policy.LogListDirs as it is named and as its symlinks resolve, so a link an
// addon put into its own tree cannot lead the walk out of it; every file only under
// LogListEveryFileDirs; and only paths under the directory asked about in the answer.
func (s *Server) listLogs(req request) response {
	if req.Name != "" || len(req.Args) > 0 || len(req.Data) > 0 || len(req.Stdin) > 0 || len(req.Env) > 0 || req.Dir != "" || req.Src != "" || req.Dst != "" || req.Target != "" || req.Hash != "" {
		s.log("helper: refused a log listing with more than a directory: %s", req.Path)
		return refuse("listlogs takes a directory and nothing else")
	}
	if !s.Policy.logListAllowed(req.Path, req.AllFiles) {
		// B-122: the storage scan asks only about directories LogListable admits, so a refusal here
		// is a guard, not something to repeat at every scan: a debug line each time, and one warning
		// per run of the helper that says so
		if s.logListRefused.CompareAndSwap(false, true) {
			s.log("helper: refused the log listing of %s; further refused log listings are logged at debug only", req.Path)
		} else {
			s.debugf("helper: refused the log listing of %s", req.Path)
		}
		return refuse("log listing " + req.Path)
	}
	files, err := s.ops().ListLogFiles(req.Path, req.AllFiles)
	if err != nil {
		return response{Error: err.Error()}
	}
	under := filepath.Clean(req.Path) + "/"
	kept := make([]LogFileInfo, 0, len(files))
	for _, f := range files {
		if strings.HasPrefix(filepath.Clean(f.Path), under) {
			kept = append(kept, f)
		}
	}
	return response{OK: true, Files: kept}
}

// boxLogPolicy is the box's policy, for LogListable.
var boxLogPolicy = DefaultPolicy("/", "")

// LogListable: dir, as the box names it, lies where the helper's log listing may walk (with
// allFiles, where every file counts) - the box's policy by the name alone. The storage scan asks the
// helper about no other directory (B-122): /run/chrony or /usr/local/etc/ssh would only be refused,
// with a line in the journal at every scan. The helper still checks every request itself, the
// resolved path too.
func LogListable(dir string, allFiles bool) bool {
	return boxLogPolicy.logListNamed(dir, allFiles)
}

// logListNamed: dir, as it is named, is one of LogListDirs or lies below one, with no ".."; with
// allFiles the same for LogListEveryFileDirs.
func (p Policy) logListNamed(dir string, allFiles bool) bool {
	rel, ok := p.rel(dir)
	if !ok || strings.Contains(rel, "..") {
		return false
	}
	return logListUnder(p.LogListDirs, rel) && (!allFiles || logListUnder(p.LogListEveryFileDirs, rel))
}

// logListAllowed is logListNamed for the directory as named and as resolved.
func (p Policy) logListAllowed(dir string, allFiles bool) bool {
	if !p.logListNamed(dir, allFiles) {
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
	return resolved.logListNamed(real, allFiles)
}

func logListUnder(list []string, rel string) bool {
	for _, d := range list {
		d = strings.TrimSuffix(d, "/")
		if d != "" && (rel == d || strings.HasPrefix(rel, d+"/")) {
			return true
		}
	}
	return false
}
