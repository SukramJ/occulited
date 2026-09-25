package rpctrace

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// a handler that records the lines
type rec struct {
	mu    sync.Mutex
	lines []string
}

func (r *rec) Enabled(context.Context, slog.Level) bool { return true }
func (r *rec) Handle(_ context.Context, rr slog.Record) error {
	r.mu.Lock()
	r.lines = append(r.lines, rr.Level.String()+" "+rr.Message)
	r.mu.Unlock()
	return nil
}
func (r *rec) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *rec) WithGroup(string) slog.Handler      { return r }
func (r *rec) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lines...)
}

type memStore struct {
	mu    sync.Mutex
	mode  string
	until time.Time
	n     int
}

func (m *memStore) ReadTrace() (string, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mode, m.until
}
func (m *memStore) WriteTrace(mode string, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode, m.until, m.n = mode, until, m.n+1
	return nil
}
func (m *memStore) get() (string, time.Time) { return m.ReadTrace() }

func TestSwitchTimerAndCap(t *testing.T) {
	r := &rec{}
	st := &memStore{}
	now := time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	tr := New(Options{Log: slog.New(r), Store: st, Now: clock, Cap: 64})
	if tr.On() || tr.State().Mode != ModeOff {
		t.Fatal("on at start")
	}
	tr.Line("not written")
	if err := tr.Set("wizard", 0); err == nil {
		t.Fatal("a bogus mode")
	}
	if err := tr.Set(ModeUntil, 0); err == nil {
		t.Fatal("a timed trace without a duration")
	}
	// on for a while: written, stored with the deadline, capped with the cut size
	if err := tr.Set(ModeUntil, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if m, u := st.get(); !tr.On() || m != ModeUntil || u.IsZero() || tr.State().Until == nil {
		t.Fatalf("on: %v %s %v", tr.On(), m, u)
	}
	tr.Line("xmlrpc a → b getValue [1]")
	tr.Line(strings.Repeat("x", 100))
	lines := r.get()
	if len(lines) != 3 || !strings.HasSuffix(lines[1], "getValue [1]") || !strings.HasPrefix(lines[1], "DEBUG ") {
		t.Fatalf("lines %q", lines)
	}
	if !strings.HasSuffix(lines[2], strings.Repeat("x", 64)+" …[36 bytes cut]") {
		t.Fatalf("cap %q", lines[2])
	}
	if n, cut := tr.Lines(); n != 2 || cut != 1 {
		t.Fatalf("counts %d %d", n, cut)
	}
	// the timer ends it (the real clock drives the timer; the fake clock is moved past the deadline)
	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m, _ := st.get(); !tr.On() && m == ModeOff {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if m, u := st.get(); tr.On() || m != ModeOff || !u.IsZero() {
		t.Fatalf("after the deadline: on=%v store=%s %v", tr.On(), m, u)
	}
	if last := r.get()[len(r.get())-1]; !strings.Contains(last, "timed trace ended") {
		t.Fatalf("no end line: %q", last)
	}
	// permanent: on, no deadline
	if err := tr.Set(ModePermanent, 0); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.get(); !tr.On() || tr.State().Until != nil || m != ModePermanent {
		t.Fatal("permanent")
	}
	if err := tr.Set(ModeOff, 0); err != nil || tr.On() {
		t.Fatal("off")
	}
}

func TestRestoreFromStore(t *testing.T) {
	r := &rec{}
	now := time.Date(2026, 9, 22, 16, 0, 0, 0, time.UTC)
	// a deadline still ahead: restored as on, with its timer
	tr := New(Options{Log: slog.New(r), Store: &memStore{mode: ModeUntil, until: now.Add(time.Hour)}, Now: func() time.Time { return now }})
	if !tr.On() || tr.State().Mode != ModeUntil || !tr.State().Until.Equal(now.Add(time.Hour)) {
		t.Fatalf("restored: %+v", tr.State())
	}
	// a deadline that passed while occulited was down: off
	tr2 := New(Options{Log: slog.New(r), Store: &memStore{mode: ModeUntil, until: now.Add(-time.Hour)}, Now: func() time.Time { return now }})
	if tr2.On() || tr2.State().Mode != ModeOff {
		t.Fatalf("stale: %+v", tr2.State())
	}
	tr3 := New(Options{Log: slog.New(r), Store: &memStore{mode: ModePermanent}})
	if !tr3.On() {
		t.Fatal("permanent not restored")
	}
	if Value(map[string]any{"a": 1}) != `{"a":1}` || Value("x\ny") != `"x\ny"` {
		t.Fatal("Value")
	}
}
