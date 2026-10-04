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

// The ways an interface process reaches its radio (occulited task 13).
const (
	LinkMultimacd = "multimacd" // through the multiplexer, which shares the module between rfd and hmipserver
	LinkDirect    = "direct"    // the process opens the module's UART itself
	LinkUSB       = "usb"       // a USB stick: the HmIP-RFUSB, the HM-CFG-USB-2
	LinkLAN       = "lan"       // a LAN gateway: HM-LGW, HM-CFG-LAN
)

// Link is the radio behind an entry of listBidcosInterfaces as the Status page names it
// (occulited task 13): the module, the adapter it sits on (HB-RF-USB-2, HB-RF-ETH), and the way
// the process reaches it.
type Link struct {
	Module  string `json:"module"`
	Adapter string `json:"adapter,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Link says which radio an entry is and how its process reaches it: the plan's module for the
// entries Transmitter keys to it, the gateway's kind for a LAN gateway, nothing for an entry the
// plan does not know.
func (p Plan) Link(iface, address, typ string, hmipEntries int) (Link, bool) {
	key, _ := p.Transmitter(iface, address, typ, hmipEntries)
	switch {
	case iface == "HmIP-RF" && p.HmIP != nil && key == "module:"+p.HmIP.Serial:
		l := Link{Module: moduleName(p.HmIP.Hardware), Adapter: adapterOf(p.HmIP.DeviceType)}
		switch {
		case p.HmIPServer.Node == "/dev/mmd_hmip":
			l.Path = LinkMultimacd
		case l.Adapter == "" && strings.Contains(p.HmIP.Hardware, "RFUSB"):
			l.Path = LinkUSB
		default:
			l.Path = LinkDirect
		}
		return l, true
	case iface == "BidCos-RF" && p.HmRF != nil && key == "module:"+p.HmRF.Serial:
		if p.HmRF.Hardware == "HM-CFG-USB-2" {
			return Link{Module: "HM-CFG-USB-2", Path: LinkUSB}, true
		}
		l := Link{Module: moduleName(p.HmRF.Hardware), Adapter: adapterOf(p.HmRF.DeviceType), Path: LinkDirect}
		if p.RFD.Node == "/dev/mmd_bidcos" {
			l.Path = LinkMultimacd
		}
		return l, true
	case iface == "BidCos-RF":
		// rfd's LAN gateways, by the Type of their rfd.conf section as listBidcosInterfaces
		// reports it (system.GatewayTypes)
		switch strings.ToUpper(strings.TrimSpace(typ)) {
		case "HMLGW2":
			return Link{Module: "HM-LGW", Path: LinkLAN}, true
		case "LAN INTERFACE":
			return Link{Module: "HM-CFG-LAN", Path: LinkLAN}, true
		}
	}
	return Link{}, false
}

// moduleName is the module as eQ-3 writes it: the probe upper-cases the hardware (HMIP-RFUSB).
func moduleName(hw string) string {
	if rest, ok := strings.CutPrefix(hw, "HMIP-"); ok {
		return "HmIP-" + rest
	}
	return hw
}

// adapterOf is the HB-RF board a module sits on, from the raw-uart device_type
// (HB-RF-USB-2@usb-…, HB-RF-ETH@<address>); "" for the GPIO header and the USB sticks.
func adapterOf(deviceType string) string {
	name, _, _ := strings.Cut(deviceType, "@")
	if strings.HasPrefix(name, "HB-RF-") {
		return name
	}
	return ""
}
