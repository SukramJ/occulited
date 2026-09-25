package radio

import "strings"

// Transmitter says which physical radio an entry of listBidcosInterfaces is (task 156), so a page
// shows one duty cycle per transmitter: both stacks of a module shared through multimacd are one,
// BidCos-RF and HmIP-RF on two modules are two, and every LAN gateway or router is its own.
//
// The entries' addresses do not name the module. rfd reports its local interface under the serial
// it keeps in its own state (an old CCU identity on a switched box), hmipserver the module's SGTIN.
// So the plan decides: the HmIP entry of the plan's HmIP module, rfd's CCU2 entry while it has its
// local module, and the HM-CFG-USB-2 by its serial, are the plan's modules; anything else is keyed
// by itself. Key is "module:<serial>" or "if:<interface>/<address>"; Name is what a page shows.
func (p Plan) Transmitter(iface, address, typ string, hmipEntries int) (key, name string) {
	own := "if:" + iface + "/" + address
	module := func(r *Role) (string, string) {
		return "module:" + r.Serial, strings.TrimSpace(r.Hardware + " " + r.Serial)
	}
	switch iface {
	case "HmIP-RF":
		if p.HmIP != nil && (strings.EqualFold(address, p.HmIP.SGTIN) || (hmipEntries == 1 && p.HmIP.SGTIN == "")) {
			return module(p.HmIP)
		}
	case "BidCos-RF":
		if p.HmRF != nil {
			if p.RFDLocal && strings.EqualFold(typ, "CCU2") {
				return module(p.HmRF)
			}
			if p.RFDUSBAdapter && strings.EqualFold(address, p.HmRF.Serial) {
				return module(p.HmRF)
			}
		}
	}
	return own, strings.TrimSpace(typ + " " + address)
}
