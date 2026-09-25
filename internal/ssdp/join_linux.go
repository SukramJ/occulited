package ssdp

import (
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// joinOn adds the socket to the SSDP group on one interface (IP_ADD_MEMBERSHIP). The standard
// library has no exported way to say "this group on that interface" for a PacketConn, so the
// setsockopt is made here on the socket's own file descriptor - pure Go, no cgo (D-15).
func joinOn(conn *net.UDPConn, iface *net.Interface) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	mreq := &unix.IPMreqn{Ifindex: int32(iface.Index)}
	copy(mreq.Multiaddr[:], net.ParseIP(Group).To4())
	var opErr error
	if err := raw.Control(func(fd uintptr) {
		opErr = unix.SetsockoptIPMreqn(int(fd), unix.IPPROTO_IP, unix.IP_ADD_MEMBERSHIP, mreq)
	}); err != nil {
		return err
	}
	// joining a group this socket is already in is not a failure; it is the usual answer after a
	// recheck that found the same addresses on one of several interfaces
	if opErr == syscall.EADDRINUSE {
		return nil
	}
	return opErr
}

// reusePort sets SO_REUSEADDR and SO_REUSEPORT on the socket before it is bound, so that SSDP's
// well-known port can be shared as multicast ports are meant to be.
func reusePort(_, _ string, c syscall.RawConn) error {
	var opErr error
	if err := c.Control(func(fd uintptr) {
		if opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); opErr != nil {
			return
		}
		opErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	}); err != nil {
		return err
	}
	return opErr
}
