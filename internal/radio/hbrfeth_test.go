package radio

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// the kernel module's files in a test root: the parameter, the class directory and its state
func hbRoot(t *testing.T, addr string, connected string) string {
	t.Helper()
	root := t.TempDir()
	for p, c := range map[string]string{
		"/sys/module/hb_rf_eth/parameters/connect": "",
		"/sys/class/hb-rf-eth/hb-rf-eth/connect":   "",
	} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte(c), 0o644)
	}
	if connected != "" {
		_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte(connected+"\n"), 0o644)
	}
	if addr != "" {
		_ = os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
		_ = os.WriteFile(filepath.Join(root, HBRFETHFile), []byte(addr+"\nsecond line\n"), 0o644)
	}
	return root
}

func hbDetector(root string, calls *[]string) Detector {
	return Detector{Root: root, Sleep: func(time.Duration) {}, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		*calls = append(*calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}}
}

func TestHBRFETHAddressAndState(t *testing.T) {
	root := hbRoot(t, " 192.0.2.10 ", "1")
	if a := HBRFETHAddress(root); a != "192.0.2.10" {
		t.Errorf("address %q", a)
	}
	if c, l := HBRFETHConnected(root); !c || !l {
		t.Errorf("state %v %v", c, l)
	}
	if c, l := HBRFETHConnected(t.TempDir()); c || l {
		t.Errorf("no module: %v %v", c, l)
	}
	if a := HBRFETHAddress(t.TempDir()); a != "" {
		t.Errorf("none configured: %q", a)
	}
}

// task 218: a board configured but not connected is tried again by the rescan; a new address lets
// the old one go first; a removed one is let go; a connected one is left alone.
func TestHBRFETHRescan(t *testing.T) {
	logf := func(string, ...any) {}
	// unreachable at boot: the address is written to the class file now
	var calls []string
	root := hbRoot(t, "192.0.2.10", "0")
	var det Detection
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10"}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "192.0.2.10" || !det.HBRFETHConnected || det.HBRFETH != "192.0.2.10" {
		t.Errorf("late board: %q %+v", b, det)
	}
	// connected: nothing is written
	root = hbRoot(t, "192.0.2.10", "1")
	det = Detection{}
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "" || !det.HBRFETHConnected {
		t.Errorf("connected board touched: %q %+v", b, det)
	}
	// a new address: the old one let go ("-" to the parameter, rmmod), the new one connected
	calls = nil
	root = hbRoot(t, "192.0.2.20", "1")
	det = Detection{}
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "192.0.2.20" || det.HBRFETH != "192.0.2.20" {
		t.Errorf("new address: %q %+v", b, det)
	}
	if !strings.Contains(strings.Join(calls, ";"), "rmmod hb-rf-eth") {
		t.Errorf("the old board was not let go: %v", calls)
	}
	// removed: let go
	calls = nil
	root = hbRoot(t, "", "1")
	det = Detection{}
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/module/hb_rf_eth/parameters/connect")); string(b) != "-" || det.HBRFETH != "" || det.HBRFETHConnected {
		t.Errorf("removed: %q %+v %v", b, det, calls)
	}
	// never configured: nothing
	calls = nil
	root = t.TempDir()
	det = Detection{}
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{}, &det, logf)
	if len(calls) != 0 || det.HBRFETH != "" {
		t.Errorf("nothing configured: %v %+v", calls, det)
	}
}

