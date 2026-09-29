package system

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// task 101, task 297: multimacd's own level, 1 debug or 2 info. A file without LOGLEVEL_MULTIMACD
// reads as info (2), never as rfd's; every save writes the key; multimacd is restarted only when its
// own level changes, never for rfd's.
func TestLogLevelsMultimacd(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	file := filepath.Join(root, "etc/config/syslog")
	os.WriteFile(file, []byte("# levels\nLOGLEVEL_RFD=4\nLOGLEVEL_HS485D=5\nLOGLEVEL_HMIP=WARN\n"), 0o644)
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	r := Root(root)

	if got := r.ReadLogLevels(); got.MultiMACD != 2 {
		t.Fatalf("without the key: %d, want 2 (info, not rfd's 4)", got.MultiMACD)
	}
	base := LogLevels{RFD: 4, HS485D: 5, HmIP: "WARN"}

	for _, step := range []struct {
		name      string
		rfd       int
		multimacd int
		restart   string
		file      string // the LOGLEVEL_ lines of the file, in order
	}{
		// info, as it read: the key is written, nothing restarts
		{"info as it was", 4, 2, "", "RFD=4 HS485D=5 HMIP=WARN MULTIMACD=2"},
		{"debug", 4, 1, "multimacd", "RFD=4 HS485D=5 HMIP=WARN MULTIMACD=1"},
		// rfd changes: rfd is applied live by the caller, multimacd keeps its own
		{"rfd changes under debug", 2, 1, "", "RFD=2 HS485D=5 HMIP=WARN MULTIMACD=1"},
		{"back to info", 2, 2, "multimacd", "RFD=2 HS485D=5 HMIP=WARN MULTIMACD=2"},
		{"rfd changes under info", 5, 2, "", "RFD=5 HS485D=5 HMIP=WARN MULTIMACD=2"},
		{"rfd to debug under info", 1, 2, "", "RFD=1 HS485D=5 HMIP=WARN MULTIMACD=2"},
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
		if got.MultiMACD != step.multimacd {
			t.Errorf("%s: read back %d, want %d", step.name, got.MultiMACD, step.multimacd)
		}
		env, _ := os.ReadFile(filepath.Join(root, "run/occulite/radio/multimacd.env"))
		if want := "MULTIMACD_LOGLEVEL=" + strconv.Itoa(step.multimacd) + "\n"; string(env) != want {
			t.Errorf("%s: multimacd.env %q, want %q", step.name, env, want)
		}
	}

	// a stale 5 an older system stored reads as info, and a save that leaves multimacd as the page
	// showed it writes 2 over it without a restart (the running multimacd was held at 2 already)
	os.WriteFile(file, []byte("LOGLEVEL_RFD=5\nLOGLEVEL_HS485D=5\nLOGLEVEL_HMIP=WARN\nLOGLEVEL_MULTIMACD=5\n"), 0o644)
	cur := r.ReadLogLevels()
	if cur.MultiMACD != 2 {
		t.Fatalf("stale 5: %d, want 2", cur.MultiMACD)
	}
	if _, restart, err := r.SetLogLevels(cur); err != nil || len(restart) != 0 {
		t.Errorf("stale 5 saved: %v %v", restart, err)
	}
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "LOGLEVEL_MULTIMACD=2\n") || strings.Contains(string(b), "MULTIMACD=5") {
		t.Errorf("stale 5 not replaced:\n%s", b)
	}
}

// Only 1 is debug; everything else - no key, 2, a 0 or a 3-7 of an older system, words - reads as
// info. The check takes only 1 and 2.
func TestLogLevelsMultimacdRead(t *testing.T) {
	for _, c := range []struct {
		line string
		want int
	}{
		{"LOGLEVEL_MULTIMACD=1", 1},
		{"LOGLEVEL_MULTIMACD=\"1\"", 1},
		{"LOGLEVEL_MULTIMACD=\"2\"", 2},
		{"LOGLEVEL_MULTIMACD=0", 2},
		{"LOGLEVEL_MULTIMACD=3", 2},
		{"LOGLEVEL_MULTIMACD=5", 2},
		{"LOGLEVEL_MULTIMACD=7", 2},
		{"LOGLEVEL_MULTIMACD=", 2},
		{"LOGLEVEL_MULTIMACD=debug", 2},
		{"LOGLEVEL_MULTIMACD=12", 2},
		{"# LOGLEVEL_MULTIMACD=1", 2},
		{"", 2},
	} {
		root := t.TempDir()
		os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
		os.WriteFile(filepath.Join(root, "etc/config/syslog"), []byte("LOGLEVEL_RFD=1\n"+c.line+"\n"), 0o644)
		if got := Root(root).ReadLogLevels().MultiMACD; got != c.want {
			t.Errorf("%q: %d, want %d", c.line, got, c.want)
		}
	}
	for n := -1; n <= 8; n++ {
		err := checkLogLevels(LogLevels{RFD: 5, HS485D: 5, HmIP: "WARN", MultiMACD: n})
		if ok := n == 1 || n == 2; ok != (err == nil) {
			t.Errorf("multimacd %d: %v", n, err)
		}
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
