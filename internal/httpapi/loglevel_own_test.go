package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/logctl"
	"github.com/hobbyquaker/occulited/internal/system"
)

// fakeOwnLevel is occulited's own level as main wires it, minus the file: a real control, and a
// store that can be made to fail.
type fakeOwnLevel struct {
	ctl  *logctl.Control
	sets int
	fail error
}

func (f *fakeOwnLevel) Get() logctl.Setting { return f.ctl.Get() }
func (f *fakeOwnLevel) Set(_ context.Context, s logctl.Setting) (logctl.Setting, error) {
	f.sets++
	if f.fail != nil {
		return logctl.Setting{}, f.fail
	}
	return f.ctl.Set(s)
}

func syslogLines(t *testing.T, r system.Root) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(string(r), "etc/config/syslog"))
	return string(b)
}

// task 101: GET and PUT /loglevels carry multimacd (a level or null) and occulited ({level,
// debug_areas}). occulited's change applies live and restarts nothing; multimacd's asks for its
// restart; a client that leaves either key out keeps what is stored.
func TestLogLevelsOwnAndMultimacd(t *testing.T) {
	r := fakeRoot(t)
	_ = os.MkdirAll(filepath.Join(string(r), "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(string(r), "etc/config/syslog"), []byte("LOGLEVEL_RFD=5\nLOGLEVEL_HS485D=5\nLOGLEVEL_HMIP=WARN\n"), 0o644)
	own := &fakeOwnLevel{ctl: logctl.New()}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, OcculitedLog: own, SetLogLevel: func(context.Context, string, int) error { return nil }}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/loglevels", "", nil)
	if st != 200 || out["multimacd"] != nil || fmt.Sprint(out["occulited"]) != "map[debug_areas:[] level:info]" {
		t.Fatalf("get: %d %v", st, out)
	}

	// occulited to warn with two areas, multimacd to debug
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","multimacd":1,"occulited":{"level":"WARN","debug_areas":["http","acme"]}}`, nil)
	if st != 200 {
		t.Fatalf("put: %d %v", st, out)
	}
	if fmt.Sprint(out["restart"]) != "[multimacd]" || fmt.Sprint(out["applied"]) != "[occulited]" {
		t.Errorf("restart %v applied %v", out["restart"], out["applied"])
	}
	if out["multimacd"] != 1.0 || fmt.Sprint(out["occulited"]) != "map[debug_areas:[acme http] level:warn]" {
		t.Errorf("answer %v %v", out["multimacd"], out["occulited"])
	}
	if g := own.ctl.Get(); g.Level != "warn" || strings.Join(g.DebugAreas, ",") != "acme,http" {
		t.Errorf("control %+v", g)
	}
	if !strings.Contains(syslogLines(t, r), "LOGLEVEL_MULTIMACD=1\n") {
		t.Errorf("file:\n%s", syslogLines(t, r))
	}

	// an older client: neither key - both stay, nothing restarts, occulited is not set again
	sets := own.sets
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN"}`, nil)
	if st != 200 || out["multimacd"] != 1.0 || fmt.Sprint(out["restart"]) != "[]" || fmt.Sprint(out["applied"]) != "[]" || own.sets != sets {
		t.Errorf("without the keys: %d %v, sets %d", st, out, own.sets-sets)
	}
	// the same occulited setting again is no change either
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","multimacd":1,"occulited":{"level":"warn","debug_areas":["acme","http"]}}`, nil)
	if st != 200 || fmt.Sprint(out["applied"]) != "[]" || own.sets != sets {
		t.Errorf("same setting: %d %v", st, out)
	}

	// null: back to rfd's; the key leaves the file, multimacd restarts (1 -> 5)
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","multimacd":null}`, nil)
	if st != 200 || out["multimacd"] != nil || fmt.Sprint(out["restart"]) != "[multimacd]" || strings.Contains(syslogLines(t, r), "MULTIMACD") {
		t.Errorf("null: %d %v\n%s", st, out, syslogLines(t, r))
	}

	// an invalid occulited level or area is refused before anything is written
	before := syslogLines(t, r)
	for _, body := range []string{
		`{"rfd":2,"hs485d":5,"hmip":"WARN","occulited":{"level":"trace","debug_areas":[]}}`,
		`{"rfd":2,"hs485d":5,"hmip":"WARN","occulited":{"level":"info","debug_areas":["kernel"]}}`,
		`{"rfd":2,"hs485d":5,"hmip":"WARN","multimacd":3}`,
	} {
		if st, out, _ := do(t, srv, "PUT", "/api/system/v1/loglevels", body, nil); st != 422 || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", body, st, out)
		}
	}
	if syslogLines(t, r) != before {
		t.Errorf("a refused PUT wrote the file:\n%s", syslogLines(t, r))
	}

	// storing occulited's level fails: the daemons' levels are saved, the error names occulited
	own.fail = errors.New("config: read-only file system")
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","occulited":{"level":"debug","debug_areas":[]}}`, nil)
	if st != 200 || out["errors"].(map[string]any)["occulited"] != "config: read-only file system" || fmt.Sprint(out["occulited"]) != "map[debug_areas:[acme http] level:warn]" {
		t.Errorf("failing store: %d %v", st, out)
	}
}