// The watch starts the hotplug while a configured board is not connected, and stops when it is.
func TestHBRFETHWatch(t *testing.T) {
	root := hbRoot(t, "192.0.2.10", "0")
	starts := make(chan struct{}, 10)
	w := &HBRFETHWatch{Root: root, Every: 20 * time.Millisecond, Start: func(context.Context) error { starts <- struct{}{}; return nil }}
	if st := w.Status(); !st.Retrying || st.Connected || st.Address != "192.0.2.10" || !st.ModuleLoaded {
		t.Fatalf("status %+v", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	w.Kick()
	select {
	case <-starts:
	case <-time.After(2 * time.Second):
		t.Fatal("no try")
	}
	_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte("1\n"), 0o644)
	time.Sleep(60 * time.Millisecond)
	for len(starts) > 0 {
		<-starts
	}
	time.Sleep(80 * time.Millisecond)
	if len(starts) != 0 {
		t.Error("tried while connected")
	}
	if st := w.Status(); st.Retrying || !st.Connected || st.Tries != 0 {
		t.Errorf("connected: %+v", st)
	}
	// removed while connected: one try lets it go
	_ = os.Remove(filepath.Join(root, HBRFETHFile))
	w.Kick()
	select {
	case <-starts:
	case <-time.After(2 * time.Second):
		t.Fatal("a removed board was not let go")
	}
}

// openccu-lite B-218: a board that connected once is the kernel's to reconnect - no hotplug while
// its link is lost; when it is back the daemons are asked after Verify, and only a No (or no answer
// three times) restarts them. A board that never connected here is tried as before.
func TestHBRFETHWatchLeavesALossToTheKernel(t *testing.T) {
	root := hbRoot(t, "192.0.2.10", "1")
	set := func(v string) { _ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte(v+"\n"), 0o644) }
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	var starts, restarts, asks int
	healthy, why := true, ""
	var askErr error
	w := &HBRFETHWatch{Root: root, Now: func() time.Time { return now }, Verify: 30 * time.Second, Grace: time.Hour,
		Start:   func(context.Context) error { starts++; return nil },
		Restart: func(context.Context) error { restarts++; return nil },
		Healthy: func(context.Context) (bool, string, error) { asks++; return healthy, why, askErr }}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tick := func(d time.Duration) { now = now.Add(d); w.step(context.Background(), log) }

	tick(0) // connected: seen
	set("0")
	tick(30 * time.Second)
	if st := w.Status(); starts != 0 || !st.Retrying || !st.Reconnecting || st.LostSince == nil {
		t.Fatalf("a loss started the hotplug (%d) or is not said: %+v", starts, st)
	}
	tick(2 * time.Minute)
	tick(30 * time.Minute)
	if starts != 0 {
		t.Fatalf("the hotplug started during a long loss: %d", starts)
	}
	// back: asked after Verify, not before; a Yes restarts nothing
	set("1")
	tick(10 * time.Second)
	if asks != 0 || w.Status().Reconnecting {
		t.Fatalf("asked at once (%d) or still reconnecting", asks)
	}
	tick(20 * time.Second)
	tick(30 * time.Second)
	if asks != 1 || restarts != 0 {
		t.Fatalf("after the reconnect: %d asks, %d restarts", asks, restarts)
	}
	// a loss after which the daemons do not have their module (a reset of the board): one restart
	set("0")
	tick(30 * time.Second)
	set("1")
	tick(30 * time.Second)
	healthy, why = false, "BidCos-RF: MEQ0835626 not connected"
	tick(30 * time.Second)
	tick(30 * time.Second)
	if restarts != 1 || w.Status().Restarts != 1 {
		t.Fatalf("restarts %d", restarts)
	}
	// daemons that do not answer: asked three times, then restarted
	set("0")
	tick(30 * time.Second)
	set("1")
	tick(30 * time.Second)
	healthy, askErr = true, errors.New("no answer within 5s")
	asks = 0
	for i := 0; i < 3; i++ {
		tick(30 * time.Second)
	}
	if asks != 3 || restarts != 2 {
		t.Fatalf("unanswered: %d asks, %d restarts", asks, restarts)
	}
	if starts != 0 {
		t.Fatalf("the hotplug started: %d", starts)
	}
	// the kernel module gone (unloaded): that is the hotplug's again
	_ = os.Remove(filepath.Join(root, HBRFETHConnectedFile))
	tick(30 * time.Second)
	if starts != 1 {
		t.Fatalf("no hotplug for an unloaded module: %d", starts)
	}
}

// A board that never connected at this address - unreachable at boot, a new address - is the
// hotplug's, every tick; the kick of a new address forgets the old one.
func TestHBRFETHWatchNeverConnected(t *testing.T) {
	root := hbRoot(t, "192.0.2.10", "0")
	now := time.Now()
	starts := 0
	w := &HBRFETHWatch{Root: root, Now: func() time.Time { return now }, Start: func(context.Context) error { starts++; return nil }}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	w.step(context.Background(), log)
	w.step(context.Background(), log)
	if starts != 2 || w.Status().Reconnecting {
		t.Fatalf("starts %d, %+v", starts, w.Status())
	}
	_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte("1\n"), 0o644)
	w.step(context.Background(), log)
	w.Kick() // a new address was saved
	_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte("0\n"), 0o644)
	w.step(context.Background(), log)
	if starts != 3 {
		t.Fatalf("a new address is not the kernel's yet: %d", starts)
	}
}

