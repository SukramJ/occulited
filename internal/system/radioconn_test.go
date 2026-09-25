package system

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

const connRFDConf = "# TCP Port for XmlRpc connections\nListen IP = 127.0.0.1\nListen Port = 32001\n\nLog Destination = Syslog\nKey File = /etc/config/keys\nImproved Coprocessor Initialization = true\n\n[Interface 0]\nType = CCU2\nComPortFile = /dev/mmd_bidcos\nAccessFile = /dev/null\nResetFile = /dev/null\n"

// charlyRoot is the Charly as the service sees it after a boot: the RPI-RF-MOD on the header
// serving both stacks, the run's modules.json and plan.json under /run/occulite/radio.
func charlyRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	w := func(p, c string) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mod := radio.Module{Name: "raw-uart", Node: "/dev/raw-uart", DeviceType: "GPIO@3f201000.serial", GPIO: true, Hardware: "RPI-RF-MOD", Serial: "58A9A728D4", SGTIN: "3014F711A0001F58A9A728D4", HmRFAddress: "0x1F6C2E", HmIPAddress: "0x3FAE2C", Version: "4.4.22", Probe: "ok"}
	det := radio.Detection{Modules: []radio.Module{mod}}
	w("var/hm_mode", "HM_HOST='rpi3'\nHM_MODE='NORMAL'\n")
	w("etc/config/rfd.conf", connRFDConf)
	w("etc/config_templates/rfd.conf", connRFDConf)
	w("etc/config_templates/InterfacesList.xml", "<interfaces><ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url><info>BidCos-RF</info></ipc><ipc><name>VirtualDevices</name><url>xmlrpc://127.0.0.1:39292/groups</url><info>Virtual Devices</info></ipc><ipc><name>HmIP-RF</name><url>xmlrpc://127.0.0.1:32010</url><info>HmIP-RF</info></ipc></interfaces>")
	b, _ := json.Marshal(det)
	w("run/occulite/radio/modules.json", string(b))
	in := radio.Load(context.Background(), root, fakeRunner, det)
	b, _ = json.Marshal(radio.MakePlan(in))
	w("run/occulite/radio/plan.json", string(b))
	return root
}

func fakeRunner(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == "uname" {
		return []byte("aarch64\n"), nil
	}
	return []byte("ccu\n"), nil
}

// connSvc records the unit calls; the detection unit's restart re-plans the sandbox as the real
// `occulited radio run` would, from the files the change wrote.
type connSvc struct {
	fakeRadioSvc
	onDetect func()
}

func (c *connSvc) Control(ctx context.Context, id, action string) (string, error) {
	if id == RadioDetectionUnit && action == "restart" && c.onDetect != nil {
		c.onDetect()
	}
	return c.fakeRadioSvc.Control(ctx, id, action)
}

func newConn(t *testing.T, root string, devices []BidCosDevice) (*RadioConnections, *connSvc) {
	t.Helper()
	svc := &connSvc{fakeRadioSvc: fakeRadioSvc{root: root, running: map[string]bool{"multimacd": true, "rfd": true, "hmipserver": true}}}
	s := &RadioConnections{Root: Root(root), Services: svc, Systemd: true, Run: fakeRunner, StateDir: filepath.Join(root, "state/radio-connections"), Timeout: 5 * time.Second}
	s.BidCosDevices = func(context.Context) ([]BidCosDevice, error) { return devices, nil }
	svc.onDetect = func() {
		det, _ := s.detection()
		in := radio.Load(context.Background(), root, fakeRunner, det)
		b, _ := json.Marshal(radio.MakePlan(in))
		_ = os.WriteFile(filepath.Join(root, "run/occulite/radio/plan.json"), b, 0o644)
	}
	return s, svc
}

