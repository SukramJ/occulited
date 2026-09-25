package trust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

func TestParse(t *testing.T) {
	leaf, key, ca := testcert.Issued([]string{"auth.example.org"}, time.Now().Add(90*24*time.Hour))
	chain := append(append([]byte{}, leaf...), ca...)
	cases := []struct {
		name string
		text string
		n    int
		err  error
	}{
		{"a CA", string(ca), 1, nil},
		{"a chain", string(chain), 2, nil},
		{"a chain with text around it", "subject=CN=x\n" + string(chain) + "\ntrailing", 2, nil},
		{"a key alone", string(key), 0, ErrPrivateKey},
		{"a certificate and its key", string(leaf) + string(key), 0, ErrPrivateKey},
		{"a key announced outside PEM armour", string(ca) + "\nBEGIN RSA PRIVATE KEY\n", 0, ErrPrivateKey},
		{"nothing", "hello", 0, ErrNoCertificate},
		{"empty", "", 0, ErrNoCertificate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse([]byte(c.text))
			if !errors.Is(err, c.err) || len(got) != c.n {
				t.Fatalf("got %d, %v; want %d, %v", len(got), err, c.n, c.err)
			}
		})
	}
	if _, err := Parse([]byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n")); err == nil || errors.Is(err, ErrNoCertificate) {
		t.Errorf("a broken certificate: %v", err)
	}
}

