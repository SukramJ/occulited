package system

import (
	"bufio"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Network is the read-only view of the network configuration (task 8) and of the live interfaces
// (task 58). Writes are the confirm-or-revert transaction in netwrite.go; nothing here changes
// anything.
type Network struct {
	Hostname string `json:"hostname"`
	// Domain is the box's DNS domain from /etc/resolv.conf (Root.Domain); empty when it has none.
	Domain     string            `json:"domain"`
	Mode       string            `json:"mode"` // "dhcp" or "static", from netconfig
	Address    string            `json:"address,omitempty"`
	Netmask    string            `json:"netmask,omitempty"`
	Gateway    string            `json:"gateway,omitempty"`
	DNS        []string          `json:"dns"`
	IPv6       string            `json:"ipv6,omitempty"` // netconfig's IPV6 key, shown, never written
	Raw        map[string]string `json:"raw"`
	Interfaces []NetIface        `json:"interfaces"`
	// IPv6State is the box-wide IPv6 picture: on or off, the default gateway, the name servers.
	IPv6State IPv6State `json:"ipv6_state"`
}

// NetIface is a live interface: its link from /sys/class/net, its addresses, its routes.
type NetIface struct {
	Name string `json:"name"`
	MAC  string `json:"mac,omitempty"`
	// Up is operstate "up"; Operstate is the word itself (up, down, dormant, unknown, ...).
	Up        bool   `json:"up"`
	Operstate string `json:"operstate,omitempty"`
	// Carrier is whether a cable (or the link partner) is there; absent while the interface is
	// administratively down, when the kernel refuses to say.
	Carrier *bool `json:"carrier,omitempty"`
	// Speed is Mbit/s; absent when sysfs has none (a link that is down, Wi-Fi, most virtual ones).
	Speed int `json:"speed,omitempty"`
	// Duplex is full or half; absent when unknown.
	Duplex string `json:"duplex,omitempty"`
	MTU    int    `json:"mtu,omitempty"`
	// Kind: ethernet, wireless, bridge, vlan, veth, virtual or other.
	Kind   string `json:"kind"`
	Driver string `json:"driver,omitempty"`
	// DefaultRoute: the interface carries the default route (IPv4, or IPv6 without an IPv4 one).
	DefaultRoute bool      `json:"default_route,omitempty"`
	IPv4         []NetAddr `json:"ipv4"`
	IPv6         []NetAddr `json:"ipv6"`
	// IPv6Enabled is the inverse of /proc/sys/net/ipv6/conf/<if>/disable_ipv6; IPv6Autoconf is
	// whether the interface takes addresses from router advertisements (autoconf and accept_ra).
	// Both absent when the files are not there.
	IPv6Enabled  *bool     `json:"ipv6_enabled,omitempty"`
	IPv6Autoconf *bool     `json:"ipv6_autoconf,omitempty"`
	Statistics   *NetStats `json:"statistics,omitempty"`
}

// NetAddr is one address with its prefix length. The IPv6 ones carry the scope - global,
// unique-local, link-local, host - and the kernel's flags: temporary (a privacy address from
// router advertisements), deprecated, tentative, dad-failed, and permanent for an address that
// was configured rather than learnt with a lifetime.
type NetAddr struct {
	Address string   `json:"address"`
	Prefix  int      `json:"prefix"`
	Scope   string   `json:"scope,omitempty"`
	Flags   []string `json:"flags,omitempty"`
}

// NetStats are the interface's counters since boot.
type NetStats struct {
	RxBytes   uint64 `json:"rx_bytes"`
	TxBytes   uint64 `json:"tx_bytes"`
	RxErrors  uint64 `json:"rx_errors"`
	TxErrors  uint64 `json:"tx_errors"`
	RxDropped uint64 `json:"rx_dropped"`
	TxDropped uint64 `json:"tx_dropped"`
}

// IPv6State is IPv6 for the box as a whole.
type IPv6State struct {
	// Available: the kernel has IPv6 (/proc/net/if_inet6 exists); Enabled: and it is not switched
	// off for all interfaces (conf/all/disable_ipv6).
	Available bool `json:"available"`
	Enabled   bool `json:"enabled"`
	// Gateway is the next hop of the IPv6 default route, GatewayInterface its interface.
	Gateway          string   `json:"gateway,omitempty"`
	GatewayInterface string   `json:"gateway_interface,omitempty"`
	DNS              []string `json:"dns"`
}

// AddrsOf returns the live addresses of an interface. It is the one read here that is not a file
// under Root - the IPv4 addresses have no file in /proc worth parsing, Go's standard library asks
// the kernel over netlink - so a test replaces it.
var AddrsOf = func(name string) ([]net.Addr, error) {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	return ifi.Addrs()
}

// ReadNetwork parses /etc/config/netconfig (KEY=value, written by the WebUI and read by
// S40network) and the live interface state: /sys/class/net, /proc/net/if_inet6, the routing
// tables in /proc/net and /etc/resolv.conf. Everything is readable by the unprivileged daemon;
// nothing shells out.
func (r Root) ReadNetwork() Network {
	kv := readKV(r.join("/etc/config/netconfig"))
	n := Network{Raw: kv, DNS: []string{}, Interfaces: []NetIface{}}
	n.Hostname = kv["HOSTNAME"]
	if n.Hostname == "" {
		n.Hostname = r.kernelHostname()
	}
	switch strings.ToUpper(kv["MODE"]) {
	case "MANUAL", "STATIC":
		n.Mode = "static"
	default:
		n.Mode = "dhcp"
	}
	n.Address, n.Netmask, n.Gateway = kv["IP"], kv["NETMASK"], kv["GATEWAY"]
	for _, k := range []string{"NAMESERVER1", "NAMESERVER2", "NAMESERVER3", "DNS1", "DNS2"} {
		if v := kv[k]; v != "" {
			n.DNS = append(n.DNS, v)
		}
	}
	n.IPv6 = kv["IPV6"]

	inet6, haveInet6 := r.readIfInet6()
	v4dev := r.defaultRoute4()
	v6gw, v6dev := r.defaultRoute6()
	entries, _ := os.ReadDir(r.join("/sys/class/net"))
	for _, e := range entries {
		name := e.Name()
		if name == "lo" {
			continue
		}
		// /sys/class/net holds a symlink per interface, and bonding_masters as a plain file
		if st, err := os.Stat(r.join("/sys/class/net/" + name)); err != nil || !st.IsDir() {
			continue
		}
		i := r.readIface(name)
		i.DefaultRoute = name == v4dev || (v4dev == "" && name == v6dev)
		for _, a := range addrs(name) {
			if a.IP.To4() != nil {
				ones, _ := a.Mask.Size()
				i.IPv4 = append(i.IPv4, NetAddr{Address: a.IP.String(), Prefix: ones})
			} else if !haveInet6 {
				ones, _ := a.Mask.Size()
				i.IPv6 = append(i.IPv6, NetAddr{Address: a.IP.String(), Prefix: ones, Scope: ipv6Scope(a.IP)})
			}
		}
		if haveInet6 {
			i.IPv6 = append(i.IPv6, inet6[name]...)
		}
		if i.IPv6 == nil {
			i.IPv6 = []NetAddr{}
		}
		n.Interfaces = append(n.Interfaces, i)
	}

	resolv := r.readResolvConf()
	n.Domain = resolv.domain()
	n.IPv6State = IPv6State{Available: haveInet6, Gateway: v6gw, GatewayInterface: v6dev, DNS: []string{}}
	n.IPv6State.Enabled = haveInet6 && strings.TrimSpace(readFile(r.join("/proc/sys/net/ipv6/conf/all/disable_ipv6"))) != "1"
	for _, ns := range resolv.Nameservers {
		if strings.Contains(ns, ":") {
			n.IPv6State.DNS = append(n.IPv6State.DNS, ns)
		}
	}
	return n
}

// readIface reads one interface's link out of /sys/class/net/<name>.
func (r Root) readIface(name string) NetIface {
	base := "/sys/class/net/" + name
	attr := func(a string) (string, bool) {
		b, err := os.ReadFile(r.join(base + "/" + a))
		if err != nil {
			return "", false
		}
		return strings.TrimSpace(string(b)), true
	}
	i := NetIface{Name: name, IPv4: []NetAddr{}}
	i.MAC, _ = attr("address")
	i.Operstate, _ = attr("operstate")
	i.Up = i.Operstate == "up"
	// the kernel answers EINVAL for carrier and speed while the interface is administratively down
	if v, ok := attr("carrier"); ok && (v == "0" || v == "1") {
		c := v == "1"
		i.Carrier = &c
	}
	if v, ok := attr("speed"); ok {
		if s, err := strconv.Atoi(v); err == nil && s > 0 {
			i.Speed = s
		}
	}
	if v, ok := attr("duplex"); ok && (v == "full" || v == "half") {
		i.Duplex = v
	}
	if v, ok := attr("mtu"); ok {
		i.MTU, _ = strconv.Atoi(v)
	}
	if target, err := os.Readlink(r.join(base + "/device/driver")); err == nil {
		i.Driver = filepath.Base(target)
	}
	i.Kind = r.ifaceKind(base, attr)
	if v, err := os.ReadFile(r.join("/proc/sys/net/ipv6/conf/" + name + "/disable_ipv6")); err == nil {
		on := strings.TrimSpace(string(v)) != "1"
		i.IPv6Enabled = &on
		autoconf := strings.TrimSpace(readFile(r.join("/proc/sys/net/ipv6/conf/"+name+"/autoconf"))) == "1"
		acceptRA := strings.TrimSpace(readFile(r.join("/proc/sys/net/ipv6/conf/"+name+"/accept_ra"))) != "0"
		ra := autoconf && acceptRA
		i.IPv6Autoconf = &ra
	}
	if _, ok := attr("statistics/rx_bytes"); ok {
		num := func(a string) uint64 {
			v, _ := attr("statistics/" + a)
			n, _ := strconv.ParseUint(v, 10, 64)
			return n
		}
		i.Statistics = &NetStats{RxBytes: num("rx_bytes"), TxBytes: num("tx_bytes"), RxErrors: num("rx_errors"), TxErrors: num("tx_errors"), RxDropped: num("rx_dropped"), TxDropped: num("tx_dropped")}
	}
	return i
}

// ifaceKind tells the kinds apart the way `ip -d link` would, from sysfs alone: a wireless
// directory or DEVTYPE=wlan is Wi-Fi, a bridge directory a bridge, DEVTYPE=vlan a VLAN; without a
// device behind it an interface is virtual, and a virtual Ethernet whose link is another index is
// one end of a veth pair (a container's eth0).
func (r Root) ifaceKind(base string, attr func(string) (string, bool)) string {
	exists := func(p string) bool {
		_, err := os.Lstat(r.join(base + "/" + p))
		return err == nil
	}
	devtype := ""
	if u, ok := attr("uevent"); ok {
		for _, l := range strings.Split(u, "\n") {
			if v, found := strings.CutPrefix(strings.TrimSpace(l), "DEVTYPE="); found {
				devtype = v
			}
		}
	}
	typ, _ := attr("type")
	switch {
	case devtype == "wlan" || exists("wireless") || exists("phy80211"):
		return "wireless"
	case devtype == "bridge" || exists("bridge"):
		return "bridge"
	case devtype == "vlan":
		return "vlan"
	case !exists("device"):
		ifindex, _ := attr("ifindex")
		iflink, _ := attr("iflink")
		if typ == "1" && devtype == "" && ifindex != "" && iflink != "" && ifindex != iflink {
			return "veth"
		}
		return "virtual"
	case typ == "1":
		return "ethernet"
	}
	return "other"
}

// addrs is AddrsOf as IP networks; an interface the kernel does not know has none.
func addrs(name string) []*net.IPNet {
	list, err := AddrsOf(name)
	if err != nil {
		return nil
	}
	out := []*net.IPNet{}
	for _, a := range list {
		if n, ok := a.(*net.IPNet); ok {
			out = append(out, n)
		}
	}
	return out
}

// ipv6Scope names where an IPv6 address is valid.
func ipv6Scope(ip net.IP) string {
	switch {
	case ip.IsLoopback():
		return "host"
	case ip.IsLinkLocalUnicast():
		return "link-local"
	case len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc:
		return "unique-local"
	}
	return "global"
}

// The address flags of /proc/net/if_inet6 (IFA_F_* in linux/if_addr.h) that say something to a
// reader, in the order they are listed.
var ipv6Flags = []struct {
	bit  uint64
	name string
}{
	{0x01, "temporary"},
	{0x20, "deprecated"},
	{0x40, "tentative"},
	{0x08, "dad-failed"},
	{0x80, "permanent"},
}

// readIfInet6 parses /proc/net/if_inet6 - address, ifindex, prefix length, scope, flags, name, all
// hex but the name - into the addresses per interface, in the kernel's order. false when the
// file is not there (a kernel without IPv6).
func (r Root) readIfInet6() (map[string][]NetAddr, bool) {
	f, err := os.Open(r.join("/proc/net/if_inet6"))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	out := map[string][]NetAddr{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 6 {
			continue
		}
		raw, err := hex.DecodeString(fields[0])
		if err != nil || len(raw) != net.IPv6len {
			continue
		}
		prefix, err1 := strconv.ParseUint(fields[2], 16, 8)
		flags, err2 := strconv.ParseUint(fields[4], 16, 32)
		if err1 != nil || err2 != nil {
			continue
		}
		ip := net.IP(raw)
		a := NetAddr{Address: ip.String(), Prefix: int(prefix), Scope: ipv6Scope(ip)}
		for _, fl := range ipv6Flags {
			if flags&fl.bit != 0 {
				a.Flags = append(a.Flags, fl.name)
			}
		}
		out[fields[5]] = append(out[fields[5]], a)
	}
	return out, true
}

// defaultRoute4 is the interface of the IPv4 default route with the lowest metric, from
// /proc/net/route (Iface Destination Gateway Flags RefCnt Use Metric Mask ...).
func (r Root) defaultRoute4() string {
	f, err := os.Open(r.join("/proc/net/route"))
	if err != nil {
		return ""
	}
	defer f.Close()
	dev, best := "", uint64(1<<63)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || fields[1] != "00000000" || fields[7] != "00000000" {
			continue
		}
		flags, err := strconv.ParseUint(fields[3], 16, 32)
		if err != nil || flags&0x1 == 0 { // RTF_UP
			continue
		}
		metric, err := strconv.ParseUint(fields[6], 10, 64)
		if err == nil && metric < best {
			dev, best = fields[0], metric
		}
	}
	return dev
}

