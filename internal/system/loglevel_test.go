package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The file is rewritten in place: known keys where they stood, unknown lines untouched, missing
// keys appended. LOGLEVEL_REGA is dead on lite and stays anyway - it is not ours to remove.
func TestLogLevelsRoundTrip(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/config/syslog"), []byte("# levels\nLOGLEVEL_RFD=5\nLOGLEVEL_HS485D=5\nLOGLEVEL_REGA=2\nLOGLEVEL_HMIP=ERROR\n"), 0o644)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)

	got := r.ReadLogLevels()
	if got.RFD != 5 || got.HS485D != 5 || got.HmIP != "ERROR" || got.LogHost != "" || got.Lighttpd != (LighttpdDebug{}) {
		t.Fatalf("%+v", got)
	}

	set, restart, err := r.SetLogLevels(LogLevels{RFD: 1, HS485D: 5, HmIP: "debug", LogHost: "log.example.net", Lighttpd: LighttpdDebug{RequestHandling: true}})
	if err != nil {
		t.Fatal(err)
	}
	if set.RFD != 1 || set.HmIP != "DEBUG" || set.LogHost != "log.example.net" || !set.Lighttpd.RequestHandling {
		t.Errorf("read back %+v", set)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc/config/syslog"))
	want := "# levels\nLOGLEVEL_RFD=1\nLOGLEVEL_HS485D=5\nLOGLEVEL_REGA=2\nLOGLEVEL_HMIP=DEBUG\nLOGHOST=log.example.net\n"
	if string(b) != want {
		t.Errorf("file:\n%s", b)
	}
	// rfd changed: multimacd shares its level; hmip and LOGHOST: hmipserver, and the forwarder;
	// the drop-in appeared: lighttpd. rfd itself is applied live by the caller, so not here.
	if strings.Join(restart, " ") != "multimacd hmipserver occu-syslog-forward lighttpd" {
		t.Errorf("restart %v", restart)
	}
	d, _ := os.ReadFile(filepath.Join(root, "etc/config/lighttpd/occulite-debug.conf"))
	if !strings.Contains(string(d), `debug.log-request-handling = "enable"`) || strings.Contains(string(d), "condition") {
		t.Errorf("drop-in:\n%s", d)
	}

	// nothing changed: nothing to restart; switches off: the drop-in goes
	_, restart, err = r.SetLogLevels(set)
	if err != nil || len(restart) != 0 {
		t.Errorf("no change: %v %v", restart, err)
	}
	set.Lighttpd = LighttpdDebug{}
	_, restart, err = r.SetLogLevels(set)
	if err != nil || strings.Join(restart, " ") != "lighttpd" {
		t.Errorf("switch off: %v %v", restart, err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/config/lighttpd/occulite-debug.conf")); !os.IsNotExist(err) {
		t.Error("drop-in still there with every switch off")
	}
}

// The access log is a drop-in of its own: on is the file with exactly the piped line, off is no
// file, and either way the debug drop-in is what the debug switches say. A change of it alone
// restarts lighttpd; setting what is already there restarts nothing.
func TestLogLevelsAccessLog(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)
	access := filepath.Join(root, "etc/config/lighttpd/occulite-accesslog.conf")
	debug := filepath.Join(root, "etc/config/lighttpd/occulite-debug.conf")
	// the box's own levels, so that only lighttpd's switches change below
	base := LogLevels{RFD: 5, HS485D: 5, HmIP: HmIPDefaultLevel}
	if r.ReadLogLevels().Lighttpd.AccessLog {
		t.Fatal("the access log is on without a drop-in")
	}

	for _, step := range []struct {
		name        string
		lighttpd    LighttpdDebug
		restart     string
		accessFile  bool
		debugFile   string // "" = no debug drop-in
		readsBackOn bool
	}{
		{"on", LighttpdDebug{AccessLog: true}, "lighttpd", true, "", true},
		{"on again", LighttpdDebug{AccessLog: true}, "", true, "", true},
		{"a debug switch beside it", LighttpdDebug{AccessLog: true, FileNotFound: true}, "lighttpd", true, `debug.log-file-not-found = "enable"`, true},
		{"off, the debug switch stays", LighttpdDebug{FileNotFound: true}, "lighttpd", false, `debug.log-file-not-found = "enable"`, false},
		{"off again", LighttpdDebug{FileNotFound: true}, "", false, `debug.log-file-not-found = "enable"`, false},
		{"everything off", LighttpdDebug{}, "lighttpd", false, "", false},
	} {
		l := base
		l.Lighttpd = step.lighttpd
		got, restart, err := r.SetLogLevels(l)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if strings.Join(restart, " ") != step.restart {
			t.Errorf("%s: restart %v, want %q", step.name, restart, step.restart)
		}
		if got.Lighttpd != step.lighttpd || got.Lighttpd.AccessLog != step.readsBackOn {
			t.Errorf("%s: read back %+v", step.name, got.Lighttpd)
		}
		b, err := os.ReadFile(access)
		switch {
		case step.accessFile && err != nil:
			t.Errorf("%s: no access drop-in: %v", step.name, err)
		case step.accessFile:
			var effective []string
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				if !strings.HasPrefix(line, "#") {
					effective = append(effective, line)
				}
			}
			if len(effective) != 1 || effective[0] != `accesslog.filename = "|/usr/bin/systemd-cat -t lighttpd-access -p info"` {
				t.Errorf("%s: access drop-in:\n%s", step.name, b)
			}
			if !strings.HasPrefix(string(b), "# written by occulited (Log page)") {
				t.Errorf("%s: access drop-in without its comment:\n%s", step.name, b)
			}
		case !os.IsNotExist(err):
			t.Errorf("%s: access drop-in still there: %v", step.name, err)
		}
		d, err := os.ReadFile(debug)
		switch {
		case step.debugFile == "" && !os.IsNotExist(err):
			t.Errorf("%s: debug drop-in there with no debug switch on:\n%s", step.name, d)
		case step.debugFile != "" && (!strings.Contains(string(d), step.debugFile) || strings.Contains(string(d), "accesslog")):
			t.Errorf("%s: debug drop-in:\n%s", step.name, d)
		}
	}

	// reading: on is an accesslog.filename that pipes to systemd-cat, nothing else
	for _, c := range []struct {
		body string
		on   bool
	}{
		{"accesslog.filename = \"|/usr/bin/systemd-cat -t lighttpd-access -p info\"\n", true},
		{"# a comment\n  accesslog.filename=\"|systemd-cat -t lighttpd-access\"\n", true},
		{"accesslog.filename = \"/var/log/lighttpd-access.log\"\n", false},
		{"# accesslog.filename = \"|/usr/bin/systemd-cat -t lighttpd-access -p info\"\n", false},
		{"accesslog.use-syslog = \"enable\"\n", false},
		{"", false},
	} {
		if got := accessLogPiped(c.body); got != c.on {
			t.Errorf("accessLogPiped(%q) = %v, want %v", c.body, got, c.on)
		}
	}
}

