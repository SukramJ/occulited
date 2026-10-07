package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// ctlRecorder records the controls and whether the radio was busy at each.
type ctlRecorder struct {
	mu    sync.Mutex
	busy  *atomic.Bool
	calls []string
}

func (c *ctlRecorder) List() ([]system.Service, error) { return []system.Service{}, nil }

func (c *ctlRecorder) Control(_ context.Context, id, action string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := "idle"
	if c.busy.Load() {
		state = "busy"
	}
	c.calls = append(c.calls, id+" "+action+" "+state)
	return "", nil
}

func (c *ctlRecorder) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

// openccu-lite B-307: a start or restart of an addon's unit through the API waits while a radio
// change runs; other units and other actions do not.
func TestAddonStartWaitsForTheRadioChange(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	rec := &ctlRecorder{busy: &busy}
	mux := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t), Services: rec, Log: testLog, RadioBusy: busy.Load}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// not an addon start: at once, the change still running
	for _, p := range []string{"rfd/restart", "addon-hm2mqtt/stop"} {
		if st, out, _ := do(t, srv, "POST", "/api/system/v1/services/"+p, "", nil); st != 200 {
			t.Fatalf("%s: %d %v", p, st, out)
		}
	}
	done := make(chan int)
	go func() {
		st, _, _ := do(t, srv, "POST", "/api/system/v1/services/addon-hm2mqtt/restart", "", nil)
		done <- st
	}()
	select {
	case st := <-done:
		t.Fatalf("the addon restart did not wait: %d", st)
	case <-time.After(400 * time.Millisecond):
	}
	busy.Store(false)
	if st := <-done; st != 200 {
		t.Fatalf("the addon restart after the change: %d", st)
	}
	want := []string{"rfd restart busy", "addon-hm2mqtt stop busy", "addon-hm2mqtt restart idle"}
	got := rec.snapshot()
	if len(got) != len(want) {
		t.Fatalf("controls: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("controls: %v, want %v", got, want)
		}
	}
}
