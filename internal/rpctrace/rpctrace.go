// Package rpctrace is the RPC trace (task 79, D-57, D-79, D-115): every call between a remote
// client or occulited and an interface process, every stream delivery and the stream's life,
// as lines in the journal under SYSLOG_IDENTIFIER=rpc-trace at priority debug - so
// `journalctl -t rpc-trace` works on the box and the Log page shows them as a tag. The journal's
// rate limit is the safety valve.
//
// The switch is off, on for a while, or on permanently. A timed trace switches itself off; the
// deadline is stored (occulited.json), so a restart keeps it. Lines are capped at 8 kB and a
// cut line says how many bytes went. Never in a line: a token itself, an Authorization header -
// the callers hand over a token's name and nothing else.
package rpctrace

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Modes of the switch.
const (
	ModeOff       = "off"
	ModeUntil     = "until"
	ModePermanent = "permanent"
)

// DefaultCap is the line cap: 8 kB (D-79).
const DefaultCap = 8 * 1024

// Store persists the switch: the mode and, for a timed trace, its deadline.
type Store interface {
	ReadTrace() (mode string, until time.Time)
	WriteTrace(mode string, until time.Time) error
}

// State is the switch as the API shows it.
type State struct {
	Mode  string     `json:"mode"`
	Until *time.Time `json:"until,omitempty"`
	On    bool       `json:"on"`
}

// Tracer writes the lines and keeps the switch.
type Tracer struct {
	log   *slog.Logger
	store Store
	now   func() time.Time
	cap   int

	mu    sync.Mutex
	mode  string
	until time.Time
	timer *time.Timer
	cut   uint64 // lines truncated, for the tests and the journal's own line at switch-off
	lines uint64
}

// Options configure a Tracer.
type Options struct {
	// Log takes the lines at debug; its handler carries the identifier rpc-trace.
	Log *slog.Logger
	// Store keeps the switch; nil = memory only.
	Store Store
	Now   func() time.Time
	// Cap is the line cap in bytes; 0 = DefaultCap.
	Cap int
}

// New makes a tracer and restores the switch: a timed trace whose deadline passed while
// occulited was down is off.
func New(o Options) *Tracer {
	t := &Tracer{log: o.Log, store: o.Store, now: o.Now, cap: o.Cap, mode: ModeOff}
	if t.log == nil {
		t.log = slog.Default()
	}
	if t.now == nil {
		t.now = time.Now
	}
	if t.cap == 0 {
		t.cap = DefaultCap
	}
	if t.store != nil {
		mode, until := t.store.ReadTrace()
		t.apply(mode, until, false)
	}
	return t
}

// State is the switch.
func (t *Tracer) State() State {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := State{Mode: t.mode, On: t.onLocked()}
	if t.mode == ModeUntil {
		u := t.until
		s.Until = &u
	}
	return s
}

// On says whether lines are written now.
func (t *Tracer) On() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.onLocked()
}

func (t *Tracer) onLocked() bool {
	switch t.mode {
	case ModePermanent:
		return true
	case ModeUntil:
		return t.now().Before(t.until)
	}
	return false
}

// Set switches: off, on for d (ModeUntil), or on permanently. It is stored, and a timed trace
// ends on its own.
func (t *Tracer) Set(mode string, d time.Duration) error {
	var until time.Time
	switch mode {
	case ModeOff, ModePermanent:
	case ModeUntil:
		if d <= 0 {
			return fmt.Errorf("a timed trace needs a duration")
		}
		until = t.now().Add(d)
	default:
		return fmt.Errorf("mode is off, until or permanent")
	}
	t.apply(mode, until, true)
	if t.store != nil {
		return t.store.WriteTrace(mode, until)
	}
	return nil
}

// apply sets the switch and arms or disarms the timer; say writes the journal's own line.
func (t *Tracer) apply(mode string, until time.Time, say bool) {
	t.mu.Lock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if mode == ModeUntil && !t.now().Before(until) {
		mode, until = ModeOff, time.Time{}
	}
	if mode != ModeUntil && mode != ModePermanent {
		mode, until = ModeOff, time.Time{}
	}
	t.mode, t.until = mode, until
	if mode == ModeUntil {
		t.timer = time.AfterFunc(until.Sub(t.now()), t.expire)
	}
	if mode == ModeOff {
		t.lines, t.cut = 0, 0
	}
	t.mu.Unlock()
	if say {
		switch mode {
		case ModeOff:
			t.log.Info("rpc trace: off")
		case ModeUntil:
			t.log.Info("rpc trace: on", "until", until.Format(time.RFC3339))
		case ModePermanent:
			t.log.Info("rpc trace: on permanently")
		}
	}
}

// expire is the timer's: the timed trace ends, and the store forgets the deadline.
func (t *Tracer) expire() {
	t.mu.Lock()
	if t.mode != ModeUntil || t.now().Before(t.until) {
		t.mu.Unlock()
		return
	}
	lines := t.lines
	t.mode, t.until, t.timer = ModeOff, time.Time{}, nil
	t.mu.Unlock()
	t.log.Info("rpc trace: the timed trace ended", "lines", lines)
	if t.store != nil {
		_ = t.store.WriteTrace(ModeOff, time.Time{})
	}
}

// Line writes one line when the trace is on, capped.
func (t *Tracer) Line(line string) {
	if !t.On() {
		return
	}
	if len(line) > t.cap {
		cut := len(line) - t.cap
		line = line[:t.cap] + fmt.Sprintf(" …[%d bytes cut]", cut)
		t.mu.Lock()
		t.cut++
		t.mu.Unlock()
	}
	t.mu.Lock()
	t.lines++
	t.mu.Unlock()
	t.log.Debug(line)
}

// Linef is Line with a format.
func (t *Tracer) Linef(format string, args ...any) {
	if !t.On() {
		return
	}
	t.Line(fmt.Sprintf(format, args...))
}

// Lines and Cut count what was written since the switch went on (tests).
func (t *Tracer) Lines() (lines, cut uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lines, t.cut
}

// Value renders a parameter or a result for a line: JSON, compact, on one line; what cannot be
// marshalled as %v.
func Value(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return strings.ReplaceAll(fmt.Sprint(v), "\n", "\\n")
	}
	return string(b)
}

// Tracing is what the request paths and the streams call: nil-safe, so a caller without a
// tracer writes nothing.
type Tracing interface {
	On() bool
	Line(string)
}
