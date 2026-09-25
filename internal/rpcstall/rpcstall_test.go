package rpcstall

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// Lines of a Pi 4 lab system's socket tables while both daemons were stalled (2026-09-25): rfd (uid
// 8110) listening on 32001 and connected to the answering 8196 and the hanging 8195, occulited
// (8100) on 8184, hmipserver (8111, Java on the IPv6 table) listening on 32010 and 39292 and
// connected to itself and to the hanging 8199.
const tcp4 = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   5: 0100007F:1FF8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  8100        0 77035 2 ffffff8080dc3780 100 0 0 10 0
   9: 0100007F:7D01 00000000:0000 0A 00000000:0000003B 00:00000000 00000000  8110        0 13052 60 ffffff8044840000 100 0 0 10 0
  20: 0100007F:2004 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 79986 2 ffffff80837fef00 100 0 0 10 0
  21: 0100007F:2003 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 77401 2 ffffff80837edc80 100 0 0 10 0
  18: 0100007F:2007 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 78330 2 ffffff8041c4e5c0 100 0 0 10 0
  36: 0100007F:A482 0100007F:2004 01 00000000:00000000 00:00000000 00000000  8110        0 77409 1 ffffff80837eae40 20 4 30 10 -1
  44: 0100007F:2004 0100007F:A482 01 00000000:00000000 02:000002C7 00000000     0        0 77410 2 ffffff80837ec0c0 20 4 31 10 -1
 106: 0100007F:DADE 0100007F:2003 01 00000000:00000000 00:00000000 00000000  8110        0 79540 1 ffffff8046bcca00 20 0 0 10 -1
  67: 0100007F:2003 0100007F:DADE 01 00000000:00000000 02:0000017A 00000000     0        0 77458 2 ffffff8046bc8000 20 4 30 10 -1
  39: 0100007F:7D01 0100007F:ABE2 01 00000000:00000000 00:00000000 00000000  8110        0 13104 1 ffffff80849365c0 20 4 29 10 -1
 108: 0100007F:B344 0100007F:1FF8 08 00000000:00000001 00:00000000 00000000  8110        0 77089 1 ffffff80837e9bc0 20 4 28 10 -1
  78: 980200C0:0016 720200C0:D4B4 01 000000D0:00000000 01:00000013 00000000     0        0 77520 3 ffffff8098ca2500 20 4 9 10 -1
`

const tcp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   4: 0000000000000000FFFF00000100007F:7D0A 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  8111        0 10877 1 ffffff804508e180 100 0 0 10 0
  11: 0000000000000000FFFF00000100007F:997C 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  8111        0 10897 1 ffffff8045089d40 100 0 0 10 0
  14: 0000000000000000FFFF00000100007F:997C 0000000000000000FFFF00000100007F:8E88 01 00000000:00000000 00:00000000 00000000  8111        0 16501 1 ffffff8046bdba80 20 4 31 10 -1
  15: 0000000000000000FFFF00000100007F:7D0A 0000000000000000FFFF00000100007F:C792 01 00000000:00000000 00:00000000 00000000  8111        0 80236 1 ffffff80468e7500 20 4 31 10 -1
  16: 0000000000000000FFFF00000100007F:CD58 0000000000000000FFFF00000100007F:2007 01 00000000:00000000 00:00000000 00000000  8111        0 77270 1 ffffff80468e4e00 20 0 0 10 -1
  17: 0000000000000000FFFF00000100007F:8E88 0000000000000000FFFF00000100007F:997C 01 00000000:00000000 00:00000000 00000000  8111        0 16500 1 ffffff8046bdba80 20 4 31 10 -1
  18: 00000000000000000000000001000000:1F90 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000    33        0 9 1 x 100 0 0 10 0
not a socket line
`

func TestParseTCP(t *testing.T) {
	cs := ParseTCP([]byte(tcp4 + tcp6))
	if len(cs) != 19 {
		t.Fatalf("%d conns", len(cs))
	}
	c := cs[5] // rfd → the answering listener
	if c.Local.String() != "127.0.0.1:42114" || c.Remote.String() != "127.0.0.1:8196" || c.State != stateEstablished || c.UID != 8110 || c.Inode != 77409 {
		t.Errorf("%+v", c)
	}
	h := cs[16] // hmipserver → the hanging listener, IPv4-mapped
	if h.Local.String() != "127.0.0.1:52568" || h.Remote.String() != "127.0.0.1:8199" || h.UID != 8111 {
		t.Errorf("mapped %+v", h)
	}
	if s := cs[11].Local.String(); s != "192.0.2.152:22" {
		t.Errorf("LAN address %s", s)
	}
	if s := cs[18].Local.Addr().String(); s != "::1" {
		t.Errorf("IPv6 loopback %s", s)
	}
}