func waitApply(t *testing.T, s *RadioConnections) *ConnApply {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Status(); st.Running == nil && st.Last != nil {
			return st.Last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the change did not finish")
	return nil
}

func TestRadioConnStatus(t *testing.T) {
	root := charlyRoot(t)
	s, _ := newConn(t, root, nil)
	st := s.Status()
	if !st.Available || st.Choices.Explicit() || st.Plan == nil || !st.Plan.Multimacd.Run || st.Plan.HmIPServer.Node != "/dev/mmd_hmip" || !st.Plan.HmIPAdvanced {
		t.Fatalf("status: %+v %+v", st, st.Plan)
	}
	if len(st.Options.HmIP) != 1 || len(st.Options.BidCos) != 1 || st.Options.HmIP[0].ID != "58A9A728D4" {
		t.Fatalf("options: %+v", st.Options)
	}
	// no plan: not available, nothing to choose
	_ = os.Remove(filepath.Join(root, "run/occulite/radio/plan.json"))
	if st = s.Status(); st.Available || st.Plan != nil {
		t.Fatalf("without a plan: %+v", st)
	}
}

func TestRadioConnPreviewAndValidate(t *testing.T) {
	root := charlyRoot(t)
	devs := []BidCosDevice{{Address: "JEQ0230153", Type: "HM-CC-TC"}}
	s, _ := newConn(t, root, devs)
	ctx := context.Background()
	pv, err := s.Preview(ctx, radio.Choices{BidCos: radio.BidCosNone})
	if err != nil {
		t.Fatal(err)
	}
	if !pv.Changed || !pv.BidCosLost || !reflect.DeepEqual(pv.Devices, devs) || pv.Plan.RFD.Run || pv.Plan.Multimacd.Run || pv.Plan.HmIPServer.Node != "/dev/raw-uart" {
		t.Fatalf("HmIP-direct preview: %+v", pv)
	}
	if !reflect.DeepEqual(pv.Restarts, []string{"multimacd", "rfd", "hmipserver"}) {
		t.Fatalf("restarts: %v", pv.Restarts)
	}
	if pv, err = s.Preview(ctx, radio.Choices{}); err != nil || pv.Changed || pv.BidCosLost {
		t.Fatalf("auto on an auto box changes nothing: %+v %v", pv, err)
	}
	// pinning the module auto chose anyway: the choice changes, the stack does not
	if pv, err = s.Preview(ctx, radio.Choices{HmIP: "58A9A728D4"}); err != nil || !pv.Changed || pv.Plan.HmIPServer.Node != "/dev/mmd_hmip" {
		t.Fatalf("pinned to the same module: %+v %v", pv, err)
	}
	for _, c := range []radio.Choices{{HmIP: radio.BidCosNone}, {HmIP: "0000000000"}, {BidCos: "../etc"}, {BidCos: "KEQ0000000"}} {
		if _, err := s.Preview(ctx, c); err == nil {
			t.Errorf("%+v accepted", c)
		}
	}
}

func TestRadioConnApplyHmIPDirectAndBack(t *testing.T) {
	root := charlyRoot(t)
	devs := []BidCosDevice{{Address: "JEQ0230153", Type: "HM-CC-TC"}}
	s, svc := newConn(t, root, devs)
	ctx := context.Background()
	// paired devices: refused without confirm, nothing written
	_, err := s.Apply(ctx, radio.Choices{BidCos: radio.BidCosNone}, false)
	var cr *ConfirmRequired
	if !errors.As(err, &cr) || len(cr.Devices) != 1 {
		t.Fatalf("confirm required: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/rfd.conf")); string(b) != connRFDConf {
		t.Fatalf("rfd.conf written without confirm:\n%s", b)
	}
	if _, err := s.Apply(ctx, radio.Choices{BidCos: radio.BidCosNone}, true); err != nil {
		t.Fatal(err)
	}
	a := waitApply(t, s)
	if !a.OK {
		t.Fatalf("the change failed: %+v", a)
	}
	b, _ := os.ReadFile(filepath.Join(root, "etc/config/rfd.conf"))
	if !strings.HasPrefix(string(b), "# occulite.bidcos.module=none\n") {
		t.Fatalf("rfd.conf:\n%s", b)
	}
	want := []string{"hmipserver stop", "rfd stop", "multimacd stop", RadioDetectionUnit + " restart", "multimacd start", "rfd start", "hmipserver start"}
	if got := svc.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("units:\n%v\nwant\n%v", got, want)
	}
	st := s.Status()
	if st.Choices.BidCos != radio.BidCosNone || st.Plan.RFD.Run || st.Plan.HmIPServer.Node != "/dev/raw-uart" {
		t.Fatalf("after: %+v %+v", st.Choices, st.Plan)
	}
	if !strings.Contains(strings.Join(a.Lines, "\n"), "now: multimacd off, rfd off, hmipserver on /dev/raw-uart") {
		t.Fatalf("lines: %v", a.Lines)
	}
	if _, err := os.Stat(filepath.Join(s.StateDir, "running.json")); err == nil {
		t.Fatal("running.json left behind")
	}
	// back to auto: no BidCos-RF is lost, no confirm needed
	if _, err := s.Apply(ctx, radio.Choices{}, false); err != nil {
		t.Fatal(err)
	}
	if a = waitApply(t, s); !a.OK {
		t.Fatalf("back: %+v", a)
	}
	b, _ = os.ReadFile(filepath.Join(root, "etc/config/rfd.conf"))
	if strings.Contains(string(b), "occulite.bidcos.module") {
		t.Fatalf("the marker stays:\n%s", b)
	}
	if st = s.Status(); !st.Plan.Multimacd.Run || st.Plan.HmIPServer.Node != "/dev/mmd_hmip" {
		t.Fatalf("dual stack again: %+v", st.Plan)
	}
}

