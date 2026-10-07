package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// radioGateSvc emulates the radio units' gate (openccu-lite B-307) around connSvc: a start of a radio
// unit while /run/occulite/radio/changing is there is skipped, as lite-radio-gate's ExecCondition=
// does it, and every stop and start is recorded with the gate's state at that moment.
type radioGateSvc struct {
	*connSvc
	root    string
	mu      sync.Mutex
	events  []string
	skipped map[string]bool // units the List reports skipped (their last start was)
	onStart func(unit string)
}

var radioGatedUnits = []string{"multimacd", "rfd", "hmipserver", "hs485d", "hmlangw"}

func (g *radioGateSvc) held() bool {
	_, err := os.Stat(radio.GatePath(g.root))
	return err == nil
}

// gateFor is what lite-radio-gate answers for the unit: open (no marker), released (its name in
// the marker) or held.
func (g *radioGateSvc) gateFor(unit string) string {
	b, err := os.ReadFile(radio.GatePath(g.root))
	if err != nil {
		return "open"
	}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n")[1:] {
		if l == unit {
			return "released"
		}
	}
	return "held"
}

func (g *radioGateSvc) record(e string) {
	g.mu.Lock()
	g.events = append(g.events, e)
	g.mu.Unlock()
}

func (g *radioGateSvc) Control(ctx context.Context, id, action string) (string, error) {
	if containsUnit(radioGatedUnits, id) && action == "start" && g.gateFor(id) == "held" {
		g.record(id + " start skipped")
		g.mu.Lock()
		g.skipped[id] = true
		g.mu.Unlock()
		return "", nil
	}
	if containsUnit(radioGatedUnits, id) {
		g.record(id + " " + action + " " + g.gateFor(id))
	}
	if g.onStart != nil && action == "start" {
		g.onStart(id)
	}
	g.mu.Lock()
	delete(g.skipped, id)
	g.mu.Unlock()
	return g.connSvc.Control(ctx, id, action)
}

func (g *radioGateSvc) List() ([]Service, error) {
	list, err := g.connSvc.List()
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range list {
		list[i].Skipped = !list[i].Running && g.skipped[list[i].ID]
	}
	return list, err
}

func (g *radioGateSvc) snapshotEvents() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func newGatedConn(t *testing.T) (*RadioConnections, *radioGateSvc, string) {
	t.Helper()
	root := charlyRoot(t)
	s, svc := newConn(t, root, nil)
	g := &radioGateSvc{connSvc: svc, root: root, skipped: map[string]bool{}}
	s.Services = g
	return s, g, root
}

// openccu-lite B-307: a start from outside the change (an addon's Wants=, the supervisor) between
// the stop and the new plan is held back; the change's own starts after the plan go through, and
// the gate is open again at the end.
func TestRadioConnGateHoldsOutsideStarts(t *testing.T) {
	s, g, _ := newGatedConn(t)
	detect := g.onDetect
	g.onDetect = func() {
		// an addon restarted mid-change pulls hmipserver and rfd in, the old plan still written
		_, _ = g.Control(context.Background(), "hmipserver", "start")
		_, _ = g.Control(context.Background(), "rfd", "start")
		detect()
	}
	// and once the change has started multimacd, another restart pulls hmipserver in before the
	// change got to it: held still - the units are let through in boot order
	once := false
	g.onStart = func(unit string) {
		if unit == "multimacd" && !once {
			once = true
			_, _ = g.Control(context.Background(), "hmipserver", "start")
		}
	}
	// HmIP only: BidCos-RF off
	if _, err := s.Apply(context.Background(), radio.Choices{BidCos: radio.BidCosNone}, true); err != nil {
		t.Fatal(err)
	}
	a := waitApply(t, s)
	if !a.OK {
		t.Fatalf("the change failed: %+v", a)
	}
	want := []string{
		"hmipserver stop held", "rfd stop held", "multimacd stop held",
		"hmipserver start skipped", "rfd start skipped",
		"multimacd start released", "hmipserver start skipped",
		"rfd start released", "hmipserver start released",
	}
	if got := g.snapshotEvents(); !reflect.DeepEqual(got, want) {
		t.Fatalf("units:\n%v\nwant\n%v", got, want)
	}
	if g.held() {
		t.Fatal("the gate is left closed")
	}
	lines := strings.Join(a.Lines, "\n")
	for _, l := range []string{"held against starts from outside the change", "the change's starts are done: radio units may start from outside it again"} {
		if !strings.Contains(lines, l) {
			t.Errorf("no line %q in\n%s", l, lines)
		}
	}
}

