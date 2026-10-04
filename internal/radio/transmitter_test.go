package radio

import "testing"

// task 156: which physical radio an interface entry is
func TestTransmitter(t *testing.T) {
	rfusb := &Role{Hardware: "HMIP-RFUSB", Serial: "0000000A02", SGTIN: "3014F711A000040000000A02"}
	shared := Plan{HmRF: rfusb, HmIP: rfusb, RFDLocal: true}
	// .119: rfd reports its local interface under an old CCU identity, hmipserver the SGTIN
	kb, nb := shared.Transmitter("BidCos-RF", "OEQ9000007", "CCU2", 1)
	ki, ni := shared.Transmitter("HmIP-RF", "3014F711A000040000000A02", "HMIP_CCU2", 1)
	if kb != "module:0000000A02" || kb != ki || nb != "HMIP-RFUSB 0000000A02" || ni != nb {
		t.Fatalf("shared: %q %q / %q %q", kb, nb, ki, ni)
	}
	// a LAN gateway beside it is its own
	if k, n := shared.Transmitter("BidCos-RF", "NEQ1234567", "HMLGW2", 1); k != "if:BidCos-RF/NEQ1234567" || n != "HMLGW2 NEQ1234567" {
		t.Fatalf("gateway: %q %q", k, n)
	}
	// .170: BidCos-RF on the HM-CFG-USB-2, HmIP directly on the RPI-RF-MOD - two radios
	two := Plan{
		HmRF:          &Role{Hardware: "HM-CFG-USB-2", Serial: "JEQ9000002"},
		HmIP:          &Role{Hardware: "RPI-RF-MOD", Serial: "0000000A04", SGTIN: "3014F711A0001F0000000A04"},
		RFDUSBAdapter: true,
	}
	kb, _ = two.Transmitter("BidCos-RF", "JEQ9000002", "HM-CFG-USB", 1)
	ki, _ = two.Transmitter("HmIP-RF", "3014F711A0001F0000000A04", "HMIP_CCU2", 1)
	if kb != "module:JEQ9000002" || ki != "module:0000000A04" {
		t.Fatalf("two: %q %q", kb, ki)
	}
	// no plan (an older image, the run not finished): every entry is its own
	if k, _ := (Plan{}).Transmitter("HmIP-RF", "3014F711A0001F0000000A04", "HMIP_CCU2", 1); k != "if:HmIP-RF/3014F711A0001F0000000A04" {
		t.Fatalf("no plan: %q", k)
	}
	// rfd without its local module: a CCU2 entry is not claimed for the HmIP module
	if k, _ := (Plan{HmRF: rfusb, HmIP: rfusb}).Transmitter("BidCos-RF", "OEQ9000007", "CCU2", 1); k != "if:BidCos-RF/OEQ9000007" {
		t.Fatalf("not local: %q", k)
	}
}

// occulited task 13: the module and the way to it, for the Status page's interface cards
func TestLink(t *testing.T) {
	rfusb := &Role{Hardware: "HMIP-RFUSB", Serial: "0000000A02", SGTIN: "3014F711A000040000000A02", DeviceType: "eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3"}
	shared := Plan{HmRF: rfusb, HmIP: rfusb, RFDLocal: true, RFD: Daemon{Run: true, Node: "/dev/mmd_bidcos"}, HmIPServer: Daemon{Run: true, Node: "/dev/mmd_hmip"}}
	pcb := &Role{Hardware: "RPI-RF-MOD", Serial: "0000000A04", SGTIN: "3014F711A0001F0000000A04", DeviceType: "HB-RF-ETH@192.0.2.40"}
	direct := Plan{HmIP: pcb, HmIPServer: Daemon{Run: true, Node: "/dev/raw-uart1"}}
	stick := Plan{HmIP: rfusb, HmIPServer: Daemon{Run: true, Node: "/dev/raw-uart1"},
		HmRF: &Role{Hardware: "HM-CFG-USB-2", Serial: "JEQ9000002", DeviceType: "USB"}, RFDUSBAdapter: true}
	for _, tc := range []struct {
		name                string
		p                   Plan
		iface, address, typ string
		want                Link
		ok                  bool
	}{
		{"BidCos-RF through multimacd", shared, "BidCos-RF", "OEQ9000007", "CCU2", Link{Module: "HmIP-RFUSB", Path: LinkMultimacd}, true},
		{"HmIP-RF through multimacd", shared, "HmIP-RF", "3014F711A000040000000A02", "HMIP_CCU2", Link{Module: "HmIP-RFUSB", Path: LinkMultimacd}, true},
		{"HmIP-RF on an HB-RF-ETH, directly", direct, "HmIP-RF", "3014F711A0001F0000000A04", "HMIP_CCU2", Link{Module: "RPI-RF-MOD", Adapter: "HB-RF-ETH", Path: LinkDirect}, true},
		{"the HmIP-RFUSB on its own", stick, "HmIP-RF", "3014F711A000040000000A02", "HMIP_CCU2", Link{Module: "HmIP-RFUSB", Path: LinkUSB}, true},
		{"the HM-CFG-USB-2", stick, "BidCos-RF", "JEQ9000002", "HM-CFG-USB", Link{Module: "HM-CFG-USB-2", Path: LinkUSB}, true},
		{"an HM-LGW", shared, "BidCos-RF", "NEQ1234567", "HMLGW2", Link{Module: "HM-LGW", Path: LinkLAN}, true},
		{"an HM-CFG-LAN", shared, "BidCos-RF", "KEQ0123456", "Lan Interface", Link{Module: "HM-CFG-LAN", Path: LinkLAN}, true},
		{"an entry the plan does not know", shared, "HmIP-RF", "3014F711A000040000000A07", "HMIP_CCU2", Link{}, false},
		{"no plan", Plan{}, "HmIP-RF", "3014F711A0001F0000000A04", "HMIP_CCU2", Link{}, false},
	} {
		hmip := 1
		if tc.name == "an entry the plan does not know" {
			hmip = 2
		}
		got, ok := tc.p.Link(tc.iface, tc.address, tc.typ, hmip)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: %+v %v, want %+v %v", tc.name, got, ok, tc.want, tc.ok)
		}
	}
	if moduleName("HMIP-RFUSB-TK") != "HmIP-RFUSB-TK" || adapterOf("HB-RF-USB-2@usb-1") != "HB-RF-USB-2" || adapterOf("GPIO@fe201000.serial") != "" {
		t.Error("moduleName/adapterOf")
	}
}
