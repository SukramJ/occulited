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
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

// localSys writes the tree directly and rebuilds the bundle the way lite-ca-certificates does:
// the image's certificates minus the distrusted ones, then the userfs additions.
type localSys struct {
	paths  Paths
	runs   int
	fail   bool
	mkdirs []string
}

func (l *localSys) WriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func (l *localSys) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (l *localSys) MkdirAll(path string, mode os.FileMode) error {
	l.mkdirs = append(l.mkdirs, path)
	return os.MkdirAll(path, mode)
}
func (l *localSys) Run(_ context.Context, name string, _ []string) (RunResult, error) {
	l.runs++
	if name != l.paths.Rebuild {
		return RunResult{}, errors.New("unexpected program " + name)
	}
	if l.fail {
		return RunResult{Exit: 1, Output: "no space"}, nil
	}
	var dis []string
	if b, err := os.ReadFile(l.paths.Distrust); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "!") {
				dis = append(dis, line[1:])
			}
		}
	}
	var bundle []byte
	_ = filepath.WalkDir(l.paths.ImageDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(l.paths.ImageDir, p)
		if slices.Contains(dis, rel) {
			return nil
		}
		b, _ := os.ReadFile(p)
		bundle = append(bundle, b...)
		return nil
	})
	if des, err := os.ReadDir(l.paths.LocalDir); err == nil {
		for _, d := range des {
			b, _ := os.ReadFile(filepath.Join(l.paths.LocalDir, d.Name()))
			bundle = append(bundle, b...)
		}
	}
	_ = os.MkdirAll(filepath.Dir(l.paths.Bundle), 0o755)
	// a new mtime every run, so the store's cache sees the change
	if err := os.WriteFile(l.paths.Bundle, bundle, 0o644); err != nil {
		return RunResult{}, err
	}
	t := time.Now().Add(time.Duration(l.runs) * time.Second)
	_ = os.Chtimes(l.paths.Bundle, t, t)
	return RunResult{}, nil
}

// leafCA is a CA with one leaf for localhost signed by it.
type leafCA struct{ leaf, key, ca []byte }

// newCA makes a CA that Go's verifier accepts as a parent (key usage CertSign; testcert's CAs
// lack it) and a localhost leaf under it.
func newCA(t *testing.T, name string) leafCA {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	serial := func() *big.Int { n, _ := rand.Int(rand.Reader, big.NewInt(1<<62)); return n }
	caTpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name, Organization: []string{"Lab"}}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(400 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	must(t, err)
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(30 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, &key.PublicKey, caKey)
	must(t, err)
	kder, _ := x509.MarshalECPrivateKey(key)
	return leafCA{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kder}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})}
}

// tree builds an image with two CAs (A in occulited's base set, B not) and returns the store; C
// is a CA in no store.
func tree(t *testing.T) (*Store, *localSys, map[string][]byte) {
	s, sys, cas := treeWithLeaves(t)
	out := map[string][]byte{}
	for k, v := range cas {
		out[k] = v.ca
	}
	return s, sys, out
}

func treeWithLeaves(t *testing.T) (*Store, *localSys, map[string]leafCA) {
	t.Helper()
	root := t.TempDir()
	paths := DefaultPaths(root)
	cas := map[string]leafCA{}
	for _, n := range []string{"A", "B", "C"} {
		cas[n] = newCA(t, "Test CA "+n)
	}
	caA, caB := cas["A"].ca, cas["B"].ca
	if err := os.MkdirAll(filepath.Join(paths.ImageDir, "mozilla"), 0o755); err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(paths.ImageDir, "mozilla", "A_Root.crt"), caA, 0o644))
	must(t, os.WriteFile(filepath.Join(paths.ImageDir, "mozilla", "B_Root.crt"), caB, 0o644))
	sys := &localSys{paths: paths}
	if _, err := sys.Run(context.Background(), paths.Rebuild, nil); err != nil {
		t.Fatal(err)
	}
	sys.runs = 0
	s := Open(filepath.Join(root, "state"))
	s.Paths, s.Sys, s.Base = paths, sys, []string{"mozilla/A_Root.crt", "mozilla/Missing.crt"}
	return s, sys, cas
}

