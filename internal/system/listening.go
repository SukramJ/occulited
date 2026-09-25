package system

import (
	"bufio"
	"encoding/hex"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/hobbyquaker/occulited/internal/firewall"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// ---- what is listening, and which rule covers it (B-41, task 157) ---------------------------
//
// The firewall knows its rules; occulited can see both sides, so the Firewall page lists every
// listening socket with the process holding it and the rule that decides its fate per family - or
// the policy, when no rule names its port.

// Coverage is what decides one family's packets to a listener: the first rule for its port and
// protocol, or (Rule empty) the policy.
type Coverage struct {
	Rule   string `json:"rule,omitempty"`
	Target string `json:"target"`
	Source string `json:"source,omitempty"`
	Owner  string `json:"owner,omitempty"`
}

// Listener is one listening socket, as /proc/net has it, with its owner and coverage.
type Listener struct {
	Proto string `json:"proto"` // tcp, tcp6, udp, udp6
	Port  int    `json:"port"`
	// Address is the local address it is bound to; Loopback says it is 127.0.0.0/8 or ::1, which
	// no rule reaches.
	Address  string `json:"address"`
	Loopback bool   `json:"loopback"`
	// PID, Process and Unit are the process holding the socket (the helper's socket owners);
	// empty when it could not be read.
	PID     int    `json:"pid,omitempty"`
	Process string `json:"process,omitempty"`
	Unit    string `json:"unit,omitempty"`
	// Cover is per family the socket serves (ipv4, ipv6) what decides its packets.
	Cover map[string]Coverage `json:"cover"`
	Inode uint64              `json:"-"`
}

// procNetFiles is the set read by ListeningPorts, overridable in tests.
var procNetFiles = map[string]string{
	"tcp":  "/proc/net/tcp",
	"tcp6": "/proc/net/tcp6",
	"udp":  "/proc/net/udp",
	"udp6": "/proc/net/udp6",
}

// tcpListen is the state a listening TCP socket has in /proc/net/tcp ("0A" = TCP_LISTEN). A UDP
// socket has no such state - every bound one is reachable - so udp rows are taken as they come.
const tcpListen = "0A"

// socketFamilies is what a socket serves: an IPv4 socket IPv4, one on :: both (dual stack), an
// IPv4-mapped IPv6 address IPv4, any other IPv6 address IPv6.
func socketFamilies(l Listener) []string {
	if !strings.HasSuffix(l.Proto, "6") {
		return []string{firewall.FamilyV4}
	}
	ip := net.ParseIP(l.Address)
	switch {
	case ip == nil:
		return []string{firewall.FamilyV6}
	case ip.IsUnspecified():
		return []string{firewall.FamilyV4, firewall.FamilyV6}
	case ip.To4() != nil:
		return []string{firewall.FamilyV4}
	}
	return []string{firewall.FamilyV6}
}

// coverage is the first rule for the port, protocol and family, or the family's policy.
func coverage(c firewall.Config, proto string, port int, family string) Coverage {
	for _, r := range c.Rules {
		// a rule narrowed to a source port (replies to the system's own discovery) or to a
		// destination (multicast) does not decide what reaches a listener from a client
		if r.SPort != 0 || r.Dest != "" || r.Disabled {
			continue
		}
		if r.HasPort(port) && r.Proto == proto && r.Covers(family) {
			return Coverage{Rule: r.ID, Target: r.Target, Source: r.Source, Owner: r.Owner}
		}
	}
	if family == firewall.FamilyV6 {
		return Coverage{Target: c.Policy.V6}
	}
	return Coverage{Target: c.Policy.V4}
}

// ListeningPorts reads the listening sockets, names their owners (the helper's socket owners, nil
// when there are none) and marks each family against the rules. Loopback-bound sockets are
// reported too, with Loopback set: they are the expected case for most of what the box runs.
func (r Root) ListeningPorts(c firewall.Config, owners []priv.SocketOwner) []Listener {
	byInode := map[uint64]priv.SocketOwner{}
	for _, o := range owners {
		if _, ok := byInode[o.Inode]; !ok {
			byInode[o.Inode] = o
		}
	}
	var out []Listener
	for proto, path := range procNetFiles {
		for _, l := range parseProcNet(readFile(r.join(path)), proto) {
			if o, ok := byInode[l.Inode]; ok && l.Inode != 0 {
				l.PID, l.Process, l.Unit = o.PID, o.Comm, o.Unit
			}
			l.Cover = map[string]Coverage{}
			base := strings.TrimSuffix(l.Proto, "6")
			for _, f := range socketFamilies(l) {
				l.Cover[f] = coverage(c, base, l.Port, f)
			}
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		if out[i].Proto != out[j].Proto {
			return out[i].Proto < out[j].Proto
		}
		return out[i].Address < out[j].Address
	})
	// One address and port can carry two sockets (SO_REUSEPORT: two mDNS responders on
	// 0.0.0.0:5353): one row, the first owner's. Protocol, address and port are unique in the
	// answer, and the page keys its rows by them.
	uniq := out[:0]
	for _, l := range out {
		if n := len(uniq); n > 0 && uniq[n-1].Proto == l.Proto && uniq[n-1].Address == l.Address && uniq[n-1].Port == l.Port {
			continue
		}
		uniq = append(uniq, l)
	}
	return uniq
}

// parseProcNet reads one /proc/net/{tcp,tcp6,udp,udp6}. The local address is hex, big or little
// endian by word depending on the architecture - the kernel writes it in host byte order - so the
// address is decoded per 4-byte word and the port is a plain big-endian hex number.
func parseProcNet(body, proto string) []Listener {
	var out []Listener
	sc := bufio.NewScanner(strings.NewReader(body))
	first := true
	for sc.Scan() {
		if first { // the header row
			first = false
			continue
		}
		f := strings.Fields(sc.Text())
		if len(f) < 4 {
			continue
		}
		if strings.HasPrefix(proto, "tcp") && f[3] != tcpListen {
			continue
		}
		host, port, ok := splitProcAddr(f[1])
		if !ok {
			continue
		}
		var inode uint64
		if len(f) > 9 {
			inode, _ = strconv.ParseUint(f[9], 10, 64)
		}
		out = append(out, Listener{Proto: proto, Address: host.String(), Port: port, Loopback: host.IsLoopback(), Inode: inode})
	}
	return out
}

// splitProcAddr turns "0100007F:1F91" or the 32-character IPv6 form into an address and a port.
func splitProcAddr(s string) (net.IP, int, bool) {
	h, p, found := strings.Cut(s, ":")
	if !found {
		return nil, 0, false
	}
	port, err := strconv.ParseUint(p, 16, 32)
	if err != nil || port == 0 || port > 65535 {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, false
	}
	// each 4-byte word is little-endian on the architectures this runs on
	for i := 0; i+4 <= len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return net.IP(raw), int(port), true
}