// B-161: a level change rewrites the environment files the units read, so a restart applies it.
// Without this the page wrote /etc/config/syslog, rfd and hs485d took the level live, multimacd
// never did, and the next restart put the boot's level back.
func TestLogLevelsWriteTheUnitsEnvironment(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	os.MkdirAll(filepath.Join(root, "run/occulite/radio"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/config/syslog"), []byte("LOGLEVEL_RFD=5\nLOGLEVEL_HS485D=5\nLOGLEVEL_HMIP=WARN\n"), 0o644)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)
	if _, _, err := r.SetLogLevels(LogLevels{RFD: 1, HS485D: 4, HmIP: "WARN"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ file, want string }{
		{"rfd.env", "LOGLEVEL_RFD=1\n"},
		{"hs485d.env", "LOGLEVEL_HS485D=4\n"},
		// without a level of its own multimacd takes rfd's, as the init script does
		{"multimacd.env", "MULTIMACD_LOGLEVEL=1\n"},
	} {
		b, err := os.ReadFile(filepath.Join(root, "run/occulite/radio", c.file))
		if err != nil || string(b) != c.want {
			t.Errorf("%s: %v %q, want %q", c.file, err, b, c.want)
		}
	}
	// its own level goes into its own file and leaves rfd's alone
	if _, _, err := r.SetLogLevels(LogLevels{RFD: 1, HS485D: 4, MultiMACD: intp(2), HmIP: "WARN"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "run/occulite/radio/multimacd.env")); string(b) != "MULTIMACD_LOGLEVEL=2\n" {
		t.Errorf("multimacd.env with its own level: %q", b)
	}
}

// No file at all: the daemons' own defaults, and a write creates it with just our keys.
func TestLogLevelsFromNothing(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)
	// hmipserver's default is WARN since 2026-09-12 (task 94); it was ERROR (task 83)
	if got := r.ReadLogLevels(); got.RFD != 5 || got.HS485D != 5 || got.HmIP != "WARN" || HmIPDefaultLevel != "WARN" {
		t.Errorf("defaults %+v", got)
	}
	if _, _, err := r.SetLogLevels(LogLevels{RFD: 2, HS485D: 4, HmIP: "INFO"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc/config/syslog"))
	if string(b) != "LOGHOST=\nLOGLEVEL_HMIP=INFO\nLOGLEVEL_HS485D=4\nLOGLEVEL_RFD=2\n" {
		t.Errorf("file:\n%s", b)
	}
}

func TestLogLevelsRefusals(t *testing.T) {
	for _, l := range []LogLevels{
		{RFD: 3, HS485D: 5, HmIP: "ERROR"},
		{RFD: 5, HS485D: 0, HmIP: "ERROR"},
		{RFD: 5, HS485D: 5, HmIP: "VERBOSE"},
		{RFD: 5, HS485D: 5, HmIP: "ERROR", LogHost: "log.example.net; reboot"},
		{RFD: 5, HS485D: 5, HmIP: "ERROR", LogHost: "a b"},
	} {
		if err := checkLogLevels(l); err == nil {
			t.Errorf("accepted %+v", l)
		}
	}
	if err := checkLogLevels(LogLevels{RFD: 4, HS485D: 1, HmIP: "trace", LogHost: "192.168.1.5"}); err != nil {
		t.Errorf("refused a good one: %v", err)
	}
}