// defaultRoute6 is the next hop and interface of the IPv6 default route with the lowest metric,
// from /proc/net/ipv6_route (destination, its length, source, its length, next hop, metric,
// refcount, use, flags, device). The kernel lists an unreachable default on lo; it is not one.
func (r Root) defaultRoute6() (gateway, dev string) {
	f, err := os.Open(r.join("/proc/net/ipv6_route"))
	if err != nil {
		return "", ""
	}
	defer f.Close()
	best := uint64(1 << 63)
	zero := strings.Repeat("0", 32)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 10 || fields[0] != zero || fields[1] != "00" || fields[9] == "lo" {
			continue
		}
		flags, err1 := strconv.ParseUint(fields[8], 16, 32)
		metric, err2 := strconv.ParseUint(fields[5], 16, 32)
		if err1 != nil || err2 != nil || flags&0x1 == 0 || flags&0x200 != 0 { // RTF_UP, not RTF_REJECT
			continue
		}
		if metric >= best {
			continue
		}
		best, dev, gateway = metric, fields[9], ""
		if fields[4] != zero {
			if raw, err := hex.DecodeString(fields[4]); err == nil && len(raw) == net.IPv6len {
				gateway = net.IP(raw).String()
			}
		}
	}
	return gateway, dev
}

// ResolvConf is what /etc/resolv.conf says: the name servers in order, the domain and the search
// list. resolvconf writes it from what the DHCP client and the static setup hand it.
type ResolvConf struct {
	Nameservers []string
	Domain      string
	Search      []string
}

