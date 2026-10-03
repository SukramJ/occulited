package trust

// The HTTP client occulited's own outbound calls use (task 231): its TLS trusts one store's pool
// and nothing else, and a handshake lost to a CA the store does not hold is recorded as a Failure
// - the Status page's warning names the host and the CA and offers the one-click copy from the
// system store. Strict, by the maintainer's decision: nothing proceeds until an administrator
// adds the CA. Task 232: a purpose with pins (OIDC, ACME) verifies through its Verifier instead
// of Go's own check, and a chain that matches none of the pins is recorded as a PinFailure - the
// warning names the host and the fingerprint it presented, the page offers Re-pin.

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"
)

// Transport is an http.RoundTripper on one store; it rebuilds its connection pool when the store
// changes.
type Transport struct {
	store *Store
	id    string
	// Record keeps a lost handshake as a failure of the store (the Status page's warning); off for
	// a check the administrator runs by hand against an address that may not be the configured one.
	Record  bool
	mu      sync.Mutex
	version uint64
	tr      *http.Transport
}

// Transport returns a round tripper that trusts the store's pool and records failures.
func (s *Store) Transport(store string) *Transport {
	return &Transport{store: s, id: store, Record: true}
}

// HTTPClient is a client on Transport(store) with the timeout.
func (s *Store) HTTPClient(store string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: s.Transport(store)}
}

// QuietClient is HTTPClient without the failure records: for a check run by hand.
func (s *Store) QuietClient(store string, timeout time.Duration) *http.Client {
	t := s.Transport(store)
	t.Record = false
	return &http.Client{Timeout: timeout, Transport: t}
}

func (t *Transport) current() *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.store.Version()
	if t.tr != nil && t.version == v {
		return t.tr
	}
	ver, err := t.store.VerifierFor(t.id)
	if err != nil || ver.Roots == nil {
		ver.Roots = x509.NewCertPool()
	}
	if t.tr != nil {
		t.tr.CloseIdleConnections()
	}
	t.tr = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{RootCAs: ver.Roots, MinVersion: tls.VersionTLS12},
	}
	if ver.Pinned() {
		// the handshake is the verifier's: the pins, then the CA check where the pin asks for it
		t.tr.DialTLSContext = ver.DialTLS
	}
	t.version = v
	return t.tr
}

// RoundTrip runs the request on the current pool. A verification failure for an unknown
// authority is recorded with the chain the server presented, a pin mismatch with what it
// presented; a request that gets through clears the host's records.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.current().RoundTrip(r)
	host := r.URL.Hostname()
	if err == nil {
		if r.URL.Scheme == "https" && t.Record {
			t.store.clearFailure(t.id, host)
			t.store.clearPinFailure(t.id, host)
		}
		return res, nil
	}
	if !t.Record {
		return nil, err
	}
	var pm *PinMismatchError
	if errors.As(err, &pm) {
		t.store.recordPinFailure(pm)
		return nil, err
	}
	var cve *tls.CertificateVerificationError
	var ua x509.UnknownAuthorityError
	if errors.As(err, &cve) && errors.As(err, &ua) {
		t.store.recordFailure(t.id, host, cve.UnverifiedCertificates, err)
	}
	return nil, err
}

// CloseIdleConnections is http.Client's hook.
func (t *Transport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tr != nil {
		t.tr.CloseIdleConnections()
	}
}
