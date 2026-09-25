package priv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// B-113: the log listing admits a directory of the addons' trees or of /var/log by its name - every
// file only under /var/log - and refuses everything else before anything is read.
func TestLogListPolicy(t *testing.T) {
	p := DefaultPolicy("/", "/usr/local/etc/occulite")
	for _, tc := range []struct {
		dir      string
		allFiles bool
		want     bool
	}{
		{"/usr/local/addons/redmatic/var/npm-cache/_logs", false, true},
		{"/usr/local/addons", false, true},
		{"/usr/local/addons/", false, true},
		{"/var/log", true, true},
		{"/var/log/private", true, true},
		{"/var/log/private", false, true},
		{"/usr/local/addons/redmatic/var", true, false},
		{"/", false, false},
		{"/etc", false, false},
		{"/etc/config", false, false},
		{"/usr/local", false, false},
		{"/usr/local/etc/config", false, false},
		{"/usr/local/addonsx", false, false},
		{"/usr/local/addons/../etc", false, false},
		{"/var/log/../../root", true, false},
		{"/var/logs", false, false},
		{"/run/occulite", false, false},
		{"/proc/1", false, false},
		{"usr/local/addons/redmatic", false, false},
		{"", false, false},
	} {
		if got := p.logListNamed(tc.dir, tc.allFiles); got != tc.want {
			t.Errorf("%q (every file %v): %v, want %v", tc.dir, tc.allFiles, got, tc.want)
		}
	}
}

// B-122: LogListable is the box's policy by name, what the storage scan checks before it asks.
func TestLogListable(t *testing.T) {
	for _, tc := range []struct {
		dir      string
		allFiles bool
		want     bool
	}{
		{"/usr/local/addons/redmatic/var/npm-cache/_logs", false, true},
		{"/var/log/private", true, true},
		{"/usr/local/addons/redmatic/var", true, false},
		{"/run/chrony", false, false},
		{"/run/systemd/propagate/addon-hmm.service", false, false},
		{"/usr/local/etc/ssh", false, false},
		{"/usr/local/etc/config/seedrng", false, false},
		{"/usr/local/hmm", false, false},
		{"/usr/local/lost+found", false, false},
		{"/usr/local/addons/../etc", false, false},
	} {
		if got := LogListable(tc.dir, tc.allFiles); got != tc.want {
			t.Errorf("%q (every file %v): %v, want %v", tc.dir, tc.allFiles, got, tc.want)
		}
	}
}

// B-122: a refused log listing is still refused, but the helper warns once per run and writes the
// rest at debug - a daemon that asks about /run/chrony at every scan does not fill the journal.
func TestLogListRefusalLoggedOnce(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"run/chrony", "usr/local/etc/ssh", "usr/local/addons/x"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var mu sync.Mutex
	var warns, debugs []string
	srv := &Server{
		Policy: DefaultPolicy(root, "/usr/local/etc/occulite"),
		Log:    func(f string, a ...any) { mu.Lock(); warns = append(warns, fmt.Sprintf(f, a...)); mu.Unlock() },
		Debug:  func(f string, a ...any) { mu.Lock(); debugs = append(debugs, fmt.Sprintf(f, a...)); mu.Unlock() },
	}
	go func() { _ = srv.Serve(ctx, l) }()
	c := Client{Socket: sock}
	for i := 0; i < 3; i++ {
		for _, d := range []string{"run/chrony", "usr/local/etc/ssh"} {
			if files, err := c.ListLogFiles(filepath.Join(root, d), false); !errors.Is(err, ErrRefused) || files != nil {
				t.Fatalf("%s: not refused: %v %+v", d, err, files)
			}
		}
	}
	if _, err := c.ListLogFiles(filepath.Join(root, "usr/local/addons/x"), false); err != nil {
		t.Fatalf("an admitted listing: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(warns) != 1 || !strings.Contains(warns[0], "refused the log listing of "+filepath.Join(root, "run/chrony")) || !strings.Contains(warns[0], "at debug only") {
		t.Errorf("warnings: %d\n%s", len(warns), strings.Join(warns, "\n"))
	}
	refusedAtDebug := 0
	for _, d := range debugs {
		if strings.Contains(d, "refused the log listing") {
			refusedAtDebug++
		}
	}
	if refusedAtDebug != 5 {
		t.Errorf("refusals at debug: %d, want 5\n%s", refusedAtDebug, strings.Join(debugs, "\n"))
	}
}

// outsideLogs answers paths outside the directory asked about, as a raced symlink could: the
// Server drops them.
type outsideLogs struct{ Local }

func (outsideLogs) ListLogFiles(dir string, _ bool) ([]LogFileInfo, error) {
	return []LogFileInfo{
		{Path: filepath.Join(dir, "a.log"), Size: 1},
		{Path: filepath.Join(filepath.Dir(dir), "b.log"), Size: 2},
		{Path: dir + "/../c.log", Size: 3},
		{Path: dir + "x/d.log", Size: 4},
	}, nil
}

func logListHelper(t *testing.T, root string, ops Ops) (Client, string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite"), Ops: ops}
	go func() { _ = srv.Serve(ctx, l) }()
	return Client{Socket: sock}, sock
}

// rawLogList sends one request as it is and returns the answer with its line as it came.
func rawLogList(t *testing.T, sock string, req request) (response, string) {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReaderSize(conn, 1<<20).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	var res response
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		t.Fatal(err)
	}
	return res, line
}

