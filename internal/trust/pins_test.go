package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"
)

// openccu-lite task 232: pins per purpose - the SPKI of a certificate matches whichever
// certificate of the chain carries the key; with-ca runs the CA check as well, only lets the pin
// alone vouch; a chain no pin matches is a loud mismatch, recorded for the page; a backup pin
// takes over at the key rollover; Re-pin replaces the stale pins.

// selfSigned is a server certificate of its own for host, with a fresh key.
func selfSigned(t *testing.T, host string) leafCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: host}, DNSNames: []string{host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	must(t, err)
	kder, _ := x509.MarshalECPrivateKey(key)
	return leafCA{leaf: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), ca: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issuer is a CA that issues localhost leaves, each with a key of its own (a key rollover
// under the same CA).
type issuer struct {
	ca  *x509.Certificate
	key *ecdsa.PrivateKey
	pem []byte
}

func newIssuer(t *testing.T, name string) *issuer {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: name, Organization: []string{"Lab"}}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(400 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	must(t, err)
	ca, _ := x509.ParseCertificate(der)
	return &issuer{ca: ca, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (i *issuer) leaf(t *testing.T) leafCA {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	n, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, i.ca, &key.PublicKey, i.key)
	must(t, err)
	kder, _ := x509.MarshalECPrivateKey(key)
	return leafCA{leaf: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), ca: i.pem}
}

func certOf(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	cs, err := Parse(b)
	must(t, err)
	return cs[0]
}

func TestSPKIAndNormal(t *testing.T) {
	c := certOf(t, newCA(t, "X").ca)
	spki := SPKI(c)
	if len(spki) != 44 || NormalSPKI(spki) != spki || NormalSPKI("sha256//"+spki) != spki {
		t.Fatalf("spki %q", spki)
	}
	// hex, with and without colons, lower case, comes back as the same base64
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i * 7)
	}
	var colon strings.Builder
	for i, b := range raw {
		if i > 0 {
			colon.WriteByte(':')
		}
		colon.WriteString(strings.ToUpper(hex2(b)))
	}
	want := NormalSPKI(colon.String())
	if want == "" || NormalSPKI(strings.ToLower(strings.ReplaceAll(colon.String(), ":", ""))) != want {
		t.Errorf("hex forms: %q", want)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("A", 43), strings.Repeat("0", 63)} {
		if NormalSPKI(bad) != "" {
			t.Errorf("%q accepted", bad)
		}
	}
	// Describe carries it
	if Describe(c, time.Now()).SPKI != spki {
		t.Error("Describe has no SPKI")
	}
}

func hex2(b byte) string {
	const d = "0123456789abcdef"
	return string([]byte{d[b>>4], d[b&15]})
}

