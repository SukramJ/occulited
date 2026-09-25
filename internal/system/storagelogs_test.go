package system

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

func TestLogName(t *testing.T) {
	for name, want := range map[string]bool{
		"hmserver.log": true, "hm2mqtt.log.1": true, "x.log.12": true, "x.log.123": false, "x.log.": false,
		"x.log.a": false, "catalina.out": false, "system.journal": false, "logrotate.conf": false, "x.logs": false,
	} {
		if logName(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}

func TestScanLogFiles(t *testing.T) {
	root := t.TempDir()
	r := Root(root)
	w := func(p string, n int) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Repeat("x", n)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("var/log/hmserver.log", 188)
	w("var/log/lighttpd-access.log", 264)
	w("var/log/wtmp", 50) // under /var/log every file counts
	w("var/log/journal/0123/system.journal", 9000)
	w("run/log/journal/0123/system.journal", 9000)
	w("run/occulite/sessions/abc", 10)
	w("run/some.log", 3)
	w("usr/local/var/log/journal/0123/system@1.journal", 9000)
	w("usr/local/addons/hm2mqtt/var/hm2mqtt.log.1", 1400)
	w("usr/local/addons/hm2mqtt/var/hm2mqtt.log", 76)
	w("usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-12T10_00_00_000Z-debug-0.log", 42)
	w("usr/local/addons/redmatic/var/node_modules/pkg/debug.log", 5000)
	w("usr/local/addons/redmatic/var/npm-cache/_cacache/index/x.log", 5000)
	w("usr/local/var/recovery/2026-09-12T12-00-00Z.log", 700)
	w("usr/local/tmp/other.log", 5)
	w("usr/local/etc/config/addons/www/x.txt", 5)

	got, _ := r.scanLogFiles(nil)
	var paths []string
	for _, f := range got {
		paths = append(paths, f.Path)
	}
	want := []string{
		"/usr/local/addons/hm2mqtt/var/hm2mqtt.log.1",
		"/var/log/lighttpd-access.log",
		"/var/log/hmserver.log",
		"/usr/local/addons/hm2mqtt/var/hm2mqtt.log",
		"/var/log/wtmp",
		"/usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-12T10_00_00_000Z-debug-0.log",
		"/usr/local/tmp/other.log",
		"/run/some.log",
	}
	if strings.Join(paths, "\n") != strings.Join(want, "\n") {
		t.Fatalf("paths:\n%s\nwant:\n%s", strings.Join(paths, "\n"), strings.Join(want, "\n"))
	}
	if got[0].Addon != "hm2mqtt" || !got[0].Userfs || got[0].Size != 1400 || got[1].Addon != "" || got[1].Userfs {
		t.Errorf("first two %+v %+v", got[0], got[1])
	}

	// the cache, the growth, the cap
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	s := &Storage{Root: r, Now: func() time.Time { return now }}
	list, more := s.logFiles(now)
	if len(list) != 8 || more != 0 || list[0].Growing {
		t.Fatalf("first look: %d %d %+v", len(list), more, list[0])
	}
	w("var/log/hmserver.log", 900) // grows by 712
	w("var/log/new.log", 1)
	if list, _ = s.logFiles(now.Add(5 * time.Minute)); len(list) != 8 {
		t.Errorf("within the cache the list is the old one: %d", len(list))
	}
	list, _ = s.logFiles(now.Add(11 * time.Minute))
	var hm, nw *LogFile
	for i := range list {
		switch list[i].Path {
		case "/var/log/hmserver.log":
			hm = &list[i]
		case "/var/log/new.log":
			nw = &list[i]
		}
	}
	if hm == nil || !hm.Growing || hm.GrownBytes != 712 || nw == nil || nw.Growing {
		t.Errorf("growth: %+v %+v", hm, nw)
	}
	for _, f := range list {
		if f.Path != "/var/log/hmserver.log" && f.Growing {
			t.Errorf("%s grew without growing", f.Path)
		}
	}
	for i := 0; i < 25; i++ {
		w(filepath.Join("usr/local/addons/many/log", strings.Repeat("a", i+1)+".log"), 2)
	}
	list, more = s.logFiles(now.Add(30 * time.Minute))
	if len(list) != logFilesMax || more != 9+25-logFilesMax {
		t.Errorf("cap: %d more %d", len(list), more)
	}
	// a clock that went back rescans rather than serving a list from the future
	if list, _ = s.logFiles(now); len(list) != logFilesMax {
		t.Errorf("clock back: %d", len(list))
	}
	// nothing at all is an empty list, not null
	empty := &Storage{Root: Root(t.TempDir())}
	if list, more := empty.logFiles(now); list == nil || len(list) != 0 || more != 0 {
		t.Errorf("empty: %v %d", list, more)
	}
}

// B-113: a log directory only its owner reads - RedMatic's npm-cache/_logs, drwx------ addon-redmatic
// on the Pi 4 and .119 - is listed through the privilege helper. The scan names the directories it
// could not open, asks once for each (every file under /var/log), and takes the answer under its own
// rules: a path outside the directory asked about adds nothing, nor does a refused listing.
func TestScanLogFilesThroughTheHelper(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every directory")
	}
	root := t.TempDir()
	r := Root(root)
	w := func(p string, n int) {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(strings.Repeat("x", n)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	w("usr/local/addons/hm2mqtt/var/hm2mqtt.log", 76)
	w("usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-07T17_10_31_403Z-debug-0.log", 2427)
	w("usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-11T11_35_23_932Z-debug-0.log", 1054)
	w("usr/local/addons/confined/data/app.log", 900)
	w("var/log/private/audit", 300)
	for _, d := range []string{"usr/local/addons/redmatic/var/npm-cache/_logs", "usr/local/addons/confined/data", "var/log/private"} {
		full := filepath.Join(root, d)
		if err := os.Chmod(full, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(full, 0o755) })
	}
	var asked []string
	helper := func(dir string, allFiles bool) ([]priv.LogFileInfo, error) {
		rel := strings.TrimPrefix(dir, root)
		asked = append(asked, rel+" "+strconv.FormatBool(allFiles))
		if rel == "/usr/local/addons/confined/data" {
			return nil, priv.ErrRefused
		}
		// what the helper sees as root: the test lifts the mode for the walk
		if err := os.Chmod(dir, 0o755); err != nil {
			return nil, err
		}
		defer os.Chmod(dir, 0)
		files, err := priv.Local{}.ListLogFiles(dir, allFiles)
		return append(files, priv.LogFileInfo{Path: filepath.Join(root, "etc/shadow.log"), Size: 99999}), err
	}
	listed := func(files []LogFile, _ int) string {
		var s []string
		for _, f := range files {
			s = append(s, f.Path+" "+strconv.FormatInt(f.Size, 10)+" "+f.Addon)
		}
		return strings.Join(s, "\n")
	}

	got := listed(r.scanLogFiles(helper))
	want := strings.Join([]string{
		"/usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-07T17_10_31_403Z-debug-0.log 2427 redmatic",
		"/usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-11T11_35_23_932Z-debug-0.log 1054 redmatic",
		"/var/log/private/audit 300 ",
		"/usr/local/addons/hm2mqtt/var/hm2mqtt.log 76 hm2mqtt",
	}, "\n")
	if got != want {
		t.Errorf("through the helper:\n%s\nwant:\n%s", got, want)
	}
	if strings.Join(asked, "; ") != "/var/log/private true; /usr/local/addons/confined/data false; /usr/local/addons/redmatic/var/npm-cache/_logs false" {
		t.Errorf("asked: %q", asked)
	}
	// without the helper those directories stay out, as before
	if got := listed(r.scanLogFiles(nil)); got != "/usr/local/addons/hm2mqtt/var/hm2mqtt.log 76 hm2mqtt" {
		t.Errorf("without the helper: %s", got)
	}
	// the panel asks through the Storage's hook; a test root never reaches for the real helper
	if list, _ := (&Storage{Root: r, ListLogs: helper}).logFiles(time.Now()); len(list) != 4 {
		t.Errorf("the hook: %+v", list)
	}
	if (&Storage{Root: r}).logLister() != nil {
		t.Error("a test root reached for the helper")
	}
}

// liftOps is the helper's Local as root would see: the listing lifts a directory's mode for its walk.
type liftOps struct{ priv.Local }

func (liftOps) ListLogFiles(dir string, allFiles bool) ([]priv.LogFileInfo, error) {
	if err := os.Chmod(dir, 0o755); err != nil {
		return nil, err
	}
	defer os.Chmod(dir, 0)
	return priv.Local{}.ListLogFiles(dir, allFiles)
}

// B-122: on the Pi 4 one storage scan left 14 to 17 warnings of the helper in the journal, one per
// directory the daemon could not open - /run/chrony, /usr/local/etc/ssh - which the helper's log
// listing refuses. The scan asks only about what the listing admits: one scan against the real
// helper protocol writes no warning, asks once (for the addon's log directory, which still reaches
// the list), and leaves one debug line of its own for the rest.
func TestScanLogFilesJournalLines(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root opens every directory")
	}
	root := t.TempDir()
	r := Root(root)
	mk := func(p string) string {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		return full
	}
	logs := mk("usr/local/addons/redmatic/var/npm-cache/_logs")
	if err := os.WriteFile(filepath.Join(logs, "2026-09-13T03_00_00_000Z-debug-0.log"), []byte("npm"), 0o600); err != nil {
		t.Fatal(err)
	}
	closedDirs := []string{
		"run/chrony", "run/credentials", "run/private", "run/systemd/propagate/addon-hmm.service", "run/user",
		"usr/local/etc/config/seedrng", "usr/local/etc/logrotate.d", "usr/local/etc/ssh", "usr/local/hmm", "usr/local/lost+found",
	}
	for _, d := range append(closedDirs, "usr/local/addons/redmatic/var/npm-cache/_logs") {
		full := mk(d)
		if err := os.Chmod(full, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(full, 0o755) })
	}

	var mu sync.Mutex
	var warnLines, debugLines []string
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := &priv.Server{
		Policy: priv.DefaultPolicy(root, "/usr/local/etc/occulite"),
		Ops:    liftOps{},
		Log: func(f string, a ...any) {
			mu.Lock()
			defer mu.Unlock()
			warnLines = append(warnLines, fmt.Sprintf(f, a...))
		},
		Debug: func(f string, a ...any) {
			mu.Lock()
			defer mu.Unlock()
			debugLines = append(debugLines, fmt.Sprintf(f, a...))
		},
	}
	go func() { _ = srv.Serve(ctx, l) }()
	var daemon bytes.Buffer
	s := &Storage{Root: r, ListLogs: priv.Client{Socket: sock}.ListLogFiles, Log: slog.New(slog.NewTextHandler(&daemon, &slog.HandlerOptions{Level: slog.LevelDebug}))}

	list, _ := s.logFiles(time.Now())
	mu.Lock()
	defer mu.Unlock()
	if len(warnLines) != 0 {
		t.Errorf("the helper's warnings of one scan: %d\n%s", len(warnLines), strings.Join(warnLines, "\n"))
	}
	if len(debugLines) != 1 || !strings.Contains(debugLines[0], "listlogs "+logs) {
		t.Errorf("the helper was asked about more than the addon's log directory:\n%s", strings.Join(debugLines, "\n"))
	}
	if len(list) != 1 || list[0].Path != "/usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-13T03_00_00_000Z-debug-0.log" {
		t.Errorf("the addon's log is not listed: %+v", list)
	}
	lines := strings.Split(strings.TrimSpace(daemon.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], "level=DEBUG") || !strings.Contains(lines[0], "count="+strconv.Itoa(len(closedDirs))) {
		t.Errorf("the daemon's lines of one scan:\n%s", daemon.String())
	}
}
