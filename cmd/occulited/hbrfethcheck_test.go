package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// the lab system's plan with the HB-RF-ETH as its only radio (.116, 2026-09-25)
func boardPlan() radio.Plan {
	on := func(addr string) *radio.Role {
		return &radio.Role{Hardware: "HM-MOD-RPI-PCB", Node: "/dev/raw-uart1", DeviceType: "HB-RF-ETH@192.0.2.209", Address: addr, Serial: "MEQ0835626", SGTIN: "3014F711A061A7D3C996282A"}
	}
	return radio.Plan{HmRF: on("0x3D1BAE"), HmIP: on("0xB4C139"), HmIPServerHmIP: true,
		Multimacd: radio.Daemon{Run: true, Node: "/dev/raw-uart1"}, RFD: radio.Daemon{Run: true}, HmIPServer: radio.Daemon{Run: true}}
}

func TestOnHBRFETH(t *testing.T) {
	hmrf, hmip, d := boardPlan().OnHBRFETH()
	if hmrf == nil || hmip == nil || strings.Join(d, ",") != "multimacd,rfd,hmipserver" {
		t.Errorf("%v %v %v", hmrf, hmip, d)
	}
	// HmIP on a USB stick beside it: only BidCos-RF is the board's; multimacd's node is the board's
	p := boardPlan()
	p.HmIP = &radio.Role{Node: "/dev/raw-uart", DeviceType: "eQ-3 HmIP-RFUSB@usb-1", SGTIN: "3014F711A000041709ADFA5E"}
	if hmrf, hmip, d := p.OnHBRFETH(); hmrf == nil || hmip != nil || strings.Join(d, ",") != "multimacd,rfd" {
		t.Errorf("mixed: %v %v %v", hmrf, hmip, d)
	}
	// no board
	if _, _, d := (radio.Plan{HmRF: &radio.Role{DeviceType: "GPIO@x"}}).OnHBRFETH(); d != nil {
		t.Errorf("no board: %v", d)
	}
}

func TestBoardHealth(t *testing.T) {
	p := boardPlan()
	good := []interfaces.RadioInterface{
		{Interface: "BidCos-RF", Address: "MEQ0835626", Connected: true, Default: true},
		{Interface: "HmIP-RF", Address: "3014F711A061A7D3C996282A", Connected: true, Default: true},
	}
	if ok, why, err := boardHealth(p, good, nil); !ok || why != "" || err != nil {
		t.Errorf("good: %v %q %v", ok, why, err)
	}
	bad := append([]interfaces.RadioInterface(nil), good...)
	bad[0].Connected = false
	if ok, why, _ := boardHealth(p, bad, nil); ok || !strings.Contains(why, "BidCos-RF: MEQ0835626 not connected") {
		t.Errorf("rfd without its module: %v %q", ok, why)
	}
	if ok, why, _ := boardHealth(p, good[:1], nil); ok || !strings.Contains(why, "HmIP-RF: the module 3014F711A061A7D3C996282A is not listed") {
		t.Errorf("hmipserver without its module: %v %q", ok, why)
	}
	if _, _, err := boardHealth(p, nil, map[string]error{"BidCos-RF": errors.New("no answer within 5s")}); err == nil {
		t.Error("an unanswered call is not an answer")
	}
}

type fakeServices struct{ calls []string }

func (f *fakeServices) Control(_ context.Context, id, action string) (string, error) {
	f.calls = append(f.calls, action+" "+id)
	return "", nil
}

func TestHBRFETHRestartOrder(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run/occulite/radio")
	_ = os.MkdirAll(dir, 0o755)
	b, _ := json.Marshal(boardPlan())
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &fakeServices{}
	if err := hbrfethRestart(root, svc)(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(svc.calls, ","); got != "stop hmipserver,stop rfd,stop multimacd,start multimacd,start rfd,start hmipserver" {
		t.Errorf("order %s", got)
	}
	// no plan: no restart, and the check says nothing is wrong
	if err := hbrfethRestart(t.TempDir(), svc)(context.Background()); err == nil {
		t.Error("a restart without a plan")
	}
	if ok, _, err := hbrfethHealthy(t.TempDir(), func() []interfaces.Interface { return nil })(context.Background()); !ok || err != nil {
		t.Errorf("no plan: %v %v", ok, err)
	}
}