func TestPinsStore(t *testing.T) {
	s, _, cas := treeWithLeaves(t)
	leafA := certOf(t, cas["A"].leaf)
	// only the two purposes take pins
	for _, bad := range []string{StoreSystem, StoreOcculited, "downloads"} {
		if _, err := s.AddPin(bad, PinRequest{Cert: leafA}); !errors.Is(err, ErrPinPurpose) {
			t.Errorf("%s: %v", bad, err)
		}
		if _, err := s.Pins(bad); !errors.Is(err, ErrPinPurpose) {
			t.Errorf("%s list: %v", bad, err)
		}
	}
	if _, err := s.AddPin(PurposeOIDC, PinRequest{Cert: leafA, Mode: "sometimes"}); !errors.Is(err, ErrPinMode) {
		t.Errorf("mode: %v", err)
	}
	if _, err := s.AddPin(PurposeOIDC, PinRequest{SPKI: "not a hash"}); !errors.Is(err, ErrPinSPKI) {
		t.Errorf("spki: %v", err)
	}
	changes := 0
	s.OnChange(func() { changes++ })
	p, err := s.AddPin(PurposeOIDC, PinRequest{Cert: leafA, By: "admin"})
	must(t, err)
	if p.Mode != ModeWithCA || p.SPKI != SPKI(leafA) || p.Subject != leafA.Subject.String() || p.Fingerprint != Fingerprint(leafA) || p.AddedBy != "admin" || p.ID == "" || p.NotAfter.IsZero() {
		t.Fatalf("pin: %+v", p)
	}
	if _, err := s.AddPin(PurposeOIDC, PinRequest{Cert: leafA}); !errors.Is(err, ErrPinPresent) {
		t.Errorf("twice: %v", err)
	}
	// a bare hash: a backup pin, no subject; the same key for ACME is a pin of its own
	b, err := s.AddPin(PurposeOIDC, PinRequest{SPKI: SPKI(certOf(t, cas["B"].leaf)), Mode: ModeOnly})
	must(t, err)
	if b.Subject != "" || b.Mode != ModeOnly || !b.NotAfter.IsZero() {
		t.Errorf("backup: %+v", b)
	}
	if _, err := s.AddPin(PurposeACME, PinRequest{Cert: leafA}); err != nil {
		t.Fatal(err)
	}
	pins, _ := s.Pins(PurposeOIDC)
	acme, _ := s.Pins(PurposeACME)
	if len(pins) != 2 || len(acme) != 1 || pins[0].ID != p.ID || pins[1].ID != b.ID {
		t.Fatalf("lists: %+v %+v", pins, acme)
	}
	v, _ := s.View()
	for _, sv := range v.Stores {
		switch sv.ID {
		case PurposeOIDC:
			if len(sv.Pins) != 2 {
				t.Errorf("view oidc pins: %+v", sv.Pins)
			}
		case PurposeACME:
			if len(sv.Pins) != 1 {
				t.Errorf("view acme pins: %+v", sv.Pins)
			}
		default:
			if sv.Pins != nil {
				t.Errorf("%s has pins", sv.ID)
			}
		}
	}
	if changes != 3 {
		t.Errorf("OnChange ran %d times", changes)
	}
	// remove one; an unknown id is ErrUnknown; the other purpose keeps its pin
	must(t, s.RemovePin(PurposeOIDC, b.ID))
	if err := s.RemovePin(PurposeOIDC, b.ID); !errors.Is(err, ErrUnknown) {
		t.Errorf("remove twice: %v", err)
	}
	pins, _ = s.Pins(PurposeOIDC)
	acme, _ = s.Pins(PurposeACME)
	if len(pins) != 1 || len(acme) != 1 {
		t.Fatalf("after remove: %+v %+v", pins, acme)
	}
	// replace: the purpose's pins are the new one alone
	r, err := s.AddPin(PurposeOIDC, PinRequest{SPKI: SPKI(certOf(t, cas["C"].leaf)), Replace: true})
	must(t, err)
	pins, _ = s.Pins(PurposeOIDC)
	if len(pins) != 1 || pins[0].ID != r.ID {
		t.Fatalf("replace: %+v", pins)
	}
	// the file survives a reopen
	s2 := Open(s.path[:len(s.path)-len(FileName)-1])
	pins, _ = s2.Pins(PurposeOIDC)
	if len(pins) != 1 || pins[0].SPKI != r.SPKI {
		t.Fatalf("reopened: %+v", pins)
	}
}

