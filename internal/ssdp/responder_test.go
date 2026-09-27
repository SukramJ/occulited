package ssdp

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair is a socket for the responder and one for the searcher, both on the loopback: the
// responder's real socket is a multicast one, but everything this exercises - the read, the
// match, the delay, the address in the answer, the announcements - happens the same way on a
// plain unicast socket, and a test that joins a group depends on the machine it runs on.
func pair(t *testing.T) (server net.PacketConn, client net.PacketConn) {
	t.Helper()
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, err = net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

func read(t *testing.T, c net.PacketConn) string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := c.ReadFrom(buf)
	if err != nil {
		t.Fatalf("nothing arrived: %v", err)
	}
	return string(buf[:n])
}

// The responder answers a search it is one of, on the socket the search came in on, with the
// address of its own that the searcher can reach.
func TestResponderAnswersASearch(t *testing.T) {
	server, client := pair(t)
	r := &Responder{
		Device: dev,
		Interfaces: func() []netip.Addr {
			return []netip.Addr{netip.MustParseAddr("192.168.1.5"), netip.MustParseAddr("127.0.0.1")}
		},
		Listen: func() (net.PacketConn, error) { return server, nil },
		Delay:  func(int) time.Duration { return 0 },
		Every:  time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	to := server.LocalAddr()
	if _, err := client.WriteTo(search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: upnp:rootdevice", "MX: 2"), to); err != nil {
		t.Fatal(err)
	}
	answer := read(t, client)
	if !strings.HasPrefix(answer, "HTTP/1.1 200 OK\r\n") || !strings.Contains(answer, "USN: "+dev.UDN()+"::upnp:rootdevice") {
		t.Fatalf("the answer is wrong:\n%s", answer)
	}
	// the searcher is on 127.0.0.1, which is one of the addresses given: the LOCATION names it
	if !strings.Contains(answer, "LOCATION: http://127.0.0.1"+DescriptionPath) {
		t.Errorf("the LOCATION must be the address the searcher can reach:\n%s", answer)
	}

	// a search for something else is not answered
	if _, err := client.WriteTo(search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: urn:schemas-upnp-org:device:MediaServer:1"), to); err != nil {
		t.Fatal(err)
	}
	// and neither is a datagram that is not a search at all
	if _, err := client.WriteTo([]byte("hello"), to); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := client.ReadFrom(make([]byte, 2048)); err == nil {
		t.Errorf("nothing should have been answered, got %d bytes", n)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Run: %v", err)
	}
}

// The MX delay is honoured: an answer does not go out before it.
func TestResponderWaitsTheSearchersMX(t *testing.T) {
	server, client := pair(t)
	var mu sync.Mutex
	var asked int
	r := &Responder{
		Device:     dev,
		Interfaces: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		Listen:     func() (net.PacketConn, error) { return server, nil },
		Delay: func(mx int) time.Duration {
			mu.Lock()
			asked = mx
			mu.Unlock()
			return 250 * time.Millisecond
		},
		Every: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	start := time.Now()
	if _, err := client.WriteTo(search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: ssdp:all", "MX: 3"), server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	read(t, client)
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("the answer came after %v, before its delay", d)
	}
	mu.Lock()
	got := asked
	mu.Unlock()
	if got != 3 {
		t.Errorf("the delay was asked for MX %d", got)
	}
}

// Every address gets its own alive, and the goodbye goes out when the context ends - neither of
// which ssdpd did.
func TestResponderAnnouncesAndSaysGoodbye(t *testing.T) {
	server, client := pair(t)
	// the announcements go to the group, which this test cannot receive; send them to the client
	// instead by making the group the client's address
	r := &Responder{
		Device:     dev,
		Interfaces: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.5")} },
		Listen:     func() (net.PacketConn, error) { return server, nil },
		Delay:      func(int) time.Duration { return 0 },
		Every:      time.Hour,
		group:      client.LocalAddr(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	alive := read(t, client)
	if !strings.Contains(alive, "NTS: ssdp:alive") || !strings.Contains(alive, "LOCATION: http://192.168.1.5"+DescriptionPath) {
		t.Fatalf("the announcement is wrong:\n%s", alive)
	}
	cancel()
	bye := read(t, client)
	if !strings.Contains(bye, "NTS: ssdp:byebye") {
		t.Errorf("the goodbye is wrong:\n%s", bye)
	}
	<-done
}

// An address that changes - a DHCP renewal, a cable plugged in - is announced again with the new
// LOCATION. ssdpd read the address once at its start and kept announcing the old one.
func TestResponderAnnouncesAgainWhenTheAddressChanges(t *testing.T) {
	server, client := pair(t)
	var mu sync.Mutex
	addrs := []netip.Addr{netip.MustParseAddr("192.168.1.5")}
	r := &Responder{
		Device: dev,
		Interfaces: func() []netip.Addr {
			mu.Lock()
			defer mu.Unlock()
			return append([]netip.Addr(nil), addrs...)
		},
		Listen:  func() (net.PacketConn, error) { return server, nil },
		Delay:   func(int) time.Duration { return 0 },
		Every:   time.Hour,
		Recheck: 20 * time.Millisecond,
		group:   client.LocalAddr(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()
	if first := read(t, client); !strings.Contains(first, "http://192.168.1.5") {
		t.Fatalf("the first announcement:\n%s", first)
	}
	mu.Lock()
	addrs = []netip.Addr{netip.MustParseAddr("10.0.0.9")}
	mu.Unlock()
	for {
		m := read(t, client)
		if strings.Contains(m, "http://10.0.0.9"+DescriptionPath) {
			return
		}
		if !strings.Contains(m, "http://192.168.1.5") {
			t.Fatalf("an unexpected announcement:\n%s", m)
		}
	}
}

func TestCommonPrefix(t *testing.T) {
	p := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	if got := commonPrefix(p("192.168.1.5"), p("192.168.1.9")); got < 24 {
		t.Errorf("the same /24 shares at least 24 bits, got %d", got)
	}
	if commonPrefix(p("10.0.0.1"), p("192.168.1.9")) >= 8 {
		t.Error("two different networks share fewer than 8 bits")
	}
	if commonPrefix(p("::1"), p("192.168.1.9")) != -1 {
		t.Error("addresses of different families are not comparable")
	}
}

func TestLocalAddressesLeavesOutTheLoopback(t *testing.T) {
	for _, a := range LocalAddresses() {
		if a.IsLoopback() || a.IsLinkLocalUnicast() || !a.Is4() {
			t.Errorf("%s must not be announced", a)
		}
	}
}

// B-245: the identity a system announces at boot may be the host name (B-199: the radio detection
// writes the serial seconds after occulited starts), and a listener that keeps devices by USN -
// Windows' network view - never showed the system whose description then said another UDN. The
// first alive waits for the identity to settle, up to Hold; an identity that changes afterwards
// is taken back with a byebye for the old UDN and announced again with the new.
func TestResponderFollowsTheIdentity(t *testing.T) {
	server, client := pair(t)
	var mu sync.Mutex
	serial, settled := "ccu-vm-1", false
	d := Device{
		SerialFunc: func() string { mu.Lock(); defer mu.Unlock(); return serial },
		Settled:    func() bool { mu.Lock(); defer mu.Unlock(); return settled },
		Hostname:   "ccu-vm-1",
	}
	r := &Responder{
		Device:          d,
		Interfaces:      func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.5")} },
		Listen:          func() (net.PacketConn, error) { return server, nil },
		Delay:           func(int) time.Duration { return 0 },
		Every:           time.Hour,
		Hold:            400 * time.Millisecond,
		IdentityRecheck: 20 * time.Millisecond,
		Repeat:          100 * time.Millisecond,
		group:           client.LocalAddr(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go func() { _ = r.Run(ctx) }()

	// the detection writes the serial inside the hold: the first alive already carries it
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	serial, settled = "3014F711A000040000000A02", true
	mu.Unlock()
	first := read(t, client)
	if !strings.Contains(first, "NTS: ssdp:alive") || !strings.Contains(first, "USN: uuid:upnp-BasicDevice-1_0-3014F711A000040000000A02::upnp:rootdevice") {
		t.Fatalf("the first alive must carry the settled identity:\n%s", first)
	}
	if time.Since(start) > 350*time.Millisecond {
		t.Errorf("the alive waited for the hold to run out (%v) instead of following the identity", time.Since(start))
	}
	// the announcement is repeated once, Repeat later, the same way (a first one lost on a port
	// that is not forwarding yet)
	second := read(t, client)
	if second != first {
		t.Fatalf("the repeated alive must equal the first:\n%s\n---\n%s", second, first)
	}

	// the identity changes later (a serial that appears after the hold): byebye for the old, alive for the new
	mu.Lock()
	serial = "JEQ9000002"
	mu.Unlock()
	bye := read(t, client)
	if !strings.Contains(bye, "NTS: ssdp:byebye") || !strings.Contains(bye, "USN: uuid:upnp-BasicDevice-1_0-3014F711A000040000000A02::upnp:rootdevice") {
		t.Fatalf("the old identity must be taken back:\n%s", bye)
	}
	alive := read(t, client)
	if !strings.Contains(alive, "NTS: ssdp:alive") || !strings.Contains(alive, "USN: uuid:upnp-BasicDevice-1_0-JEQ9000002::upnp:rootdevice") || !strings.Contains(alive, "LOCATION: http://192.168.1.5"+DescriptionPath) {
		t.Fatalf("the new identity must be announced:\n%s", alive)
	}
	// and nothing more while it stays
	_ = client.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	if n, _, err := client.ReadFrom(make([]byte, 2048)); err == nil {
		t.Errorf("an unchanged identity is not announced again: %d bytes", n)
	}
}

// The hold runs out: a system whose identity never settles (a container without the files)
// announces after Hold with what it has, and a device without Settled announces at once.
func TestResponderHoldRunsOut(t *testing.T) {
	server, client := pair(t)
	d := dev
	d.Settled = func() bool { return false }
	r := &Responder{
		Device:     d,
		Interfaces: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.5")} },
		Listen:     func() (net.PacketConn, error) { return server, nil },
		Delay:      func(int) time.Duration { return 0 },
		Every:      time.Hour,
		Hold:       150 * time.Millisecond,
		group:      client.LocalAddr(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	go func() { _ = r.Run(ctx) }()
	alive := read(t, client)
	if !strings.Contains(alive, "USN: "+dev.UDN()+"::upnp:rootdevice") {
		t.Fatalf("the alive after the hold:\n%s", alive)
	}
	if d := time.Since(start); d < 150*time.Millisecond {
		t.Errorf("the alive went out after %v, before the hold ran out", d)
	}
}

// Identity.Settled: false while no file has a value, true from the first value on.
func TestIdentitySettled(t *testing.T) {
	root := t.TempDir()
	id := &Identity{Root: root, Hostname: "ccu-vm-1"}
	if id.Settled() {
		t.Fatal("settled without a file")
	}
	writeVar(t, root, "var/board_serial", "JEQ9000002\n")
	if !id.Settled() || id.Serial() != "JEQ9000002" {
		t.Fatalf("settled=%v serial=%q", id.Settled(), id.Serial())
	}
}
