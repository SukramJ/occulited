package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// task 94: GET /status carries the boot's clock gate, and after a timeout chrony's word on it
func TestStatusClock(t *testing.T) {
	r := fakeRoot(t)
	calls := 0
	clock := &system.ClockCheck{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte("Stratum         : 0\nLeap status     : Not synchronised\n"), nil
	}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Clock: clock}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// an image without the gate: no clock, and chrony is not asked
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/status", "", nil); st != 200 || out["clock"] != nil || calls != 0 {
		t.Fatalf("without the file: %d %v, %d calls", st, out["clock"], calls)
	}

	state := filepath.Join(string(r), "run/occulite/clock-state")
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("timeout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ := do(t, srv, "GET", "/api/system/v1/status", "", nil)
	c, _ := out["clock"].(map[string]any)
	if c["state"] != "timeout" || c["synchronised"] != false || calls != 1 {
		t.Errorf("after a timeout: %v, %d calls", out["clock"], calls)
	}

	if err := os.WriteFile(state, []byte("rtc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out, _ = do(t, srv, "GET", "/api/system/v1/status", "", nil)
	if c, _ := out["clock"].(map[string]any); c["state"] != "rtc" || c["synchronised"] != true || calls != 1 {
		t.Errorf("from the RTC: %v, %d calls", out["clock"], calls)
	}

	// an API without a clock check (development): nothing, whatever the file says
	mux2 := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux2)
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	if _, out, _ := do(t, srv2, "GET", "/api/system/v1/status", "", nil); out["clock"] != nil {
		t.Errorf("without a clock check: %v", out["clock"])
	}
}
