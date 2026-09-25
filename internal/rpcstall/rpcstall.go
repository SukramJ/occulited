// Package rpcstall finds the callback listener that stalls an interface process (openccu-lite
// B-201): one XML-RPC client that registers with rfd or hmipserver and then accepts the daemon's
// calls without ever answering them stops the daemon for everybody.
//
// Measured on a Pi 4 lab system on 2026-09-25 (dev.24), with a listener that reads a call and never
// answers:
//
//   - hmipserver: its init(url, id) returns at once, and its listDevices to the new listener hangs.
//     From then on no event reaches any subscriber - occulited's own, RedMatic's, every other - while
//     hmipserver still answers every XML-RPC call (ping, init). It never timed out (see B-201 for
//     how long was watched), and the hanging listener is not in LegacyService.handlers, because its
//     registration never finished.
//   - rfd: its init itself does not return (rfd calls system.listMethods back before it answers), and
//     rfd's whole XML-RPC server stops answering - every client's calls time out.
//   - init(url, "") for the hanging listener answers, but does not release either daemon: the thread
//     stays in its read. Only the end of that TCP connection does.
//
// So the listener is found by what the daemon is connected to, not only by what its handlers file
// lists: the kernel's socket table (/proc/net/tcp, world-readable) says which sockets belong to the
// daemon's user. A registered listener is asked system.listMethods with its own id - the call every
// daemon makes on a new listener, exactly as it makes it: node-red-contrib-ccu, for one, reads the
// id and ends its whole Node-RED process on a call without one (seen on the lab system when this
// probe first went out without it). A listener the daemon is connected to that is not on its list
// is not called at all - there is no id to call it with - but it is the one the daemon waits on
// when every registered listener answers: hmipserver closes idle connections after about 30 s even
// while its delivery is held, and rfd connects to a listener that is not on its list only inside
// that listener's init.
package rpcstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Socket states in /proc/net/tcp.
const (
	stateEstablished = 0x01
	stateListen      = 0x0A
)

// Conn is one line of /proc/net/tcp or /proc/net/tcp6.
type Conn struct {
	Local, Remote netip.AddrPort
	State         int
	UID           int
	Inode         uint64
}

// ParseTCP reads the kernel's socket table, IPv4 and IPv6 alike; lines it cannot read are skipped.
// An IPv4-mapped IPv6 address (hmipserver's Java listens on :: and connects over it) is unmapped,
// so the same listener has the same address whichever table it is in.
func ParseTCP(b []byte) []Conn {
	var out []Conn
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 10 || !strings.HasSuffix(f[0], ":") {
			continue
		}
		local, err1 := parseAddr(f[1])
		remote, err2 := parseAddr(f[2])
		st, err3 := strconv.ParseInt(f[3], 16, 32)
		uid, err4 := strconv.Atoi(f[7])
		inode, err5 := strconv.ParseUint(f[9], 10, 64)
		if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
			continue
		}
		out = append(out, Conn{Local: local, Remote: remote, State: int(st), UID: uid, Inode: inode})
	}
	return out
}

// parseAddr reads "0100007F:1F90" (IPv4) or the 32-digit IPv6 form. The kernel prints each 32-bit
// word of the address in host byte order; every system openccu-lite runs on is little-endian.
func parseAddr(s string) (netip.AddrPort, error) {
	h, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, fmt.Errorf("no port in %q", s)
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, err
	}
	raw, err := hex.DecodeString(h)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, fmt.Errorf("bad address %q", h)
	}
	for i := 0; i < len(raw); i += 4 {
		w := binary.LittleEndian.Uint32(raw[i:])
		binary.BigEndian.PutUint32(raw[i:], w)
	}
	var a netip.Addr
	if len(raw) == 4 {
		a = netip.AddrFrom4([4]byte(raw))
	} else {
		a = netip.AddrFrom16([16]byte(raw)).Unmap()
	}
	return netip.AddrPortFrom(a, uint16(port)), nil
}

// Daemon is what the socket table says about the process listening on port: its user, and the
// addresses its connections go out to - the listeners it calls, and whatever else it talks to.
type Daemon struct {
	UID int
	// Listen are the ports the daemon's user listens on.
	Listen []uint16
	// Peers are the remote ends of its established outgoing connections, each once, sorted.
	Peers []netip.AddrPort
	// Conns are those connections, for Drop.
	Conns []Conn
}