func TestStoreAddListRemove(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir)
	now := time.Now()
	leaf, _, ca := testcert.Issued([]string{"auth.example.org"}, now.Add(90*24*time.Hour))
	if l, err := s.List(PurposeOIDC); err != nil || len(l) != 0 {
		t.Fatalf("empty store: %v %v", l, err)
	}
	added, err := s.Add(append(append([]byte{}, leaf...), ca...), PurposeOIDC, "admin")
	if err != nil || len(added) != 2 {
		t.Fatalf("add: %v %v", added, err)
	}
	if added[0].CA || !added[1].CA || added[1].Subject != "CN=Test CA" || added[0].AddedBy != "admin" {
		t.Errorf("described: %+v", added)
	}
	if len(added[0].Fingerprint) != 95 || !strings.Contains(added[0].Fingerprint, ":") {
		t.Errorf("fingerprint: %q", added[0].Fingerprint)
	}
	// the same text again: nothing doubles; another purpose is added to the same anchor
	if _, err := s.Add(ca, PurposeOIDC, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(ca, "acme", "admin"); err != nil {
		t.Fatal(err)
	}
	all, _ := s.List("")
	oidc, _ := s.List(PurposeOIDC)
	acme, _ := s.List("acme")
	if len(all) != 2 || len(oidc) != 2 || len(acme) != 1 || strings.Join(acme[0].Purposes, ",") != "oidc,acme" {
		t.Fatalf("lists: all %d, oidc %d, acme %+v", len(all), len(oidc), acme)
	}
	// the file is private and outside occulited.json
	st, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file: %v %v", st, err)
	}
	// removing the purpose oidc keeps the CA for acme, drops the leaf
	for _, i := range oidc {
		if err := s.Remove(i.ID, PurposeOIDC); err != nil {
			t.Fatal(err)
		}
	}
	oidc, _ = s.List(PurposeOIDC)
	all, _ = s.List("")
	if len(oidc) != 0 || len(all) != 1 || all[0].Purposes[0] != "acme" {
		t.Fatalf("after remove: oidc %v, all %v", oidc, all)
	}
	if err := s.Remove(all[0].ID, PurposeOIDC); !errors.Is(err, ErrUnknown) {
		t.Errorf("removing a purpose it does not have: %v", err)
	}
	if err := s.Remove("nope", "acme"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestStoreRefuses(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Now()
	_, key, _ := testcert.Issued([]string{"a"}, now.Add(time.Hour))
	if _, err := s.Add(key, PurposeOIDC, ""); !errors.Is(err, ErrPrivateKey) {
		t.Errorf("key: %v", err)
	}
	old, _ := testcert.SelfSigned([]string{"old"}, nil, now.Add(-time.Minute))
	if _, err := s.Add(old, PurposeOIDC, ""); !errors.Is(err, ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	ok, _ := testcert.SelfSigned([]string{"ok"}, nil, now.Add(time.Hour))
	for _, p := range []string{"", "OIDC", "a b", strings.Repeat("x", 40)} {
		if _, err := s.Add(ok, p, ""); !errors.Is(err, ErrPurpose) {
			t.Errorf("purpose %q: %v", p, err)
		}
	}
	s.now = func() time.Time { return now.Add(-2 * time.Hour) }
	if _, err := s.Add(ok, PurposeOIDC, ""); !errors.Is(err, ErrNotYetValid) {
		t.Errorf("not yet valid: %v", err)
	}
	if l, _ := s.List(""); len(l) != 0 {
		t.Errorf("a refused text stored something: %v", l)
	}
}

func TestDescribeExpiry(t *testing.T) {
	now := time.Now()
	soon, _ := testcert.SelfSigned([]string{"soon"}, nil, now.Add(10*24*time.Hour))
	cs, _ := Parse(soon)
	i := Describe(cs[0], now)
	if !i.ExpiresSoon || i.Expired || !i.SelfSigned || i.Names[0] != "soon" {
		t.Errorf("soon: %+v", i)
	}
	if i := Describe(cs[0], now.Add(20*24*time.Hour)); !i.Expired || i.ExpiresSoon {
		t.Errorf("later: %+v", i)
	}
	if i := Describe(cs[0], now.Add(-30*24*time.Hour)); i.ExpiresSoon {
		t.Errorf("long before: %+v", i)
	}
}

// issued is a leaf for name from a fresh CA that may sign (testcert's CA has no certSign usage,
// enough for the tests that only parse it, not for a verification)
func issued(t *testing.T, name string, notAfter time.Time) (leafPEM, caPEM []byte) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Lab CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter.Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

// Pool: the system's pool plus the purpose's anchors - a leaf signed by the added CA verifies,
// one signed by another CA does not, and another purpose's anchors are not in it.
func TestPool(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Now()
	leaf, ca := issued(t, "auth.example.org", now.Add(time.Hour))
	other, otherCA := issued(t, "auth.example.org", now.Add(time.Hour))
	if p, err := s.Pool(PurposeOIDC); err != nil || p != nil {
		t.Fatalf("empty: %v %v", p, err)
	}
	if _, err := s.Add(ca, PurposeOIDC, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(otherCA, "acme", ""); err != nil {
		t.Fatal(err)
	}
	pool, err := s.Pool(PurposeOIDC)
	if err != nil || pool == nil {
		t.Fatal(err)
	}
	verify := func(p []byte) error {
		cs, _ := Parse(p)
		_, err := cs[0].Verify(x509.VerifyOptions{Roots: pool, DNSName: "auth.example.org"})
		return err
	}
	if err := verify(leaf); err != nil {
		t.Errorf("the added CA's leaf: %v", err)
	}
	if err := verify(other); err == nil {
		t.Error("another purpose's CA verified a leaf for oidc")
	}
	// a server's own certificate, pinned: in the pool, it verifies itself
	pinned, _ := testcert.SelfSigned([]string{"idp.lan"}, nil, now.Add(time.Hour))
	if _, err := s.Add(pinned, PurposeOIDC, ""); err != nil {
		t.Fatal(err)
	}
	pool, _ = s.Pool(PurposeOIDC)
	cs, _ := Parse(pinned)
	if _, err := cs[0].Verify(x509.VerifyOptions{Roots: pool, DNSName: "idp.lan"}); err != nil {
		t.Errorf("pinned: %v", err)
	}
}

func TestFingerprintNormal(t *testing.T) {
	if NormalFingerprint(" ab:cd-EF 01 ") != "ABCDEF01" {
		t.Error(NormalFingerprint(" ab:cd-EF 01 "))
	}
}
