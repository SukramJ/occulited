package certpem

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Task 38: the manual mode brings certificates and keys from anywhere - a company CA, a bought
// one, a Windows CA that hands out DER - and a key and CSR made on the box. What is below turns
// any of that into the PEM the rest of the package works with, says what is wrong with it in
// words the page can show, and makes the key and the request.

// ErrEncrypted is a private key with a passphrase. There is no password field on purpose: the
// box would have to store it, and the file it writes is unencrypted anyway.
var ErrEncrypted = errors.New("the private key is encrypted - decrypt it first (openssl pkey -in key.pem -out key-plain.pem) and upload the plain key")

// IsDER says whether b is DER rather than PEM: the first non-blank byte is the SEQUENCE tag
// (0x30) every certificate, request and key encoding starts with. A PEM starts with a dash.
func IsDER(b []byte) bool {
	b = bytes.TrimLeft(b, " \t\r\n")
	return len(b) > 0 && b[0] == 0x30
}

// NormalizeCertificates takes certificates as PEM (any number of CERTIFICATE blocks; other
// block types are ignored) or as DER (one certificate, or several back to back, as some CAs
// export a chain) and returns them as PEM, in the order given.
func NormalizeCertificates(b []byte) ([]byte, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("empty")
	}
	if !IsDER(b) {
		certs, err := ParseCertificates(b)
		if err != nil {
			return nil, err
		}
		return EncodeCertificates(certs), nil
	}
	var certs []*x509.Certificate
	// leading blanks only: a DER's last byte can be one of TrimSpace's characters
	rest := bytes.TrimLeft(b, " \t\r\n")
	for len(rest) > 0 {
		n := derLen(rest)
		if n <= 0 || n > len(rest) {
			if len(certs) == 0 {
				return nil, errors.New("not a DER certificate")
			}
			break
		}
		c, err := x509.ParseCertificate(rest[:n])
		if err != nil {
			if len(certs) == 0 {
				return nil, fmt.Errorf("not a DER certificate: %w", err)
			}
			return nil, fmt.Errorf("certificate %d of the DER: %w", len(certs)+1, err)
		}
		certs = append(certs, c)
		rest = bytes.TrimLeft(rest[n:], " \t\r\n")
	}
	return EncodeCertificates(certs), nil
}

// derLen is the length of the first DER element of b (tag, length bytes and content), or -1
// when the header does not parse - what splits certificates exported back to back.
func derLen(b []byte) int {
	if len(b) < 2 {
		return -1
	}
	if b[1] < 0x80 {
		return 2 + int(b[1])
	}
	n := int(b[1] & 0x7f)
	if n == 0 || n > 4 || len(b) < 2+n {
		return -1
	}
	l := 0
	for _, x := range b[2 : 2+n] {
		l = l<<8 | int(x)
	}
	return 2 + n + l
}

// EncodeCertificates is the PEM of certs, in order.
func EncodeCertificates(certs []*x509.Certificate) []byte {
	var out bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	return out.Bytes()
}

// NormalizeKey takes a private key as PEM (PKCS#8, PKCS#1 or SEC 1, encrypted ones refused
// with ErrEncrypted) or as DER in any of the three encodings and returns it as PKCS#8 PEM.
func NormalizeKey(b []byte) ([]byte, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("empty")
	}
	var k crypto.Signer
	var err error
	if IsDER(b) {
		k, err = parseDERKey(bytes.TrimLeft(b, " \t\r\n"))
	} else {
		k, err = ParsePrivateKey(b)
	}
	if err != nil {
		return nil, err
	}
	return EncodeKey(k)
}

func parseDERKey(der []byte) (crypto.Signer, error) {
	if k, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		s, ok := k.(crypto.Signer)
		if !ok {
			return nil, errors.New("unsupported key type")
		}
		return s, nil
	}
	if k, err := x509.ParseECPrivateKey(der); err == nil {
		return k, nil
	}
	if k, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return k, nil
	}
	return nil, errors.New("not a DER private key (PKCS#8, PKCS#1 or SEC 1)")
}

// EncodeKey is the PKCS#8 PEM of k.
func EncodeKey(k crypto.Signer) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// KeyMatches says whether k is the private half of c's public key.
func KeyMatches(c *x509.Certificate, k crypto.Signer) bool { return keyMatches(c, k) }

// KeyInfo describes a private key for the page: its algorithm and the fingerprint of its public
// key, which is what a certificate made for it carries as well.
type KeyInfo struct {
	Algorithm   string `json:"algorithm"`
	Fingerprint string `json:"fingerprint"`
}

// DescribeKey names the algorithm (P-256, P-384, RSA-2048, Ed25519) and fingerprints the key.
func DescribeKey(k crypto.Signer) KeyInfo {
	info := KeyInfo{Algorithm: "unknown"}
	switch p := k.Public().(type) {
	case *ecdsa.PublicKey:
		info.Algorithm = p.Curve.Params().Name
	case *rsa.PublicKey:
		info.Algorithm = fmt.Sprintf("RSA-%d", p.N.BitLen())
	case ed25519.PublicKey:
		info.Algorithm = "Ed25519"
	}
	info.Fingerprint, _ = PublicKeyFingerprint(k.Public())
	return info
}

