package eq3disc

// The other half of the discovery protocol (openccu-lite task 220): finding eQ-3's LAN devices -
// the LAN gateways, the HmIP access points, other CCUs - and reading their network settings, the
// way eQ-3's NetFinder does. Written from the protocol's documented facts and the lab's captured
// answers, not from eQ-3's code:
//
//	request  02 <sender id 3B> <counter> <type pattern> 00 <serial pattern> 00 <opcode> [payload]
//	answer   <version> <sender id> <counter> <type> 00 <serial> 00 '>' <opcode> <code> [data]
//
// The version byte of an answer is 2 for the gateways and CCUs and 4 for the access points (HAP,
// DRAP), whose layout is otherwise the same - both are read. The code is 0 error, 1 OK, 2 OK and
// the device restarts, 3 encryption required (then 16 bytes of IV follow).
//
// Identify (`I`) answers the version string, 00, and (protocol, port) u16 pairs up to a 0; `n`
// answers the address the device runs with, `c` the configured one with the flags and the DNS
// name. The addresses are four bytes each: address, gateway, netmask, DNS 1, DNS 2.
//
// No listener: a scan is one short-lived client socket on an ephemeral port (D-29), and the
// answers come in through the firewall's rule for replies from udp 43439.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// The answer codes.
const (
	CodeError     = 0
	CodeOK        = 1
	CodeOKRestart = 2
	CodeEncrypted = 3
)

// Answer is one parsed answer.
type Answer struct {
	Version byte
	Sender  [3]byte
	Counter byte
	Type    string
	Serial  string
	Opcode  byte
	Code    byte
	Data    []byte
	From    netip.Addr
}

// ParseAnswer reads an answer datagram (version 2 or later: sender id and counter present).
func ParseAnswer(b []byte) (Answer, bool) {
	if len(b) < 6 || b[0] < 2 {
		return Answer{}, false
	}
	a := Answer{Version: b[0], Counter: b[4]}
	copy(a.Sender[:], b[1:4])
	at := 5
	str := func() (string, bool) {
		end := bytes.IndexByte(b[at:], 0)
		if end < 0 {
			return "", false
		}
		s := string(b[at : at+end])
		at += end + 1
		return s, true
	}
	var ok bool
	if a.Type, ok = str(); !ok {
		return Answer{}, false
	}
	if a.Serial, ok = str(); !ok {
		return Answer{}, false
	}
	if len(b) < at+3 || b[at] != replyMark {
		return Answer{}, false
	}
	a.Opcode, a.Code, a.Data = b[at+1], b[at+2], append([]byte(nil), b[at+3:]...)
	return a, true
}

// BuildRequest makes a request datagram, version 2.
func BuildRequest(sender [3]byte, counter byte, typ, serial string, op byte, payload []byte) []byte {
	b := make([]byte, 0, 8+len(typ)+len(serial)+len(payload))
	b = append(b, 2)
	b = append(b, sender[:]...)
	b = append(b, counter)
	b = append(b, typ...)
	b = append(b, 0)
	b = append(b, serial...)
	b = append(b, 0, op)
	return append(b, payload...)
}

// Service is one (protocol, port) pair of an Identify answer: the LAN gateways name the TCP ports
// rfd or hs485d connects to (the HM-LGW: 2 → 2000, 42 → 2001).
type Service struct {
	Protocol int `json:"protocol"`
	Port     int `json:"port"`
}

// ParseIdentify reads the data of an `I` answer: the version string, then the service pairs.
func ParseIdentify(d []byte) (version string, services []Service) {
	end := bytes.IndexByte(d, 0)
	if end < 0 {
		return string(d), nil
	}
	version = string(d[:end])
	rest := d[end+1:]
	for len(rest) >= 4 {
		p := int(binary.BigEndian.Uint16(rest))
		if p == 0 {
			break
		}
		services = append(services, Service{Protocol: p, Port: int(binary.BigEndian.Uint16(rest[2:]))})
		rest = rest[4:]
	}
	return version, services
}

// Addresses are the five IPv4 values of `n`, `c` and `C`.
type Addresses struct {
	IP      string `json:"ip"`
	Gateway string `json:"gateway"`
	Netmask string `json:"netmask"`
	DNS1    string `json:"dns1"`
	DNS2    string `json:"dns2"`
}

func readAddresses(d []byte) (Addresses, bool) {
	if len(d) < 20 {
		return Addresses{}, false
	}
	ip := func(i int) string { return netip.AddrFrom4([4]byte(d[i : i+4])).String() }
	return Addresses{IP: ip(0), Gateway: ip(4), Netmask: ip(8), DNS1: ip(12), DNS2: ip(16)}, true
}

