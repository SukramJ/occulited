package certpem

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

func TestParseInfo(t *testing.T) {
	now := time.Now()
	cert, key := testcert.SelfSigned([]string{"openccu", "openccu.lan"}, []string{"192.0.2.119"}, now.Add(3650*24*time.Hour))
	info, err := ParseInfo(append(append([]byte{}, cert...), key...), now)
	if err != nil {
		t.Fatal(err)
	}
	if !info.SelfSigned || info.DaysLeft != 3649 && info.DaysLeft != 3650 || len(info.Names) != 2 || info.IPs[0] != "192.0.2.119" || info.Chain != 1 {
		t.Fatalf("%+v", info)
	}
	if !strings.Contains(info.Subject, "CN=openccu") || len(strings.Split(info.Fingerprint, ":")) != 32 {
		t.Fatalf("%+v", info)
	}
	leaf, _, ca := testcert.Issued([]string{"box.example.org"}, now.Add(90*24*time.Hour))
	info, err = ParseInfo(append(leaf, ca...), now)
	if err != nil {
		t.Fatal(err)
	}
	if info.SelfSigned || info.DaysLeft != 89 || info.Chain != 2 || !strings.Contains(info.Issuer, "Test CA") {
		t.Fatalf("%+v", info)
	}
	// expired: negative days
	old, _, _ := testcert.Issued([]string{"old"}, now.Add(-2*time.Hour))
	info, _ = ParseInfo(old, now)
	if info.DaysLeft >= 0 {
		t.Fatalf("expired must be negative: %+v", info)
	}
	if _, err := ParseInfo([]byte("not pem"), now); err == nil {
		t.Fatal("garbage parsed")
	}
}

// selfSignedWith is a self-signed certificate whose subject, and so its issuer, is the given name.
func selfSignedWith(t *testing.T, subject pkix.Name) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: subject, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), DNSNames: []string{"box.example.org"}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// B-74: the pages showed the issuer's raw DN, the e-mail attribute hex-encoded. The common name
// and the organisation are what they show now; the DN stays for the tooltip.
func TestParseInfoIssuerName(t *testing.T) {
	email := []pkix.AttributeTypeAndValue{{Type: asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 1}, Value: "ca-admin@lan.example.org"}}
	tests := []struct {
		name            string
		issuer          pkix.Name
		wantCN, wantOrg string
		dnHas           string // the DN keeps what the parts leave out
	}{
		// the DN names the e-mail attribute by its OID; whether its value is hex (#0c18…, as on the
		// Charly) or plain depends on the Go the binary was built with, so only the OID is checked
		{"a step-ca issuer with an e-mail attribute", pkix.Name{CommonName: "step-ca.lan.example.org", Organization: []string{"Example LAN"}, ExtraNames: email}, "step-ca.lan.example.org", "Example LAN", "1.2.840.113549.1.9.1="},
		{"a common name only", pkix.Name{CommonName: "Test CA"}, "Test CA", "", "CN=Test CA"},
		{"an organisation only", pkix.Name{Organization: []string{"HomeMatic"}, Country: []string{"DE"}}, "", "HomeMatic", "C=DE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseInfo(selfSignedWith(t, tt.issuer), time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if info.IssuerCN != tt.wantCN || info.IssuerOrg != tt.wantOrg {
				t.Errorf("issuer_cn %q issuer_org %q, want %q and %q", info.IssuerCN, info.IssuerOrg, tt.wantCN, tt.wantOrg)
			}
			if !strings.Contains(info.Issuer, tt.dnHas) {
				t.Errorf("the DN %q lost %q", info.Issuer, tt.dnHas)
			}
		})
	}
	// an issued certificate names its CA, not itself
	leaf, _, ca := testcert.Issued([]string{"box.example.org"}, time.Now().Add(24*time.Hour))
	info, err := ParseInfo(append(leaf, ca...), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if info.IssuerCN != "Test CA" || info.IssuerOrg != "" {
		t.Errorf("issued: %+v", info)
	}
}

func TestAssembleAndValidate(t *testing.T) {
	now := time.Now()
	leaf, key, ca := testcert.Issued([]string{"box.example.org"}, now.Add(90*24*time.Hour))
	live, err := AssemblePEM(append(leaf, ca...), key)
	if err != nil {
		t.Fatal(err)
	}
	// the chain first, the key last, one PEM
	if !bytes.HasPrefix(live, []byte("-----BEGIN CERTIFICATE-----")) || bytes.Count(live, []byte("BEGIN CERTIFICATE")) != 2 || !bytes.Contains(live, []byte("BEGIN PRIVATE KEY")) {
		t.Fatalf("%s", live)
	}
	if bytes.Index(live, []byte("BEGIN PRIVATE KEY")) < bytes.LastIndex(live, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("the key must come after the chain")
	}
	if err := ValidLivePEM(live); err != nil {
		t.Fatal(err)
	}
	// the wrong key is refused, on both paths
	_, otherKey, _ := testcert.Issued([]string{"other"}, now.Add(time.Hour))
	if _, err := AssemblePEM(leaf, otherKey); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("wrong key: %v", err)
	}
	if err := ValidLivePEM(append(leaf, otherKey...)); err == nil {
		t.Fatal("wrong key accepted by ValidLivePEM")
	}
	// two keys, a foreign block, no key, no certificate
	if err := ValidLivePEM(append(append([]byte{}, live...), key...)); err == nil || !strings.Contains(err.Error(), "2 private keys") {
		t.Fatalf("two keys: %v", err)
	}
	if err := ValidLivePEM(append(append([]byte{}, live...), "-----BEGIN X-JUNK-----\nAAAA\n-----END X-JUNK-----\n"...)); err == nil || !strings.Contains(err.Error(), "X-JUNK") {
		t.Fatalf("junk block: %v", err)
	}
	if err := ValidLivePEM(leaf); err == nil {
		t.Fatal("certificate without a key accepted")
	}
	if err := ValidLivePEM(key); err == nil {
		t.Fatal("key without a certificate accepted")
	}
	// the certificate half only
	only := CertificatesOnly(live)
	if bytes.Contains(only, []byte("PRIVATE KEY")) || bytes.Count(only, []byte("BEGIN CERTIFICATE")) != 2 {
		t.Fatalf("%s", only)
	}
	if len(CertificatesOnly([]byte("nothing"))) != 0 {
		t.Fatal("garbage yields certificates")
	}
	// the self-signed EC key S50lighttpd writes (SEC 1) is read as well
	cert, ecKey := testcert.SelfSigned([]string{"openccu"}, nil, now.Add(time.Hour))
	if err := ValidLivePEM(append(cert, ecKey...)); err != nil {
		t.Fatal(err)
	}
}
