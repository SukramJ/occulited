package eq3disc

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Answers captured in the lab (openccu-lite task 220, 2026-09-24): the HAP-B1 answers with version
// 4, the HM-LGW with version 2 and its two service ports. The devices' serials and the encrypted
// answer's 16 bytes are invented ones in place of the captured (B-24); the shape is the capture's.
const (
	capHAPIdentify = "0470fafe016551332d484d49502d4841502d41707000333031353033373744433030303330303030303030413133003e4901322e322e3138000000"
	capLGWIdentify = "0270fafe016551332d484d2d4c47572d417070004b455139303030303036003e4901312e312e3500000207d0002a07d10000"
	capHAPCurrent  = "048a7eb0016551332d484d49502d4841502d41707000333031353033373744433030303330303030303030413133003e6e01c000029bc0000201ffffff00c0000201c00002e1"
	capHAPConfig   = "04645543016551332d484d49502d4841502d41707000333031353033373744433030303330303030303030413133003e6301c0a801e0c0a80101ffffff00c0a801010000000003011043303030333030303030303041313300"
	capHAPEncrypt  = "04a31442016551332d484d49502d4841502d41707000333031353033373744433030303330303030303030413133003e4303000102030405060708090a0b0c0d0e0f"
)

func TestParseCaptures(t *testing.T) {
	a, ok := ParseAnswer(unhex(t, capHAPIdentify))
	if !ok || a.Version != 4 || a.Type != "eQ3-HMIP-HAP-App" || a.Serial != "30150377DC00030000000A13" || a.Opcode != 'I' || a.Code != CodeOK || a.Sender != [3]byte{0x70, 0xfa, 0xfe} || a.Counter != 1 {
		t.Fatalf("%+v %v", a, ok)
	}
	if v, s := ParseIdentify(a.Data); v != "2.2.18" || len(s) != 0 {
		t.Errorf("%q %v", v, s)
	}
	a, _ = ParseAnswer(unhex(t, capLGWIdentify))
	if v, s := ParseIdentify(a.Data); v != "1.1.5" || len(s) != 2 || s[0] != (Service{2, 2000}) || s[1] != (Service{42, 2001}) {
		t.Errorf("%q %v", v, s)
	}
	a, _ = ParseAnswer(unhex(t, capHAPCurrent))
	if r, ok := readAddresses(a.Data); !ok || r != (Addresses{"192.0.2.155", "192.0.2.1", "255.255.255.0", "192.0.2.1", "192.0.2.225"}) {
		t.Errorf("%+v", r)
	}
	a, _ = ParseAnswer(unhex(t, capHAPConfig))
	c, ok := ParseConfig(a.Data)
	if !ok || c.IP != "192.168.1.224" || c.Gateway != "192.168.1.1" || c.DNS2 != "0.0.0.0" || !c.DHCP || !c.AutoIP || c.Crypt != CryptOn || c.NameMax != 16 || c.Name != "C00030000000A13" {
		t.Errorf("%+v", c)
	}
	// the configuration written back unchanged is the configuration read (without the name's
	// maximum length, which is the device's)
	p, err := SetConfigPayload(c)
	if err != nil || !bytes.Equal(p, append(append(append([]byte{}, a.Data[0:22]...), "C00030000000A13"...), 0)) {
		t.Errorf("%x %v", p, err)
	}
	a, _ = ParseAnswer(unhex(t, capHAPEncrypt))
	if a.Opcode != 'C' || a.Code != CodeEncrypted || len(a.Data) != 16 {
		t.Errorf("%+v", a)
	}
	for _, bad := range []string{"", "01", "0270fafe01", "0270fafe016551", "0270fafe01650000", "0270fafe016500004149"} {
		if _, ok := ParseAnswer(unhex(t, bad)); ok {
			t.Errorf("%s parsed", bad)
		}
	}
	if _, ok := ParseConfig([]byte{1, 2}); ok {
		t.Error("short config parsed")
	}
	if v, s := ParseIdentify([]byte("1.0")); v != "1.0" || s != nil {
		t.Errorf("%q %v", v, s)
	}
	if _, err := SetConfigPayload(Config{Addresses: Addresses{IP: "::1"}}); err == nil {
		t.Error("an IPv6 address was taken")
	}
}

func TestBuildRequest(t *testing.T) {
	got := BuildRequest([3]byte{1, 2, 3}, 7, "eQ3-*", "*", 'I', nil)
	want := []byte{2, 1, 2, 3, 7, 'e', 'Q', '3', '-', '*', 0, '*', 0, 'I'}
	if !bytes.Equal(got, want) {
		t.Errorf("%x", got)
	}
	r, ok := ParseRequest(got)
	if !ok || r.Opcode != 'I' || r.Counter != 7 {
		t.Errorf("our own responder does not read it: %+v", r)
	}
}

