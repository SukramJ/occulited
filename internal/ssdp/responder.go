package ssdp

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Responder is the socket half: it listens on the group, answers M-SEARCH and announces itself.
//
// One socket does both, as ssdpd's did: a UDP socket bound to :1900 that has joined the group
// receives the multicast searches, and the unicast answers go out of it too, so the source port a
// searcher sees is 1900 - which is what a control point expects of a device.
//
// Nothing here is privileged: 1900 is above 1024, and joining a multicast group needs no
// capability. occulited runs as occulite and stays there (D-29 is about listening beyond the
// loopback, which this must do to be discovered at all - the firewall's 1900/udp entry is its
// door, and its owner is occulited since this task).
type Responder struct {
	Device Device
	// Interfaces lists the addresses to announce from and join on; nil = LocalAddresses. It is a
	// function because the answer changes: DHCP renews, a cable is plugged in, Wi-Fi joins.
	Interfaces func() []netip.Addr
	// Interval between two ssdp:alive announcements; 0 = Interval.
	Every time.Duration
	// Recheck is how often the addresses are looked at again; 0 = 30 s.
	Recheck time.Duration
	Log     *slog.Logger
	// Listen and Delay are test seams: the socket, and the random wait before an answer.
	Listen func() (net.PacketConn, error)
	Delay  func(mx int) time.Duration
	// group is where the announcements go; nil = Addr. A test points it at a socket it can read.
	group net.Addr

	mu      sync.Mutex
	current []netip.Addr
}

func (s *Responder) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Responder) every() time.Duration {
	if s.Every > 0 {
		return s.Every
	}
	return Interval
}

func (s *Responder) recheck() time.Duration {
	if s.Recheck > 0 {
		return s.Recheck
	}
	return 30 * time.Second
}

// delay is the wait before an answer: a random moment inside the MX the searcher named, so that a
// LAN full of devices does not answer a broadcast in the same millisecond. Without an MX the
// answer goes at once, as ssdpd did.
func (s *Responder) delay(mx int) time.Duration {
	if s.Delay != nil {
		return s.Delay(mx)
	}
	if mx <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(mx) * int64(time.Second)))
}

// listen opens the SSDP socket. SO_REUSEADDR and SO_REUSEPORT are set because a multicast port is
// shared by convention: another SSDP implementation on the system (an addon, a library inside
// hmipserver) may hold 1900 too, and without them whichever started second would simply fail. They
// also let a restart of occulited take the port back at once.
func (s *Responder) listen() (net.PacketConn, error) {
	if s.Listen != nil {
		return s.Listen()
	}
	lc := net.ListenConfig{Control: reusePort}
	return lc.ListenPacket(context.Background(), "udp4", ":"+strconv.Itoa(Port))
}

func (s *Responder) addresses() []netip.Addr {
	if s.Interfaces != nil {
		return s.Interfaces()
	}
	return LocalAddresses()
}

// LocalAddresses is every routable IPv4 address of the system, loopback and link-local left out:
// what an announcement can carry as a LOCATION and what a searcher could reach it at.
func LocalAddresses() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			p, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip, ok := netip.AddrFromSlice(p.IP.To4())
			if !ok || !ip.Is4() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				continue
			}
			out = append(out, ip)
		}
	}
	return out
}

// Run listens until the context ends, then says goodbye. It returns only when the socket is shut:
// a failure to open it is reported and not retried here, because the unit restarts the daemon and
// a system with no network yet has none to announce on either.
func (s *Responder) Run(ctx context.Context) error {
	conn, err := s.listen()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if pc, ok := conn.(*net.UDPConn); ok {
		joinGroup(pc, s.addresses(), s.log())
	}

	// announce returns only once the context has ended and its goodbye has gone out; the socket
	// is closed after that, which is what stops the reader. Closing it on the context itself
	// would race the goodbye off the wire.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.announce(ctx, conn)
	}()
	go func() {
		wg.Wait()
		_ = conn.Close()
	}()
	s.serve(ctx, conn)
	wg.Wait()
	return nil
}

// announce sends ssdp:alive at the start and every interval, re-joins the group when the
// addresses change, and sends ssdp:byebye when the context ends. ssdpd never said goodbye and
// never noticed an address change; both are the point of doing this here.
func (s *Responder) announce(ctx context.Context, conn net.PacketConn) {
	s.setAddresses(s.addresses())
	s.send(conn, Alive)
	alive := time.NewTicker(s.every())
	defer alive.Stop()
	look := time.NewTicker(s.recheck())
	defer look.Stop()
	for {
		select {
		case <-ctx.Done():
			// the goodbye goes before the socket closes; a listener drops the system at once
			// instead of waiting out max-age
			s.send(conn, Byebye)
			return
		case <-alive.C:
			s.send(conn, Alive)
		case <-look.C:
			now := s.addresses()
			if s.setAddresses(now) {
				if pc, ok := conn.(*net.UDPConn); ok {
					joinGroup(pc, now, s.log())
				}
				s.log().Info("ssdp: the addresses changed, announcing again", "addresses", addrList(now))
				s.send(conn, Alive)
			}
		}
	}
}