func TestDaemonOf(t *testing.T) {
	cs := ParseTCP([]byte(tcp4 + tcp6))
	rfd, ok := DaemonOf(cs, 32001)
	if !ok || rfd.UID != 8110 {
		t.Fatalf("rfd %+v %v", rfd, ok)
	}
	// the accepted connection on 32001 is a client, the CLOSE_WAIT to occulited is not established
	if got := peers(rfd); got != "127.0.0.1:8195 127.0.0.1:8196" {
		t.Errorf("rfd peers %q", got)
	}
	hm, ok := DaemonOf(cs, 32010)
	if !ok || hm.UID != 8111 {
		t.Fatalf("hmipserver %+v", hm)
	}
	// its connection to its own 39292 is not a listener
	if got := peers(hm); got != "127.0.0.1:8199" {
		t.Errorf("hmipserver peers %q", got)
	}
	if len(hm.Conns) != 1 || hm.Conns[0].Inode != 77270 {
		t.Errorf("hmipserver conns %+v", hm.Conns)
	}
	if _, ok := DaemonOf(cs, 2000); ok {
		t.Error("nothing listens on 2000")
	}
	if u := ListenOwner(cs, 8195); u != 0 {
		t.Errorf("owner of 8195: %d", u)
	}
	if u := ListenOwner(cs, 1); u != -1 {
		t.Errorf("owner of nothing: %d", u)
	}
}

func peers(d Daemon) string {
	var s []string
	for _, p := range d.Peers {
		s = append(s, p.String())
	}
	return strings.Join(s, " ")
}

func TestCallbackAddress(t *testing.T) {
	for _, tc := range []struct {
		url, addr, path string
		bin             bool
	}{
		{"http://127.0.0.1:8184/cb/HmIP-RF", "127.0.0.1:8184", "/cb/HmIP-RF", false},
		{"binrpc://127.0.0.1:2047", "127.0.0.1:2047", "", true},
		{"xmlrpc_bin://192.0.2.50:2001", "192.0.2.50:2001", "", true},
		{"http://localhost:1234", "127.0.0.1:1234", "", false},
		{"http://[::ffff:127.0.0.1]:1234", "127.0.0.1:1234", "", false},
		{"http://nas.lan:8080/x", "nas.lan:8080", "/x", false},
		{"http://127.0.0.1", "127.0.0.1:80", "", false},
		{"binrpc://127.0.0.1", "", "", false},
		{"nonsense", "", "", false},
	} {
		addr, bin, path := callbackAddress(tc.url)
		if addr != tc.addr || (addr != "" && (bin != tc.bin || path != tc.path)) {
			t.Errorf("%s: %q %v %q", tc.url, addr, bin, path)
		}
	}
}

func TestListMethodsCall(t *testing.T) {
	b := ListMethodsCall("127.0.0.1:2047", true, "", "nr_AKHydZ_BidCos-RF")
	if !bytes.HasPrefix(b, []byte("Bin\x00")) || binary.BigEndian.Uint32(b[4:]) != uint32(len(b)-8) {
		t.Errorf("bin header % x", b)
	}
	// the method, one parameter, a string (3) of 19 bytes: the id, as rfd sends it
	if string(b[12:30]) != "system.listMethods" || binary.BigEndian.Uint32(b[30:]) != 1 || binary.BigEndian.Uint32(b[34:]) != 3 ||
		binary.BigEndian.Uint32(b[38:]) != 19 || string(b[42:]) != "nr_AKHydZ_BidCos-RF" {
		t.Errorf("bin body % x", b)
	}
	req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(ListMethodsCall("127.0.0.1:8184", false, "/cb/HmIP-RF", "a<b"))))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(req.Body)
	if req.Method != "POST" || req.URL.Path != "/cb/HmIP-RF" || !strings.Contains(string(body), "<methodName>system.listMethods</methodName><params><param><value><string>a&lt;b</string></value></param></params>") {
		t.Errorf("%s %s %s", req.Method, req.URL.Path, body)
	}
}

// listener starts a test callback: answer answers every call, hang reads and never answers, closer
// closes at once.
func listener(t *testing.T, mode string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				switch mode {
				case "closer":
					return
				case "hang":
					buf := make([]byte, 4096)
					_, _ = c.Read(buf)
					<-done
				case "bin-only":
					// a BIN-RPC server that takes an HTTP request for a header and waits for the rest
					head := make([]byte, 4)
					if _, err := io.ReadFull(c, head); err != nil || string(head) != "Bin\x00" {
						<-done
						return
					}
					_, _ = c.Write([]byte("Bin\x01\x00\x00\x00\x00"))
				default:
					buf := make([]byte, 4096)
					_, _ = c.Read(buf)
					_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func TestProbe(t *testing.T) {
	p := &Prober{Timeout: 300 * time.Millisecond}
	ctx := context.Background()
	if v := p.Probe(ctx, listener(t, "answer"), false, "/", "x"); v != VerdictAnswers {
		t.Errorf("answer: %s", v)
	}
	if v := p.Probe(ctx, listener(t, "closer"), true, "", "x"); v != VerdictAnswers {
		t.Errorf("closer: %s", v)
	}
	if v := p.Probe(ctx, listener(t, "hang"), false, "/", "x"); v != VerdictNoAnswer {
		t.Errorf("hang: %s", v)
	}
	// a port nobody listens on
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := ln.Addr().String()
	ln.Close()
	if v := p.Probe(ctx, dead, false, "/", "x"); v != VerdictRefused {
		t.Errorf("dead: %s", v)
	}
	p.Dial = func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Err: context.DeadlineExceeded}
	}
	if v := p.Probe(ctx, "192.0.2.1:1", false, "/", "x"); v != VerdictUnreachable {
		t.Errorf("unreachable: %s", v)
	}
}