// PublicKeyFingerprint is the SHA-256 of the SubjectPublicKeyInfo DER, colon-separated - the
// same for the key and for every certificate issued to it, so a mismatch can be shown as two
// values.
func PublicKeyFingerprint(pub crypto.PublicKey) (string, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return "", err
	}
	return colonHex(sha256.Sum256(der)), nil
}

func colonHex(sum [32]byte) string {
	out := make([]byte, 0, len(sum)*3)
	for i, b := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, fmt.Sprintf("%02X", b)...)
	}
	return string(out)
}

// CheckValidity refuses a certificate that is expired or not yet valid, with its dates.
func CheckValidity(c *x509.Certificate, now time.Time) error {
	const f = "2006-01-02 15:04 MST"
	if now.After(c.NotAfter) {
		return fmt.Errorf("the certificate expired on %s (valid from %s)", c.NotAfter.Format(f), c.NotBefore.Format(f))
	}
	if now.Before(c.NotBefore) {
		return fmt.Errorf("the certificate is not valid before %s (until %s); the system's clock says %s", c.NotBefore.Format(f), c.NotAfter.Format(f), now.Format(f))
	}
	return nil
}

// Order puts the leaf first and its issuers after it, each signed by the next, then whatever
// else was given. A chain pasted root-first, or a file with the intermediate before the leaf,
// still becomes the file lighttpd expects (the leaf first).
func Order(certs []*x509.Certificate) []*x509.Certificate {
	if len(certs) < 2 {
		return certs
	}
	// the leaf is a certificate that issued none of the others
	leaf := -1
	for i, c := range certs {
		issued := false
		for j, o := range certs {
			if i != j && !isSelfSigned(o) && signedBy(o, c) {
				issued = true
				break
			}
		}
		if !issued {
			leaf = i
			break
		}
	}
	if leaf < 0 {
		leaf = 0
	}
	out := []*x509.Certificate{certs[leaf]}
	used := map[int]bool{leaf: true}
	cur := certs[leaf]
	for !isSelfSigned(cur) {
		next := -1
		for i, c := range certs {
			if !used[i] && signedBy(cur, c) {
				next = i
				break
			}
		}
		if next < 0 {
			break
		}
		used[next] = true
		cur = certs[next]
		out = append(out, cur)
	}
	for i, c := range certs {
		if !used[i] {
			out = append(out, c)
		}
	}
	return out
}

func isSelfSigned(c *x509.Certificate) bool {
	return bytes.Equal(c.RawIssuer, c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

// signedBy: the issuer names parent and parent's key verifies the signature. The signature
// alone, not x509's CheckSignatureFrom: this is about which certificate belongs after which in
// the file, not about trust, and a CA whose key usage omits certSign still issued the leaf.
func signedBy(child, parent *x509.Certificate) bool {
	return bytes.Equal(child.RawIssuer, parent.RawSubject) && parent.CheckSignature(child.SignatureAlgorithm, child.RawTBSCertificate, child.Signature) == nil
}

// ChainWarning is the line for a chain whose leaf's issuer is not in it: a missing
// intermediate is a warning, not a refusal - the user may know that every client has it. A
// leaf that is self-signed, or whose issuer is in the chain, gets no line; the chain ending at
// an intermediate whose root is absent is the normal case (browsers hold the roots).
func ChainWarning(certs []*x509.Certificate) string {
	if len(certs) == 0 {
		return ""
	}
	leaf := certs[0]
	if isSelfSigned(leaf) {
		return ""
	}
	for _, c := range certs[1:] {
		if signedBy(leaf, c) {
			return ""
		}
	}
	return fmt.Sprintf("the certificate that issued %q (%s) is not in the chain: a browser or an addon that does not already know that intermediate will not trust the system - paste the issuer's certificate as the chain unless every client has it", leaf.Subject.CommonName, leaf.Issuer.String())
}

// The key algorithms the box generates.
const (
	AlgP256    = "p256"
	AlgP384    = "p384"
	AlgRSA2048 = "rsa2048"
)

// GenerateKey makes a key for a CSR: P-256 by default (small, fast, what every CA takes),
// P-384 (the CCU's own certificate's curve) or RSA-2048 for a CA that wants RSA.
func GenerateKey(alg string) (crypto.Signer, error) {
	switch alg {
	case AlgP256, "":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case AlgP384:
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case AlgRSA2048:
		return rsa.GenerateKey(rand.Reader, 2048)
	}
	return nil, fmt.Errorf("unknown key algorithm %q (p256, p384, rsa2048)", alg)
}

// CSR makes a PKCS#10 request, PEM: the common name as the subject and the first SAN, every
// other name a SAN too (an address becomes an IP SAN), the organisation when given.
func CSR(k crypto.Signer, cn string, sans []string, org string) ([]byte, error) {
	cn = strings.ToLower(strings.TrimSpace(cn))
	if cn == "" {
		return nil, errors.New("the common name is empty")
	}
	tpl := &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}
	if org = strings.TrimSpace(org); org != "" {
		tpl.Subject.Organization = []string{org}
	}
	seen := map[string]bool{}
	for _, n := range append([]string{cn}, sans...) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		if ip := net.ParseIP(n); ip != nil {
			tpl.IPAddresses = append(tpl.IPAddresses, ip)
		} else {
			tpl.DNSNames = append(tpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tpl, k)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// ParseCSR reads a request back (the tests, and the page's text preview).
func ParseCSR(b []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("no CERTIFICATE REQUEST block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, err
	}
	return csr, csr.CheckSignature()
}
