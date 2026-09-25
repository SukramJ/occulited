// Package certpem reads and writes the box's TLS material in the one format lighttpd and the
// addons use: /etc/config/server.pem, the certificate chain followed by the private key. It is
// shared by the ACME service (which assembles the file) and the privilege helper (which checks
// it at its boundary before writing it as root) - and it never depends on either.
package certpem

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Info is what the page shows of a certificate: the live one in /etc/config/server.pem, or the
// one the service issued.
type Info struct {
	Subject string `json:"subject"`
	// Issuer is the whole DN as Go writes it - an attribute it has no name for, like the e-mail
	// address a step-ca puts in, appears by its OID and, depending on the Go version, hex-encoded
	// (1.2.840.113549.1.9.1=#0c18…). IssuerCN and IssuerOrg are what a person reads of it, the
	// common name and the organisation (the first as parsed when there are several); the pages
	// show those and keep the DN as the tooltip.
	Issuer    string    `json:"issuer"`
	IssuerCN  string    `json:"issuer_cn,omitempty"`
	IssuerOrg string    `json:"issuer_org,omitempty"`
	Names     []string  `json:"names"`
	IPs       []string  `json:"ips,omitempty"`
	NotBefore time.Time `json:"not_before"`
	NotAfter  time.Time `json:"not_after"`
	// SelfSigned: issuer and subject are the same key - S50lighttpd's certificate.
	SelfSigned bool `json:"self_signed"`
	// DaysLeft until NotAfter, rounded down; negative once expired.
	DaysLeft int `json:"days_left"`
	// Chain is how many certificates the file carries (the leaf first).
	Chain int `json:"chain"`
	// Serial and the SHA-256 fingerprint of the leaf, for comparing with what a browser shows.
	Serial      string `json:"serial"`
	Fingerprint string `json:"fingerprint"`
}

// ParseCertificates reads every CERTIFICATE block of a PEM.
func ParseCertificates(b []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("no CERTIFICATE block")
	}
	return out, nil
}

// CertificatesOnly returns the CERTIFICATE blocks of a PEM and nothing else: what the helper
// hands the daemon of the live file, whose private key stays on the root side.
func CertificatesOnly(b []byte) []byte {
	var out bytes.Buffer
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			break
		}
		if block.Type == "CERTIFICATE" {
			_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})
		}
	}
	return out.Bytes()
}

// ParseInfo reads the first certificate of a PEM (the leaf) and describes it, as of now.
func ParseInfo(b []byte, now time.Time) (*Info, error) {
	certs, err := ParseCertificates(b)
	if err != nil {
		return nil, err
	}
	leaf := certs[0]
	info := &Info{Subject: leaf.Subject.String(), Issuer: leaf.Issuer.String(), IssuerCN: leaf.Issuer.CommonName, Names: append([]string{}, leaf.DNSNames...),
		NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, Chain: len(certs), Serial: leaf.SerialNumber.Text(16)}
	if len(leaf.Issuer.Organization) > 0 {
		info.IssuerOrg = leaf.Issuer.Organization[0]
	}
	for _, ip := range leaf.IPAddresses {
		info.IPs = append(info.IPs, ip.String())
	}
	// issuer == subject and the signature verifies with the leaf's own key; not CheckSignatureFrom,
	// which also wants CA basic constraints that a self-signed server certificate need not carry
	info.SelfSigned = bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	info.DaysLeft = int(leaf.NotAfter.Sub(now).Hours() / 24)
	if leaf.NotAfter.Before(now) && info.DaysLeft == 0 {
		info.DaysLeft = -1
	}
	info.Fingerprint = Fingerprint(leaf)
	return info, nil
}

// Fingerprint is the SHA-256 of the DER, colon-separated, as browsers print it.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	out := make([]byte, 0, len(sum)*3)
	for i, b := range sum {
		if i > 0 {
			out = append(out, ':')
		}
		out = append(out, fmt.Sprintf("%02X", b)...)
	}
	return string(out)
}

// AssemblePEM is the live file's format - the certificate chain first, the private key after it,
// one PEM, what lighttpd's ssl.pemfile and the addons read. Both halves are checked: every
// block is a certificate or a key, the leaf's public key matches the private key, and nothing
// else (a comment, a second key) gets into the file that becomes root:certs.
func AssemblePEM(chain, key []byte) ([]byte, error) {
	certs, err := ParseCertificates(chain)
	if err != nil {
		return nil, fmt.Errorf("certificate: %w", err)
	}
	priv, err := ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("private key: %w", err)
	}
	if !keyMatches(certs[0], priv) {
		return nil, errors.New("the private key does not belong to the certificate")
	}
	var out bytes.Buffer
	for _, c := range certs {
		_ = pem.Encode(&out, &pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	_ = pem.Encode(&out, &pem.Block{Type: "PRIVATE KEY", Bytes: der})
	return out.Bytes(), nil
}

// ValidLivePEM says whether b is exactly what AssemblePEM produces: certificates and one private
// key that belongs to the first. The helper checks this at its boundary before it writes the
// live file as root (the same rule as the password hash: what arrives is a string the
// unprivileged side produced).
func ValidLivePEM(b []byte) error {
	certs, err := ParseCertificates(b)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	keys := 0
	rest := b
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch {
		case block.Type == "CERTIFICATE":
		case isKeyBlock(block.Type):
			keys++
		default:
			return fmt.Errorf("unexpected %s block", block.Type)
		}
	}
	if keys != 1 {
		return fmt.Errorf("%d private keys, want one", keys)
	}
	priv, err := ParsePrivateKey(b)
	if err != nil {
		return fmt.Errorf("private key: %w", err)
	}
	if !keyMatches(certs[0], priv) {
		return errors.New("the private key does not belong to the certificate")
	}
	return nil
}

func isKeyBlock(t string) bool {
	switch t {
	case "PRIVATE KEY", "EC PRIVATE KEY", "RSA PRIVATE KEY":
		return true
	}
	return false
}

// ParsePrivateKey reads the first private key block of a PEM (PKCS#8, SEC 1 or PKCS#1). An
// encrypted key - PKCS#8's own block type, or the legacy Proc-Type header - is ErrEncrypted.
func ParsePrivateKey(b []byte) (crypto.Signer, error) {
	for {
		var block *pem.Block
		block, b = pem.Decode(b)
		if block == nil {
			return nil, errors.New("no private key block")
		}
		if block.Type == "ENCRYPTED PRIVATE KEY" || strings.HasPrefix(block.Headers["Proc-Type"], "4,ENCRYPTED") {
			return nil, ErrEncrypted
		}
		switch block.Type {
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			s, ok := k.(crypto.Signer)
			if !ok {
				return nil, errors.New("unsupported key type")
			}
			return s, nil
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		}
	}
}

func keyMatches(c *x509.Certificate, k crypto.Signer) bool {
	switch pub := c.PublicKey.(type) {
	case *ecdsa.PublicKey:
		p, ok := k.Public().(*ecdsa.PublicKey)
		return ok && p.Equal(pub)
	case *rsa.PublicKey:
		p, ok := k.Public().(*rsa.PublicKey)
		return ok && p.Equal(pub)
	case ed25519.PublicKey:
		p, ok := k.Public().(ed25519.PublicKey)
		return ok && p.Equal(pub)
	}
	return false
}
