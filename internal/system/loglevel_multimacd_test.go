package system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func intp(n int) *int { return &n }

// task 101: multimacd's own level. A file without LOGLEVEL_MULTIMACD reads as unset (rfd's level,
// as the init script falls back to it); setting one writes the key, and null takes it out again.
// multimacd is restarted when the level it starts with changes: its own, or rfd's while unset.
func TestLogLevelsMultimacd(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	file := filepath.Join(root, "etc/config/syslog")
	os.WriteFile(file, []byte("# levels\nLOGLEVEL_RFD=4\nLOGLEVEL_HS485D=5\nLOGLEVEL_HMIP=WARN\n"), 0o644)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)

	got := r.ReadLogLevels()
	if got.MultiMACD != nil || got.MultiMACDLevel() != 4 {
		t.Fatalf("without the key: %+v, level %d", got.MultiMACD, got.MultiMACDLevel())
	}
	base := LogLevels{RFD: 4, HS485D: 5, HmIP: "WARN"}

	for _, step := range []struct {
		name      string
		rfd       int
		multimacd *int
		restart   string
		file      string // the LOGLEVEL_ lines of the file, in order
		readBack  string // "" = nil
	}{
		// the same level rfd has: the key is written, but multimacd starts with what it had
		{"own level equal to rfd's", 4, intp(4), "", "RFD=4 HS485D=5 HMIP=WARN MULTIMACD=4", "4"},
		{"debug", 4, intp(1), "multimacd", "RFD=4 HS485D=5 HMIP=WARN MULTIMACD=1", "1"},
		// rfd changes, multimacd has its own: rfd is applied live by the caller, nothing restarts
		{"rfd changes under an own level", 2, intp(1), "", "RFD=2 HS485D=5 HMIP=WARN MULTIMACD=1", "1"},
		// back to rfd's: the key goes, multimacd starts with 2 now instead of 1
		{"same as rfd again", 2, nil, "multimacd", "RFD=2 HS485D=5 HMIP=WARN", ""},
		// unset, rfd changes: multimacd's level changes with it
		{"rfd changes while unset", 5, nil, "multimacd", "RFD=5 HS485D=5 HMIP=WARN", ""},
		{"nothing changes", 5, nil, "", "RFD=5 HS485D=5 HMIP=WARN", ""},
	} {
		l := base
		l.RFD, l.MultiMACD = step.rfd, step.multimacd
		got, restart, err := r.SetLogLevels(l)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if strings.Join(restart, " ") != step.restart {
			t.Errorf("%s: restart %v, want %q", step.name, restart, step.restart)
		}
		b, _ := os.ReadFile(file)
		var keys []string
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "LOGLEVEL_") {
				keys = append(keys, strings.TrimPrefix(line, "LOGLEVEL_"))
			}
		}
		if strings.Join(keys, " ") != step.file {
			t.Errorf("%s: file levels %q, want %q\n%s", step.name, strings.Join(keys, " "), step.file, b)
		}
		if !strings.HasPrefix(string(b), "# levels\n") {
			t.Errorf("%s: the comment went:\n%s", step.name, b)
		}
		switch {
		case step.readBack == "" && got.MultiMACD != nil:
			t.Errorf("%s: read back %d, want unset", step.name, *got.MultiMACD)
		case step.readBack != "" && (got.MultiMACD == nil || step.readBack != strconv.Itoa(*got.MultiMACD)):
			t.Errorf("%s: read back %v, want %s", step.name, got.MultiMACD, step.readBack)
		}
	}
}

