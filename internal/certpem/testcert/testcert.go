// Package testcert makes throwaway certificates for the tests of the packages around the box's
// TLS file: a self-signed leaf, or a leaf signed by a throwaway CA, with the names and the expiry
// the test asks for.
package testcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"
)

// SelfSigned is one certificate that signed itself, as S50lighttpd makes them.
func SelfSigned(names []string, ips []string, notAfter time.Time) (certPEM, keyPEM []byte) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := template(names, ips, notAfter)
	tpl.IsCA = true
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	return encode(der, key)
}

// Issued is a leaf signed by a fresh CA; the CA's PEM comes back too, so a chain can be built.
func Issued(names []string, notAfter time.Time) (certPEM, keyPEM, caPEM []byte) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := template([]string{"Test CA"}, nil, notAfter.Add(24*time.Hour))
	caTpl.IsCA = true
	caTpl.Subject = pkix.Name{CommonName: "Test CA"}
	caTpl.BasicConstraintsValid = true
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := template(names, nil, notAfter)
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	certPEM, keyPEM = encode(der, key)
	return certPEM, keyPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func template(names []string, ips []string, notAfter time.Time) *x509.Certificate {
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tpl := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter, DNSNames: names,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if len(names) > 0 {
		tpl.Subject = pkix.Name{CommonName: names[0], Organization: []string{"HomeMatic"}}
	}
	for _, ip := range ips {
		tpl.IPAddresses = append(tpl.IPAddresses, net.ParseIP(ip))
	}
	return tpl
}

func encode(der []byte, key *ecdsa.PrivateKey) ([]byte, []byte) {
	kder, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder})
}

// Sign issues a leaf for a request's public key from a fresh CA - what a company CA does with
// the CSR the box made; the CA's PEM comes back for the chain.
func Sign(csr *x509.CertificateRequest, notAfter time.Time) (certPEM, caPEM []byte) {
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := template([]string{"Test CA"}, nil, notAfter.Add(24*time.Hour))
	caTpl.IsCA = true
	caTpl.Subject = pkix.Name{CommonName: "Test CA"}
	caTpl.BasicConstraintsValid = true
	caDER, _ := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	tpl := template(csr.DNSNames, nil, notAfter)
	tpl.Subject = csr.Subject
	tpl.IPAddresses = csr.IPAddresses
	der, _ := x509.CreateCertificate(rand.Reader, tpl, ca, csr.PublicKey, caKey)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}