// DaemonOf finds the daemon listening on port. ok is false when nothing listens there.
func DaemonOf(conns []Conn, port uint16) (Daemon, bool) {
	d := Daemon{UID: -1}
	for _, c := range conns {
		if c.State == stateListen && c.Local.Port() == port {
			d.UID = c.UID
			break
		}
	}
	if d.UID < 0 {
		return Daemon{}, false
	}
	listen := map[uint16]bool{}
	for _, c := range conns {
		if c.State == stateListen && c.UID == d.UID {
			listen[c.Local.Port()] = true
		}
	}
	for p := range listen {
		d.Listen = append(d.Listen, p)
	}
	sort.Slice(d.Listen, func(i, j int) bool { return d.Listen[i] < d.Listen[j] })
	seen := map[netip.AddrPort]bool{}
	for _, c := range conns {
		if c.State != stateEstablished || c.UID != d.UID || listen[c.Local.Port()] {
			continue // an accepted connection - a client calling the daemon - not one it made
		}
		if c.Remote.Addr().IsLoopback() && listen[c.Remote.Port()] {
			continue // the daemon's connection to itself (hmipserver's two halves)
		}
		d.Conns = append(d.Conns, c)
		if !seen[c.Remote] {
			seen[c.Remote] = true
			d.Peers = append(d.Peers, c.Remote)
		}
	}
	sort.Slice(d.Peers, func(i, j int) bool { return d.Peers[i].String() < d.Peers[j].String() })
	return d, true
}

// Subscriber is one entry of the daemon's handlers file.
type Subscriber struct{ ID, URL string }

// Listener is one callback the check looked at.
type Listener struct {
	// Address is host:port.
	Address string `json:"address"`
	// ID and URL are the handlers file's entry; empty when the listener is not in it (a
	// registration that never finished - the case measured on both daemons).
	ID  string `json:"id,omitempty"`
	URL string `json:"url,omitempty"`
	// Connected: the daemon holds a connection to it right now.
	Connected bool `json:"connected"`
	// Local: the listener is on this system; Owner is then the user its listening socket belongs
	// to (an addon's own user), when the system knows it.
	Local bool   `json:"local"`
	Owner string `json:"owner,omitempty"`
	// Verdict: answers, no-answer (took the call and said nothing within the probe's time),
	// refused, unreachable - for a registered listener; holds or unregistered for one the daemon is
	// connected to that is not on its list (see Check); blocked for one hmipserver's log names
	// (VerdictBlocked).
	Verdict string `json:"verdict"`
	// BlockedFor is how long hmipserver's log says the listener's pool has been held, in seconds
	// (VerdictBlocked only).
	BlockedFor int64 `json:"blocked_for,omitempty"`
}

// Stuck says whether this listener is one that holds a daemon: it takes a call and never answers.
func (l Listener) Stuck() bool {
	return l.Verdict == VerdictNoAnswer || l.Verdict == VerdictHolds || l.Verdict == VerdictBlocked
}

// The verdicts.
const (
	VerdictAnswers     = "answers"
	VerdictNoAnswer    = "no-answer"
	VerdictRefused     = "refused"
	VerdictUnreachable = "unreachable"
	// VerdictHolds: connected, not on the list, and every registered listener answers - the one
	// the daemon waits on. VerdictUnregistered: connected and not on the list, while a registered
	// listener is found not answering.
	VerdictHolds        = "holds"
	VerdictUnregistered = "unregistered"
)

