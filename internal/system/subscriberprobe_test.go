package system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCallbackAddress(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://127.0.0.1:2048", "127.0.0.1:2048"},
		{"xmlrpc_bin://127.0.0.1:31999", "127.0.0.1:31999"},
		{"binrpc://198.51.100.9:2049", "198.51.100.9:2049"},
		{"http://127.0.0.1:39292/bidcos", "127.0.0.1:39292"},
		{"xmlrpc://127.0.0.1:9292/groups", "127.0.0.1:9292"},
		{"http://[::1]:2048/x", "[::1]:2048"},
		{"http://ccu.example:8080?x=1", "ccu.example:8080"},
		{"http://user@10.0.0.5:81/", "10.0.0.5:81"},
		// the defaults of http and https; none for the RPC schemes
		{"http://nodered.local/rpc", "nodered.local:80"},
		{"https://10.0.0.5", "10.0.0.5:443"},
		{"http://[fe80::1]", "fe80::1:80"},
		{"xmlrpc://127.0.0.1", ""},
		{"xmlrpc_bin://127.0.0.1/", ""},
		// nothing to connect to
		{"127.0.0.1:2048", ""},
		{"http://", ""},
		{"http://127.0.0.1:0", ""},
		{"http://127.0.0.1:99999", ""},
		{"http://127.0.0.1:port", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := callbackAddress(c.url)
		if c.url == "http://[fe80::1]" {
			// JoinHostPort brackets an IPv6 host again
			c.want = "[fe80::1]:80"
		}
		if got != c.want {
			t.Errorf("callbackAddress(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// probeRoot is a box whose rfd has the given handlers file and whose VirtualDevices process has one
// entry without a port.
func probeRoot(t *testing.T, rfd string) Root {
	t.Helper()
	return rootWith(t, map[string]string{
		"var/RFD.handlers":      rfd,
		"var/HMSERVER.handlers": "xmlrpc://127.0.0.1\thmm_VirtualDevices\n",
	})
}

func reachOf(t *testing.T, list []SubscriberReach, id string) SubscriberReach {
	t.Helper()
	for _, r := range list {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no verdict for %s in %+v", id, list)
	return SubscriberReach{}
}

// An open port is reachable, a closed one refused, and a URL without an address is not probed.
func TestSubscriberProbeOnLoopback(t *testing.T) {
	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	go func() {
		for {
			c, err := open.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := gone.Addr().String()
	_ = gone.Close()

	root := probeRoot(t, fmt.Sprintf("http://%s\tnr_live_BidCos-RF\nhttp://%s\tnr_gone_BidCos-RF\nxmlrpc_bin://%s\t1007\n", open.Addr(), closed, open.Addr()))
	p := &SubscriberProbe{Timeout: 2 * time.Second}
	list := p.Probe(context.Background(), root, []string{"BidCos-RF", "VirtualDevices", "HmIP-RF"})

	if len(list) != 4 {
		t.Fatalf("want the three rfd entries and the VirtualDevices one, got %+v", list)
	}
	if list[0].Interface != "BidCos-RF" || list[3].Interface != "VirtualDevices" {
		t.Errorf("not in the order of the interfaces: %+v", list)
	}
	if r := reachOf(t, list, "nr_live_BidCos-RF"); r.Reachable == nil || !*r.Reachable || r.Reason != "" {
		t.Errorf("an open port: %+v", r)
	}
	if r := reachOf(t, list, "1007"); r.Reachable == nil || !*r.Reachable {
		t.Errorf("a BIN-RPC callback on an open port: %+v", r)
	}
	if r := reachOf(t, list, "nr_gone_BidCos-RF"); r.Reachable == nil || *r.Reachable || r.Reason != "refused" {
		t.Errorf("a closed port: %+v", r)
	}
	if r := reachOf(t, list, "hmm_VirtualDevices"); r.Reachable != nil || r.Reason != "" {
		t.Errorf("a URL without a port: %+v", r)
	}
}

// A connect that does not end is given up after the timeout; its verdict is kept for TTL, one
// address is connected to once however many entries name it, and the connects run side by side.
func TestSubscriberProbeTimeoutCacheAndParallel(t *testing.T) {
	var dials atomic.Int32
	hang := func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	root := probeRoot(t, "http://10.9.9.9:2048\tnr_a_BidCos-RF\nbinrpc://10.9.9.9:2048\tnr_b_BidCos-RF\n")
	p := &SubscriberProbe{Timeout: 60 * time.Millisecond, TTL: time.Hour, Dial: hang}

	start := time.Now()
	list := p.Probe(context.Background(), root, []string{"BidCos-RF"})
	if took := time.Since(start); took > time.Second {
		t.Errorf("the probe took %v with a 60 ms timeout", took)
	}
	for _, id := range []string{"nr_a_BidCos-RF", "nr_b_BidCos-RF"} {
		if r := reachOf(t, list, id); r.Reachable == nil || *r.Reachable || r.Reason != "timeout" {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if n := dials.Load(); n != 1 {
		t.Errorf("one address, %d connects", n)
	}
	p.Probe(context.Background(), root, []string{"BidCos-RF"})
	if n := dials.Load(); n != 1 {
		t.Errorf("a second probe within TTL connected again (%d connects)", n)
	}
	// the verdict expires
	p.TTL = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	p.Probe(context.Background(), root, []string{"BidCos-RF"})
	if n := dials.Load(); n != 2 {
		t.Errorf("after TTL: %d connects, want 2", n)
	}

	// twenty addresses that all hang are done in about one timeout, not twenty
	var many strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&many, "http://10.9.9.%d:2048\tclient%02d\n", i+10, i)
	}
	p = &SubscriberProbe{Timeout: 60 * time.Millisecond, Dial: hang}
	start = time.Now()
	list = p.Probe(context.Background(), probeRoot(t, many.String()), []string{"BidCos-RF"})
	if took := time.Since(start); took > 600*time.Millisecond || len(list) != 20 {
		t.Errorf("20 hanging connects took %v (%d verdicts)", took, len(list))
	}
}

// A request that goes away ends the connects, and what they found is not kept.
func TestSubscriberProbeCancelled(t *testing.T) {
	var dials atomic.Int32
	hang := func(ctx context.Context, _, _ string) (net.Conn, error) {
		dials.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	root := probeRoot(t, "http://10.9.9.9:2048\tnr_a_BidCos-RF\n")
	p := &SubscriberProbe{Timeout: time.Hour, Dial: hang}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	p.Probe(ctx, root, []string{"BidCos-RF"})
	if took := time.Since(start); took > time.Second {
		t.Errorf("a cancelled request waited %v for its connects", took)
	}
	// the next request connects again (with a short limit of its own, so the test ends)
	p.Timeout = 30 * time.Millisecond
	p.Probe(context.Background(), root, []string{"BidCos-RF"})
	if n := dials.Load(); n != 2 {
		t.Errorf("a cancelled connect was kept as a verdict (%d connects)", n)
	}
}

func TestProbeReason(t *testing.T) {
	if r := probeReason(fmt.Errorf("dial: %w", errors.New("no route to host"))); r != "unreachable" {
		t.Errorf("other error: %q", r)
	}
	if r := probeReason(context.DeadlineExceeded); r != "timeout" {
		t.Errorf("deadline: %q", r)
	}
}