// TestVerifier: the pin modes against chains under CA A (in the pool), under C (not), and
// self-signed.
func TestVerifier(t *testing.T) {
	s, _, cas := treeWithLeaves(t)
	pool, _ := s.PoolFor(PurposeACME) // occulited's base set: A
	underA := []*x509.Certificate{certOf(t, cas["A"].leaf), certOf(t, cas["A"].ca)}
	underC := []*x509.Certificate{certOf(t, cas["C"].leaf), certOf(t, cas["C"].ca)}
	self := []*x509.Certificate{certOf(t, selfSigned(t, "localhost").leaf)}
	pinOf := func(c *x509.Certificate, mode string) Pin { return Pin{ID: pinID(SPKI(c)), SPKI: SPKI(c), Mode: mode} }
	var pm *PinMismatchError
	cases := []struct {
		name    string
		pins    []Pin
		chain   []*x509.Certificate
		host    string
		ok      bool
		pinOnly bool
		pinned  bool
		mism    bool
	}{
		{"no pins, under A", nil, underA, "localhost", true, false, false, false},
		{"no pins, under C", nil, underC, "localhost", false, false, false, false},
		{"leaf pinned with-ca, under A", []Pin{pinOf(underA[0], ModeWithCA)}, underA, "localhost", true, false, true, false},
		{"CA pinned with-ca, under A", []Pin{pinOf(underA[1], ModeWithCA)}, underA, "localhost", true, false, true, false},
		{"leaf pinned with-ca, wrong name", []Pin{pinOf(underA[0], ModeWithCA)}, underA, "other.example", false, false, false, false},
		{"leaf pinned only, wrong name passes", []Pin{pinOf(underA[0], ModeOnly)}, underA, "other.example", true, true, true, false},
		{"pinned with-ca, under C: the CA check fails", []Pin{pinOf(underC[0], ModeWithCA)}, underC, "localhost", false, false, false, false},
		{"pinned only, under C: the pin vouches", []Pin{pinOf(underC[0], ModeOnly)}, underC, "localhost", true, true, true, false},
		{"self-signed pinned only", []Pin{pinOf(self[0], ModeOnly)}, self, "localhost", true, true, true, false},
		{"self-signed pinned with-ca: no root", []Pin{pinOf(self[0], ModeWithCA)}, self, "localhost", false, false, false, false},
		{"another key pinned: mismatch", []Pin{pinOf(underC[0], ModeOnly)}, underA, "localhost", false, false, false, true},
		{"backup pin matches", []Pin{pinOf(underC[0], ModeWithCA), pinOf(underA[0], ModeWithCA)}, underA, "localhost", true, false, true, false},
		{"only wins over with-ca on the same chain", []Pin{pinOf(underC[0], ModeWithCA), pinOf(underC[1], ModeOnly)}, underC, "localhost", true, true, true, false},
		{"no certificate, pins", []Pin{pinOf(underA[0], ModeWithCA)}, nil, "localhost", false, false, false, true},
		{"no certificate, no pins", nil, nil, "localhost", false, false, false, false},
	}
	for _, c := range cases {
		v := Verifier{Purpose: PurposeACME, Roots: pool, Pins: c.pins}
		o, err := v.Verify(c.host, c.chain)
		if (err == nil) != c.ok {
			t.Errorf("%s: ok %v, err %v", c.name, err == nil, err)
			continue
		}
		if errors.As(err, &pm) != c.mism {
			t.Errorf("%s: mismatch error %v (%v)", c.name, errors.As(err, &pm), err)
		}
		if c.ok && (o.PinOnly != c.pinOnly || (o.Pin != nil) != c.pinned || len(o.Chain) == 0) {
			t.Errorf("%s: outcome %+v", c.name, o)
		}
		if c.ok && !c.pinOnly && c.pins == nil && !o.Chain[len(o.Chain)-1].Equal(underA[1]) {
			t.Errorf("%s: the verified chain does not end in A", c.name)
		}
		if v.Pinned() != (len(c.pins) > 0) {
			t.Errorf("%s: Pinned", c.name)
		}
	}
	// the mismatch error names the host, the presented key and the certificate
	_, err := Verifier{Purpose: PurposeOIDC, Roots: pool, Pins: []Pin{pinOf(underC[0], ModeWithCA)}}.Verify("idp.example", underA)
	if !errors.As(err, &pm) || pm.Host != "idp.example" || pm.Purpose != PurposeOIDC || pm.Pins != 1 || !strings.Contains(err.Error(), SPKI(underA[0])) || !strings.Contains(err.Error(), Fingerprint(underA[0])) {
		t.Errorf("mismatch error: %v", err)
	}
	// a CA failure under pins keeps the chain for the record
	var cve *tls.CertificateVerificationError
	_, err = Verifier{Purpose: PurposeOIDC, Roots: pool, Pins: []Pin{pinOf(underC[0], ModeWithCA)}}.Verify("localhost", underC)
	if !errors.As(err, &cve) || len(cve.UnverifiedCertificates) != 2 {
		t.Errorf("CA failure under a pin: %v", err)
	}
}

