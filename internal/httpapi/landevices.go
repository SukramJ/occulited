package httpapi

// openccu-lite task 220: eQ-3's LAN devices - found on the LAN (read-only, like NetFinder's list)
// and, for the LAN gateways and the HmIP access points, their network settings. Other CCUs are
// listed with a link and never written to. No key change, no reboot, no firmware, no factory reset.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/eq3disc"
	"github.com/hobbyquaker/occulited/internal/system"
)

// LANFinder is eq3disc.Client's face for the handlers.
type LANFinder interface {
	Scan(ctx context.Context) ([]eq3disc.Found, error)
	Lookup(ctx context.Context, serial string) (*eq3disc.Found, error)
	Configure(ctx context.Context, to netip.Addr, typ, serial string, cfg eq3disc.Config, password string) (bool, error)
}

// lanRefindAfter and lanRefindFor: after a write the device is looked for again every
// lanRefindAfter until lanRefindFor has passed - an access point restarts its network.
var (
	lanRefindAfter = 5 * time.Second
	lanRefindFor   = 45 * time.Second
)

type lanState struct {
	mu      sync.Mutex
	at      time.Time
	devices []LANDevice
	err     string
}

var lanStates sync.Map // *SystemAPI -> *lanState

func (a *SystemAPI) lanState() *lanState {
	v, _ := lanStates.LoadOrStore(a, &lanState{})
	return v.(*lanState)
}

// LANDevice is one found device with what this system knows about it.
type LANDevice struct {
	eq3disc.Found
	// Kind: gateway (a LAN gateway), access-point (HAP, DRAP), ccu (another system), other.
	Kind string `json:"kind"`
	// Writable: its network settings can be set from here (gateways and access points).
	Writable bool `json:"writable"`
	// Password: where the password for a write comes from - "configured" (the gateway's key in
	// rfd.conf / hs485d.conf, used by the system itself) or "sticker" (asked every time).
	Password string `json:"password,omitempty"`
	// Configured: the gateway is in rfd.conf ("rf") or hs485d.conf ("wired"), under that name.
	Configured string `json:"configured,omitempty"`
	Name       string `json:"name,omitempty"`
	// Paired: an access point paired to this system's HmIP-RF (its SGTIN is the serial).
	Paired bool `json:"paired,omitempty"`
	// SameSubnet: the address it runs with is in one of this system's IPv4 networks.
	SameSubnet bool `json:"same_subnet"`
	// Link is another system's web UI.
	Link string `json:"link,omitempty"`
}

func lanKind(typ string) string {
	t := strings.ToUpper(typ)
	switch {
	case eq3disc.IsCCU(typ):
		return "ccu"
	case strings.Contains(t, "HM-LGW") || strings.Contains(t, "HMW-LGW"):
		return "gateway"
	case strings.Contains(t, "HMIP-HAP") || strings.Contains(t, "DRAP"):
		return "access-point"
	}
	return "other"
}

func (a *SystemAPI) lanFinder() LANFinder {
	if a.LAN != nil {
		return a.LAN
	}
	return &eq3disc.Client{}
}

func (a *SystemAPI) lanNetworks() []netip.Prefix {
	if a.LANNetworks != nil {
		return a.LANNetworks()
	}
	return localNetworks()
}

// localNetworks are this system's IPv4 networks.
func localNetworks() []netip.Prefix {
	var out []netip.Prefix
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, ad := range addrs {
			if n, ok := ad.(*net.IPNet); ok && n.IP.To4() != nil {
				ones, _ := n.Mask.Size()
				if p, ok := netip.AddrFromSlice(n.IP.To4()); ok {
					out = append(out, netip.PrefixFrom(p, ones).Masked())
				}
			}
		}
	}
	return out
}

