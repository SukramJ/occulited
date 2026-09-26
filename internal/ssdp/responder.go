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
	// Hold is how long the first announcement waits for the identity to settle (Device.Settled)
	// before it goes out with whatever the device answers then; 0 = 15 s. IdentityRecheck is how
	// often the identity is compared with the one announced afterwards; 0 = 2 s (B-245).
	Hold            time.Duration
	IdentityRecheck time.Duration
	// Repeat is how long after the first alive it is sent once more; 0 = 60 s. The spec says an
	// announcement should go out more than once, and on a LAN whose switch port passes nothing for
	// its first half minute after link-up (a spanning-tree delay, seen on a lab system: the first
	// alive was lost on four boots, ssh answered from the same moment on) the repeat is the
	// announcement that arrives (B-245).
	Repeat time.Duration
	Log    *slog.Logger
	// Listen and Delay are test seams: the socket, and the random wait before an answer.
	Listen func() (net.PacketConn, error)
	Delay  func(mx int) time.Duration
	// group is where the announcements go; nil = Addr. A test points it at a socket it can read.
	group net.Addr

	mu        sync.Mutex
	current   []netip.Addr
	announced string // the UDN the last alive carried
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

func (s *Responder) hold() time.Duration {
	if s.Hold > 0 {
		return s.Hold
	}
	return 15 * time.Second
}

func (s *Responder) identityRecheck() time.Duration {
	if s.IdentityRecheck > 0 {
		return s.IdentityRecheck
	}
	return 2 * time.Second
}

func (s *Responder) repeat() time.Duration {
	if s.Repeat > 0 {
		return s.Repeat
	}
	return time.Minute
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
//
// B-245: occulited starts beside the radio detection, whose files give the system its serial a
// few seconds later (B-199); an alive sent before that carries a USN built from the host name,
// which the description contradicts once the serial is there, and a listener that keeps devices
// by USN - Windows' network view - never shows the system. So the first alive waits for the
// identity to settle, up to Hold, and whenever the announced UDN turns out to differ from the
// device's, a byebye for the old one and an alive for the new go out.
func (s *Responder) announce(ctx context.Context, conn net.PacketConn) {
	if !s.waitSettled(ctx) {
		return
	}
	s.setAddresses(s.addresses())
	if known := s.known(); len(known) == 0 {
		s.log().Info("ssdp: no address to announce from yet; the next look at the addresses announces", "udn", s.Device.UDN())
	} else {
		s.log().Info("ssdp: announcing", "udn", s.Device.UDN(), "addresses", addrList(known))
	}
	s.send(conn, Alive)
	again := time.NewTimer(s.repeat())
	defer again.Stop()
	alive := time.NewTicker(s.every())
	defer alive.Stop()
	look := time.NewTicker(s.recheck())
	defer look.Stop()
	ident := time.NewTicker(s.identityRecheck())
	defer ident.Stop()
	for {
		select {
		case <-ctx.Done():
			// the goodbye goes before the socket closes; a listener drops the system at once
			// instead of waiting out max-age
			s.send(conn, Byebye)
			return
		case <-again.C:
			s.log().Info("ssdp: announcing again", "udn", s.Device.UDN(), "addresses", addrList(s.known()))
			s.send(conn, Alive)
		case <-alive.C:
			s.send(conn, Alive)
		case <-ident.C:
			if old, now := s.announcedUDN(), s.Device.UDN(); old != "" && now != old {
				s.log().Info("ssdp: the identity changed, announcing again", "was", old, "now", now)
				s.sendAs(conn, old, Byebye)
				s.send(conn, Alive)
			}
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

// waitSettled holds the first announcement while the device's identity is not final, up to Hold;
// false when the context ended meanwhile. A device without Settled is final at once.
func (s *Responder) waitSettled(ctx context.Context) bool {
	if s.Device.Settled == nil || s.Device.Settled() {
		return true
	}
	deadline := time.NewTimer(s.hold())
	defer deadline.Stop()
	poll := time.NewTicker(min(200*time.Millisecond, s.hold()))
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			s.log().Info("ssdp: the identity has not settled yet, announcing as it is", "udn", s.Device.UDN())
			return true
		case <-poll.C:
			if s.Device.Settled() {
				return true
			}
		}
	}
}

// send writes one NOTIFY per address to the group, so that a system on two networks is found on
// both with the LOCATION of the one it was heard on. An alive remembers the UDN it carried.
func (s *Responder) send(conn net.PacketConn, nts string) {
	udn := s.Device.UDN()
	s.sendAs(conn, udn, nts)
	if nts == Alive {
		s.mu.Lock()
		s.announced = udn
		s.mu.Unlock()
	}
}

func (s *Responder) announcedUDN() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.announced
}

// sendAs is send under a given UDN.
func (s *Responder) sendAs(conn net.PacketConn, udn, nts string) {
	group := s.group
	if group == nil {
		g, err := net.ResolveUDPAddr("udp4", Addr)
		if err != nil {
			return
		}
		group = g
	}
	for _, ip := range s.known() {
		if _, err := conn.WriteTo(s.Device.NotifyAs(udn, Location(ip.String()), nts), group); err != nil {
			s.log().Warn("ssdp: the announcement could not be sent", "nts", nts, "address", ip.String(), "error", err)
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