// TestTransportPins: the ACME client with pins - with-ca passes under a trusted CA and fails
// loudly on another key (recorded, the Status page's warning), only passes a self-signed server,
// the backup pin takes over at the rollover, Re-pin replaces the stale pin and clears the record.
func TestTransportPins(t *testing.T) {
	s, _, cas := treeWithLeaves(t)
	// a CA of this test's own in the ACME store, so it can issue a second leaf under the same CA
	ca := newIssuer(t, "Test CA P")
	if _, err := s.AddTo(context.Background(), PurposeACME, ca.pem, "admin", OriginPage); err != nil {
		t.Fatal(err)
	}
	first := ca.leaf(t)
	srvA := tlsServer(t, first)
	defer srvA.Close()
	leafA := certOf(t, first.leaf)
	client := s.HTTPClient(PurposeACME, 5*time.Second)
	ok := func(c *http.Client, url string) error {
		res, err := c.Get(url)
		if err != nil {
			return err
		}
		res.Body.Close()
		if res.StatusCode != 204 {
			t.Fatalf("status %d", res.StatusCode)
		}
		return nil
	}
	// no pins: the CA check alone
	must(t, ok(client, srvA.URL))
	// the leaf pinned with-ca: passes, and the test under the same client once the store changed
	_, err := s.AddPin(PurposeACME, PinRequest{Cert: leafA})
	must(t, err)
	must(t, ok(client, srvA.URL))
	// another server under the same CA, another key: a loud mismatch, recorded once per host
	other := ca.leaf(t)
	srvOther := tlsServer(t, other)
	defer srvOther.Close()
	err = ok(client, srvOther.URL)
	var pm *PinMismatchError
	if !errors.As(err, &pm) {
		t.Fatalf("the other key passed or failed otherwise: %v", err)
	}
	_ = ok(client, srvOther.URL)
	fails := s.PinFailures()
	if len(fails) != 1 || fails[0].Purpose != PurposeACME || fails[0].Host != "localhost" || len(fails[0].Chain) != 1 || fails[0].Chain[0].SPKI != SPKI(certOf(t, other.leaf)) {
		t.Fatalf("pin failures: %+v", fails)
	}
	if len(s.Failures()) != 0 {
		t.Errorf("a pin mismatch was recorded as a CA failure: %+v", s.Failures())
	}
	v, _ := s.View()
	if len(v.PinFailures) != 1 {
		t.Errorf("view: %+v", v.PinFailures)
	}
	// Re-pin: the presented key replaces the stale pin; the record goes; the call passes
	_, err = s.AddPin(PurposeACME, PinRequest{Cert: certOf(t, other.leaf), Replace: true})
	must(t, err)
	if len(s.PinFailures()) != 0 {
		t.Fatalf("the record stays after the re-pin: %+v", s.PinFailures())
	}
	must(t, ok(client, srvOther.URL))
	// a pin under a CA the store lacks (C), with-ca: the CA check fails and is recorded as such
	srvC := tlsServer(t, cas["C"])
	defer srvC.Close()
	leafC := certOf(t, cas["C"].leaf)
	_, err = s.AddPin(PurposeACME, PinRequest{Cert: leafC, Replace: true})
	must(t, err)
	err = ok(client, srvC.URL)
	var ua x509.UnknownAuthorityError
	if !errors.As(err, &ua) {
		t.Fatalf("under C with a with-ca pin: %v", err)
	}
	if fs := s.Failures(); len(fs) != 1 || fs[0].Store != PurposeACME || fs[0].Candidate != nil {
		t.Fatalf("the CA failure under a pin: %+v", fs)
	}
	// the same pin as only: the pin vouches, the record clears
	must(t, s.RemovePin(PurposeACME, pinID(SPKI(leafC))))
	_, err = s.AddPin(PurposeACME, PinRequest{Cert: leafC, Mode: ModeOnly})
	must(t, err)
	must(t, ok(client, srvC.URL))
	if len(s.Failures()) != 0 || len(s.PinFailures()) != 0 {
		t.Errorf("records after the pass: %+v %+v", s.Failures(), s.PinFailures())
	}
	// a self-signed server: only its pin lets it through
	self := selfSigned(t, "localhost")
	srvSelf := tlsServer(t, self)
	defer srvSelf.Close()
	if err := ok(client, srvSelf.URL); !errors.As(err, &pm) {
		t.Fatalf("self-signed without its pin: %v", err)
	}
	_, err = s.AddPin(PurposeACME, PinRequest{Cert: certOf(t, self.leaf), Mode: ModeOnly})
	must(t, err)
	must(t, ok(client, srvSelf.URL))
	// the rollover: the backup pin (a bare hash of the next key) is in place before the server
	// switches, so the switch passes; the old pin is removed afterwards
	next := selfSigned(t, "localhost")
	_, err = s.AddPin(PurposeACME, PinRequest{SPKI: SPKI(certOf(t, next.leaf)), Mode: ModeOnly})
	must(t, err)
	srvNext := tlsServer(t, next)
	defer srvNext.Close()
	must(t, ok(client, srvNext.URL))
	must(t, s.RemovePin(PurposeACME, pinID(SPKI(certOf(t, self.leaf)))))
	if err := ok(client, srvSelf.URL); !errors.As(err, &pm) {
		t.Errorf("the old key after its pin went: %v", err)
	}
	// without pins the mismatch records go
	pins, _ := s.Pins(PurposeACME)
	for _, p := range pins {
		must(t, s.RemovePin(PurposeACME, p.ID))
	}
	if len(s.PinFailures()) != 0 {
		t.Errorf("records without pins: %+v", s.PinFailures())
	}
	// a quiet client records nothing
	_, err = s.AddPin(PurposeACME, PinRequest{Cert: leafA})
	must(t, err)
	if err := ok(s.QuietClient(PurposeACME, 5*time.Second), srvOther.URL); !errors.As(err, &pm) || len(s.PinFailures()) != 0 {
		t.Errorf("quiet: %v %+v", err, s.PinFailures())
	}
	// the OIDC purpose is independent: its pool is the system's (B is in the bundle, this test's
	// CA is not), and the ACME pins do not apply to it
	srvB := tlsServer(t, cas["B"])
	defer srvB.Close()
	must(t, ok(s.HTTPClient(PurposeOIDC, 5*time.Second), srvB.URL))
	if err := ok(s.HTTPClient(PurposeOIDC, 5*time.Second), srvOther.URL); err == nil || errors.As(err, &pm) {
		t.Errorf("OIDC under the ACME-only CA: %v", err)
	}
}

func TestPeerChain(t *testing.T) {
	_, _, cas := treeWithLeaves(t)
	srv := tlsServer(t, cas["C"])
	defer srv.Close()
	ctx := context.Background()
	host, chain, err := PeerChain(ctx, srv.URL)
	if err != nil || host != "localhost" || len(chain) == 0 || !chain[0].Equal(certOf(t, cas["C"].leaf)) {
		t.Fatalf("peer chain: %s %v %v", host, chain, err)
	}
	if _, _, err := PeerChain(ctx, "http://example.org"); err == nil {
		t.Error("a plain http address has no chain")
	}
	if _, _, err := PeerChain(ctx, "not a url"); err == nil {
		t.Error("not a URL")
	}
	srv.Close()
	if _, _, err := PeerChain(ctx, srv.URL); err == nil {
		t.Error("a closed server")
	}
}
