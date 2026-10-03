package radio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openccu-lite task 318 (D-120): on Automatic HmIP-RF stays on the module it last ran on, on this
// board; missing, it is not replaced; an explicit choice and a system without a record decide as
// before.
func TestPlanKeepsHmIPOnItsModule(t *testing.T) {
	const mac = "aa:aa:aa:aa:aa:01"
	pinOn := func(sgtin, board string) *HmIPPin { return &HmIPPin{SGTIN: sgtin, BoardMAC: board} }
	mod, stick := rpiRFMod("/dev/raw-uart"), rfusb("/dev/raw-uart1")
	for _, c := range []struct {
		name    string
		mods    []Module
		pin     *HmIPPin
		hmipCh  string
		want    string // the HmIP module's SGTIN, "" for none
		missing string
	}{
		{"no record: the automatic pick (the first module)", []Module{mod, stick}, nil, "", mod.SGTIN, ""},
		{"the stick holds the network: kept beside a module the pick prefers", []Module{mod, stick}, pinOn(stick.SGTIN, mac), "", stick.SGTIN, ""},
		{"the module holds it and is there", []Module{mod, stick}, pinOn(strings.ToLower(mod.SGTIN), strings.ToUpper(mac)), "", mod.SGTIN, ""},
		{"its module is missing: no other one", []Module{stick}, pinOn(mod.SGTIN, mac), "", "", mod.SGTIN},
		{"its module does not answer: no other one", []Module{stick, {Name: "raw-uart", Node: "/dev/raw-uart", GPIO: true, Probe: "timeout"}}, pinOn(mod.SGTIN, mac), "", "", mod.SGTIN},
		{"another board's record (a restored backup): the automatic pick", []Module{stick}, pinOn(mod.SGTIN, "aa:aa:aa:aa:aa:02"), "", stick.SGTIN, ""},
		{"a record without a board: the automatic pick", []Module{stick}, pinOn(mod.SGTIN, ""), "", stick.SGTIN, ""},
		{"an explicit choice wins", []Module{mod, stick}, pinOn(mod.SGTIN, mac), stick.Serial, stick.SGTIN, ""},
	} {
		in := inputs(c.mods...)
		in.Detection.BoardMAC = mac
		in.HmIPPin = c.pin
		if c.hmipCh != "" {
			in.HmIPUserConf = SetHmIPChoice("", c.hmipCh)
		}
		p := MakePlan(in)
		got := ""
		if p.HmIP != nil {
			got = p.HmIP.SGTIN
		}
		if got != c.want || p.MissingHmIP != c.missing {
			t.Errorf("%s: HmIP %q missing %q, want %q %q\n%v", c.name, got, p.MissingHmIP, c.want, c.missing, p.Notes)
		}
		if c.missing != "" && (p.HmIPServerHmIP || !p.HmIPServer.Run || p.HmIPPin != c.missing || !strings.Contains(strings.Join(p.Notes, "\n"), "which holds the HmIP network, is missing")) {
			t.Errorf("%s: hmipserver %+v hmip %v pin %q", c.name, p.HmIPServer, p.HmIPServerHmIP, p.HmIPPin)
		}
	}
}

func TestRecordHmIPPin(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	p := Plan{HmIP: &Role{SGTIN: "3014f711a0001f0000000a03", Serial: "0000000A03"}}
	if ok, err := recordHmIPPin(root, Plan{}, "aa:aa:aa:aa:aa:01", now); ok || err != nil {
		t.Fatalf("no HmIP module: %v %v", ok, err)
	}
	if ok, err := recordHmIPPin(root, p, "", now); ok || err != nil {
		t.Fatalf("no board: %v %v", ok, err)
	}
	if ok, err := recordHmIPPin(root, p, "aa:aa:aa:aa:aa:01", now); !ok || err != nil {
		t.Fatalf("first: %v %v", ok, err)
	}
	got := ReadHmIPPin(root)
	if got == nil || got.SGTIN != "3014F711A0001F0000000A03" || got.Serial != "0000000A03" || got.BoardMAC != "aa:aa:aa:aa:aa:01" || !got.At.Equal(now) {
		t.Fatalf("record: %+v", got)
	}
	if ok, _ := recordHmIPPin(root, p, "AA:AA:AA:AA:AA:01", now.Add(time.Hour)); ok {
		t.Error("an unchanged record was rewritten")
	}
	p.HmIP.SGTIN = "3014F711A000040000000A01"
	if ok, _ := recordHmIPPin(root, p, "aa:aa:aa:aa:aa:01", now); !ok || ReadHmIPPin(root).SGTIN != "3014F711A000040000000A01" {
		t.Error("a move was not recorded")
	}
	_ = os.WriteFile(filepath.Join(root, HmIPPinFile), []byte("{"), 0o644)
	if ReadHmIPPin(root) != nil {
		t.Error("a broken record was read")
	}
}
