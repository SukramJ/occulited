package system

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ---- whether a subscriber's callback accepts a connection (task 76's follow-up, D-64) ----------
//
// An interface process sends every event to every registered callback, and a callback that is gone
// costs it a timeout per event (hmipserver's Legacy.Client.Connection.Timeout is 10 s). The handlers
// file cannot say which entry is gone; a TCP connect can hint at it. The Interfaces page asks once
// when it opens: occulited connects to each callback's host and port - on this box or on the LAN -
// within a second, and the card marks an entry that did not accept as "not reachable". A connect
// is all it does: nothing is sent, the connection is closed at once, and nothing acts on the verdict.

// SubscriberReach is the verdict on one registered callback.
type SubscriberReach struct {
	Interface string `json:"interface"`
	ID        string `json:"id"`
	URL       string `json:"url"`
	// Reachable is whether a TCP connection to the callback's host and port was accepted within the
	// probe's timeout; absent when the URL names no address to connect to.
	Reachable *bool `json:"reachable,omitempty"`
	// Reason says why not: "refused", "timeout" or "unreachable".
	Reason string `json:"reason,omitempty"`
}

// callbackAddress is the host:port a callback URL points at, "" when it names none. http and https
// without a port have their defaults; the XML-RPC and BIN-RPC schemes have no default a client relies
// on, so such a URL without a port is not probed.
func callbackAddress(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok || scheme == "" {
		return ""
	}
	hostport, _, _ := strings.Cut(rest, "/")
	hostport, _, _ = strings.Cut(hostport, "?")
	if at := strings.LastIndex(hostport, "@"); at >= 0 {
		hostport = hostport[at+1:]
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		switch strings.ToLower(scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
		host = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 || host == "" || strings.ContainsAny(host, " /[]") {
		return ""
	}
	return net.JoinHostPort(host, port)
}

// SubscriberProbe connects to the callbacks. A verdict is kept for TTL per address, so a page opened by
// several people, or reloaded, does not make the box connect again and again.
type SubscriberProbe struct {
	Timeout time.Duration // per connect; 0 = 1 s
	TTL     time.Duration // how long a verdict is kept; 0 = 30 s
	// Dial makes the connection; nil = a net.Dialer (the tests put a fake here)
	Dial func(ctx context.Context, network, address string) (net.Conn, error)

	mu    sync.Mutex
	cache map[string]probeVerdict
}

type probeVerdict struct {
	ok     bool
	reason string
	at     time.Time
}

// probeParallel is how many connects run at once.
const probeParallel = 16

// Probe checks every subscriber of the named interfaces, in the order of the names and of each
// interface's list; every address is connected to once, however many entries name it.
func (p *SubscriberProbe) Probe(ctx context.Context, root Root, interfaces []string) []SubscriberReach {
	out := []SubscriberReach{}
	var addrs []string
	seen := map[string]bool{}
	for _, name := range interfaces {
		for _, s := range root.InterfaceSubscribers(name) {
			out = append(out, SubscriberReach{Interface: name, ID: s.ID, URL: s.URL})
			if a := callbackAddress(s.URL); a != "" && !seen[a] {
				seen[a] = true
				addrs = append(addrs, a)
			}
		}
	}
	verdicts := make([]probeVerdict, len(addrs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, probeParallel)
	for i, a := range addrs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			verdicts[i] = p.verdict(ctx, a)
		}()
	}
	wg.Wait()
	byAddr := make(map[string]probeVerdict, len(addrs))
	for i, a := range addrs {
		byAddr[a] = verdicts[i]
	}
	for i := range out {
		a := callbackAddress(out[i].URL)
		if a == "" {
			continue
		}
		v := byAddr[a]
		ok := v.ok
		out[i].Reachable = &ok
		if !ok {
			out[i].Reason = v.reason
		}
	}
	return out
}

// verdict connects to one address, or answers what a connect within TTL found.
func (p *SubscriberProbe) verdict(ctx context.Context, addr string) probeVerdict {
	ttl := p.TTL
	if ttl == 0 {
		ttl = 30 * time.Second
	}
	p.mu.Lock()
	if v, ok := p.cache[addr]; ok && time.Since(v.at) < ttl {
		p.mu.Unlock()
		return v
	}
	p.mu.Unlock()

	timeout := p.Timeout
	if timeout == 0 {
		timeout = time.Second
	}
	dial := p.Dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	conn, err := dial(cctx, "tcp", addr)
	cancel()
	v := probeVerdict{ok: err == nil, at: time.Now()}
	if err == nil {
		_ = conn.Close()
	} else {
		v.reason = probeReason(err)
	}
	// the request went away before the connect ended: that is no verdict on the address
	if ctx.Err() != nil {
		return v
	}
	p.mu.Lock()
	if p.cache == nil {
		p.cache = map[string]probeVerdict{}
	}
	// old verdicts go, so the map does not grow with every address that was ever registered
	for a, old := range p.cache {
		if time.Since(old.at) >= ttl {
			delete(p.cache, a)
		}
	}
	p.cache[addr] = v
	p.mu.Unlock()
	return v
}

// probeReason is the short word for a failed connect.
func probeReason(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return "refused"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "timeout"
	default:
		return "unreachable"
	}
}
