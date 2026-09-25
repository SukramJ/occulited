package certpem

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

func derOf(t *testing.T, pemBytes []byte) []byte {
	t.Helper()
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("not PEM")
	}
	return block.Bytes
}

// TestNormalize: DER and PEM certificates and keys in every encoding become the one PEM the
// package works with; an encrypted key is refused by name.
func TestNormalize(t *testing.T) {
	now := time.Now()
	leaf, key, ca := testcert.Issued([]string{"box.example.org"}, now.Add(90*24*time.Hour))
	if IsDER(leaf) || !IsDER(derOf(t, leaf)) || IsDER([]byte("  \n-----BEGIN")) || !IsDER(append([]byte("\n\n"), derOf(t, leaf)...)) {
		t.Fatal("IsDER")
	}
	// a PEM stays a PEM; the chain keeps its order
	out, err := NormalizeCertificates(append(leaf, ca...))
	if err != nil || strings.Count(string(out), "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("%v %s", err, out)
	}
	// one DER certificate, and two back to back
	out, err = NormalizeCertificates(derOf(t, leaf))
	if err != nil || string(out) != string(leaf) {
		t.Fatalf("%v %s", err, out)
	}
	out, err = NormalizeCertificates(append(derOf(t, leaf), derOf(t, ca)...))
	if err != nil || strings.Count(string(out), "BEGIN CERTIFICATE") != 2 {
		t.Fatalf("%v %s", err, out)
	}
	if _, err := NormalizeCertificates([]byte{0x30, 0x03, 0x02, 0x01, 0x01}); err == nil {
		t.Fatal("garbage DER parsed")
	}
	if _, err := NormalizeCertificates([]byte("   ")); err == nil {
		t.Fatal("empty parsed")
	}
	if _, err := NormalizeCertificates(key); err == nil {
		t.Fatal("a key is not a certificate")
	}
	// keys: SEC 1 PEM (what testcert writes), PKCS#8 PEM, PKCS#1 PEM, and each as DER
	sec1, err := ParsePrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8, _ := EncodeKey(sec1)
	rsaKey, _ := GenerateKey(AlgRSA2048)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey.(*rsa.PrivateKey))})
	for name, in := range map[string][]byte{
		"sec1 pem": key, "pkcs8 pem": pkcs8, "pkcs1 pem": pkcs1,
		"sec1 der": derOf(t, key), "pkcs8 der": derOf(t, pkcs8), "pkcs1 der": derOf(t, pkcs1),
	} {
		out, err := NormalizeKey(in)
		if err != nil || !strings.HasPrefix(string(out), "-----BEGIN PRIVATE KEY-----") {
			t.Fatalf("%s: %v %s", name, err, out)
		}
		k, err := ParsePrivateKey(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.HasPrefix(name, "pkcs1") {
			if _, ok := k.(*rsa.PrivateKey); !ok {
				t.Fatalf("%s: %T", name, k)
			}
		} else if !KeyMatches(mustCert(t, leaf), k) {
			t.Fatalf("%s: the key no longer matches", name)
		}
	}
	// encrypted: PKCS#8's block type, and the legacy header
	enc := "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB\n-----END ENCRYPTED PRIVATE KEY-----\n"
	if _, err := NormalizeKey([]byte(enc)); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("pkcs8 encrypted: %v", err)
	}
	legacy := "-----BEGIN EC PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-256-CBC,00\n\nMIIB\n-----END EC PRIVATE KEY-----\n"
	if _, err := NormalizeKey([]byte(legacy)); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("legacy encrypted: %v", err)
	}
	if _, err := NormalizeKey([]byte{0x30, 0x01, 0x00}); err == nil {
		t.Fatal("garbage DER key parsed")
	}
	if _, err := NormalizeKey(leaf); err == nil {
		t.Fatal("a certificate is not a key")
	}
	// the key's description and fingerprint: the same value as the certificate's public key
	info := DescribeKey(sec1)
	if info.Algorithm != "P-256" || len(strings.Split(info.Fingerprint, ":")) != 32 {
		t.Fatalf("%+v", info)
	}
	if fp, _ := PublicKeyFingerprint(mustCert(t, leaf).PublicKey); fp != info.Fingerprint {
		t.Fatalf("%s != %s", fp, info.Fingerprint)
	}
	if DescribeKey(rsaKey).Algorithm != "RSA-2048" {
		t.Fatalf("%+v", DescribeKey(rsaKey))
	}
	if _, otherKey, _ := testcert.Issued([]string{"other"}, now.Add(time.Hour)); KeyMatches(mustCert(t, leaf), mustKey(t, otherKey)) {
		t.Fatal("the wrong key matched")
	}
}