// Without the hook (a development daemon) occulited's level reads as the default, and a PUT that
// would change it says it cannot.
func TestLogLevelsOwnUnavailable(t *testing.T) {
	r := fakeRoot(t)
	_ = os.MkdirAll(filepath.Join(string(r), "etc/config"), 0o755)
	mux := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","occulited":{"level":"info","debug_areas":[]}}`, nil); st != 200 {
		t.Errorf("unchanged default: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/loglevels", `{"rfd":5,"hs485d":5,"hmip":"WARN","occulited":{"level":"debug","debug_areas":[]}}`, nil); st != 501 {
		t.Errorf("a change without the hook: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/radio/restart", "", nil); st != 501 {
		t.Errorf("radio restart without services: %d", st)
	}
}

// stackServices knows which radio units run and records the calls.
type stackServices struct {
	running map[string]bool
	calls   []string
	failing string
}

func (s *stackServices) List() ([]system.Service, error) {
	var out []system.Service
	for _, u := range []string{"multimacd", "rfd", "hmipserver", "hs485d"} {
		out = append(out, system.Service{ID: u, Running: s.running[u]})
	}
	return out, nil
}

func (s *stackServices) Control(_ context.Context, id, action string) (string, error) {
	s.calls = append(s.calls, id+" "+action)
	if id == s.failing && action == "start" {
		return "", errors.New("start refused")
	}
	return "", nil
}

func TestRadioRestartRoute(t *testing.T) {
	svc := &stackServices{running: map[string]bool{"multimacd": true, "rfd": true, "hmipserver": true, "hs485d": true}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t), Services: svc}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	st, out, _ := do(t, srv, "POST", "/api/system/v1/radio/restart", "", nil)
	if st != 200 || fmt.Sprint(out["stopped"]) != "[hmipserver rfd multimacd]" || fmt.Sprint(out["started"]) != "[multimacd rfd hmipserver]" {
		t.Errorf("%d %v", st, out)
	}
	if strings.Join(svc.calls, ", ") != "hmipserver stop, rfd stop, multimacd stop, multimacd start, rfd start, hmipserver start" {
		t.Errorf("calls %v", svc.calls)
	}
	svc.calls, svc.failing = nil, "rfd"
	st, out, _ = do(t, srv, "POST", "/api/system/v1/radio/restart", "", nil)
	if st != 422 || !strings.Contains(fmt.Sprint(out["message"]), "starting rfd") || fmt.Sprint(out["started"]) != "[multimacd hmipserver]" {
		t.Errorf("failing start: %d %v", st, out)
	}
}