// domain is the box's DNS domain: the domain line, else the first entry of the search line, else
// none - lower case, without a trailing dot.
func (rc ResolvConf) domain() string {
	d := rc.Domain
	if d == "" && len(rc.Search) > 0 {
		d = rc.Search[0]
	}
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
}

// Hostname is the box's name as the Network page shows it: netconfig's HOSTNAME, else the kernel's.
// A cheap read of two files, for callers that need nothing else of ReadNetwork.
func (r Root) Hostname() string {
	if h := readKV(r.join("/etc/config/netconfig"))["HOSTNAME"]; h != "" {
		return h
	}
	return r.kernelHostname()
}

// Domain is the box's DNS domain from /etc/resolv.conf - the domain line, else the first search
// entry, else "". A DHCP lease provides it: the client's dhcp.script hands domain and search to
// resolvconf, which writes the file.
func (r Root) Domain() string { return r.readResolvConf().domain() }

func (r Root) kernelHostname() string {
	b, err := os.ReadFile(r.join("/proc/sys/kernel/hostname"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (r Root) readResolvConf() ResolvConf {
	b, err := os.ReadFile(r.join("/etc/resolv.conf"))
	if err != nil {
		return ResolvConf{}
	}
	return parseResolv(string(b))
}

// parseResolv reads resolv.conf's format, as resolvconf's records have it too.
func parseResolv(text string) ResolvConf {
	var rc ResolvConf
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			rc.Nameservers = append(rc.Nameservers, fields[1])
		case "domain":
			rc.Domain = fields[1]
		case "search":
			rc.Search = fields[1:]
		}
	}
	return rc
}
