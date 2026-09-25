// Package trust is occulited's store of extra trust anchors (openccu-lite task 230): certificates
// an administrator added - a private CA, a chain, or a server's own certificate pinned - each with
// the purposes it is trusted for. Task 230 uses the purpose "oidc" (the identity provider's
// certificate); task 231's trust store page takes the same store over and adds purposes (acme,
// downloads, ...) without a migration. A purpose widens nothing else: Pool(purpose) is the
// system's pool plus the anchors of that purpose only.
//
// The store is one JSON file in the state directory, written atomically.
package trust

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// PurposeOIDC is the identity provider's TLS (task 230).
const PurposeOIDC = "oidc"

// FileName is the store's file in the state directory.
const FileName = "trust-anchors.json"

// ExpiresSoon is how close to its end a certificate is warned about.
const ExpiresSoon = 30 * 24 * time.Hour

// Errors of Add; the API answers them as 422 with the code in the error text's place.
var (
	ErrPrivateKey    = errors.New("the text holds a private key: only certificates are accepted - never paste or upload a key here")
	ErrNoCertificate = errors.New("no certificate found: paste a PEM block beginning with -----BEGIN CERTIFICATE-----")
	ErrExpired       = errors.New("the certificate has expired")
	ErrNotYetValid   = errors.New("the certificate is not valid yet")
	ErrUnknown       = errors.New("no such anchor")
	ErrPurpose       = errors.New("a purpose is a short lower-case word")
)

// Anchor is one stored certificate.
type Anchor struct {
	// ID is the first 16 hex digits of the SHA-256 fingerprint.
	ID       string    `json:"id"`
	Purposes []string  `json:"purposes"`
	PEM      string    `json:"pem"`
	Added    time.Time `json:"added"`
	AddedBy  string    `json:"added_by,omitempty"`
}