// The marker's content while the change holds the units: the deadline in seconds since boot.
func TestRadioConnGateDeadline(t *testing.T) {
	s, g, root := newGatedConn(t)
	_ = os.MkdirAll(filepath.Join(root, "proc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/uptime"), []byte("500.70 1.00\n"), 0o444)
	var content string
	detect := g.onDetect
	g.onDetect = func() {
		b, _ := os.ReadFile(radio.GatePath(root))
		content = string(b)
		detect()
	}
	if _, err := s.Apply(context.Background(), radio.Choices{BidCos: radio.BidCosNone}, true); err != nil {
		t.Fatal(err)
	}
	if a := waitApply(t, s); !a.OK {
		t.Fatalf("%+v", a)
	}
	// 500.7 s + 5 s (the test's Timeout) + 60 s, rounded up
	if content != "566\n" {
		t.Fatalf("marker during the change: %q", content)
	}
}

// A change that fails opens the gate before it starts the stack again, and leaves no marker.
func TestRadioConnGateOpenAfterAFailure(t *testing.T) {
	s, g, _ := newGatedConn(t)
	g.failing = RadioDetectionUnit
	if _, err := s.Apply(context.Background(), radio.Choices{HmIP: "0000000A03"}, false); err != nil {
		t.Fatal(err)
	}
	if a := waitApply(t, s); a.OK {
		t.Fatalf("%+v", a)
	}
	ev := g.snapshotEvents()
	if len(ev) != 6 || ev[3] != "multimacd start released" || ev[5] != "hmipserver start released" {
		t.Fatalf("units after a failed run: %v", ev)
	}
	if g.held() {
		t.Fatal("the gate is left closed after a failure")
	}
}