// fakeDevices answers like the lab's HAP (version 4), an HM-LGW and a CCU, echoing the sender id
// and the counter; the CCU is never asked n/c, and a second copy of the HAP's answer is dropped.
func fakeDevices(t *testing.T) (*net.UDPConn, func() map[byte]int) {
	t.Helper()
	srv, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	asked := map[byte]int{}
	var mu sync.Mutex
	go func() {
		buf := make([]byte, 2048)
		for {
			n, from, err := srv.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r, ok := ParseRequest(buf[:n])
			if !ok {
				continue
			}
			mu.Lock()
			asked[r.Opcode]++
			mu.Unlock()
			reply := func(ver byte, typ, serial string, code byte, data []byte) {
				b := []byte{ver, r.Sender[0], r.Sender[1], r.Sender[2], r.Counter}
				b = append(b, typ...)
				b = append(b, 0)
				b = append(b, serial...)
				b = append(b, 0, '>', r.Opcode, code)
				_, _ = srv.WriteToUDP(append(b, data...), from)
			}
			switch r.Opcode {
			case 'I':
				reply(4, "eQ3-HMIP-HAP-App", "30150377DC00030000000A13", 1, []byte("2.2.18\x00\x00\x00"))
				reply(4, "eQ3-HMIP-HAP-App", "30150377DC00030000000A13", 1, []byte("2.2.18\x00\x00\x00"))
				reply(2, "eQ3-HM-LGW-App", "KEQ9000006", 1, []byte("1.1.5\x00\x00\x02\x07\xd0\x00\x00"))
				reply(2, "eQ3-HmIP-CCU3-App", "4110000a1", 1, []byte("3.89.8\x00\x00"))
				// somebody else's answer (another sender id) is not ours
				_, _ = srv.WriteToUDP([]byte{2, 9, 9, 9, 1, 'x', 0, 'y', 0, '>', 'I', 1}, from)
			case 'n':
				if r.Serial == "KEQ9000006" {
					continue // the gateway does not answer n: asked twice, left without
				}
				reply(4, "eQ3-HMIP-HAP-App", r.Serial, 1, unhex(t, "c000029bc0000201ffffff00c0000201c00002e1"))
			case 'c':
				reply(4, "eQ3-HMIP-HAP-App", r.Serial, 1, unhex(t, "c0a801e0c0a80101ffffff00c0a801010000000003011043303030333030303030303041313300"))
			}
		}
	}()
	return srv, func() map[byte]int {
		mu.Lock()
		defer mu.Unlock()
		return map[byte]int{'n': asked['n'], 'c': asked['c']}
	}
}

func TestScan(t *testing.T) {
	srv, asked := fakeDevices(t)
	c := &Client{Wait: 300 * time.Millisecond, Timeout: 200 * time.Millisecond, Port: srv.LocalAddr().(*net.UDPAddr).Port,
		Targets: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("127.0.0.1")} },
		Own:     func() map[netip.Addr]bool { return map[netip.Addr]bool{} }}
	found, err := c.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 3 {
		t.Fatalf("%+v", found)
	}
	hap, lgw, ccu := found[1], found[0], found[2]
	if lgw.Type != "eQ3-HM-LGW-App" || lgw.Runtime != nil || lgw.Config == nil || len(lgw.Services) != 1 {
		t.Errorf("lgw %+v", lgw)
	}
	if hap.ProtocolVersion != 4 || hap.IP != "127.0.0.1" || hap.Runtime == nil || hap.Runtime.IP != "192.0.2.155" || hap.Config == nil || !hap.Config.DHCP {
		t.Errorf("hap %+v", hap)
	}
	if ccu.Type != "eQ3-HmIP-CCU3-App" || ccu.Runtime != nil || ccu.Config != nil {
		t.Errorf("ccu %+v", ccu)
	}
	if a := asked(); a['n'] != 3 || a['c'] != 2 {
		t.Errorf("asked %v: n twice for the silent gateway, once for the HAP; c once each, never the CCU", a)
	}
	// this system's own answer is left out
	c.Own = func() map[netip.Addr]bool { return map[netip.Addr]bool{netip.MustParseAddr("127.0.0.1"): true} }
	if found, _ = c.Scan(context.Background()); len(found) != 0 {
		t.Errorf("own answers kept: %+v", found)
	}
}

func TestIsCCU(t *testing.T) {
	for typ, want := range map[string]bool{"eQ3-HmIP-CCU3-App": true, "eQ3-HmIP-CCU3-App-lite": true, "eQ3-HM-CCU2-App": true, "eQ3-HMIP-HAP-App": false, "eQ3-HM-LGW-App": false} {
		if IsCCU(typ) != want {
			t.Errorf("%s", typ)
		}
	}
}
