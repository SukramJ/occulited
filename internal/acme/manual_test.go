package acme

import (
	"context"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

// TestManual: a key and CSR made on the box, the certificate for it installed with the pending
// key, the mode manual, the marker naming it; the refusals with their reasons; the chain warning;
// DER accepted; the switch back.
func TestManual(t *testing.T) {
	inst := &fakeInstaller{restarted: []string{"mosquitto"}}
	_, _ = inst.Remove(context.Background(), func(string) {})
	s := newService(t, &fakeIssuer{}, inst)
	now := time.Now()

	// nothing pending, nothing to download
	if _, _, err := s.CSR(); !errors.Is(err, ErrNoCSR) {
		t.Fatalf("csr before generate: %v", err)
	}
	if st := s.Status(); st.Pending != nil || st.Settings.Mode != ModeSelfSigned {
		t.Fatalf("%+v", st)
	}
	// generate: the request parses back with the names, the key stays under tls/ as 0600
	p, err := s.GenerateKey(KeyRequest{Algorithm: "p384", CN: "CCU.Example.org", SANs: []string{"ccu.lan", "ccu.example.org", " "}, Org: "Home"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Algorithm != "P-384" || p.CN != "ccu.example.org" || strings.Join(p.SANs, ",") != "ccu.lan" || p.Org != "Home" || p.CSR == "" {
		t.Fatalf("%+v", p)
	}
	csr, err := certpem.ParseCSR([]byte(p.CSR))
	if err != nil || csr.Subject.CommonName != "ccu.example.org" || strings.Join(csr.DNSNames, ",") != "ccu.example.org,ccu.lan" {
		t.Fatalf("%v %+v", err, csr)
	}
	keyPath := filepath.Join(filepath.Dir(s.st.dir), "tls", "key.pem")
	if fi, err := os.Stat(keyPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v", err)
	}
	if b, name, err := s.CSR(); err != nil || string(b) != p.CSR || name != "ccu.example.org.csr" {
		t.Fatalf("%v %q", err, name)
	}
	if st := s.Status(); st.Pending == nil || st.Pending.Fingerprint != p.Fingerprint || st.Pending.CSR == "" {
		t.Fatalf("%+v", st.Pending)
	}
	// the CA signs the request: a leaf for the pending key
	key, _ := s.pendingKey()
	leafPEM, caPEM := testcert.Sign(csr, now.Add(90*24*time.Hour))
	// a certificate for another key is refused, both fingerprints named
	other, otherKey, _ := testcert.Issued([]string{"other"}, now.Add(time.Hour))
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: other}); err == nil || !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "generated on this system") {
		t.Fatalf("wrong pending key: %v", err)
	}
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: other, Key: []byte(p.CSR)}); err == nil || !strings.Contains(err.Error(), "does not parse") {
		t.Fatalf("csr as key: %v", err)
	}
	// the leaf alone: installed with the pending key, a warning for the missing issuer
	res, err := s.InstallManual(context.Background(), ManualInput{Certificate: leafPEM})
	if err != nil {
		t.Fatal(err)
	}
	if res.KeyFrom != "pending" || !strings.Contains(res.Warning, "not in the chain") || res.Certificate.Subject != "CN=ccu.example.org,O=Home" || res.RestartedAddons[0] != "mosquitto" {
		t.Fatalf("%+v", res)
	}
	if inst.mode != "manual" || inst.installs != 1 {
		t.Fatalf("installer: %+v", inst)
	}
	st := s.Status()
	if st.Settings.Mode != ModeManual || !st.Managed || st.ManagedMode != "manual" || st.ManagedBy != "CN=Test CA" || st.Pending != nil || st.Current.Subject != "CN=ccu.example.org,O=Home" {
		t.Fatalf("%+v", st)
	}
	if st.Warning != "" {
		t.Fatalf("warning: %s", st.Warning)
	}
	// the key stays for a later request with the same key; the request is fulfilled
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatal("the key is gone")
	}
	if _, _, err := s.CSR(); !errors.Is(err, ErrNoCSR) {
		t.Fatal("the fulfilled request is still pending")
	}
	// leaf plus chain, and the chain as DER: no warning; the key uploaded as DER
	leafDER, _ := pem.Decode(leafPEM)
	caDER, _ := pem.Decode(caPEM)
	keyPEM, _ := certpem.EncodeKey(key)
	keyDER, _ := pem.Decode(keyPEM)
	res, err = s.InstallManual(context.Background(), ManualInput{Certificate: leafDER.Bytes, Chain: caDER.Bytes, Key: keyDER.Bytes})
	if err != nil {
		t.Fatal(err)
	}
	if res.Warning != "" || res.KeyFrom != "upload" || res.Certificate.Chain != 2 {
		t.Fatalf("%+v", res)
	}
	// the chain appended to the certificate, root first: reordered, no warning
	res, err = s.InstallManual(context.Background(), ManualInput{Certificate: append(append([]byte{}, caPEM...), leafPEM...), Key: keyPEM})
	if err != nil || res.Warning != "" || res.Certificate.Subject != "CN=ccu.example.org,O=Home" {
		t.Fatalf("%v %+v", err, res)
	}
	// nothing renews a manual certificate
	info, _ := certpem.ParseInfo(leafPEM, now)
	if ShouldRenew(ModeManual, info, now.Add(80*24*time.Hour)) {
		t.Fatal("a manual certificate was due for renewal")
	}
	// refusals: expired with the date, encrypted key, no key at all, garbage
	expired, expiredKey, _ := testcert.Issued([]string{"old"}, now.Add(-time.Hour))
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: expired, Key: expiredKey}); err == nil || !strings.Contains(err.Error(), "expired on") {
		t.Fatalf("expired: %v", err)
	}
	enc := "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB\n-----END ENCRYPTED PRIVATE KEY-----\n"
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: other, Key: []byte(enc)}); err == nil || !strings.Contains(err.Error(), "decrypt it first") {
		t.Fatalf("encrypted: %v", err)
	}
	if _, err := s.InstallManual(context.Background(), ManualInput{Key: otherKey}); err == nil || !strings.Contains(err.Error(), "no certificate") {
		t.Fatalf("no certificate: %v", err)
	}
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: []byte("hello")}); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage: %v", err)
	}
	// the key for another certificate, uploaded: refused with both fingerprints
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: leafPEM, Key: otherKey}); err == nil || !strings.Contains(err.Error(), "the key given is") {
		t.Fatalf("wrong uploaded key: %v", err)
	}
	// the inspect preview: the certificate, the chain warning, the key, the match
	ins := s.Inspect(ManualInput{Certificate: leafPEM, Key: otherKey})
	if ins.Certificate == nil || ins.Certificate.Subject != "CN=ccu.example.org,O=Home" || ins.ChainWarning == "" || ins.Key == nil || ins.Key.Algorithm != "P-256" || ins.Matches == nil || *ins.Matches || ins.PendingMatches == nil || !*ins.PendingMatches {
		t.Fatalf("%+v", ins)
	}
	ins = s.Inspect(ManualInput{Certificate: leafPEM, Chain: caPEM, Key: keyPEM})
	if ins.ChainWarning != "" || ins.Chain != 2 || ins.Matches == nil || !*ins.Matches || ins.Validity != "" {
		t.Fatalf("%+v", ins)
	}
	ins = s.Inspect(ManualInput{Certificate: []byte("junk"), Key: []byte(enc)})
	if ins.CertificateError == "" || !strings.Contains(ins.KeyError, "decrypt") {
		t.Fatalf("%+v", ins)
	}
	ins = s.Inspect(ManualInput{Certificate: expired})
	if ins.Validity == "" || !strings.Contains(ins.Validity, "expired") {
		t.Fatalf("%+v", ins)
	}
	// a manual box with fewer than 14 days: the Status card warns; back to self-signed clears
	short, shortKey, shortCA := testcert.Issued([]string{"short"}, now.Add(5*24*time.Hour))
	if _, err := s.InstallManual(context.Background(), ManualInput{Certificate: append(short, shortCA...), Key: shortKey}); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Warning != "expiring" {
		t.Fatalf("warning: %q", st.Warning)
	}
	if _, err := s.SelfSigned(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Settings.Mode != ModeSelfSigned || st.Managed || st.Warning != "" {
		t.Fatalf("%+v", st)
	}
	// a new key replaces the pending one; an empty CN is refused
	if _, err := s.GenerateKey(KeyRequest{CN: " "}); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty cn: %v", err)
	}
	if _, err := s.GenerateKey(KeyRequest{Algorithm: "dsa", CN: "x"}); err == nil || !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad algorithm: %v", err)
	}
	p2, err := s.GenerateKey(KeyRequest{CN: "box.lan"})
	if err != nil || p2.Algorithm != "P-256" || p2.Fingerprint == p.Fingerprint {
		t.Fatalf("%v %+v", err, p2)
	}
}