// Info is what the page shows of an anchor.
type Info struct {
	ID          string    `json:"id"`
	Purposes    []string  `json:"purposes"`
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"not_before"`
	NotAfter    time.Time `json:"not_after"`
	Fingerprint string    `json:"fingerprint"`
	// CA: a certificate authority; false: a server's own certificate, pinned
	CA          bool      `json:"ca"`
	SelfSigned  bool      `json:"self_signed"`
	Names       []string  `json:"names,omitempty"`
	Expired     bool      `json:"expired,omitempty"`
	ExpiresSoon bool      `json:"expires_soon,omitempty"`
	Added       time.Time `json:"added,omitzero"`
	AddedBy     string    `json:"added_by,omitempty"`
}

// Fingerprint is the SHA-256 of the certificate's DER, upper-case hex in colon-separated pairs,
// as browsers and openssl show it.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i : i+2])
	}
	return b.String()
}

// NormalFingerprint strips separators and case, so a fingerprint typed or copied either way compares.
func NormalFingerprint(s string) string {
	return strings.ToUpper(strings.NewReplacer(":", "", " ", "", "-", "").Replace(strings.TrimSpace(s)))
}

func idOf(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:8])
}

// Describe is a certificate's Info at now.
func Describe(c *x509.Certificate, now time.Time) Info {
	names := append([]string{}, c.DNSNames...)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	return Info{
		ID: idOf(c), Subject: c.Subject.String(), Issuer: c.Issuer.String(), NotBefore: c.NotBefore, NotAfter: c.NotAfter,
		Fingerprint: Fingerprint(c), CA: c.IsCA, SelfSigned: bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil,
		Names: names, Expired: now.After(c.NotAfter), ExpiresSoon: !now.After(c.NotAfter) && c.NotAfter.Sub(now) < ExpiresSoon,
	}
}

// Parse reads the certificates of a PEM text: a CA, a chain or a server's certificate. A private
// key anywhere in it refuses the whole text; so does a text without a certificate.
func Parse(text []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := text
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if strings.Contains(block.Type, "PRIVATE KEY") {
			return nil, ErrPrivateKey
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("a certificate does not parse: %w", err)
		}
		out = append(out, c)
	}
	// a key outside PEM armour, or a DER blob, is not guessed at; only the marker is looked for
	if bytes.Contains(text, []byte("PRIVATE KEY")) {
		return nil, ErrPrivateKey
	}
	if len(out) == 0 {
		return nil, ErrNoCertificate
	}
	return out, nil
}

func encode(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

// Store is the anchors file.
type Store struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

// Open returns the store in dir (the state directory); the file need not exist.
func Open(dir string) *Store {
	return &Store{path: filepath.Join(dir, FileName), now: time.Now}
}

type file struct {
	Anchors []Anchor `json:"anchors"`
}

func (s *Store) load() (file, error) {
	var f file
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return f, nil
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("%s: %w", s.path, err)
	}
	return f, nil
}

func (s *Store) save(f file) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func validPurpose(p string) bool {
	if p == "" || len(p) > 32 {
		return false
	}
	for _, r := range p {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// List is the anchors trusted for purpose ("" for all), described at now, oldest first.
func (s *Store) List(purpose string) ([]Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := []Info{}
	now := s.now()
	for _, a := range f.Anchors {
		if purpose != "" && !slices.Contains(a.Purposes, purpose) {
			continue
		}
		cs, err := Parse([]byte(a.PEM))
		if err != nil || len(cs) == 0 {
			continue
		}
		i := Describe(cs[0], now)
		i.Purposes, i.Added, i.AddedBy = a.Purposes, a.Added, a.AddedBy
		out = append(out, i)
	}
	return out, nil
}

// Add trusts every certificate of text for purpose. An expired certificate, or one not valid
// yet, refuses the text; a certificate already stored gains the purpose. by is who added it.
func (s *Store) Add(text []byte, purpose, by string) ([]Info, error) {
	if !validPurpose(purpose) {
		return nil, ErrPurpose
	}
	certs, err := Parse(text)
	if err != nil {
		return nil, err
	}
	now := s.now()
	for _, c := range certs {
		if now.After(c.NotAfter) {
			return nil, fmt.Errorf("%w: %s (until %s)", ErrExpired, c.Subject, c.NotAfter.Format(time.DateOnly))
		}
		if now.Before(c.NotBefore) {
			return nil, fmt.Errorf("%w: %s (from %s)", ErrNotYetValid, c.Subject, c.NotBefore.Format(time.DateOnly))
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	var added []Info
	for _, c := range certs {
		id := idOf(c)
		k := slices.IndexFunc(f.Anchors, func(a Anchor) bool { return a.ID == id })
		if k < 0 {
			f.Anchors = append(f.Anchors, Anchor{ID: id, Purposes: []string{purpose}, PEM: encode(c), Added: now.UTC(), AddedBy: by})
			k = len(f.Anchors) - 1
		} else if !slices.Contains(f.Anchors[k].Purposes, purpose) {
			f.Anchors[k].Purposes = append(f.Anchors[k].Purposes, purpose)
		}
		i := Describe(c, now)
		i.Purposes, i.Added, i.AddedBy = f.Anchors[k].Purposes, f.Anchors[k].Added, f.Anchors[k].AddedBy
		added = append(added, i)
	}
	if err := s.save(f); err != nil {
		return nil, err
	}
	return added, nil
}

// Remove takes purpose from the anchor id; an anchor left without a purpose is deleted.
func (s *Store) Remove(id, purpose string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	k := slices.IndexFunc(f.Anchors, func(a Anchor) bool { return a.ID == id && slices.Contains(a.Purposes, purpose) })
	if k < 0 {
		return ErrUnknown
	}
	a := &f.Anchors[k]
	a.Purposes = slices.DeleteFunc(a.Purposes, func(p string) bool { return p == purpose })
	if len(a.Purposes) == 0 {
		f.Anchors = slices.Delete(f.Anchors, k, k+1)
	}
	return s.save(f)
}

// Certificates is the parsed anchors of purpose.
func (s *Store) Certificates(purpose string) ([]*x509.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	var out []*x509.Certificate
	for _, a := range f.Anchors {
		if !slices.Contains(a.Purposes, purpose) {
			continue
		}
		if cs, err := Parse([]byte(a.PEM)); err == nil {
			out = append(out, cs...)
		}
	}
	return out, nil
}

// Pool is the system's pool plus the anchors of purpose; with none, the system's pool as it is
// (nil when the system has none, which crypto/tls reads as the system pool).
func (s *Store) Pool(purpose string) (*x509.CertPool, error) {
	extra, err := s.Certificates(purpose)
	if err != nil {
		return nil, err
	}
	return PoolWith(extra), nil
}

// PoolWith is the system's pool plus extra.
func PoolWith(extra []*x509.Certificate) *x509.CertPool {
	if len(extra) == 0 {
		return nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, c := range extra {
		pool.AddCert(c)
	}
	return pool
}