// The rfd case: nothing on its list holds it, and it is connected to a listener that is not on it
// (the one whose init it is in). That one is named without being called; the registered ones are
// asked with their own ids; occulited's own and the LAN gateway are left alone.
func TestCheckUnregisteredHolds(t *testing.T) {
	good, binOnly, silent := listener(t, "answer"), listener(t, "bin-only"), listener(t, "hang")
	ap := func(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }
	own := "127.0.0.1:8184"
	in := Input{
		Subscribers: []Subscriber{
			{ID: "occulited_BidCos-RF", URL: "http://" + own + "/cb/BidCos-RF"},
			{ID: "nr_x", URL: "binrpc://" + binOnly},
			{ID: "b201a2", URL: "http://" + good},
			{ID: "dup", URL: "http://" + good},
		},
		Daemon: Daemon{Peers: []netip.AddrPort{ap(own), ap(good), ap(binOnly), ap(silent), ap("192.0.2.227:2000")}},
		Skip:   func(s Subscriber) bool { return strings.HasPrefix(s.ID, "occulited_") },
		Owner:  func(port uint16) string { return "addon-" + strings.Repeat("x", int(port%2)+1) },
	}
	got := (&Prober{Timeout: 300 * time.Millisecond}).Check(context.Background(), in)
	if len(got) != 3 {
		t.Fatalf("%d listeners: %+v", len(got), got)
	}
	if got[0].Address != silent || got[0].Verdict != VerdictHolds || !got[0].Stuck() || got[0].ID != "" || !got[0].Connected || !got[0].Local || got[0].Owner == "" {
		t.Errorf("the unregistered one %+v", got[0])
	}
	for _, l := range got[1:] {
		if l.Stuck() || l.Verdict != VerdictAnswers {
			t.Errorf("not stuck %+v", l)
		}
		if l.Address == good && l.ID != "b201a2" {
			t.Errorf("the first entry of an address wins %+v", l)
		}
		if l.Address == binOnly && l.URL != "binrpc://"+binOnly {
			t.Errorf("asked as BIN-RPC %+v", l)
		}
	}
}

// The hmipserver case of a listener that finished its registration and froze later: it is on the
// list and is asked - and a listener that is not on the list is not blamed beside it.
func TestCheckRegisteredNoAnswer(t *testing.T) {
	frozen, other := listener(t, "hang"), listener(t, "hang")
	in := Input{
		Subscribers: []Subscriber{{ID: "frozen", URL: "http://" + frozen}},
		Daemon:      Daemon{Peers: []netip.AddrPort{netip.MustParseAddrPort(frozen), netip.MustParseAddrPort(other)}},
	}
	got := (&Prober{Timeout: 200 * time.Millisecond}).Check(context.Background(), in)
	if len(got) != 2 || got[0].Address != frozen || got[0].Verdict != VerdictNoAnswer || got[1].Verdict != VerdictUnregistered || got[1].Stuck() {
		t.Errorf("%+v", got)
	}
}

// task 234: the checker's line as hmipserver wrote it on the Pi 4 (2026-09-25), and the lines
// around it that must not count.
func TestBlockedPool(t *testing.T) {
	for _, c := range []struct {
		msg  string
		id   string
		held time.Duration
		ok   bool
	}{
		{"io.vertx.core.impl.BlockedThreadChecker [vertx-blocked-thread-checker] Thread t234hang_WorkerPool-1 has been blocked for 60562 ms, time limit is 60000 ms", "t234hang", 60562 * time.Millisecond, true},
		// an id with underscores of its own (node-red-contrib-ccu's)
		{"io.vertx.core.impl.BlockedThreadChecker [vertx-blocked-thread-checker] Thread nr_x_HmIP-RF_WorkerPool-2 has been blocked for 181578 ms, time limit is 60000 ms", "nr_x_HmIP-RF", 181578 * time.Millisecond, true},
		// the shared pool (a listener hanging inside its registration, B-201) names nobody
		{"io.vertx.core.impl.BlockedThreadChecker [vertx-blocked-thread-checker] Thread vert.x-worker-thread-1 has been blocked for 1530097 ms, time limit is 60000 ms", "", 0, false},
		{"io.vertx.core.VertxException: Thread blocked", "", 0, false},
		{"        at de.eq3.cbcs.legacy.bidcos.rpc.internal.LegacyBackendClient.event(LegacyBackendClient.java:122)", "", 0, false},
		{"Thread x_WorkerPool-1 has been blocked for 99999999999999999999 ms", "", 0, false},
	} {
		id, held, ok := BlockedPool(c.msg)
		if id != c.id || held != c.held || ok != c.ok {
			t.Errorf("%q: %q %v %v", c.msg, id, held, ok)
		}
	}
	if !(Listener{Verdict: VerdictBlocked}).Stuck() {
		t.Error("a blocked listener is not stuck")
	}
}