// B-113: the listing answers the log files of an addon's private directory - paths, sizes and
// times, never content - and nothing a symlink, a wider directory or an extra field would reach.
func TestListLogsOperation(t *testing.T) {
	root := t.TempDir()
	w := func(p, content string, mode os.FileMode) string {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
		return full
	}
	logs := filepath.Join(root, "usr/local/addons/redmatic/var/npm-cache/_logs")
	debug := w("usr/local/addons/redmatic/var/npm-cache/_logs/2026-09-11T11_35_23_932Z-debug-0.log", "npm token=secret-token\n", 0o600)
	w("usr/local/addons/redmatic/var/npm-cache/_logs/notes.txt", "not a log", 0o600)
	w("usr/local/addons/redmatic/var/node_modules/pkg/debug.log", "x", 0o644)
	w("var/log/private/audit", "12345", 0o600)
	w("etc/passwd.log", "root", 0o644)
	if err := os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "usr/local/addons/evil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc/passwd.log"), filepath.Join(logs, "linked.log")); err != nil {
		t.Fatal(err)
	}
	c, sock := logListHelper(t, root, nil)

	// the addon's tree: the npm log, not the text file, not node_modules, not the linked file
	files, err := c.ListLogFiles(filepath.Join(root, "usr/local/addons/redmatic"), false)
	st, _ := os.Stat(debug)
	if err != nil || len(files) != 1 || files[0].Path != debug || files[0].Size != st.Size() || !files[0].ModTime.Equal(st.ModTime()) {
		t.Fatalf("the addon's tree: %v %+v", err, files)
	}
	// /var/log: every file
	if files, err := c.ListLogFiles(filepath.Join(root, "var/log/private"), true); err != nil || len(files) != 1 || !strings.HasSuffix(files[0].Path, "/audit") || files[0].Size != 5 {
		t.Errorf("/var/log, every file: %v %+v", err, files)
	}
	// never content: no byte of the file on the wire
	res, line := rawLogList(t, sock, request{Op: opListLogs, Path: logs})
	if !res.OK || len(res.Files) != 1 || res.Stdout != nil || strings.Contains(line, "secret-token") {
		t.Errorf("the answer: %s", line)
	}

	for name, tc := range map[string]struct {
		dir string
		all bool
	}{
		"a directory outside the lists":      {filepath.Join(root, "etc"), false},
		"the userfs itself":                  {filepath.Join(root, "usr/local"), false},
		"a symlink out of the addons' trees": {filepath.Join(root, "usr/local/addons/evil"), false},
		"every file in an addon's tree":      {filepath.Join(root, "usr/local/addons/redmatic"), true},
		"a dot-dot out of the addons' trees": {filepath.Join(root, "usr/local/addons") + "/../../../etc", false},
		"a path outside the helper's root":   {"/usr/local/addons/redmatic", false},
		"a relative path":                    {"usr/local/addons/redmatic", false},
	} {
		if files, err := c.ListLogFiles(tc.dir, tc.all); !errors.Is(err, ErrRefused) || files != nil {
			t.Errorf("%s: %v %+v", name, err, files)
		}
	}
	// a request that carries more than a directory
	for name, req := range map[string]request{
		"arguments": {Op: opListLogs, Path: logs, Args: []string{"-exec", "cat"}},
		"a program": {Op: opListLogs, Path: logs, Name: "find"},
		"data":      {Op: opListLogs, Path: logs, Data: []byte("x")},
		"a target":  {Op: opListLogs, Path: logs, Target: "/etc"},
	} {
		if res, line := rawLogList(t, sock, req); res.OK || !strings.Contains(res.Error, "refused") || len(res.Files) != 0 {
			t.Errorf("%s: %s", name, line)
		}
	}

	// what a listing answers outside the directory asked about does not reach the caller
	c2, _ := logListHelper(t, root, outsideLogs{})
	if files, err := c2.ListLogFiles(logs, false); err != nil || len(files) != 1 || files[0].Size != 1 {
		t.Errorf("answers outside the directory: %v %+v", err, files)
	}
}

// Local walks without following a symlink and within its bounds.
func TestLocalListLogFiles(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a/b/c/d/e/f/g/h/i/j")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "a/top.log"), filepath.Join(deep, "deep.log"), filepath.Join(root, "outside/x.log")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "a/link")); err != nil {
		t.Fatal(err)
	}
	files, err := Local{}.ListLogFiles(filepath.Join(root, "a"), false)
	if err != nil || len(files) != 1 || files[0].Path != filepath.Join(root, "a/top.log") {
		t.Errorf("%v %+v", err, files)
	}
	if _, err := (Local{}).ListLogFiles(filepath.Join(root, "a/link"), false); err == nil {
		t.Error("a symlinked directory was walked")
	}
	if _, err := (Local{}).ListLogFiles(filepath.Join(root, "a/top.log"), false); err == nil {
		t.Error("a file was walked as a directory")
	}
}
