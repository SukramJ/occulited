package trust

// openccu-lite task 232 (the maintainer, 2026-10-03: "only OIDC and ACME ... per pin, the admin
// chooses whether the pin is checked in addition to the CA validation (the default) or instead of
// it"): certificate pinning per purpose. A pin is the SHA-256 of a certificate's public key (its
// SubjectPublicKeyInfo, as HPKP and curl's --pinnedpubkey take it), so it survives a renewal with
// the same key; a purpose may hold several (a backup pin for a key rollover). A connection of a
// purpose with pins must present a chain one of the pins matches - any certificate of it, the
// leaf or a CA - or it fails loudly, and the failure is kept for the Status page with the
// fingerprint the server presented and the page's Re-pin. The matching pin's mode says what else
// is checked: ModeWithCA (the default) runs the CA validation of the purpose's pool as well;
// ModeOnly lets the pin alone vouch (a self-signed identity provider, a step-ca without a
// trusted root) - the UI asks in red before it is set. The purposes are PinPurposes; nothing else
// is pinned, by decision. The code is shaped so another purpose is one more entry.

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// The modes of a pin.
const (
	// ModeWithCA: the pin must match and the chain must verify against the purpose's pool.
	ModeWithCA = "with-ca"
	// ModeOnly: the pin alone vouches for the connection; no CA validation, no name check.
	ModeOnly = "only"
)

// PinPurposes are the purposes that take pins (the maintainer: OIDC and ACME, nothing else).
var PinPurposes = []string{PurposeOIDC, PurposeACME}

// Errors of the pin operations.
var (
	ErrPinPurpose = errors.New("this store takes no pins: only the OAuth / OIDC and the ACME store do")
	ErrPinMode    = errors.New("the mode is with-ca (the CA check as well, the default) or only (the pin alone)")
	ErrPinSPKI    = errors.New("the public key hash is the SHA-256 of a certificate's SubjectPublicKeyInfo: 44 base64 characters or 64 hex digits")
	ErrPinPresent = errors.New("this public key is pinned already")
)

// Pin is one pinned public key of a purpose.
type Pin struct {
	// ID is the first 16 hex digits of the SPKI SHA-256.
	ID      string `json:"id"`
	Purpose string `json:"purpose"`
	// SPKI is the SHA-256 of the SubjectPublicKeyInfo, base64 (as browsers and curl show it).
	SPKI string `json:"spki"`
	Mode string `json:"mode"`
	// Subject and NotAfter are the certificate's the pin was taken from; empty for a pin entered
	// as a bare hash (a backup pin for a key not in use yet).
	Subject  string    `json:"subject,omitempty"`
	NotAfter time.Time `json:"not_after,omitzero"`
	// Fingerprint is that certificate's SHA-256, for the comparison with what a server shows.
	Fingerprint string    `json:"fingerprint,omitempty"`
	Added       time.Time `json:"added"`
	AddedBy     string    `json:"added_by,omitempty"`
}

// PinFailure is a connection of a purpose whose chain matched none of its pins: what the server
// presented, for the Status page's warning and the Re-pin.
type PinFailure struct {
	Purpose string    `json:"purpose"`
	Host    string    `json:"host"`
	At      time.Time `json:"at"`
	Error   string    `json:"error"`
	// Chain is what the server presented, the leaf first, each with its SPKI.
	Chain []Info `json:"chain,omitempty"`
}

// PinMismatchError is the handshake's refusal: the chain matched no pin of the purpose.
type PinMismatchError struct {
	Purpose string
	Host    string
	Chain   []*x509.Certificate
	Pins    int
}

func (e *PinMismatchError) Error() string {
	if len(e.Chain) == 0 {
		return fmt.Sprintf("%s: the server presented no certificate; %d pin(s) are set for %s", e.Host, e.Pins, e.Purpose)
	}
	c := e.Chain[0]
	return fmt.Sprintf("%s presents a certificate none of the %d pin(s) for %s match: %s, public key SHA-256 %s, certificate SHA-256 %s", e.Host, e.Pins, e.Purpose, c.Subject, SPKI(c), Fingerprint(c))
}

// SPKI is the SHA-256 of a certificate's public key (its SubjectPublicKeyInfo), base64.
func SPKI(c *x509.Certificate) string {
	sum := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return base64.StdEncoding.EncodeToString(sum[:])
}

func pinID(spki string) string {
	b, err := base64.StdEncoding.DecodeString(spki)
	if err != nil || len(b) < 8 {
		return ""
	}
	return hex.EncodeToString(b[:8])
}

// NormalSPKI reads a public key hash as typed or copied - base64, or hex with or without colons
// (openssl's output) - and returns it as base64; "" when it is neither.
func NormalSPKI(s string) string {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "sha256//"))
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == sha256.Size {
		return base64.StdEncoding.EncodeToString(b)
	}
	if b, err := hex.DecodeString(NormalFingerprint(s)); err == nil && len(b) == sha256.Size {
		return base64.StdEncoding.EncodeToString(b)
	}
	return ""
}

