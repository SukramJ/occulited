package eq3disc

import (
	"context"
	"net"
	"net/netip"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// listenDiscovery opens the socket: SO_BROADCAST so the answer to a broadcast probe may itself be
// broadcast, and IP_PKTINFO so every datagram says which address it was sent to - that is how a
// broadcast probe is told from one addressed to this system, without reading `netconfig` the way
// eq3configd did. SO_REUSEADDR lets a restart take the port back at once.
func listenDiscovery(addr string) (net.PacketConn, error) {
	lc := net.ListenConfig{Control: func(_, _ string, c syscall.RawConn) error {
		var opErr error
		if err := c.Control(func(fd uintptr) {
			for _, o := range []struct {
				level, opt int
			}{
				{unix.SOL_SOCKET, unix.SO_REUSEADDR},
				{unix.SOL_SOCKET, unix.SO_BROADCAST},
				{unix.IPPROTO_IP, unix.IP_PKTINFO},
			} {
				if opErr = unix.SetsockoptInt(int(fd), o.level, o.opt, 1); opErr != nil {
					return
				}
			}
		}); err != nil {
			return err
		}
		return opErr
	}}
	return lc.ListenPacket(context.Background(), "udp4", addr)
}

// readFrom reads one datagram and, where the socket carries it, the address it was sent to. A
// connection that is not a UDP one, or one without IP_PKTINFO, answers with an invalid address and
// the caller replies to the sender.
func readFrom(conn net.PacketConn, buf, oob []byte) (n int, dest netip.Addr, from net.Addr, err error) {
	uc, ok := conn.(*net.UDPConn)
	if !ok {
		n, from, err = conn.ReadFrom(buf)
		return n, netip.Addr{}, from, err
	}
	n, oobn, _, addr, err := uc.ReadMsgUDP(buf, oob)
	if err != nil {
		return n, netip.Addr{}, addr, err
	}
	msgs, perr := unix.ParseSocketControlMessage(oob[:oobn])
	if perr != nil {
		return n, netip.Addr{}, addr, nil
	}
	for _, m := range msgs {
		if m.Header.Level != unix.IPPROTO_IP || m.Header.Type != unix.IP_PKTINFO {
			continue
		}
		// struct in_pktinfo: the interface index, the local address the route chose, and the
		// destination the datagram carried - the last is the one that says broadcast or not
		if len(m.Data) < unix.SizeofInet4Pktinfo {
			continue
		}
		var info unix.Inet4Pktinfo
		copy((*[unix.SizeofInet4Pktinfo]byte)(unsafe.Pointer(&info))[:], m.Data[:unix.SizeofInet4Pktinfo])
		dest = netip.AddrFrom4(info.Addr)
	}
	return n, dest, addr, nil
}