func inNetworks(nets []netip.Prefix, s string) bool {
	ip, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// annotate joins a found device with the configured gateways, the paired access points and this
// system's networks.
func (a *SystemAPI) annotate(f eq3disc.Found, nets []netip.Prefix, gws map[string][2]string, sgtins map[string]bool) LANDevice {
	d := LANDevice{Found: f, Kind: lanKind(f.Type)}
	d.Writable = d.Kind == "gateway" || d.Kind == "access-point"
	ip := f.IP
	if f.Runtime != nil && f.Runtime.IP != "0.0.0.0" {
		ip = f.Runtime.IP
	}
	d.SameSubnet = inNetworks(nets, ip)
	switch d.Kind {
	case "gateway":
		d.Password = "sticker"
		if g, ok := gws[strings.ToUpper(f.Serial)]; ok {
			d.Configured, d.Name, d.Password = g[0], g[1], "configured"
		}
	case "access-point":
		d.Password = "sticker"
		d.Paired = sgtins[strings.ToUpper(f.Serial)]
	case "ccu":
		d.Link = "http://" + f.IP + "/"
	}
	return d
}

func (a *SystemAPI) configuredGateways() map[string][2]string {
	out := map[string][2]string{}
	for _, c := range []struct {
		class system.GatewayClass
		name  string
	}{{system.GatewayRF, "rf"}, {system.GatewayWired, "wired"}} {
		for _, g := range a.Root.ReadLANGateways(c.class) {
			if g.Serial != "" {
				out[strings.ToUpper(g.Serial)] = [2]string{c.name, g.Name}
			}
		}
	}
	return out
}

func (a *SystemAPI) pairedSGTINs() map[string]bool {
	out := map[string]bool{}
	for _, s := range a.Root.HmIPDeviceSGTINs() {
		out[strings.ToUpper(s)] = true
	}
	return out
}

// lanDevices is GET /radio/lan-devices: the last search's result, never a new one - opening a
// page sends nothing to the network (openccu-lite task 237, the maintainer: an outgoing connection
// only when the user presses the button). Before the first search: no "scanned", no devices.
func (a *SystemAPI) lanDevices(w http.ResponseWriter, r *http.Request) {
	st := a.lanState()
	st.mu.Lock()
	defer st.mu.Unlock()
	writeJSON(w, 200, st.view())
}

// lanSearch is POST /radio/lan-devices/search: the eQ-3 discovery broadcast, now; its answer is
// kept for the GET.
func (a *SystemAPI) lanSearch(w http.ResponseWriter, r *http.Request) {
	st := a.lanState()
	st.mu.Lock()
	defer st.mu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	found, err := a.lanFinder().Scan(ctx)
	cancel()
	st.at, st.err, st.devices = time.Now(), "", []LANDevice{}
	if err != nil {
		st.err = err.Error()
	}
	nets, gws, sg := a.lanNetworks(), a.configuredGateways(), a.pairedSGTINs()
	for _, f := range found {
		st.devices = append(st.devices, a.annotate(f, nets, gws, sg))
	}
	writeJSON(w, 200, st.view())
}

// view is the answer of both: the time of the last search (absent before the first) and its devices.
func (st *lanState) view() map[string]any {
	out := map[string]any{"devices": st.devices}
	if st.devices == nil {
		out["devices"] = []LANDevice{}
	}
	if !st.at.IsZero() {
		out["scanned"] = st.at
	}
	if st.err != "" {
		out["error"] = st.err
	}
	return out
}

// lanNetworkBody is POST /radio/lan-devices/{serial}/network.
type lanNetworkBody struct {
	DHCP    bool   `json:"dhcp"`
	IP      string `json:"ip"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
	DNS1    string `json:"dns1"`
	DNS2    string `json:"dns2"`
	// Password: an access point's sticker PW, or a gateway's key when the system has none for it.
	Password string `json:"password"`
	// OtherSubnet: the user knows the device will not be reachable from this system afterwards.
	OtherSubnet bool `json:"other_subnet"`
}

// validateStatic checks a static address: IPv4 everywhere, a contiguous mask, not the network's
// or the broadcast address, the gateway inside the subnet.
func validateStatic(b lanNetworkBody) (netip.Prefix, error) {
	ip, err := netip.ParseAddr(b.IP)
	if err != nil || !ip.Is4() || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
		return netip.Prefix{}, errors.New("ip: not a usable IPv4 address")
	}
	mask, err := netip.ParseAddr(b.Netmask)
	if err != nil || !mask.Is4() {
		return netip.Prefix{}, errors.New("netmask: not an IPv4 netmask")
	}
	m := mask.As4()
	ones, bits := net.IPMask(m[:]).Size()
	if bits == 0 || ones < 8 || ones > 30 {
		return netip.Prefix{}, errors.New("netmask: not a contiguous mask between /8 and /30")
	}
	p := netip.PrefixFrom(ip, ones).Masked()
	b4, i4 := p.Addr().As4(), ip.As4()
	var bc [4]byte
	for k := range bc {
		bc[k] = b4[k] | ^m[k]
	}
	if ip == p.Addr() || i4 == bc {
		return netip.Prefix{}, errors.New("ip: the network's or the broadcast address")
	}
	if b.Gateway != "" && b.Gateway != "0.0.0.0" {
		g, err := netip.ParseAddr(b.Gateway)
		if err != nil || !g.Is4() || !p.Contains(g) || g == ip {
			return netip.Prefix{}, errors.New("gateway: not an address in the new subnet")
		}
	}
	for _, d := range []string{b.DNS1, b.DNS2} {
		if d == "" {
			continue
		}
		if a, err := netip.ParseAddr(d); err != nil || !a.Is4() {
			return netip.Prefix{}, errors.New("dns: not an IPv4 address")
		}
	}
	return p, nil
}

// addressInUse says whether something answers for the address on this system's LAN: a datagram
// provokes the neighbour lookup, and a completed entry in /proc/net/arp is somebody. Only
// meaningful for an address in one of this system's networks.
func addressInUse(ctx context.Context, ip netip.Addr) bool {
	if c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(netip.AddrPortFrom(ip, 9))); err == nil {
		_, _ = c.Write([]byte{0})
		c.Close()
	}
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if arpComplete(ip) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return arpComplete(ip)
}

func arpComplete(ip netip.Addr) bool {
	b, err := os.ReadFile("/proc/net/arp")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 4 && f[0] == ip.String() && f[2] == "0x2" && f[3] != "00:00:00:00:00:00" {
			return true
		}
	}
	return false
}

// lanNetwork writes one device's network settings (system:write). The device is looked up
// afresh by its serial; the password is the configured gateway key or the one given; a configured
// gateway's new address goes into rfd.conf / hs485d.conf and the daemon restarts; afterwards the
// device is looked for again, and every write is logged (without the password).
func (a *SystemAPI) lanNetwork(w http.ResponseWriter, r *http.Request) {
	// system:write (the route's scope) is an administrator's; the user is named in the log
	user := ""
	if s := SessionFrom(r); s != nil {
		user = s.User
	}
	serial := r.PathValue("serial")
	var b lanNetworkBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad-request", Message: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lanRefindFor+30*time.Second)
	defer cancel()
	finder := a.lanFinder()
	dev, err := finder.Lookup(ctx, serial)
	if err != nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: "no answer from a device with that serial"})
		return
	}
	nets := a.lanNetworks()
	d := a.annotate(*dev, nets, a.configuredGateways(), a.pairedSGTINs())
	if !d.Writable {
		writeJSON(w, http.StatusForbidden, apiError{Error: "not-writable", Message: "only the network settings of LAN gateways and HmIP access points are set from here"})
		return
	}
	if dev.Config == nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: "no-answer", Message: "the device did not answer with its settings"})
		return
	}
	cfg := *dev.Config
	cfg.DHCP = b.DHCP
	cfg.AutoIP = dev.Config.AutoIP
	if !b.DHCP {
		if _, err := validateStatic(b); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			return
		}
		ip := netip.MustParseAddr(b.IP)
		if !inNetworks(nets, b.IP) && !b.OtherSubnet {
			writeJSON(w, http.StatusConflict, apiError{Error: "other-subnet", Message: "the address is in none of this system's networks: the device would not be reachable from here"})
			return
		}
		current := ""
		if dev.Runtime != nil {
			current = dev.Runtime.IP
		}
		if b.IP != current && b.IP != dev.IP {
			if ownAddress(ip) || a.lanTaken(ip, serial) || (inNetworks(nets, b.IP) && a.probe(ctx, ip)) {
				writeJSON(w, http.StatusConflict, apiError{Error: "address-in-use", Message: "something on the network already answers for " + b.IP})
				return
			}
		}
		cfg.Addresses = eq3disc.Addresses{IP: b.IP, Netmask: b.Netmask, Gateway: b.Gateway, DNS1: b.DNS1, DNS2: b.DNS2}
	} else if b.IP != "" {
		// DHCP with the addresses the device keeps beside it (its fallback, what NetFinder shows in
		// the fields): checked for form only - they are not what it runs with while DHCP answers
		if _, err := validateStatic(b); err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			return
		}
		cfg.Addresses = eq3disc.Addresses{IP: b.IP, Netmask: b.Netmask, Gateway: b.Gateway, DNS1: b.DNS1, DNS2: b.DNS2}
	}
	password := b.Password
	var gw *system.LANGateway
	var class system.GatewayClass
	if d.Configured != "" {
		class = system.GatewayRF
		if d.Configured == "wired" {
			class = system.GatewayWired
		}
		for _, g := range a.Root.ReadLANGateways(class) {
			if strings.EqualFold(g.Serial, serial) {
				gw = &g
				if password == "" {
					password = g.Key()
				}
				break
			}
		}
	}
	to, _ := netip.ParseAddr(dev.IP)
	if !inNetworks(nets, dev.IP) {
		to = netip.AddrFrom4([4]byte{255, 255, 255, 255}) // a device in another subnet hears the broadcast
	}
	old := dev.Config.Addresses
	oldRun := ""
	if dev.Runtime != nil {
		oldRun = dev.Runtime.IP
	}
	restarts, err := finder.Configure(ctx, to, dev.Type, dev.Serial, cfg, password)
	logAttrs := []any{"user", user, "device", dev.Type, "serial", serial, "old_ip", oldRun, "old_static_ip", old.IP, "old_dhcp", dev.Config.DHCP, "new_dhcp", cfg.DHCP, "new_ip", cfg.IP, "new_netmask", cfg.Netmask, "new_gateway", cfg.Gateway}
	if err != nil {
		slog.Warn("lan device: the network settings were not taken", append(logAttrs, "err", err)...)
		switch {
		case errors.Is(err, eq3disc.ErrWrongPassword):
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "wrong-password", Message: "the device did not take the password"})
		case errors.Is(err, eq3disc.ErrNoAnswer):
			writeJSON(w, http.StatusBadGateway, apiError{Error: "no-answer", Message: err.Error()})
		default:
			writeJSON(w, http.StatusBadGateway, apiError{Error: "refused", Message: err.Error()})
		}
		return
	}
	slog.Info("lan device: network settings written", append(logAttrs, "restarts", restarts)...)
	out := map[string]any{"written": true, "restarts": restarts}
	// a configured gateway keeps its place in rfd.conf / hs485d.conf: the daemon connects to the
	// address written there, so a new static address goes in, and the daemon restarts
	if gw != nil && !cfg.DHCP && gw.Address != "" && gw.Address != cfg.IP {
		specs := []system.LANGatewaySpec{}
		for _, g := range a.Root.ReadLANGateways(class) {
			sp := system.LANGatewaySpec{Type: g.Type, Name: g.Name, Serial: g.Serial, IP: g.Address}
			if strings.EqualFold(g.Serial, serial) {
				sp.IP = cfg.IP
			}
			specs = append(specs, sp)
		}
		if _, err := a.Root.WriteLANGateways(class, specs); err != nil {
			out["gateway_file_error"] = err.Error()
		} else {
			out["gateway_file"] = true
			slog.Info("lan device: the gateway's address updated", "serial", serial, "service", class.Service(), "ip", cfg.IP)
			if a.Services != nil {
				if o, err := a.Services.Control(ctx, class.Service(), "restart"); err != nil {
					out["restart_error"] = fmt.Sprintf("%v: %s", err, o)
				} else {
					out["restarted"] = class.Service()
				}
			}
		}
	}
	// found again: the device's own word on what it runs with now
	deadline := time.Now().Add(lanRefindFor)
	for {
		if f, err := finder.Lookup(ctx, serial); err == nil && f.Config != nil {
			// taken: the device's own configuration says what was sent; a static address also runs
			differs := configDiffers(cfg, *f.Config)
			running := cfg.DHCP || f.Runtime != nil && f.Runtime.IP == cfg.IP
			if len(differs) == 0 && running || time.Now().After(deadline) {
				out["device"] = a.annotate(*f, nets, a.configuredGateways(), a.pairedSGTINs())
				if len(differs) > 0 {
					// lab 2026-09-24: the HAP-B1 and the HMW-LGW took the addresses and kept DHCP on
					out["differs"] = differs
				}
				break
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			out["refind_error"] = "the device did not answer with the new settings in time; search again later"
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(lanRefindAfter):
		}
	}
	// the device found again replaces its entry in the last search's list (the page searches
	// again only when the user asks)
	if d, ok := out["device"].(LANDevice); ok {
		st := a.lanState()
		st.mu.Lock()
		for i := range st.devices {
			if strings.EqualFold(st.devices[i].Serial, d.Serial) {
				st.devices[i] = d
			}
		}
		st.mu.Unlock()
	}
	writeJSON(w, 200, out)
}

// lanTaken: another device of the last scan runs with that address.
func (a *SystemAPI) lanTaken(ip netip.Addr, serial string) bool {
	st := a.lanState()
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, d := range st.devices {
		if strings.EqualFold(d.Serial, serial) {
			continue
		}
		if d.IP == ip.String() || d.Runtime != nil && d.Runtime.IP == ip.String() {
			return true
		}
	}
	return false
}

func (a *SystemAPI) probe(ctx context.Context, ip netip.Addr) bool {
	if a.AddressInUse != nil {
		return a.AddressInUse(ctx, ip)
	}
	return addressInUse(ctx, ip)
}

// ownAddress: one of this system's own addresses.
func ownAddress(ip netip.Addr) bool {
	addrs, _ := net.InterfaceAddrs()
	for _, ad := range addrs {
		if n, ok := ad.(*net.IPNet); ok {
			if a, ok := netip.AddrFromSlice(n.IP); ok && a.Unmap() == ip {
				return true
			}
		}
	}
	return false
}

// configDiffers names what the device's configuration shows differently from what was written.
func configDiffers(want, got eq3disc.Config) []string {
	var out []string
	if want.DHCP != got.DHCP {
		out = append(out, "dhcp")
	}
	for _, f := range []struct{ name, a, b string }{{"ip", want.IP, got.IP}, {"netmask", want.Netmask, got.Netmask}, {"gateway", want.Gateway, got.Gateway}} {
		if f.a != "" && f.a != f.b {
			out = append(out, f.name)
		}
	}
	return out
}
