package system

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// task 84: the storage panel's hint about log files beside the journal. The journal is the one log
// on the box (D-59); a file that is still written next to it - an addon's own log, npm's debug logs,
// a third-party installer's - costs RAM on /var and wears the card on the userfs. The panel lists
// them with their size and whether they grew since the previous look. A hint, not a verdict: an
// addon's files are its own project's to change.
//
// The rule is the lab boot check's (the fork's scripts/lite-log-inventory.sh): every regular file
// under /var/log outside the journal, and every file named *.log, *.log.N or *.log.NN under /run and
// /usr/local. Left out: the journal's directories, node_modules and npm's content cache (tens of
// thousands of files, no logs), and /usr/local/var/recovery/*.log, the recovery system's install log
// waiting to be carried into the journal (the documented exception, task 145). /tmp is not looked at: the
// daemon's unit has PrivateTmp, so its /tmp is not the box's.

// LogFile is one file written beside the journal.
type LogFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`   // bytes, the file's length
	Userfs bool   `json:"userfs"` // on /usr/local, the card; otherwise a tmpfs (RAM)
	Addon  string `json:"addon,omitempty"`
	// Growing: the file was there at the previous look and is longer now, by GrownBytes. A file
	// seen for the first time has no growth yet.
	Growing    bool  `json:"growing"`
	GrownBytes int64 `json:"grown_bytes,omitempty"`
}

const (
	logScanEvery = 10 * time.Minute // the scan's cache; the Status page asks once a minute
	logFilesMax  = 20               // the list: the largest ones; LogFilesMore counts the rest
	logScanDepth = 8                // levels below a scan root
	logScanLimit = 200000           // entries visited per scan, whatever the tree
)

// logScanRoots are where log files are looked for; allFiles: every regular file counts, not only
// the ones named like logs.
var logScanRoots = []struct {
	dir      string
	allFiles bool
}{
	{"/var/log", true},
	{"/run", false},
	{"/usr/local", false},
}

var logScanSkip = map[string]bool{
	"/var/log/journal":           true,
	"/run/log/journal":           true,
	"/usr/local/var/log/journal": true,
	"/proc":                      true,
}

// logName: *.log, *.log.N, *.log.NN - the inventory's rule, one definition with the helper's listing.
func logName(name string) bool { return priv.LogFileName(name) }

// logListDirsMax bounds the directories one scan asks the privilege helper about.
const logListDirsMax = 32

// logDirLister lists the log files of a directory the daemon cannot open (priv.Ops.ListLogFiles).
type logDirLister func(dir string, allFiles bool) ([]priv.LogFileInfo, error)

// relOf is a path of the walk as the box names it.
func (r Root) relOf(p string) string {
	if string(r) == "" || string(r) == "/" {
		return p
	}
	return "/" + strings.TrimPrefix(strings.TrimPrefix(p, string(r)), "/")
}

// logSkipped: a path in one of the trees the scan leaves out.
func logSkipped(rel string) bool {
	for dir := range logScanSkip {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "node_modules" || part == "_cacache" {
			return true
		}
	}
	return false
}

// scanLogFiles walks the roots and returns every log file found, largest first. A directory the
// daemon cannot open - an addon's own, RedMatic's npm-cache/_logs is drwx------ addon-redmatic
// (B-113) - is listed through list, the privilege helper's names, sizes and times, and what it
// answers is taken under the scan's own rules; a nil list leaves such a directory out. Only a
// directory the helper's listing admits is asked about (priv.LogListable, B-122); outside counts
// the others, /run/chrony or /usr/local/etc/ssh, which are left out.
func (r Root) scanLogFiles(list logDirLister) (files []LogFile, outside int) {
	var out []LogFile
	visited := 0
	seen := map[string]bool{}
	add := func(rel string, size int64) {
		if seen[rel] || (strings.HasPrefix(rel, RecoveryLogDir+"/") && strings.HasSuffix(rel, ".log")) {
			return
		}
		seen[rel] = true
		f := LogFile{Path: rel, Size: size, Userfs: strings.HasPrefix(rel, "/usr/local/")}
		if rest, ok := strings.CutPrefix(rel, "/usr/local/addons/"); ok {
			if id, _, ok := strings.Cut(rest, "/"); ok {
				f.Addon = id
			}
		}
		out = append(out, f)
	}
	type closedDir struct {
		dir      string // as the box names it
		allFiles bool
	}
	var closed []closedDir
	for _, root := range logScanRoots {
		base := r.join(root.dir)
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if visited++; visited > logScanLimit {
				return filepath.SkipAll
			}
			rel := r.relOf(p)
			if err != nil {
				if d != nil && d.IsDir() && p != base {
					if errors.Is(err, fs.ErrPermission) {
						switch {
						case !priv.LogListable(rel, root.allFiles):
							outside++
						case len(closed) < logListDirsMax:
							closed = append(closed, closedDir{rel, root.allFiles})
						}
					}
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				depth := strings.Count(strings.TrimPrefix(rel, root.dir), "/")
				if logScanSkip[rel] || d.Name() == "node_modules" || d.Name() == "_cacache" || depth > logScanDepth {
					return fs.SkipDir
				}
				return nil
			}
			if !d.Type().IsRegular() || (!root.allFiles && !logName(d.Name())) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			add(rel, info.Size())
			return nil
		})
	}
	if list != nil {
		for _, c := range closed {
			files, err := list(r.join(c.dir), c.allFiles)
			if err != nil {
				continue
			}
			for _, f := range files {
				rel := r.relOf(filepath.Clean(f.Path))
				// only what lies in the directory asked about, under the scan's own rules
				if !strings.HasPrefix(rel, c.dir+"/") || logSkipped(rel) || (!c.allFiles && !logName(filepath.Base(rel))) {
					continue
				}
				add(rel, f.Size)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Size != out[j].Size {
			return out[i].Size > out[j].Size
		}
		return out[i].Path < out[j].Path
	})
	return out, outside
}

// logLister is how the scan lists a directory it cannot open (B-113): ListLogs, else the privilege
// helper on a real box; a listing that fails is logged at debug and leaves that directory out.
func (s *Storage) logLister() logDirLister {
	list := s.ListLogs
	if list == nil {
		if string(s.Root) != "/" || Priv == nil {
			return nil
		}
		list = Priv.ListLogFiles
	}
	return func(dir string, allFiles bool) ([]priv.LogFileInfo, error) {
		files, err := list(dir, allFiles)
		if err != nil {
			s.log().Debug("storage: a log directory could not be listed", "dir", dir, "err", err)
		}
		return files, err
	}
}

// logFiles is the cached scan with the growth since the previous one: the list (at most
// logFilesMax, largest first) and how many more there are.
func (s *Storage) logFiles(now time.Time) ([]LogFile, int) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.logList != nil && now.Sub(s.logAt) < logScanEvery && !now.Before(s.logAt) {
		return s.logList, s.logMore
	}
	all, outside := s.Root.scanLogFiles(s.logLister())
	if outside > 0 {
		// one line per scan, not one per directory (B-122)
		s.log().Debug("storage: directories the daemon cannot open and the helper does not list were left out of the log files", "count", outside)
	}
	sizes := make(map[string]int64, len(all))
	for i := range all {
		f := &all[i]
		if prev, ok := s.logSizes[f.Path]; ok && f.Size > prev {
			f.Growing, f.GrownBytes = true, f.Size-prev
		}
		sizes[f.Path] = f.Size
	}
	more := 0
	if len(all) > logFilesMax {
		more = len(all) - logFilesMax
		all = all[:logFilesMax]
	}
	if all == nil {
		all = []LogFile{}
	}
	s.logList, s.logMore, s.logSizes, s.logAt = all, more, sizes, now
	return all, more
}