// Prober asks a listener for system.listMethods and waits for anything back.
type Prober struct {
	// Timeout is how long an answer may take after the connect; 0 = 5 s. A callback that answers
	// the daemons at all answers this in milliseconds (the lab's: 1-13 ms).
	Timeout time.Duration
	// ConnectTimeout bounds the connect; 0 = 2 s.
	ConnectTimeout time.Duration
	// Dial makes the connection; nil = a net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Probe asks one listener, registered under id. bin sends the call as BIN-RPC (a binrpc:// or
// xmlrpc_bin:// callback); path is the URL's path for XML-RPC. Any byte back, or the listener
// closing the connection, is an answer: either way the daemon's call would not hang.
func (p *Prober) Probe(ctx context.Context, address string, bin bool, path, id string) string {
	ct := p.ConnectTimeout
	if ct == 0 {
		ct = 2 * time.Second
	}
	to := p.Timeout
	if to == 0 {
		to = 5 * time.Second
	}
	dial := p.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	cctx, cancel := context.WithTimeout(ctx, ct)
	conn, err := dial(cctx, "tcp", address)
	cancel()
	if err != nil {
		if strings.Contains(err.Error(), "refused") {
			return VerdictRefused
		}
		return VerdictUnreachable
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(to))
	if _, err := conn.Write(ListMethodsCall(address, bin, path, id)); err != nil {
		return VerdictAnswers // it closed on us: a daemon's call would fail at once, not hang
	}
	var one [1]byte
	_, err = conn.Read(one[:])
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return VerdictNoAnswer
	}
	if err != nil && ctx.Err() != nil {
		return VerdictNoAnswer
	}
	return VerdictAnswers
}

