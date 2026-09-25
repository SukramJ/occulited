package eq3disc

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"strconv"
	"time"
)

// maxWildcardDelay is how long an answer to a broadcast probe may wait, as eq3configd had it: a
// random moment up to 1.3 s, so a network full of systems does not answer in the same instant.
const maxWildcardDelay = 1300 * time.Millisecond

// Responder is the socket half: a UDP socket on 43439 that answers the probes.
//
// It replies where the probe came from when the probe was addressed to this system, and to the
// broadcast address when it was broadcast - which is eq3configd's behaviour, arrived at without
// reading `netconfig` for it: the destination address of the datagram itself says which it was
// (IP_PKTINFO), and a destination that is none of this system's own addresses was a broadcast.
type Responder struct {
	Device Device
	Log    *slog.Logger
	// Addresses lists this system's own IPv4 addresses; nil = the interfaces'. A destination that
	// is not one of them was a broadcast.
	Addresses func() []netip.Addr
	// Listen and Delay are test seams.
	Listen func() (net.PacketConn, error)
	Delay  func(wildcard bool) time.Duration
}

func (s *Responder) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Responder) delay(wildcard bool) time.Duration {
	if s.Delay != nil {
		return s.Delay(wildcard)
	}
	if !wildcard {
		return 0
	}
	return time.Duration(rand.Int64N(int64(maxWildcardDelay)))
}

func (s *Responder) own() map[netip.Addr]bool {
	var list []netip.Addr
	if s.Addresses != nil {
		list = s.Addresses()
	} else {
		list = localAddresses()
	}
	m := make(map[netip.Addr]bool, len(list))
	for _, a := range list {
		m[a] = true
	}
	return m
}

// localAddresses is every IPv4 address of this system, the loopback included: a probe sent to
// 127.0.0.1 is as much a unicast probe as one to the LAN address.
func localAddresses() []netip.Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []netip.Addr
	for _, i := range ifaces {
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			p, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip, ok := netip.AddrFromSlice(p.IP.To4()); ok {
				out = append(out, ip)
			}
		}
	}
	return out
}

// Run answers probes until the context ends.
func (s *Responder) Run(ctx context.Context) error {
	conn, err := s.listen()
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 2048)
	oob := make([]byte, 1024)
	for {
		n, dest, from, err := readFrom(conn, buf, oob)
		if err != nil {
			return nil // the socket is closed: the context ended
		}
		req, ok := ParseRequest(buf[:n])
		if !ok || !s.Device.Answers(req) {
			continue
		}
		msg := s.Device.Answer(req)
		to := s.replyTo(dest, from)
		wait := s.delay(Wildcard(req))
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
				s.log().Debug("eq3 discovery: the answer could not be sent", "to", to.String(), "error", err)
			}
		}()
	}
}

// replyTo is where the answer goes: back to the sender for a probe addressed to this system, and
// to the broadcast address for one that was broadcast - the sender's port either way. Without a
// destination address (no IP_PKTINFO, a test's plain socket) the answer goes back to the sender,
// which is the safe half: every client of ours reads its own socket.
func (s *Responder) replyTo(dest netip.Addr, from net.Addr) net.Addr {
	if !dest.IsValid() || s.own()[dest] {
		return from
	}
	port := 0
	if ap, err := netip.ParseAddrPort(from.String()); err == nil {
		port = int(ap.Port())
	}
	return &net.UDPAddr{IP: net.IPv4bcast, Port: port}
}

func (s *Responder) listen() (net.PacketConn, error) {
	if s.Listen != nil {
		return s.Listen()
	}
	return listenDiscovery(":" + strconv.Itoa(Port))
}