func validPinPurpose(p string) bool { return slices.Contains(PinPurposes, p) }

func validPinMode(m string) bool { return m == ModeWithCA || m == ModeOnly }

// Pins lists a purpose's pins, oldest first.
func (s *Store) Pins(purpose string) ([]Pin, error) {
	if !validPinPurpose(purpose) {
		return nil, ErrPinPurpose
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	return pinsOf(f, purpose), nil
}

func pinsOf(f file, purpose string) []Pin {
	out := []Pin{}
	for _, p := range f.Pins {
		if p.Purpose == purpose {
			out = append(out, p)
		}
	}
	return out
}

// PinRequest is what a pin is made from: a certificate (its public key is pinned, its subject and
// fingerprint kept for the display) or a bare public key hash (SPKI); Mode (ModeWithCA when
// empty); Replace drops the purpose's other pins first - the page's Re-pin.
type PinRequest struct {
	Cert    *x509.Certificate
	SPKI    string
	Mode    string
	By      string
	Replace bool
}

// AddPin pins a public key for a purpose. ErrPinPresent when the key is pinned already (and
// Replace is not set).
func (s *Store) AddPin(purpose string, r PinRequest) (Pin, error) {
	if !validPinPurpose(purpose) {
		return Pin{}, ErrPinPurpose
	}
	if r.Mode == "" {
		r.Mode = ModeWithCA
	}
	if !validPinMode(r.Mode) {
		return Pin{}, ErrPinMode
	}
	now := s.now().UTC()
	p := Pin{Purpose: purpose, Mode: r.Mode, Added: now, AddedBy: r.By}
	switch {
	case r.Cert != nil:
		p.SPKI, p.Subject, p.NotAfter, p.Fingerprint = SPKI(r.Cert), r.Cert.Subject.String(), r.Cert.NotAfter, Fingerprint(r.Cert)
	default:
		p.SPKI = NormalSPKI(r.SPKI)
		if p.SPKI == "" {
			return Pin{}, ErrPinSPKI
		}
	}
	p.ID = pinID(p.SPKI)
	if err := s.addPinLocked(purpose, p, r.Replace); err != nil {
		return Pin{}, err
	}
	s.changed() // after the lock is released (B-248)
	return p, nil
}

func (s *Store) addPinLocked(purpose string, p Pin, replace bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	if replace {
		f.Pins = slices.DeleteFunc(f.Pins, func(x Pin) bool { return x.Purpose == purpose })
	} else if slices.ContainsFunc(f.Pins, func(x Pin) bool { return x.Purpose == purpose && x.SPKI == p.SPKI }) {
		return ErrPinPresent
	}
	f.Pins = append(f.Pins, p)
	// a pin that matches what the server presented clears the mismatch it caused
	f.PinFailures = slices.DeleteFunc(f.PinFailures, func(x PinFailure) bool {
		return x.Purpose == purpose && slices.ContainsFunc(x.Chain, func(i Info) bool { return i.SPKI == p.SPKI })
	})
	return s.save(f)
}

// RemovePin forgets one pin; ErrUnknown when there is none.
func (s *Store) RemovePin(purpose, id string) error {
	if !validPinPurpose(purpose) {
		return ErrPinPurpose
	}
	if err := s.removePinLocked(purpose, id); err != nil {
		return err
	}
	s.changed()
	return nil
}

func (s *Store) removePinLocked(purpose, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	n := len(f.Pins)
	f.Pins = slices.DeleteFunc(f.Pins, func(x Pin) bool { return x.Purpose == purpose && x.ID == id })
	if len(f.Pins) == n {
		return ErrUnknown
	}
	if len(pinsOf(f, purpose)) == 0 {
		// without pins there is nothing to mismatch
		f.PinFailures = slices.DeleteFunc(f.PinFailures, func(x PinFailure) bool { return x.Purpose == purpose })
	}
	return s.save(f)
}

// --- the verifier --------------------------------------------------------------------------------

// Verifier is a purpose's TLS verification: the pool its chains must end in (nil: Go's system
// roots) and its pins.
type Verifier struct {
	Purpose string
	Roots   *x509.CertPool
	Pins    []Pin
}

// Outcome is what Verify accepted: the chain (verified to a root, or the one presented when a
// pin alone vouched) and the pin that matched, if any.
type Outcome struct {
	Chain []*x509.Certificate
	Pin   *Pin
	// PinOnly: the pin alone vouched; no CA validation was run
	PinOnly bool
}

// VerifierFor is the purpose's verifier as its connections use it: PoolFor and the pins.
func (s *Store) VerifierFor(purpose string) (Verifier, error) {
	pool, err := s.PoolFor(purpose)
	if err != nil {
		return Verifier{}, err
	}
	v := Verifier{Purpose: purpose, Roots: pool}
	if validPinPurpose(purpose) {
		s.mu.Lock()
		f, err := s.load()
		s.mu.Unlock()
		if err != nil {
			return Verifier{}, err
		}
		v.Pins = pinsOf(f, purpose)
	}
	return v, nil
}

// Verify checks the chain a server presented for serverName (a host name or an IP address):
// with pins, one must match a certificate of the chain, else PinMismatchError; the matching
// pin's mode decides whether the chain must verify against Roots as well.
func (v Verifier) Verify(serverName string, chain []*x509.Certificate) (Outcome, error) {
	if len(chain) == 0 {
		if len(v.Pins) > 0 {
			return Outcome{}, &PinMismatchError{Purpose: v.Purpose, Host: serverName, Pins: len(v.Pins)}
		}
		return Outcome{}, errors.New("the server presented no certificate")
	}
	var matched *Pin
	if len(v.Pins) > 0 {
		for _, c := range chain {
			spki := SPKI(c)
			for i := range v.Pins {
				if v.Pins[i].SPKI == spki {
					if matched == nil || v.Pins[i].Mode == ModeOnly {
						matched = &v.Pins[i]
					}
				}
			}
		}
		if matched == nil {
			return Outcome{}, &PinMismatchError{Purpose: v.Purpose, Host: serverName, Chain: chain, Pins: len(v.Pins)}
		}
		if matched.Mode == ModeOnly {
			return Outcome{Chain: chain, Pin: matched, PinOnly: true}, nil
		}
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	chains, err := chain[0].Verify(x509.VerifyOptions{Roots: v.Roots, Intermediates: inter, DNSName: serverName})
	if err != nil {
		// the shape Go's own handshake gives a lost verification, so the transport's failure
		// record sees the chain whichever path verified
		return Outcome{}, &tls.CertificateVerificationError{UnverifiedCertificates: chain, Err: err}
	}
	return Outcome{Chain: chains[0], Pin: matched}, nil
}

// Pinned: the verifier has pins, so its connections skip Go's own verification for Verify.
func (v Verifier) Pinned() bool { return len(v.Pins) > 0 }

// TLSConfig is the verifier's client configuration for serverName: Go's own verification against
// Roots without pins; with them, Verify on the presented chain (the name check is part of it).
func (v Verifier) TLSConfig(serverName string) *tls.Config {
	cfg := &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"}}
	if !v.Pinned() {
		cfg.RootCAs = v.Roots
		return cfg
	}
	cfg.InsecureSkipVerify = true //nolint:gosec // VerifyConnection does the verification, with the pins
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		_, err := v.Verify(serverName, cs.PeerCertificates)
		return err
	}
	return cfg
}

// DialTLS dials addr (host:port) and runs the handshake under TLSConfig for its host.
func (v Verifier) DialTLS(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}, Config: v.TLSConfig(host)}
	return d.DialContext(ctx, network, addr)
}

