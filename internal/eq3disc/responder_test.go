package eq3disc

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

// A socket test on the loopback: the read, the match, the delay and the answer are the same there
// as on the real port; only the destination address of the datagram (IP_PKTINFO) is not, and its
// absence is the "reply to the sender" half, which this exercises.
func TestResponderAnswersAProbe(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	r := &Responder{
		Device:    box,
		Listen:    func() (net.PacketConn, error) { return server, nil },
		Delay:     func(bool) time.Duration { return 0 },
		Addresses: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	if _, err := client.WriteTo(probe, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := client.ReadFrom(buf)
	if err != nil {
		t.Fatalf("nothing arrived: %v", err)
	}
	req, _ := ParseRequest(probe)
	if want := box.Answer(req); !bytes.Equal(buf[:n], want) {
		t.Errorf("\n%q\nwant\n%q", buf[:n], want)
	}

	// a probe for another product, and a datagram that is not a probe at all
	for _, junk := range [][]byte{
		append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("eQ3-HM-CCU2-App\x00*\x00I")...),
		[]byte("hello"),
	} {
		if _, err := client.WriteTo(junk, server.LocalAddr()); err != nil {
			t.Fatal(err)
		}
	}
	_ = client.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, _, err := client.ReadFrom(buf); err == nil {
		t.Errorf("nothing should have been answered, got %d bytes: %q", n, buf[:n])
	}
}

// A broadcast probe waits a random moment before it is answered, a probe naming this system does
// not - so a network full of systems does not answer in the same instant.
func TestResponderWaitsForABroadcastProbe(t *testing.T) {
	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	asked := make(chan bool, 4)
	r := &Responder{
		Device: box,
		Listen: func() (net.PacketConn, error) { return server, nil },
		Delay: func(wildcard bool) time.Duration {
			asked <- wildcard
			return 0
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	if _, err := client.WriteTo(probe, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if w := <-asked; !w {
		t.Error("a probe with a serial pattern is a broadcast: the answer waits")
	}
	exact := append([]byte{0x02, 0x8f, 0x91, 0xc0, 0x01}, []byte("*\x00"+box.Serial+"\x00I")...)
	if _, err := client.WriteTo(exact, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if w := <-asked; w {
		t.Error("a probe naming this system is answered at once")
	}
}

// Where the answer goes: back to the sender for a probe addressed to this system, to the broadcast
// address for one that was broadcast. eq3configd decided this by reading netconfig's CURRENT_IP;
// here it is the datagram's own destination address.
func TestReplyTo(t *testing.T) {
	r := &Responder{Addresses: func() []netip.Addr {
		return []netip.Addr{netip.MustParseAddr("192.168.1.5"), netip.MustParseAddr("127.0.0.1")}
	}}
	from := &net.UDPAddr{IP: net.ParseIP("192.168.1.9"), Port: 5000}
	for _, dest := range []netip.Addr{netip.MustParseAddr("192.168.1.5"), netip.MustParseAddr("127.0.0.1")} {
		if got := r.replyTo(dest, from); got.String() != from.String() {
			t.Errorf("to %s: the answer goes back to the sender, got %s", dest, got)
		}
	}
	// without a destination (no IP_PKTINFO) the answer goes back to the sender too
	if got := r.replyTo(netip.Addr{}, from); got.String() != from.String() {
		t.Errorf("without a destination: %s", got)
	}
	for _, dest := range []netip.Addr{netip.MustParseAddr("192.168.1.255"), netip.MustParseAddr("255.255.255.255")} {
		got := r.replyTo(dest, from)
		if got.String() != "255.255.255.255:5000" {
			t.Errorf("to %s: the answer is broadcast to the sender's port, got %s", dest, got)
		}
	}
}