func TestRadioConnApplyHmIPChoiceFile(t *testing.T) {
	root := charlyRoot(t)
	s, _ := newConn(t, root, nil)
	hu := filepath.Join(root, "etc/config/crRFD/hmip_user.conf")
	_ = os.MkdirAll(filepath.Dir(hu), 0o755)
	_ = os.WriteFile(hu, []byte("Legacy.VirtualRemoteControl.Enabled=false\n"), 0o644)
	if _, err := s.Apply(context.Background(), radio.Choices{HmIP: "3014F711A0001F58A9A728D4"}, false); err != nil {
		t.Fatal(err)
	}
	if a := waitApply(t, s); !a.OK {
		t.Fatalf("%+v", a)
	}
	b, _ := os.ReadFile(hu)
	if string(b) != "Legacy.VirtualRemoteControl.Enabled=false\noccu"+"lite.hmip.adapter=3014F711A0001F58A9A728D4\n" {
		t.Fatalf("hmip_user.conf: %q", b)
	}
}

func TestRadioConnApplyFailureStartsTheStack(t *testing.T) {
	root := charlyRoot(t)
	s, svc := newConn(t, root, nil)
	svc.failing = RadioDetectionUnit
	if _, err := s.Apply(context.Background(), radio.Choices{HmIP: "58A9A728D4"}, false); err != nil {
		t.Fatal(err)
	}
	a := waitApply(t, s)
	if a.OK || !strings.Contains(a.Error, "the detection and plan did not run") {
		t.Fatalf("%+v", a)
	}
	calls := svc.snapshot()
	if calls[len(calls)-1] != "hmipserver start" || calls[len(calls)-3] != "multimacd start" {
		t.Fatalf("the stack is started after a failed run: %v", calls)
	}
}

func TestRadioConnBusyAndLoad(t *testing.T) {
	root := charlyRoot(t)
	s, svc := newConn(t, root, nil)
	fw := &RadioFirmware{Root: Root(root), StateDir: filepath.Join(root, "state/radio-firmware")}
	fw.running = &FlashAttempt{Module: "RPI-RF-MOD"}
	s.Firmware = fw
	if _, err := s.Apply(context.Background(), radio.Choices{HmIP: "58A9A728D4"}, false); !errors.Is(err, ErrConnApplyRunning) {
		t.Fatalf("a change during a flash: %v", err)
	}
	fw.running = nil
	fw.ConnBusy = func() bool { return true }
	if err := fw.Flash("RPI-RF-MOD", "x.eq3"); !errors.Is(err, ErrConnApplyRunning) {
		t.Fatalf("a flash during a change: %v", err)
	}
	// a change cut off by a restart of occulited: closed as failed, the stack started
	_ = writeJSONFile(filepath.Join(s.StateDir, "running.json"), &ConnApply{Choices: radio.Choices{BidCos: radio.BidCosNone}, Started: time.Now()})
	s.Load(context.Background())
	st := s.Status()
	if st.Last == nil || st.Last.OK || !strings.Contains(st.Last.Error, "restarted") {
		t.Fatalf("last: %+v", st.Last)
	}
	want := []string{RadioDetectionUnit + " start", "multimacd start", "rfd start", "hmipserver start"}
	if got := svc.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("units: %v", got)
	}
}

func TestRadioConnServiceNotes(t *testing.T) {
	root := charlyRoot(t)
	s, _ := newConn(t, root, nil)
	list := []Service{{ID: "hs485d", Skipped: true}, {ID: "rfd", Running: true}, {ID: "lighttpd", Skipped: true}}
	list = s.OverlayServiceNotes(list)
	if list[0].Note != "disabled: no wired interface configured" || list[1].Note != "" || list[2].Note != "" {
		t.Fatalf("notes: %+v", list)
	}
	var none *RadioConnections
	if got := none.OverlayServiceNotes(list); len(got) != 3 {
		t.Fatal("a box without the service keeps its list")
	}
}