func (a Addresses) bytes() ([]byte, error) {
	out := make([]byte, 0, 20)
	for _, s := range []string{a.IP, a.Gateway, a.Netmask, a.DNS1, a.DNS2} {
		if s == "" {
			s = "0.0.0.0"
		}
		p, err := netip.ParseAddr(s)
		if err != nil || !p.Is4() {
			return nil, fmt.Errorf("not an IPv4 address: %q", s)
		}
		b := p.As4()
		out = append(out, b[:]...)
	}
	return out, nil
}

// The IP flags of `c` and `C`.
const (
	FlagDHCP   = 1 << 0
	FlagAutoIP = 1 << 1
)

// The crypt byte of `c`: bit 0 encryption on, bit 1 a key of the user's (not the factory key).
const (
	CryptOn      = 1 << 0
	CryptUserKey = 1 << 1
)

// Config is what `c` answers and `C` writes.
type Config struct {
	Addresses
	DHCP    bool   `json:"dhcp"`
	AutoIP  bool   `json:"auto_ip"`
	Crypt   byte   `json:"crypt"`
	NameMax int    `json:"name_max,omitempty"`
	Name    string `json:"name"`
}

// ParseConfig reads the data of a `c` answer.
func ParseConfig(d []byte) (Config, bool) {
	a, ok := readAddresses(d)
	if !ok || len(d) < 23 {
		return Config{}, false
	}
	c := Config{Addresses: a, DHCP: d[20]&FlagDHCP != 0, AutoIP: d[20]&FlagAutoIP != 0, Crypt: d[21], NameMax: int(d[22])}
	name := d[23:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	c.Name = string(name)
	return c, true
}

// SetConfigPayload is the payload of `C`: the five addresses, the IP flags, the encryption switch
// (kept as the device has it) and the DNS name.
func SetConfigPayload(c Config) ([]byte, error) {
	b, err := c.Addresses.bytes()
	if err != nil {
		return nil, err
	}
	var flags byte
	if c.DHCP {
		flags |= FlagDHCP
	}
	if c.AutoIP {
		flags |= FlagAutoIP
	}
	b = append(b, flags, c.Crypt&CryptOn)
	b = append(b, c.Name...)
	return append(b, 0), nil
}

// Found is one device a scan found.
type Found struct {
	Type            string    `json:"type"`
	Serial          string    `json:"serial"`
	Version         string    `json:"version"`
	ProtocolVersion int       `json:"protocol_version"`
	IP              string    `json:"ip"`
	Services        []Service `json:"services,omitempty"`
	// Runtime is `n`, Config `c`; nil when the device did not answer them (a CCU is not asked).
	Runtime *Addresses `json:"runtime,omitempty"`
	Config  *Config    `json:"config,omitempty"`
}

// IsCCU says whether a type is a system (a CCU, an OpenCCU, an openccu-lite), not a LAN device.
func IsCCU(typ string) bool { return strings.Contains(strings.ToUpper(typ), "CCU") }

// Client finds devices. The zero value is ready; the fields are test seams and tunables.
type Client struct {
	// Wait is how long a scan collects Identify answers; 0 = 2.5 s.
	Wait time.Duration
	// Timeout is how long a unicast question waits for its answer; 0 = 1 s. Asked twice.
	Timeout time.Duration
	// Targets are where the Identify goes; nil = 255.255.255.255, 224.0.0.1 and the directed
	// broadcast of every IPv4 interface.
	Targets func() []netip.Addr
	// Own are this system's addresses, whose own answer is left out; nil = the interfaces'.
	Own func() map[netip.Addr]bool
	// Port is where requests go; 0 = 43439.
	Port int
	// Listen opens the client socket; nil = an ephemeral udp4 socket with SO_BROADCAST.
	Listen func() (net.PacketConn, error)

	mu sync.Mutex // one exchange at a time: the counter and the socket are per call
}

func (c *Client) wait() time.Duration {
	if c.Wait > 0 {
		return c.Wait
	}
	return 2500 * time.Millisecond
}

func (c *Client) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return time.Second
}

func (c *Client) port() int {
	if c.Port > 0 {
		return c.Port
	}
	return Port
}

func (c *Client) listen() (net.PacketConn, error) {
	if c.Listen != nil {
		return c.Listen()
	}
	lc := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		var opErr error
		if err := rc.Control(func(fd uintptr) {
			opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
		}); err != nil {
			return err
		}
		return opErr
	}}
	return lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:0")
}