// ListMethodsCall is the request Probe sends: system.listMethods with the listener's own id, the
// call rfd and hmipserver make on a new listener first, as they make it.
func ListMethodsCall(address string, bin bool, path, id string) []byte {
	const method = "system.listMethods"
	if bin {
		// "Bin" 0x00, the length of the rest, the method's length and name, one parameter: a
		// string (type 3), its length and bytes
		body := binary.BigEndian.AppendUint32(nil, uint32(len(method)))
		body = append(body, method...)
		body = binary.BigEndian.AppendUint32(body, 1)
		body = binary.BigEndian.AppendUint32(body, 3)
		body = binary.BigEndian.AppendUint32(body, uint32(len(id)))
		body = append(body, id...)
		out := append([]byte("Bin\x00"), binary.BigEndian.AppendUint32(nil, uint32(len(body)))...)
		return append(out, body...)
	}
	if path == "" {
		path = "/"
	}
	x := `<?xml version="1.0"?><methodCall><methodName>` + method + `</methodName><params><param><value><string>` +
		xmlEscape(id) + `</string></value></param></params></methodCall>`
	return []byte("POST " + path + " HTTP/1.1\r\nHost: " + address + "\r\nContent-Type: text/xml\r\nContent-Length: " +
		strconv.Itoa(len(x)) + "\r\nConnection: close\r\n\r\n" + x)
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Input is one check of one interface.
type Input struct {
	// Subscribers is the daemon's handlers file.
	Subscribers []Subscriber
	// Daemon is what the socket table says; zero when the daemon was not found in it.
	Daemon Daemon
	// Skip says which entries are the system's own (occulited's subscriber): never probed.
	Skip func(s Subscriber) bool
	// Owner names the user of a local listening port; nil = no names.
	Owner func(port uint16) string
}

// Check asks every listener of one daemon: the handlers file's entries, each with its own id, and
// every loopback address the daemon is connected to that is not on the list (not called; see the
// package comment). A connection to an address that is on the LAN and not on the list is left
// alone - a LAN gateway, the key server - it is not a callback at all.
func (p *Prober) Check(ctx context.Context, in Input) []Listener {
	type cand struct {
		l    Listener
		bin  bool
		path string
	}
	byAddr := map[string]*cand{}
	var order []string
	add := func(addr string) *cand {
		if c, ok := byAddr[addr]; ok {
			return c
		}
		c := &cand{l: Listener{Address: addr}}
		byAddr[addr] = c
		order = append(order, addr)
		return c
	}
	own := map[string]bool{}
	for _, s := range in.Subscribers {
		addr, bin, path := callbackAddress(s.URL)
		if addr == "" {
			continue
		}
		if in.Skip != nil && in.Skip(s) {
			own[addr] = true
			continue
		}
		c := add(addr)
		if c.l.ID == "" {
			c.l.ID, c.l.URL, c.bin, c.path = s.ID, s.URL, bin, path
		}
	}
	connected := map[string]bool{}
	for _, peer := range in.Daemon.Peers {
		a := peer.String()
		connected[a] = true
		if own[a] {
			continue
		}
		if _, ok := byAddr[a]; !ok && !peer.Addr().IsLoopback() {
			continue
		}
		add(a)
	}
	out := make([]Listener, len(order))
	var wg sync.WaitGroup
	for i, a := range order {
		c := byAddr[a]
		c.l.Connected = connected[a]
		if ap, err := netip.ParseAddrPort(a); err == nil && ap.Addr().IsLoopback() {
			c.l.Local = true
			if in.Owner != nil {
				c.l.Owner = in.Owner(ap.Port())
			}
		}
		if c.l.ID == "" {
			c.l.Verdict = VerdictUnregistered // decided below, never called
			out[i] = c.l
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.l.Verdict = p.Probe(ctx, a, c.bin, c.path, c.l.ID)
			out[i] = c.l
		}()
	}
	wg.Wait()
	named := false
	for _, l := range out {
		named = named || l.Verdict == VerdictNoAnswer
	}
	if !named {
		for i := range out {
			if out[i].Verdict == VerdictUnregistered {
				out[i].Verdict = VerdictHolds
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Stuck() != out[j].Stuck() {
			return out[i].Stuck()
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// ListenOwner is the user id of the socket listening on port, -1 when none is found.
func ListenOwner(conns []Conn, port uint16) int {
	for _, c := range conns {
		if c.State == stateListen && c.Local.Port() == port {
			return c.UID
		}
	}
	return -1
}

// ListenerAddress is a callback URL's host:port as Check names the listener, "" when it has none.
func ListenerAddress(u string) string {
	a, _, _ := callbackAddress(u)
	return a
}

// callbackAddress is a callback URL's host:port, whether it is BIN-RPC, and its path; "" when the
// URL names no address (no port on a scheme without a default).
func callbackAddress(u string) (addr string, bin bool, path string) {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return "", false, ""
	}
	scheme = strings.ToLower(scheme)
	bin = scheme == "binrpc" || scheme == "xmlrpc_bin"
	hostport, p, _ := strings.Cut(rest, "/")
	if p != "" {
		path = "/" + p
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		switch scheme {
		case "http", "xmlrpc":
			host, port = strings.Trim(hostport, "[]"), "80"
		case "https":
			host, port = strings.Trim(hostport, "[]"), "443"
		default:
			return "", false, ""
		}
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		if host != "localhost" {
			return net.JoinHostPort(host, port), bin, path
		}
		a = netip.MustParseAddr("127.0.0.1")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return "", false, ""
	}
	return netip.AddrPortFrom(a.Unmap(), uint16(n)).String(), bin, path
}

// ReadAll is a helper for the callers: both socket tables, from a reader function (the root's
// ReadFile), what could be read of them.
func ReadAll(read func(string) ([]byte, error)) []Conn {
	var out []Conn
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if b, err := read(f); err == nil {
			out = append(out, ParseTCP(b)...)
		}
	}
	return out
}

// VerdictBlocked: hmipserver's own log says the worker pool of this listener's registration is
// blocked (openccu-lite task 234) - the listener took an event and does not answer it. Not probed:
// the log is the evidence.
const VerdictBlocked = "blocked"

// BlockedMatch is the fixed part of the line for journalctl's grep (a PCRE pattern). Vert.x's
// blocked-thread checker writes it once a second, with a stack trace, for as long as a worker
// thread is held:
//
//	io.vertx.core.impl.BlockedThreadChecker [vertx-blocked-thread-checker] Thread t234hang_WorkerPool-1 has been blocked for 60562 ms, time limit is 60000 ms
//
// hmipserver gives every listener whose registration finished a pool of its own, named after the
// listener's id, and delivers its events on it; the first line comes after the checker's limit
// (60 s). A thread of the shared pool ("vert.x-worker-thread-1", a listener that hangs inside its
// registration, B-201) does not match - it names nobody, and the subscriber's stall detection sees
// that case anyway.
const BlockedMatch = `_WorkerPool-[0-9]+ has been blocked for`

var blockedRe = regexp.MustCompile(`Thread (\S+)_WorkerPool-\d+ has been blocked for (\d+) ms`)

// BlockedPool reads one such line: the listener's id and how long its pool has been blocked. ok is
// false for any other line.
func BlockedPool(msg string) (id string, blocked time.Duration, ok bool) {
	m := blockedRe.FindStringSubmatch(msg)
	if m == nil {
		return "", 0, false
	}
	ms, err := strconv.ParseInt(m[2], 10, 64)
	if err != nil {
		return "", 0, false
	}
	return m[1], time.Duration(ms) * time.Millisecond, true
}