// What the init script would not take reads as unset here too: the page then says "same as rfd",
// which is what multimacd runs with.
func TestLogLevelsMultimacdRead(t *testing.T) {
	for _, c := range []struct {
		line string
		want *int
	}{
		{"LOGLEVEL_MULTIMACD=1", intp(1)},
		{"LOGLEVEL_MULTIMACD=\"2\"", intp(2)},
		{"LOGLEVEL_MULTIMACD=0", intp(0)},
		{"LOGLEVEL_MULTIMACD=", nil},
		{"LOGLEVEL_MULTIMACD=debug", nil},
		{"LOGLEVEL_MULTIMACD=12", nil},
		{"LOGLEVEL_MULTIMACD=7", nil},
		{"# LOGLEVEL_MULTIMACD=1", nil},
	} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
		os.WriteFile(filepath.Join(root, "etc/config/syslog"), []byte("LOGLEVEL_RFD=5\n"+c.line+"\n"), 0o644)
		got := Root(root).ReadLogLevels().MultiMACD
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("%q: %v, want %v", c.line, got, c.want)
		}
	}
	if err := checkLogLevels(LogLevels{RFD: 5, HS485D: 5, HmIP: "WARN", MultiMACD: intp(3)}); err == nil {
		t.Error("accepted multimacd 3")
	}
	if err := checkLogLevels(LogLevels{RFD: 5, HS485D: 5, HmIP: "WARN", MultiMACD: intp(2)}); err != nil {
		t.Errorf("refused multimacd 2: %v", err)
	}
}

// renderSyslogConfig drops an unset key wherever it stands, even twice, and never a comment.
func TestRenderSyslogConfigUnset(t *testing.T) {
	in := "# LOGLEVEL_MULTIMACD=1 was here\nLOGLEVEL_MULTIMACD=1\nLOGLEVEL_RFD=5\nLOGLEVEL_MULTIMACD=2\nOTHER=x\n"
	got := renderSyslogConfig(in, map[string]string{"LOGLEVEL_RFD": "2"}, "LOGLEVEL_MULTIMACD")
	want := "# LOGLEVEL_MULTIMACD=1 was here\nLOGLEVEL_RFD=2\nOTHER=x\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// The radio stack restarts in order: the running units stop hmipserver first, multimacd last, and
// start multimacd first; a unit that did not run is not touched, hs485d and lighttpd never.
func TestRestartRadioStack(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "dev"), 0o755)
	os.MkdirAll(filepath.Join(root, "sys/class/raw-uart/raw-uart1"), 0o755)
	svc := &fakeRadioSvc{root: root, running: map[string]bool{"multimacd": true, "rfd": true, "hmipserver": true, "hs485d": true, "lighttpd": true}}
	res, err := RestartRadioStack(context.Background(), svc)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(svc.snapshot(), ", "); got != "hmipserver stop, rfd stop, multimacd stop, multimacd start, rfd start, hmipserver start" {
		t.Errorf("calls: %s", got)
	}
	if strings.Join(res.Stopped, " ") != "hmipserver rfd multimacd" || strings.Join(res.Started, " ") != "multimacd rfd hmipserver" || res.Errors != nil {
		t.Errorf("result %+v", res)
	}

	// multimacd not required on this box (not running): rfd and hmipserver restart around it
	svc = &fakeRadioSvc{root: root, running: map[string]bool{"rfd": true, "hmipserver": true}}
	if _, err := RestartRadioStack(context.Background(), svc); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(svc.snapshot(), ", "); got != "hmipserver stop, rfd stop, rfd start, hmipserver start" {
		t.Errorf("without multimacd: %s", got)
	}

	// rfd refuses to stop: the stop phase ends there, and hmipserver, which did stop, starts again
	svc = &fakeRadioSvc{root: root, running: map[string]bool{"multimacd": true, "rfd": true, "hmipserver": true}, failing: "rfd"}
	res, err = RestartRadioStack(context.Background(), svc)
	if err == nil || !strings.Contains(err.Error(), "stopping rfd") {
		t.Errorf("err %v", err)
	}
	if got := strings.Join(svc.snapshot(), ", "); got != "hmipserver stop, rfd stop, hmipserver start" {
		t.Errorf("failed stop: %s", got)
	}
	if res.Errors["rfd"] == "" || strings.Join(res.Started, " ") != "hmipserver" {
		t.Errorf("failed stop result %+v", res)
	}

	if _, err := RestartRadioStack(context.Background(), nil); err == nil {
		t.Error("no service manager accepted")
	}
}
