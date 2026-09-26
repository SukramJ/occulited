package trust

// The HTTP client occulited's own outbound calls use (task 231): its TLS trusts one store's pool
// and nothing else, and a handshake lost to a CA the store does not hold is recorded as a Failure
// - the Status page's warning names the host and the CA and offers the one-click copy from the
// system store. Strict, by the maintainer's decision: nothing proceeds until an administrator
// adds the CA.

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
	store   *Store
	id      string
	mu      sync.Mutex
	version uint64
	tr      *http.Transport
}

// Transport returns a round tripper that trusts the store's pool.
func (s *Store) Transport(store string) *Transport {
	return &Transport{store: s, id: store}
}

// HTTPClient is a client on Transport(store) with the timeout.
func (s *Store) HTTPClient(store string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: s.Transport(store)}
}

func (t *Transport) current() *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	v := t.store.Version()
	if t.tr != nil && t.version == v {
		return t.tr
	}
	pool, err := t.store.PoolFor(t.id)
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
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
		TLSClientConfig:       &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
	}
	t.version = v
	return t.tr
}

// RoundTrip runs the request on the current pool. A verification failure for an unknown
// authority is recorded with the chain the server presented; a request that gets through clears
// the host's record.
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.current().RoundTrip(r)
	host := r.URL.Hostname()
	if err == nil {
		if r.URL.Scheme == "https" {
			t.store.clearFailure(t.id, host)
		}
		return res, nil
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
