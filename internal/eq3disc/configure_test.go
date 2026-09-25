package eq3disc

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func TestSealOpen(t *testing.T) {
	key := KeyOf("8charPW!")
	iv := bytes.Repeat([]byte{7}, 16)
	for _, data := range [][]byte{nil, {1}, bytes.Repeat([]byte{9}, 40)} {
		s, err := Seal(key, iv, 'C', data)
		if err != nil || len(s)%16 != 0 || len(s) < 48 {
			t.Fatalf("%d %v", len(s), err)
		}
		op, got, err := Open(key, iv, s)
		if err != nil || op != 'C' || !bytes.HasPrefix(got, data) {
			t.Errorf("%c %x %v", op, got, err)
		}
		if _, _, err := Open(KeyOf("other"), iv, s); !errors.Is(err, ErrWrongPassword) {
			t.Errorf("another key opened it: %v", err)
		}
	}
	if _, err := Seal(key, []byte{1}, 'C', nil); err == nil {
		t.Error("a short IV")
	}
	if _, _, err := Open(key, iv, []byte{1, 2}); err == nil {
		t.Error("a short payload")
	}
	// MD5 of the password, as the devices derive it
	if k := KeyOf("abc"); k[0] != 0x90 || k[15] != 0x72 {
		t.Errorf("%x", k)
	}
}

// secureDevice answers `C` in clear with code 3 and an IV when crypt is on, takes the wrapped
// command with the right password, and records what it was set to.
type secureDevice struct {
	srv      *net.UDPConn
	password string
	crypt    bool
	mu       sync.Mutex
	set      []byte
	wrapped  int
}

func (d *secureDevice) run(t *testing.T) {
	iv := bytes.Repeat([]byte{0x42}, 16)
	buf := make([]byte, 2048)
	for {
		n, from, err := d.srv.ReadFromUDP(buf)
		if err != nil {
			return
		}
		r, ok := ParseRequest(buf[:n])
		if !ok {
			continue
		}
		reply := func(code byte, data []byte) {
			b := []byte{4, r.Sender[0], r.Sender[1], r.Sender[2], r.Counter}
			b = append(b, "eQ3-HMIP-HAP-App\x00HAPSERIAL\x00>"...)
			b = append(b, r.Opcode, code)
			_, _ = d.srv.WriteToUDP(append(b, data...), from)
		}
		// the payload starts after the header and the opcode
		at := 5 + len(r.Type) + 1 + len(r.Serial) + 1 + 1
		payload := append([]byte(nil), buf[at:n]...)
		switch r.Opcode {
		case 'I':
			reply(1, []byte("2.2.18\x00\x00\x00"))
		case 'n':
			reply(1, make([]byte, 20))
		case 'c':
			reply(1, append(make([]byte, 20), 3, 1, 16, 'x', 0))
		case 'C':
			if d.crypt {
				reply(3, iv)
				continue
			}
			d.mu.Lock()
			d.set = payload
			d.mu.Unlock()
			reply(2, nil)
		case '*':
			d.mu.Lock()
			d.wrapped++
			d.mu.Unlock()
			op, data, err := Open(KeyOf(d.password), iv, payload)
			if err != nil || op != 'C' {
				reply(3, iv)
				continue
			}
			d.mu.Lock()
			d.set = data
			d.mu.Unlock()
			reply(1, nil)
		}
	}
}

func newSecureDevice(t *testing.T, password string, crypt bool) (*secureDevice, *Client) {
	t.Helper()
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	d := &secureDevice{srv: srv, password: password, crypt: crypt}
	go d.run(t)
	c := &Client{Wait: 300 * time.Millisecond, Timeout: 200 * time.Millisecond, Port: srv.LocalAddr().(*net.UDPAddr).Port,
		Targets: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		Own:     func() map[netip.Addr]bool { return map[netip.Addr]bool{} }}
	return d, c
}

func TestConfigure(t *testing.T) {
	cfg := Config{Addresses: Addresses{IP: "192.0.2.155", Gateway: "192.0.2.1", Netmask: "255.255.255.0", DNS1: "192.0.2.1"}, Crypt: CryptOn | CryptUserKey, Name: "hap"}
	want, _ := SetConfigPayload(cfg)
	lo := netip.MustParseAddr("127.0.0.1")

	d, c := newSecureDevice(t, "pw", true)
	restarts, err := c.Configure(context.Background(), lo, "eQ3-HMIP-HAP-App", "HAPSERIAL", cfg, "pw")
	if err != nil || restarts {
		t.Fatalf("%v %v", restarts, err)
	}
	d.mu.Lock()
	if !bytes.HasPrefix(d.set, want) {
		t.Errorf("set %x, want %x", d.set, want)
	}
	d.mu.Unlock()

	// a wrong password: said, and tried once only
	d, c = newSecureDevice(t, "pw", true)
	if _, err := c.Configure(context.Background(), lo, "eQ3-HMIP-HAP-App", "HAPSERIAL", cfg, "nope"); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("%v", err)
	}
	if _, err := c.Configure(context.Background(), lo, "eQ3-HMIP-HAP-App", "HAPSERIAL", cfg, ""); !errors.Is(err, ErrWrongPassword) {
		t.Errorf("no password: %v", err)
	}
	d.mu.Lock()
	if d.wrapped != 1 || d.set != nil {
		t.Errorf("wrapped %d, set %x", d.wrapped, d.set)
	}
	d.mu.Unlock()

	// encryption off: the command in clear is taken, and the device restarts
	d, c = newSecureDevice(t, "pw", false)
	if restarts, err := c.Configure(context.Background(), lo, "eQ3-HMIP-HAP-App", "HAPSERIAL", cfg, ""); err != nil || !restarts {
		t.Errorf("%v %v", restarts, err)
	}
	// nobody there
	dead := &Client{Timeout: 100 * time.Millisecond, Port: 9}
	if _, err := dead.Configure(context.Background(), lo, "x", "y", cfg, ""); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("%v", err)
	}
	if _, err := dead.Configure(context.Background(), lo, "x", "y", Config{Addresses: Addresses{IP: "bad"}}, ""); err == nil {
		t.Error("a bad address was sent")
	}
}

func TestLookup(t *testing.T) {
	_, c := newSecureDevice(t, "pw", true)
	f, err := c.Lookup(context.Background(), "HAPSERIAL")
	if err != nil || f.Type != "eQ3-HMIP-HAP-App" || f.Runtime == nil || f.Config == nil || f.Config.Name != "x" || f.ProtocolVersion != 4 {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := c.Lookup(context.Background(), "OTHER"); !errors.Is(err, ErrNoAnswer) {
		t.Errorf("%v", err)
	}
}