func mustCert(t *testing.T, b []byte) *x509.Certificate {
	t.Helper()
	certs, err := ParseCertificates(b)
	if err != nil {
		t.Fatal(err)
	}
	return certs[0]
}

func mustKey(t *testing.T, b []byte) crypto.Signer {
	t.Helper()
	k, err := ParsePrivateKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestValidityAndChain: the dates in the refusal, the chain reordered leaf-first, the warning
// for a missing intermediate and its absence when the issuer is there.
func TestValidityAndChain(t *testing.T) {
	now := time.Now()
	leaf, _, ca := testcert.Issued([]string{"box.example.org"}, now.Add(90*24*time.Hour))
	if err := CheckValidity(mustCert(t, leaf), now); err != nil {
		t.Fatal(err)
	}
	old, _, _ := testcert.Issued([]string{"old"}, now.Add(-2*time.Hour))
	if err := CheckValidity(mustCert(t, old), now); err == nil || !strings.Contains(err.Error(), "expired on") {
		t.Fatalf("expired: %v", err)
	}
	if err := CheckValidity(mustCert(t, leaf), now.Add(-48*time.Hour)); err == nil || !strings.Contains(err.Error(), "not valid before") {
		t.Fatalf("not yet valid: %v", err)
	}
	// root first is turned around; the leaf alone warns; leaf plus issuer does not
	certs, _ := ParseCertificates(append(ca, leaf...))
	ordered := Order(certs)
	if ordered[0].Subject.CommonName != "box.example.org" || ordered[1].Subject.CommonName != "Test CA" {
		t.Fatalf("%v", []string{ordered[0].Subject.CommonName, ordered[1].Subject.CommonName})
	}
	if w := ChainWarning(ordered); w != "" {
		t.Fatalf("complete chain warned: %s", w)
	}
	if w := ChainWarning(ordered[:1]); !strings.Contains(w, "not in the chain") || !strings.Contains(w, "box.example.org") {
		t.Fatalf("missing issuer: %q", w)
	}
	self, _ := testcert.SelfSigned([]string{"openccu"}, nil, now.Add(time.Hour))
	if w := ChainWarning([]*x509.Certificate{mustCert(t, self)}); w != "" {
		t.Fatalf("self-signed warned: %s", w)
	}
	if got := Order([]*x509.Certificate{mustCert(t, leaf)}); len(got) != 1 {
		t.Fatal("one certificate reordered")
	}
}

// TestCSR: every algorithm, the request parsed back with its names, an address as an IP SAN,
// the organisation, and the key that made it.
func TestCSR(t *testing.T) {
	for _, alg := range []string{"", AlgP256, AlgP384, AlgRSA2048} {
		k, err := GenerateKey(alg)
		if err != nil {
			t.Fatal(err)
		}
		csrPEM, err := CSR(k, " CCU.Example.org ", []string{"ccu.lan", "192.0.2.119", "ccu.example.org", ""}, "Home")
		if err != nil {
			t.Fatal(err)
		}
		csr, err := ParseCSR(csrPEM)
		if err != nil {
			t.Fatalf("%s: %v", alg, err)
		}
		if csr.Subject.CommonName != "ccu.example.org" || strings.Join(csr.Subject.Organization, ",") != "Home" {
			t.Fatalf("%s: %v", alg, csr.Subject)
		}
		if strings.Join(csr.DNSNames, ",") != "ccu.example.org,ccu.lan" || len(csr.IPAddresses) != 1 || csr.IPAddresses[0].String() != "192.0.2.119" {
			t.Fatalf("%s: %v %v", alg, csr.DNSNames, csr.IPAddresses)
		}
		switch alg {
		case AlgRSA2048:
			if _, ok := csr.PublicKey.(*rsa.PublicKey); !ok {
				t.Fatalf("%s: %T", alg, csr.PublicKey)
			}
		default:
			p, ok := csr.PublicKey.(*ecdsa.PublicKey)
			if !ok || (alg == AlgP384 && p.Curve.Params().Name != "P-384") || (alg != AlgP384 && p.Curve.Params().Name != "P-256") {
				t.Fatalf("%s: %T %v", alg, csr.PublicKey, ok)
			}
		}
		fp, _ := PublicKeyFingerprint(csr.PublicKey)
		if fp != DescribeKey(k).Fingerprint {
			t.Fatal("the request carries another key")
		}
	}
	if _, err := GenerateKey("dsa"); err == nil {
		t.Fatal("unknown algorithm accepted")
	}
	k, _ := GenerateKey("")
	if _, err := CSR(k, " ", nil, ""); err == nil {
		t.Fatal("empty CN accepted")
	}
	if _, err := ParseCSR([]byte("nope")); err == nil {
		t.Fatal("garbage parsed as a request")
	}
}