// B-218: a rescan while the kernel reconnects the board keeps its node and writes no address.
func TestHBRFETHRescanDuringReconnect(t *testing.T) {
	root := hbRoot(t, "192.0.2.10", "0")
	for p, c := range map[string]string{
		"sys/module/hb_rf_eth/parameters/autoreconnect": "1\n",
		"sys/class/raw-uart/raw-uart1/device_type":      "HB-RF-ETH@192.0.2.10\n",
		"sys/class/raw-uart/raw-uart1/connection_state": "0\n",
		"dev/raw-uart1": "",
	} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte(c), 0o644)
	}
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	var calls []string
	var det Detection
	d := hbDetector(root, &calls)
	d.hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "" || det.HBRFETH != "192.0.2.10" {
		t.Errorf("the address was written during the kernel's reconnect: %q %+v", b, det)
	}
	d.enumerate(context.Background(), &det, logf)
	if len(det.Modules) != 1 || det.Modules[0].Node != "/dev/raw-uart1" || det.Modules[0].DeviceType != "HB-RF-ETH@192.0.2.10" {
		t.Errorf("the node was dropped: %+v %v", det.Modules, lines)
	}
	// autoreconnect off: the old way - the address is written, the node taken for gone
	_ = os.WriteFile(filepath.Join(root, "sys/module/hb_rf_eth/parameters/autoreconnect"), []byte("0\n"), 0o644)
	det = Detection{}
	d.hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, logf)
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "192.0.2.10" {
		t.Errorf("without autoreconnect the address is written: %q", b)
	}
	det = Detection{}
	d.enumerate(context.Background(), &det, logf)
	if len(det.Modules) != 0 {
		t.Errorf("without autoreconnect the node stays: %+v", det.Modules)
	}
}

// After a real power cycle the kernel's reconnect never took the board back (the lab, 14:43): a loss
// longer than Grace is connected afresh by the hotplug, with the note for the rescan.
func TestHBRFETHWatchGraceAfterPowerCycle(t *testing.T) {
	root := hbRoot(t, "192.0.2.10", "1")
	_ = os.MkdirAll(filepath.Join(root, filepath.Dir(HBRFETHReconnectFile)), 0o755)
	now := time.Date(2026, 9, 25, 14, 43, 0, 0, time.UTC)
	starts := 0
	w := &HBRFETHWatch{Root: root, Now: func() time.Time { return now }, Grace: 45 * time.Second, Start: func(context.Context) error { starts++; return nil }}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tick := func(d time.Duration) { now = now.Add(d); w.step(context.Background(), log) }
	tick(0)
	_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte("0\n"), 0o644)
	tick(30 * time.Second)
	tick(30 * time.Second) // 30 s lost: still the kernel's
	if starts != 0 {
		t.Fatalf("started within the grace: %d", starts)
	}
	tick(30 * time.Second) // 60 s lost
	if starts != 1 {
		t.Fatalf("no fresh connect after the grace: %d", starts)
	}
	if b, _ := os.ReadFile(filepath.Join(root, HBRFETHReconnectFile)); strings.TrimSpace(string(b)) != "192.0.2.10" {
		t.Errorf("note %q", b)
	}
	// the rescan takes the note: the address is written afresh, and the note is gone
	for p, c := range map[string]string{
		"sys/module/hb_rf_eth/parameters/autoreconnect": "1\n",
		"sys/class/raw-uart/raw-uart1/device_type":      "HB-RF-ETH@192.0.2.10\n",
		"sys/class/raw-uart/raw-uart1/connection_state": "0\n",
	} {
		_ = os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte(c), 0o644)
	}
	var calls []string
	var det Detection
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, func(string, ...any) {})
	if b, _ := os.ReadFile(filepath.Join(root, "sys/class/hb-rf-eth/hb-rf-eth/connect")); string(b) != "192.0.2.10" {
		t.Errorf("not connected afresh: %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, HBRFETHReconnectFile)); err == nil {
		t.Error("the note stayed")
	}
	// connected by that hotplug: the daemons are asked after Verify, as after the kernel's reconnect
	asks := 0
	w.Healthy = func(context.Context) (bool, string, error) { asks++; return true, "", nil }
	_ = os.WriteFile(filepath.Join(root, HBRFETHConnectedFile), []byte("1\n"), 0o644)
	tick(5 * time.Second)
	tick(30 * time.Second)
	if asks != 1 {
		t.Errorf("not asked after the hotplug's connect: %d", asks)
	}
	// a note left for a udev hotplug with the board connected is taken away all the same
	_ = os.WriteFile(filepath.Join(root, HBRFETHReconnectFile), []byte("192.0.2.10\n"), 0o644)
	det = Detection{}
	hbDetector(root, &calls).hbRFETHRescan(context.Background(), Detection{HBRFETH: "192.0.2.10", HBRFETHConnected: true}, &det, func(string, ...any) {})
	if _, err := os.Stat(filepath.Join(root, HBRFETHReconnectFile)); err == nil {
		t.Error("the note stayed on a connected board")
	}
}
