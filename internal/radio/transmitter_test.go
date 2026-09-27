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