// tlsServer serves 204 under the leaf; its URL names localhost, which the leaf carries.
func tlsServer(t *testing.T, i leafCA) *httptest.Server {
	t.Helper()
	cert, err := tls.X509KeyPair(i.leaf, i.key)
	must(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	srv.URL = strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	return srv
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ids(list []Info) []string {
	var out []string
	for _, i := range list {
		out = append(out, i.ID)
	}
	return out
}

func idOfPEM(t *testing.T, b []byte) string {
	t.Helper()
	cs, err := Parse(b)
	if err != nil || len(cs) == 0 {
		t.Fatal("no certificate", err)
	}
	return idOf(cs[0])
}

func TestSystemStore(t *testing.T) {
	s, sys, ca := tree(t)
	ctx := context.Background()
	v, err := s.View()
	must(t, err)
	if len(v.Stores) != 4 || v.Stores[0].ID != StoreSystem || !v.Stores[0].Editable || len(v.Stores[0].Certificates) != 2 {
		t.Fatalf("view: %+v", v.Stores)
	}
	for _, c := range v.Stores[0].Certificates {
		if c.Source != SourceImage || !c.Removable || c.Distrusted {
			t.Errorf("an image certificate: %+v", c)
		}
	}
	// add C: a file under the local directory, the bundle rebuilt once
	added, err := s.AddTo(ctx, StoreSystem, ca["C"], "admin", OriginPage)
	must(t, err)
	if len(added) != 1 || added[0].Source != SourceAdded || added[0].Origin != OriginPage || !added[0].Removable || sys.runs != 1 {
		t.Fatalf("added: %+v, runs %d", added, sys.runs)
	}
	if _, err := os.Stat(filepath.Join(s.Paths.LocalDir, LocalPrefix+added[0].ID+".crt")); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListStore(StoreSystem)
	if len(list) != 3 || !slices.Contains(ids(list), idOfPEM(t, ca["C"])) {
		t.Fatalf("list after add: %v", ids(list))
	}
	if _, err := s.AddTo(ctx, StoreSystem, ca["C"], "admin", OriginPage); !errors.Is(err, ErrPresent) {
		t.Errorf("adding it again: %v", err)
	}
	pool, _ := s.PoolFor(StoreSystem)
	if !poolHas(pool, ca["C"]) {
		t.Error("the system pool lacks the added certificate")
	}
	// distrust B: a line in the distrust file, B out of the bundle but still listed; the file's
	// directory exists (/usr/local/etc on the box, which the helper would refuse to create) and is
	// not asked for
	must(t, os.MkdirAll(filepath.Dir(s.Paths.Distrust), 0o755))
	sys.mkdirs = nil
	must(t, s.RemoveFrom(ctx, StoreSystem, idOfPEM(t, ca["B"])))
	dis, _ := os.ReadFile(s.Paths.Distrust)
	if !strings.Contains(string(dis), "!mozilla/B_Root.crt\n") || sys.runs != 2 {
		t.Fatalf("distrust file: %q, runs %d", dis, sys.runs)
	}
	if len(sys.mkdirs) != 0 {
		t.Errorf("the distrust file's directory was asked for although it exists: %v", sys.mkdirs)
	}
	list, _ = s.ListStore(StoreSystem)
	k := slices.IndexFunc(list, func(i Info) bool { return i.ID == idOfPEM(t, ca["B"]) })
	if k < 0 || !list[k].Distrusted || list[k].Removable {
		t.Fatalf("B after the removal: %+v", list)
	}
	pool, _ = s.PoolFor(StoreSystem)
	if poolHas(pool, ca["B"]) || !poolHas(pool, ca["A"]) {
		t.Error("the system pool after distrusting B")
	}
	if err := s.RemoveFrom(ctx, StoreSystem, idOfPEM(t, ca["B"])); !errors.Is(err, ErrNoRemove) {
		t.Errorf("removing a distrusted one: %v", err)
	}
	// and back
	must(t, s.Restore(ctx, StoreSystem, idOfPEM(t, ca["B"])))
	dis, _ = os.ReadFile(s.Paths.Distrust)
	if _, err := os.Stat(s.Paths.Distrust); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("distrust file after the restore: %q %v", dis, err)
	}
	pool, _ = s.PoolFor(StoreSystem)
	if !poolHas(pool, ca["B"]) {
		t.Error("B not back in the pool")
	}
	// adding a distrusted one restores it too
	must(t, s.RemoveFrom(ctx, StoreSystem, idOfPEM(t, ca["B"])))
	if r, err := s.AddTo(ctx, StoreSystem, ca["B"], "admin", OriginPage); err != nil || len(r) != 1 || r[0].Distrusted {
		t.Fatalf("re-adding B: %v %v", r, err)
	}
	// remove the added C: its file goes
	must(t, s.RemoveFrom(ctx, StoreSystem, idOfPEM(t, ca["C"])))
	if _, err := os.Stat(filepath.Join(s.Paths.LocalDir, LocalPrefix+idOfPEM(t, ca["C"])+".crt")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the local file is still there")
	}
	if err := s.RemoveFrom(ctx, StoreSystem, "nope"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown id: %v", err)
	}
	// a rebuild that fails is the caller's error
	sys.fail = true
	if _, err := s.AddTo(ctx, StoreSystem, ca["C"], "admin", OriginPage); err == nil || !strings.Contains(err.Error(), "no space") {
		t.Errorf("failed rebuild: %v", err)
	}
	// without a writer the store is read-only
	s.Sys = nil
	if _, err := s.AddTo(ctx, StoreSystem, ca["C"], "admin", OriginPage); !errors.Is(err, ErrReadOnly) {
		t.Errorf("read-only: %v", err)
	}
	v, _ = s.View()
	if v.Stores[0].Editable {
		t.Error("editable without a writer")
	}
}

func TestOcculitedStore(t *testing.T) {
	s, _, ca := tree(t)
	ctx := context.Background()
	list, err := s.ListStore(StoreOcculited)
	must(t, err)
	// the base set: A (the missing name skipped)
	if len(list) != 1 || list[0].Source != SourceImage || list[0].ID != idOfPEM(t, ca["A"]) || !list[0].Removable {
		t.Fatalf("base: %+v", list)
	}
	pool, _ := s.PoolFor(StoreOcculited)
	if !poolHas(pool, ca["A"]) || poolHas(pool, ca["B"]) {
		t.Error("the base pool is not A alone")
	}
	// add C as an anchor; a chain of two counts two
	leaf, _, caD := testcert.Issued([]string{"d.example.org"}, time.Now().Add(90*24*time.Hour))
	added, err := s.AddTo(ctx, StoreOcculited, append(append([]byte{}, leaf...), caD...), "admin", OriginPage)
	must(t, err)
	if len(added) != 2 || added[1].Origin != OriginPage || added[1].Source != SourceAdded {
		t.Fatalf("added: %+v", added)
	}
	pool, _ = s.PoolFor(StoreOcculited)
	if !poolHas(pool, caD) || !poolHas(pool, ca["A"]) {
		t.Error("the pool lacks an anchor or the base")
	}
	// remove A (base): remembered, listed as distrusted, out of the pool
	must(t, s.RemoveFrom(ctx, StoreOcculited, idOfPEM(t, ca["A"])))
	list, _ = s.ListStore(StoreOcculited)
	if k := slices.IndexFunc(list, func(i Info) bool { return i.ID == idOfPEM(t, ca["A"]) }); k < 0 || !list[k].Distrusted {
		t.Fatalf("A after the removal: %+v", list)
	}
	pool, _ = s.PoolFor(StoreOcculited)
	if poolHas(pool, ca["A"]) {
		t.Error("A still in the pool")
	}
	if err := s.RemoveFrom(ctx, StoreOcculited, idOfPEM(t, ca["A"])); !errors.Is(err, ErrNoRemove) {
		t.Errorf("removing twice: %v", err)
	}
	// adding A again restores it (no anchor made)
	r, err := s.AddTo(ctx, StoreOcculited, ca["A"], "admin", OriginPage)
	if err != nil || len(r) != 1 || r[0].Source != SourceImage {
		t.Fatalf("re-adding A: %v %v", r, err)
	}
	f, _ := s.load()
	if len(f.Removed[StoreOcculited]) != 0 || len(f.Anchors) != 2 {
		t.Errorf("file after the restore: %+v", f)
	}
	must(t, s.RemoveFrom(ctx, StoreOcculited, idOfPEM(t, ca["A"])))
	must(t, s.Restore(ctx, StoreOcculited, idOfPEM(t, ca["A"])))
	if err := s.Restore(ctx, StoreOcculited, idOfPEM(t, ca["A"])); !errors.Is(err, ErrUnknown) {
		t.Errorf("restoring a present one: %v", err)
	}
	// the anchor goes on remove
	must(t, s.RemoveFrom(ctx, StoreOcculited, idOfPEM(t, caD)))
	list, _ = s.ListStore(StoreOcculited)
	if slices.Contains(ids(list), idOfPEM(t, caD)) {
		t.Error("the anchor is still listed")
	}
	// ACME's pool = occulited's + the acme anchors; OIDC's = the system's + the oidc anchors
	_, err = s.AddTo(ctx, PurposeACME, ca["C"], "admin", OriginACMESettings)
	must(t, err)
	pool, _ = s.PoolFor(PurposeACME)
	if !poolHas(pool, ca["C"]) || !poolHas(pool, ca["A"]) || poolHas(pool, ca["B"]) {
		t.Error("the ACME pool")
	}
	pool, _ = s.PoolFor(PurposeOIDC)
	if !poolHas(pool, ca["A"]) || !poolHas(pool, ca["B"]) || poolHas(pool, ca["C"]) {
		t.Error("the OIDC pool")
	}
	if _, err := s.PoolFor("nope"); !errors.Is(err, ErrStore) {
		t.Errorf("unknown store: %v", err)
	}
	if _, err := s.ListStore("nope"); !errors.Is(err, ErrStore) {
		t.Errorf("unknown store: %v", err)
	}
}

func TestCopyAndChangeHook(t *testing.T) {
	s, _, ca := tree(t)
	ctx := context.Background()
	changes := 0
	s.OnChange(func() { changes++ })
	v0 := s.Version()
	// system B into OIDC: a copy, with its origin
	added, err := s.Copy(ctx, StoreSystem, PurposeOIDC, idOfPEM(t, ca["B"]), "admin")
	must(t, err)
	if len(added) != 1 || added[0].Origin != OriginCopy || !slices.Contains(added[0].Purposes, PurposeOIDC) {
		t.Fatalf("copied: %+v", added)
	}
	if changes != 1 || s.Version() != v0+1 {
		t.Errorf("changes %d, version %d", changes, s.Version())
	}
	// independent afterwards: distrusting B in the system store leaves the OIDC copy
	must(t, s.RemoveFrom(ctx, StoreSystem, idOfPEM(t, ca["B"])))
	list, _ := s.ListStore(PurposeOIDC)
	if len(list) != 1 {
		t.Fatalf("the OIDC store after the system removal: %v", list)
	}
	if _, err := s.Copy(ctx, StoreSystem, StoreSystem, idOfPEM(t, ca["A"]), "admin"); !errors.Is(err, ErrSame) {
		t.Errorf("same store: %v", err)
	}
	if _, err := s.Copy(ctx, StoreSystem, PurposeACME, "nope", "admin"); !errors.Is(err, ErrUnknown) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := s.Copy(ctx, "x", PurposeACME, "nope", "admin"); !errors.Is(err, ErrStore) {
		t.Errorf("unknown store: %v", err)
	}
	p, err := s.PEMOf(PurposeOIDC, idOfPEM(t, ca["B"]))
	if err != nil || !strings.HasPrefix(p, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("PEMOf: %q %v", p, err)
	}
}

func TestParseAnyDER(t *testing.T) {
	_, key, ca := testcert.Issued([]string{"x"}, time.Now().Add(time.Hour))
	block, _ := pem.Decode(ca)
	certs, err := ParseAny(block.Bytes)
	if err != nil || len(certs) != 1 {
		t.Fatalf("DER: %v %v", certs, err)
	}
	kb, _ := pem.Decode(key)
	if _, err := ParseAny(kb.Bytes); !errors.Is(err, ErrPrivateKey) {
		t.Errorf("a DER key: %v", err)
	}
	if _, err := ParseAny([]byte{0x30, 0x03, 0x02, 0x01, 0x01}); err == nil || errors.Is(err, ErrNoCertificate) {
		t.Errorf("garbage DER: %v", err)
	}
	if _, err := ParseAny([]byte("hello")); !errors.Is(err, ErrNoCertificate) {
		t.Errorf("text: %v", err)
	}
	// B-237: a DER whose last byte is whitespace is parsed as uploaded, not trimmed into a
	// malformed one; whitespace around a DER a tool added is forgiven
	for _, last := range []byte{0x20, 0x0a, 0x09, 0x0d} {
		der := testcert.CAEndingIn(last, "B-237", time.Now().Add(time.Hour))
		for name, b := range map[string][]byte{
			"as is":               der,
			"leading whitespace":  append([]byte("\n "), der...),
			"trailing whitespace": append(append([]byte{}, der...), '\n', '\n'),
		} {
			certs, err := ParseAny(b)
			if err != nil || len(certs) != 1 || certs[0].Subject.CommonName != "B-237" {
				t.Errorf("DER ending in %#x, %s: %v %v", last, name, certs, err)
			}
		}
	}
}

func poolHas(pool *x509.CertPool, pemCert []byte) bool {
	cs, err := Parse(pemCert)
	if err != nil || len(cs) == 0 {
		return false
	}
	_, err = cs[0].Verify(x509.VerifyOptions{Roots: pool})
	return err == nil
}

// TestTransportStrict: a server whose CA occulited's store lacks fails, the failure names the host
// and the CA and offers the system store's copy of it; the copy makes the call pass and clears the
// failure.
func TestTransportStrict(t *testing.T) {
	s, _, cas := treeWithLeaves(t)
	ctx := context.Background()
	// a server under B - in the system store, not in occulited's base set (A alone)
	srv := tlsServer(t, cas["B"])
	defer srv.Close()

	client := s.HTTPClient(StoreOcculited, 5*time.Second)
	_, err := client.Get(srv.URL)
	if err == nil {
		t.Fatal("the call passed with a CA the store lacks")
	}
	fails := s.Failures()
	if len(fails) != 1 || fails[0].Host != "localhost" || fails[0].Store != StoreOcculited || fails[0].Candidate == nil || fails[0].Candidate.ID != idOfPEM(t, cas["B"].ca) || !strings.Contains(fails[0].Issuer, "Test CA B") {
		t.Fatalf("failures: %+v", fails)
	}
	v, _ := s.View()
	if len(v.Pending) != 1 {
		t.Fatalf("pending: %+v", v.Pending)
	}
	// twice: still one record
	_, _ = client.Get(srv.URL)
	if len(s.Failures()) != 1 {
		t.Fatal("a second failure doubled the record")
	}
	// the one-click fix: the candidate from the system store into occulited's; the record goes
	if _, err := s.Copy(ctx, StoreSystem, StoreOcculited, fails[0].Candidate.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if len(s.Failures()) != 0 {
		t.Fatalf("the failure stays after the copy: %+v", s.Failures())
	}
	res, err := client.Get(srv.URL)
	if err != nil || res.StatusCode != 204 {
		t.Fatalf("after the copy: %v %v", res, err)
	}
	// a server under a CA the system store has not either: no candidate
	srv2 := tlsServer(t, cas["C"])
	defer srv2.Close()
	if _, err := client.Get(srv2.URL); err == nil {
		t.Fatal("passed")
	}
	fails = s.Failures()
	if len(fails) != 1 || fails[0].Candidate != nil || len(fails[0].Chain) != 1 {
		t.Fatalf("no-candidate failure: %+v", fails)
	}
	// a plain connection error is not a trust failure
	srv2.Close()
	_, _ = client.Get(srv2.URL)
	if len(s.Failures()) != 1 {
		t.Error("a connection error was recorded as a trust failure")
	}
	// the server under A (the base set) passes; the host answered, so its record is cleared
	srv3 := tlsServer(t, cas["A"])
	defer srv3.Close()
	if res, err := client.Get(srv3.URL); err != nil || res.StatusCode != 204 {
		t.Fatalf("under the base set: %v %v", res, err)
	}
	if len(s.Failures()) != 0 {
		t.Errorf("the host answered, the record stays: %+v", s.Failures())
	}
	// the store changed: the transport rebuilds its pool (C added directly to occulited's)
	if _, err := s.AddTo(ctx, StoreOcculited, cas["C"].ca, "admin", OriginPage); err != nil {
		t.Fatal(err)
	}
	srv4 := tlsServer(t, cas["C"])
	defer srv4.Close()
	if res, err := client.Get(srv4.URL); err != nil || res.StatusCode != 204 {
		t.Fatalf("after adding C: %v %v", res, err)
	}
}