func (c *Client) targets() []netip.Addr {
	if c.Targets != nil {
		return c.Targets()
	}
	out := []netip.Addr{netip.AddrFrom4([4]byte{255, 255, 255, 255}), netip.AddrFrom4([4]byte{224, 0, 0, 1})}
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 || i.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if ip4 := n.IP.To4(); ip4 != nil && len(n.Mask) == net.IPv4len {
					var b [4]byte
					for k := range b {
						b[k] = ip4[k] | ^n.Mask[k]
					}
					out = append(out, netip.AddrFrom4(b))
				}
			}
		}
	}
	return out
}

func (c *Client) own() map[netip.Addr]bool {
	if c.Own != nil {
		return c.Own()
	}
	out := map[netip.Addr]bool{}
	for _, a := range localAddresses() {
		out[a] = true
	}
	return out
}

func newSender() [3]byte {
	var s [3]byte
	_, _ = rand.Read(s[:])
	return s
}

// exchange sends one datagram and returns the answers carrying its sender id and counter until
// done says so or the wait ends.
func (c *Client) exchange(ctx context.Context, conn net.PacketConn, to []netip.Addr, req []byte, sender [3]byte, counter byte, wait time.Duration, done func([]Answer) bool) ([]Answer, error) {
	sent := 0
	var lastErr error
	for _, t := range to {
		if _, err := conn.WriteTo(req, net.UDPAddrFromAddrPort(netip.AddrPortFrom(t, uint16(c.port())))); err != nil {
			lastErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		return nil, fmt.Errorf("sending: %w", lastErr)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	var out []Answer
	buf := make([]byte, 2048)
	for {
		_ = conn.SetReadDeadline(deadline)
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return out, nil
			}
			return out, err
		}
		a, ok := ParseAnswer(buf[:n])
		if !ok || a.Sender != sender || a.Counter != counter {
			continue
		}
		if ua, ok := from.(*net.UDPAddr); ok {
			a.From = ua.AddrPort().Addr().Unmap()
		}
		out = append(out, a)
		if done != nil && done(out) {
			return out, nil
		}
	}
}

// Scan finds the eQ-3 devices on the LAN: one Identify to the broadcast and multicast targets,
// then `n` and `c` to each device that is not a CCU. This system's own answer is left out.
func (c *Client) Scan(ctx context.Context) ([]Found, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	sender := newSender()
	answers, err := c.exchange(ctx, conn, c.targets(), BuildRequest(sender, 1, "eQ3-*", "*", OpIdentify, nil), sender, 1, c.wait(), nil)
	if err != nil {
		return nil, err
	}
	own := c.own()
	seen := map[string]bool{}
	var found []Found
	for _, a := range answers {
		if a.Opcode != OpIdentify || a.Code != CodeOK || own[a.From] || seen[a.Type+"\x00"+a.Serial] {
			continue
		}
		seen[a.Type+"\x00"+a.Serial] = true
		v, svc := ParseIdentify(a.Data)
		found = append(found, Found{Type: a.Type, Serial: a.Serial, Version: v, ProtocolVersion: int(a.Version), IP: a.From.String(), Services: svc})
	}
	var counter byte = 1
	for k := range found {
		f := &found[k]
		if IsCCU(f.Type) || ctx.Err() != nil {
			continue
		}
		to, _ := netip.ParseAddr(f.IP)
		for _, op := range []byte{OpCurrent, OpConfig} {
			counter++
			a, ok := c.ask(ctx, conn, []netip.Addr{to}, sender, counter, f.Type, f.Serial, op, nil)
			if !ok || a.Code != CodeOK {
				continue
			}
			switch op {
			case OpCurrent:
				if r, ok := readAddresses(a.Data); ok {
					f.Runtime = &r
				}
			case OpConfig:
				if cf, ok := ParseConfig(a.Data); ok {
					f.Config = &cf
				}
			}
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if IsCCU(found[i].Type) != IsCCU(found[j].Type) {
			return !IsCCU(found[i].Type)
		}
		if found[i].Type != found[j].Type {
			return found[i].Type < found[j].Type
		}
		return found[i].Serial < found[j].Serial
	})
	return found, nil
}

// ask sends one request to one device (by its exact type and serial) and waits for its answer,
// twice at most.
func (c *Client) ask(ctx context.Context, conn net.PacketConn, to []netip.Addr, sender [3]byte, counter byte, typ, serial string, op byte, payload []byte) (Answer, bool) {
	req := BuildRequest(sender, counter, typ, serial, op, payload)
	for try := 0; try < 2 && ctx.Err() == nil; try++ {
		got, err := c.exchange(ctx, conn, to, req, sender, counter, c.timeout(), func(a []Answer) bool {
			for _, x := range a {
				if x.Serial == serial {
					return true
				}
			}
			return false
		})
		if err != nil {
			return Answer{}, false
		}
		for _, x := range got {
			if x.Serial == serial && x.Opcode == op {
				return x, true
			}
		}
	}
	return Answer{}, false
}