// --- the failures --------------------------------------------------------------------------------

// PinFailures lists the pending mismatches.
func (s *Store) PinFailures() []PinFailure {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil
	}
	return slices.Clone(f.PinFailures)
}

// recordPinFailure keeps one mismatch per purpose and host.
func (s *Store) recordPinFailure(e *PinMismatchError) {
	now := s.now()
	fl := PinFailure{Purpose: e.Purpose, Host: e.Host, At: now.UTC(), Error: e.Error()}
	for _, c := range e.Chain {
		fl.Chain = append(fl.Chain, Describe(c, now))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return
	}
	k := slices.IndexFunc(f.PinFailures, func(x PinFailure) bool { return x.Purpose == e.Purpose && x.Host == e.Host })
	if k >= 0 {
		f.PinFailures[k] = fl
	} else {
		f.PinFailures = append(f.PinFailures, fl)
		s.log().Warn("trust: a server's certificate matches none of its purpose's pins", "purpose", e.Purpose, "host", e.Host, "pins", e.Pins)
	}
	_ = s.save(f)
}

// clearPinFailure forgets a host's mismatch once a connection to it verifies.
func (s *Store) clearPinFailure(purpose, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return
	}
	n := len(f.PinFailures)
	f.PinFailures = slices.DeleteFunc(f.PinFailures, func(x PinFailure) bool { return x.Purpose == purpose && x.Host == host })
	if len(f.PinFailures) != n {
		_ = s.save(f)
	}
}

// --- the peer's chain ----------------------------------------------------------------------------

// PeerChain is the certificate chain the server of rawurl presents, read without verifying it -
// for the administrator to compare fingerprints before trusting or pinning one (task 230's trust
// on first use, explicit; task 232's Pin the current certificate). Nothing read here is trusted
// by itself. The host of the URL is returned with it.
func PeerChain(ctx context.Context, rawurl string) (string, []*x509.Certificate, error) {
	u, err := url.Parse(strings.TrimSpace(rawurl))
	if err != nil || u.Host == "" {
		return "", nil, errors.New("the address is not a URL")
	}
	if u.Scheme != "https" {
		return "", nil, errors.New("the address is not https: there is no certificate to fetch")
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{ServerName: u.Hostname(), InsecureSkipVerify: true}} //nolint:gosec // shown for comparison, never trusted from here
	conn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return u.Hostname(), nil, err
	}
	defer conn.Close()
	return u.Hostname(), conn.(*tls.Conn).ConnectionState().PeerCertificates, nil
}
