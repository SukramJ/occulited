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

// fwGateSvc answers like the radio units' ExecCondition (lite-radio-gate) around the flash's fake
// service manager: a start of a radio unit the marker holds is skipped; every stop and start is
// recorded with the gate's state for that unit.
type fwGateSvc struct {
	*fakeRadioSvc
	mu      sync.Mutex
	events  []string
	skipped map[string]bool
}

func (g *fwGateSvc) gateFor(unit string) string {
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

func (g *fwGateSvc) Control(ctx context.Context, id, action string) (string, error) {
	state := g.gateFor(id)
	g.mu.Lock()
	if containsUnit(radio.Daemons, id) && action == "start" && state == "held" {
		g.events = append(g.events, id+" start skipped")
		g.skipped[id] = true
		g.mu.Unlock()
		return "", nil
	}
	if containsUnit(radio.Daemons, id) {
		g.events = append(g.events, id+" "+action+" "+state)
	}
	delete(g.skipped, id)
	g.mu.Unlock()
	return g.fakeRadioSvc.Control(ctx, id, action)
}

func (g *fwGateSvc) List() ([]Service, error) {
	list, err := g.fakeRadioSvc.List()
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range list {
		list[i].Skipped = !list[i].Running && g.skipped[list[i].ID]
	}
	return list, err
}

func (g *fwGateSvc) snapshotEvents() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func newGatedFW(t *testing.T, fp *fakeCopro) (*RadioFirmware, *fwGateSvc, string) {
	t.Helper()
	root := piRoot(t)
	_ = os.MkdirAll(filepath.Join(root, radio.RunDir), 0o755)
	s, svc := newRadioFW(t, root, fp)
	g := &fwGateSvc{fakeRadioSvc: svc, skipped: map[string]bool{}}
	s.Services = g
	return s, g, root
}

// openccu-lite B-308: the flash holds the radio units like a connection change - an outside start
// while the module is being flashed is skipped (a user's systemctl, a Restart=), the flash's own
// starts afterwards are let through one by one, hs485d skipped meanwhile is started again when the
// plan runs it, and the gate is open at the end.
func TestRadioFirmwareFlashGatesTheRadioUnits(t *testing.T) {
	fp := &fakeCopro{version: "4.4.18", flashSets: "4.4.18"}
	s, g, root := newGatedFW(t, fp)
	_ = os.WriteFile(filepath.Join(root, radio.RunDir, "hs485d.enabled"), []byte("NORMAL\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "proc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/uptime"), []byte("1000.5 1.0\n"), 0o444)
	var marker string
	fp.onFlash = func() {
		b, _ := os.ReadFile(radio.GatePath(root))
		marker = string(b)
		// mid-flash: someone starts hmipserver, and hs485d's Restart= comes
		go func() {
			_, _ = g.Control(context.Background(), "hmipserver", "start")
			_, _ = g.Control(context.Background(), "hs485d", "start")
		}()
		deadline := time.Now().Add(2 * time.Second)
		for len(g.snapshotEvents()) < 5 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.Flash("HMIP-RFUSB", "dualcopro_update_blhmip-4.4.18.eq3"); err != nil {
		t.Fatal(err)
	}
	st := waitDone(t, s)
	if st.Last == nil || !st.Last.OK {
		t.Fatalf("the flash failed: %+v", st.Last)
	}
	// 1000.5 s of uptime + 15 min + 1 min, rounded up
	if marker != "1961\n" {
		t.Fatalf("marker during the flash: %q", marker)
	}
	want := []string{
		"hmipserver stop held", "rfd stop held", "multimacd stop held",
		"hmipserver start skipped", "hs485d start skipped",
		"multimacd start released", "rfd start released", "hmipserver start released",
		"hs485d start open",
	}
	if got := g.snapshotEvents(); !reflect.DeepEqual(got, want) {
		t.Fatalf("units:\n%v\nwant\n%v", got, want)
	}
	if _, err := os.Stat(radio.GatePath(root)); err == nil {
		t.Fatal("the gate is left closed")
	}
}

// A flash that fails - the flasher cannot run - starts the stack through the gate and opens it.
func TestRadioFirmwareFlashGateOpenAfterAFailure(t *testing.T) {
	fp := &fakeCopro{version: "4.4.18", flashErr: errors.New("no flasher")}
	s, g, root := newGatedFW(t, fp)
	if err := s.Flash("HMIP-RFUSB", "dualcopro_update_blhmip-4.4.18.eq3"); err != nil {
		t.Fatal(err)
	}
	if st := waitDone(t, s); st.Last == nil || st.Last.OK {
		t.Fatalf("%+v", st.Last)
	}
	ev := g.snapshotEvents()
	if len(ev) != 6 || ev[3] != "multimacd start released" || ev[5] != "hmipserver start released" {
		t.Fatalf("units after a failed flash: %v", ev)
	}
	if _, err := os.Stat(radio.GatePath(root)); err == nil {
		t.Fatal("the gate is left closed after a failure")
	}
}

// A flash cut off by a restart of occulited leaves its marker; the start removes it before it starts
// the stack. Without an interrupted flash a marker (a connection change's, the hotplug's) stays.
func TestRadioFirmwareGateOpenedByLoad(t *testing.T) {
	s, g, root := newGatedFW(t, &fakeCopro{version: "4.4.18"})
	if err := radio.CloseGate(root, time.Hour); err != nil {
		t.Fatal(err)
	}
	s.Load(context.Background())
	if _, err := os.Stat(radio.GatePath(root)); err != nil {
		t.Fatal("Load removed a gate no interrupted flash left")
	}
	_ = os.MkdirAll(s.StateDir, 0o755)
	_ = writeJSONFile(filepath.Join(s.StateDir, "running.json"), &FlashAttempt{Module: "HMIP-RFUSB"})
	s.Load(context.Background())
	if _, err := os.Stat(radio.GatePath(root)); err == nil {
		t.Fatal("the interrupted flash's gate is still closed")
	}
	if ev := g.snapshotEvents(); !reflect.DeepEqual(ev, []string{"multimacd start open", "rfd start open", "hmipserver start open"}) {
		t.Fatalf("units: %v", ev)
	}
}