// A change cut off by a restart of occulited leaves its marker; the start of occulited removes it
// before it starts the stack.
func TestRadioConnGateOpenedByLoad(t *testing.T) {
	s, g, root := newGatedConn(t)
	if err := radio.CloseGate(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	_ = writeJSONFile(filepath.Join(s.StateDir, "running.json"), &ConnApply{Choices: radio.Choices{BidCos: radio.BidCosNone}, Started: time.Now()})
	s.Load(context.Background())
	if g.held() {
		t.Fatal("the interrupted change's gate is still closed")
	}
	if ev := g.snapshotEvents(); !reflect.DeepEqual(ev, []string{"multimacd start open", "rfd start open", "hmipserver start open"}) {
		t.Fatalf("units: %v", ev)
	}
	// no change was running: a marker that is there (the hotplug's) is not touched
	if err := radio.CloseGate(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	s.Load(context.Background())
	if !g.held() {
		t.Fatal("Load removed a gate no interrupted change of its own left")
	}
}

// hs485d is not stopped by the change, but held all the same: one whose Restart= the gate refused
// is started after the new plan when the plan runs it; one the plan does not run, or that runs,
// is left alone.
func TestRadioConnStartsWhatTheGateHeldBack(t *testing.T) {
	s, g, root := newGatedConn(t)
	enabled := filepath.Join(root, "run/occulite/radio/hs485d.enabled")
	_ = os.WriteFile(enabled, []byte("NORMAL\n"), 0o644)
	detect := g.onDetect
	g.onDetect = func() {
		// hs485d ended and systemd's Restart= tries it during the change
		_, _ = g.Control(context.Background(), "hs485d", "start")
		detect()
	}
	if _, err := s.Apply(context.Background(), radio.Choices{BidCos: radio.BidCosNone}, true); err != nil {
		t.Fatal(err)
	}
	a := waitApply(t, s)
	ev := g.snapshotEvents()
	if !a.OK || ev[len(ev)-1] != "hs485d start open" || !strings.Contains(strings.Join(a.Lines, "\n"), "hs485d started: its start during the change was held back") {
		t.Fatalf("hs485d after the change: %v\n%v", ev, a.Lines)
	}
	// not in the plan: stays off
	_ = os.Remove(enabled)
	g.mu.Lock()
	g.events = nil
	g.mu.Unlock()
	if _, err := s.Apply(context.Background(), radio.Choices{}, false); err != nil {
		t.Fatal(err)
	}
	if a = waitApply(t, s); !a.OK {
		t.Fatalf("%+v", a)
	}
	for _, e := range g.snapshotEvents() {
		if e == "hs485d start open" {
			t.Fatalf("hs485d started without its plan: %v", g.snapshotEvents())
		}
	}
}

// openccu-lite B-307, the other half: the addon supervisor does not restart an addon while a
// change runs (its restart would pull the stopped radio daemons in), and does afterwards.
func TestSupervisorWaitsForTheRadioChange(t *testing.T) {
	s, g, _ := newGatedConn(t)
	release := make(chan struct{})
	inChange := make(chan struct{})
	detect := g.onDetect
	g.onDetect = func() {
		close(inChange)
		<-release
		detect()
	}
	f := &fakeUnits{boot: time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)}
	f.now = f.boot.Add(time.Hour)
	running := map[string]bool{}
	var mu sync.Mutex
	var restarted []string
	c := f.watcher()
	c.Supervised = func() []string { return []string{"hm2mqtt"} }
	c.Procs = func(cg string) bool { return running[cg] }
	c.Restart = func(_ context.Context, unit string) error {
		mu.Lock()
		restarted = append(restarted, unit)
		mu.Unlock()
		return nil
	}
	c.Paused = AnyBusy(func() bool { return false }, s.Busy) // main.go: the addon jobs, the radio
	const unit = "addon-hm2mqtt.service"
	cg := "/system.slice/" + unit
	f.set(unit, "active", "exited", f.boot.Add(time.Minute), 0, "success")
	running[cg] = true
	c.Sample(context.Background())

	if _, err := s.Apply(context.Background(), radio.Choices{BidCos: radio.BidCosNone}, true); err != nil {
		t.Fatal(err)
	}
	<-inChange
	// the addon's daemon ends while rfd is gone; minutes pass in the change
	running[cg] = false
	for i := 0; i < 8; i++ {
		c.Sample(context.Background())
		f.now = f.now.Add(15 * time.Second)
	}
	mu.Lock()
	during := len(restarted)
	mu.Unlock()
	if during != 0 {
		t.Fatalf("restarted during the radio change: %v", restarted)
	}
	close(release)
	if a := waitApply(t, s); !a.OK {
		t.Fatalf("%+v", a)
	}
	// afterwards: the first sample schedules, the backoff's end restarts
	c.Sample(context.Background())
	f.now = f.now.Add(2 * time.Second)
	c.Sample(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(restarted, []string{"addon-hm2mqtt"}) {
		t.Fatalf("after the change: %v", restarted)
	}
}

func TestWaitRadioIdle(t *testing.T) {
	old := radioIdlePoll
	radioIdlePoll = time.Millisecond
	t.Cleanup(func() { radioIdlePoll = old })
	if w, err := WaitRadioIdle(context.Background(), nil); w != 0 || err != nil {
		t.Fatalf("nil: %v %v", w, err)
	}
	var mu sync.Mutex
	busy := true
	go func() {
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		busy = false
		mu.Unlock()
	}()
	b := func() bool { mu.Lock(); defer mu.Unlock(); return busy }
	if w, err := WaitRadioIdle(context.Background(), b); err != nil || w < 15*time.Millisecond {
		t.Fatalf("waited %v %v", w, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := WaitRadioIdle(ctx, func() bool { return true }); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cancelled wait: %v", err)
	}
	if AnyBusy(nil, func() bool { return false })() || !AnyBusy(nil, func() bool { return true })() || AnyBusy()() {
		t.Fatal("AnyBusy")
	}
}

func TestAddonStartAction(t *testing.T) {
	for _, c := range []struct {
		unit, action string
		want         bool
	}{
		{"addon-hmm", "start", true},
		{"addon-hmm.service", "restart", true},
		{"addon-hmm", "try-restart", true},
		{"addon-hmm", "stop", false},
		{"addon-hmm", "enable", false},
		{"rfd", "start", false},
		{"hmipserver", "restart", false},
	} {
		if got := AddonStartAction(c.unit, c.action); got != c.want {
			t.Errorf("AddonStartAction(%q, %q) = %v", c.unit, c.action, got)
		}
	}
}