// setAddresses stores the addresses and says whether they differ from the ones before.
func (s *Responder) setAddresses(now []netip.Addr) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := len(now) != len(s.current)
	if !changed {
		for i := range now {
			if now[i] != s.current[i] {
				changed = true
				break
			}
		}
	}
	s.current = append(s.current[:0:0], now...)
	return changed
}

func (s *Responder) known() []netip.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]netip.Addr(nil), s.current...)
}

// send writes one NOTIFY per address to the group, so that a system on two networks is found on
// both with the LOCATION of the one it was heard on.
func (s *Responder) send(conn net.PacketConn, nts string) {
	group := s.group
	if group == nil {
		g, err := net.ResolveUDPAddr("udp4", Addr)
		if err != nil {
			return
		}
		group = g
	}
	for _, ip := range s.known() {
		if _, err := conn.WriteTo(s.Device.Notify(Location(ip.String()), nts), group); err != nil {
			s.log().Debug("ssdp: the announcement could not be sent", "nts", nts, "address", ip.String(), "error", err)
		}
	}
}

// serve reads the datagrams and answers the searches this device is one of.
func (s *Responder) serve(ctx context.Context, conn net.PacketConn) {
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return // the socket is closed: the context ended, or the network went
		}
		search, ok := ParseSearch(buf[:n])
		if !ok || !s.Device.Answers(search.ST) {
			continue
		}
		msg := s.Device.Reply(Location(s.replyAddress(from).String()))
		wait, to := s.delay(search.MX), from
		go func() {
			if wait > 0 {
				t := time.NewTimer(wait)
				defer t.Stop()
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
			if _, err := conn.WriteTo(msg, to); err != nil {
				s.log().Debug("ssdp: the answer could not be sent", "to", to.String(), "error", err)
			}
		}()
	}
}

// replyAddress is the address to put in the LOCATION of an answer: the one of this system's own
// that shares the most leading bits with the searcher, so a searcher on one network is not sent to
// this system's address on another. With nothing to compare, the first address.
func (s *Responder) replyAddress(from net.Addr) netip.Addr {
	mine := s.known()
	if len(mine) == 0 {
		return netip.AddrFrom4([4]byte{127, 0, 0, 1})
	}
	ap, err := netip.ParseAddrPort(from.String())
	if err != nil {
		return mine[0]
	}
	peer := ap.Addr().Unmap()
	best, bits := mine[0], -1
	for _, ip := range mine {
		if n := commonPrefix(ip, peer); n > bits {
			best, bits = ip, n
		}
	}
	return best
}

// commonPrefix is how many leading bits two addresses share, -1 when they are not comparable.
func commonPrefix(a, b netip.Addr) int {
	if !a.Is4() || !b.Is4() {
		return -1
	}
	x, y := a.As4(), b.As4()
	n := 0
	for i := range x {
		if x[i] == y[i] {
			n += 8
			continue
		}
		d := x[i] ^ y[i]
		for bit := 7; bit >= 0 && d&(1<<bit) == 0; bit-- {
			n++
		}
		break
	}
	return n
}

// joinGroup joins 239.255.255.250 on every interface that carries one of the addresses. A failure
// is logged and not fatal: on a system with one interface the default membership of a bound
// socket is usually enough, and there is nothing better to do than carry on announcing.
func joinGroup(conn *net.UDPConn, addrs []netip.Addr, log *slog.Logger) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	want := map[netip.Addr]bool{}
	for _, a := range addrs {
		want[a] = true
	}
	for i := range ifaces {
		iface := ifaces[i]
		if iface.Flags&net.FlagMulticast == 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		list, err := iface.Addrs()
		if err != nil {
			continue
		}
		on := false
		for _, a := range list {
			if p, ok := a.(*net.IPNet); ok {
				if ip, ok := netip.AddrFromSlice(p.IP.To4()); ok && want[ip] {
					on = true
				}
			}
		}
		if !on {
			continue
		}
		if err := joinOn(conn, &iface); err != nil {
			log.Debug("ssdp: the group could not be joined", "interface", iface.Name, "error", err)
		}
	}
}

func addrList(a []netip.Addr) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += ", "
		}
		s += x.String()
	}
	return s
}
