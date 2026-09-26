package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
	"github.com/hobbyquaker/occulited/internal/trust"
)

// testWriter is the trust store's system writer without a helper: files written directly, the
// rebuild a concatenation of the image's (minus the distrusted) and the local certificates.
type testWriter struct{ p trust.Paths }

func (w testWriter) WriteFile(path string, data []byte, mode os.FileMode) error {
	return os.WriteFile(path, data, mode)
}
func (w testWriter) Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func (w testWriter) MkdirAll(path string, mode os.FileMode) error { return os.MkdirAll(path, mode) }
func (w testWriter) Run(context.Context, string, []string) (trust.RunResult, error) {
	var dis []string
	if b, err := os.ReadFile(w.p.Distrust); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "!") {
				dis = append(dis, l[1:])
			}
		}
	}
	var out []byte
	_ = filepath.WalkDir(w.p.ImageDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(w.p.ImageDir, p)
		for _, x := range dis {
			if x == rel {
				return nil
			}
		}
		b, _ := os.ReadFile(p)
		out = append(out, b...)
		return nil
	})
	if des, err := os.ReadDir(w.p.LocalDir); err == nil {
		for _, d := range des {
			b, _ := os.ReadFile(filepath.Join(w.p.LocalDir, d.Name()))
			out = append(out, b...)
		}
	}
	_ = os.MkdirAll(filepath.Dir(w.p.Bundle), 0o755)
	if err := os.WriteFile(w.p.Bundle, out, 0o644); err != nil {
		return trust.RunResult{}, err
	}
	now := time.Now().Add(time.Duration(len(out)) * time.Millisecond)
	_ = os.Chtimes(w.p.Bundle, now, now)
	return trust.RunResult{}, nil
}

// trustRig: a SystemAPI whose trust store has an image with two CAs (A in occulited's base set).
func trustRig(t *testing.T) (*http.ServeMux, *SystemAPI, map[string][]byte) {
	t.Helper()
	r := fakeRoot(t)
	root := t.TempDir()
	paths := trust.DefaultPaths(root)
	_, _, caA := testcert.Issued([]string{"a"}, time.Now().Add(365*24*time.Hour))
	_, _, caB := testcert.Issued([]string{"b"}, time.Now().Add(365*24*time.Hour))
	_, _, caC := testcert.Issued([]string{"c"}, time.Now().Add(365*24*time.Hour))
	if err := os.MkdirAll(filepath.Join(paths.ImageDir, "mozilla"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(paths.ImageDir, "mozilla", "A.crt"), caA, 0o644)
	_ = os.WriteFile(filepath.Join(paths.ImageDir, "mozilla", "B.crt"), caB, 0o644)
	w := testWriter{paths}
	if _, err := w.Run(context.Background(), "", nil); err != nil {
		t.Fatal(err)
	}
	ts := trust.Open(filepath.Join(root, "state"))
	ts.Paths, ts.Sys, ts.Base = paths, w, []string{"mozilla/A.crt"}
	a := &SystemAPI{Root: r, Trust: ts}
	a.WarningTracker(filepath.Join(t.TempDir(), "warnings.json"), nil)
	mux := http.NewServeMux()
	a.Register(mux)
	return mux, a, map[string][]byte{"A": caA, "B": caB, "C": caC}
}

func storeOf(out map[string]any, id string) map[string]any {
	for _, s := range out["stores"].([]any) {
		m := s.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func certIDs(store map[string]any) []string {
	var ids []string
	for _, c := range store["certificates"].([]any) {
		ids = append(ids, c.(map[string]any)["id"].(string))
	}
	return ids
}

// B-237: a DER upload whose last byte is a space or a line feed is taken as uploaded, not trimmed
// into a malformed certificate.
func TestTrustAddDERWithAWhitespaceLastByte(t *testing.T) {
	mux, _, _ := trustRig(t)
	for _, last := range []byte{0x20, 0x0a} {
		der := testcert.CAEndingIn(last, fmt.Sprintf("B-237 %#x", last), time.Now().Add(time.Hour))
		b, _ := json.Marshal(map[string]string{"der": base64.StdEncoding.EncodeToString(der)})
		if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/oidc", string(b), "admin", auth.RoleAdmin); st != 200 || len(out["added"].([]any)) != 1 {
			t.Errorf("DER ending in %#x: %d %v", last, st, out)
		}
	}
}

func TestTrustRoutes(t *testing.T) {
	mux, _, ca := trustRig(t)
	body := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	// the view: four stores in the page's order, the system store with two image certificates,
	// occulited's with its base set of one, both editable
	st, out := warnCall(t, mux, "GET", "/api/system/v1/trust", "", "admin", auth.RoleAdmin)
	if st != 200 {
		t.Fatalf("view: %d %v", st, out)
	}
	stores := out["stores"].([]any)
	if len(stores) != 4 || stores[0].(map[string]any)["id"] != "system" || stores[1].(map[string]any)["id"] != "occulited" || stores[2].(map[string]any)["id"] != "oidc" || stores[3].(map[string]any)["id"] != "acme" {
		t.Fatalf("stores: %v", stores)
	}
	sys := storeOf(out, "system")
	if sys["editable"] != true || len(certIDs(sys)) != 2 || len(certIDs(storeOf(out, "occulited"))) != 1 || len(out["pending"].([]any)) != 0 {
		t.Fatalf("view: %v", out)
	}
	first := sys["certificates"].([]any)[0].(map[string]any)
	if first["source"] != "image" || first["removable"] != true || first["fingerprint"] == "" || first["subject"] == "" {
		t.Errorf("an image certificate: %v", first)
	}
	// a user reads (system:read); the writes are system:write routes, which the middleware
	// refuses a user (the scope table, scopes_test.go)
	if st, _ := warnCall(t, mux, "GET", "/api/system/v1/trust", "", "monitor", auth.RoleUser); st != 200 {
		t.Errorf("a user reads: %d", st)
	}
	// add C to ACME by PEM: the answer names what was added and the store as it is now
	st, out = warnCall(t, mux, "POST", "/api/system/v1/trust/acme", body(map[string]string{"pem": string(ca["C"])}), "admin", auth.RoleAdmin)
	if st != 200 || len(out["added"].([]any)) != 1 || len(out["store"].(map[string]any)["certificates"].([]any)) != 1 {
		t.Fatalf("add to acme: %d %v", st, out)
	}
	added := out["added"].([]any)[0].(map[string]any)
	if added["origin"] != "page" || added["added_by"] != "admin" || added["source"] != "added" {
		t.Errorf("added: %v", added)
	}
	cID := added["id"].(string)
	// add by DER (base64) to OIDC
	block, _ := pem.Decode(ca["C"])
	st, out = warnCall(t, mux, "POST", "/api/system/v1/trust/oidc", body(map[string]string{"der": base64.StdEncoding.EncodeToString(block.Bytes)}), "admin", auth.RoleAdmin)
	if st != 200 || len(out["added"].([]any)) != 1 || out["added"].([]any)[0].(map[string]any)["id"] != cID {
		t.Fatalf("add DER: %d %v", st, out)
	}
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/oidc", body(map[string]string{"der": "%%%"}), "admin", auth.RoleAdmin); st != 422 || out["error"] != "bad_certificate" {
		t.Errorf("bad base64: %d %v", st, out)
	}
	// the refusals: a key, nothing, an unknown store
	_, key, _ := testcert.Issued([]string{"k"}, time.Now().Add(time.Hour))
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/acme", body(map[string]string{"pem": string(key)}), "admin", auth.RoleAdmin); st != 422 || out["error"] != "private_key" {
		t.Errorf("a key: %d %v", st, out)
	}
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/acme", body(map[string]string{"pem": "x"}), "admin", auth.RoleAdmin); st != 422 || out["error"] != "no_certificate" {
		t.Errorf("no certificate: %d %v", st, out)
	}
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/nope", body(map[string]string{"pem": string(ca["C"])}), "admin", auth.RoleAdmin); st != 404 || out["error"] != "not_found" {
		t.Errorf("unknown store: %d %v", st, out)
	}
	// copy B from the system store into occulited's
	bID := certIDs(sys)[1]
	if certIDs(sys)[0] == cID {
		bID = certIDs(sys)[0]
	}
	st, out = warnCall(t, mux, "POST", "/api/system/v1/trust/system/"+bID+"/copy", body(map[string]string{"to": "occulited"}), "admin", auth.RoleAdmin)
	if st != 200 || len(out["added"].([]any)) != 1 || out["added"].([]any)[0].(map[string]any)["origin"] != "copy" || len(out["store"].(map[string]any)["certificates"].([]any)) != 2 {
		t.Fatalf("copy: %d %v", st, out)
	}
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/system/"+bID+"/copy", body(map[string]string{"to": "system"}), "admin", auth.RoleAdmin); st != 422 {
		t.Errorf("copy onto itself: %d %v", st, out)
	}
	if st, _ := warnCall(t, mux, "POST", "/api/system/v1/trust/system/nope/copy", body(map[string]string{"to": "acme"}), "admin", auth.RoleAdmin); st != 404 {
		t.Errorf("copy unknown: %d", st)
	}
	// distrust B in the system store: still listed, distrusted; restore it
	st, out = warnCall(t, mux, "DELETE", "/api/system/v1/trust/system/"+bID, "", "admin", auth.RoleAdmin)
	if st != 200 {
		t.Fatalf("distrust: %d %v", st, out)
	}
	var dis map[string]any
	for _, c := range out["store"].(map[string]any)["certificates"].([]any) {
		if m := c.(map[string]any); m["id"] == bID {
			dis = m
		}
	}
	if dis == nil || dis["distrusted"] != true || dis["removable"] == true {
		t.Fatalf("B after the removal: %v", out)
	}
	if st, out := warnCall(t, mux, "DELETE", "/api/system/v1/trust/system/"+bID, "", "admin", auth.RoleAdmin); st != 422 {
		t.Errorf("removing a distrusted one: %d %v", st, out)
	}
	if st, _ := warnCall(t, mux, "POST", "/api/system/v1/trust/system/"+bID+"/restore", "", "admin", auth.RoleAdmin); st != 200 {
		t.Errorf("restore: %d", st)
	}
	if st, _ := warnCall(t, mux, "POST", "/api/system/v1/trust/system/"+bID+"/restore", "", "admin", auth.RoleAdmin); st != 404 {
		t.Errorf("restore twice: %d", st)
	}
	// the OIDC copy is independent of the ACME one: removing it from ACME leaves OIDC
	if st, _ := warnCall(t, mux, "DELETE", "/api/system/v1/trust/acme/"+cID, "", "admin", auth.RoleAdmin); st != 200 {
		t.Errorf("remove from acme: %d", st)
	}
	_, out = warnCall(t, mux, "GET", "/api/system/v1/trust", "", "admin", auth.RoleAdmin)
	if len(certIDs(storeOf(out, "acme"))) != 0 || len(certIDs(storeOf(out, "oidc"))) != 1 {
		t.Errorf("after the acme removal: %v", out)
	}
	// the download
	req := httptest.NewRequest("GET", "/api/system/v1/trust/oidc/"+cID+"/pem", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s-admin", User: "admin", Role: auth.RoleAdmin, Scopes: auth.RoleScopes(auth.RoleAdmin)}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "-----BEGIN CERTIFICATE-----") || !strings.Contains(rec.Header().Get("Content-Disposition"), cID) {
		t.Errorf("pem: %d %q", rec.Code, rec.Body.String()[:40])
	}
	if st, _ := warnCall(t, mux, "DELETE", "/api/system/v1/trust/oidc/nope", "", "admin", auth.RoleAdmin); st != 404 {
		t.Errorf("remove unknown: %d", st)
	}
}

func TestTrustWithoutStore(t *testing.T) {
	a := &SystemAPI{Root: fakeRoot(t)}
	mux := http.NewServeMux()
	a.Register(mux)
	if st, out := warnCall(t, mux, "GET", "/api/system/v1/trust", "", "admin", auth.RoleAdmin); st != 501 || out["error"] != "no-trust-store" {
		t.Errorf("without a store: %d %v", st, out)
	}
	if list, ok := a.trustWarnings(context.Background()); ok != true || len(list) != 0 {
		t.Errorf("warnings without a store: %v %v", list, ok)
	}
}

// TestTrustWarning: a server occulited's store does not trust leaves a Status warning that names
// the host; adding its certificate to the store clears it.
func TestTrustWarning(t *testing.T) {
	mux, a, _ := trustRig(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	t.Cleanup(srv.Close)
	client := a.Trust.HTTPClient(trust.StoreOcculited, 5*time.Second)
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("the self-signed server passed")
	}
	st, out := warnCall(t, mux, "GET", "/api/system/v1/warnings", "", "admin", auth.RoleAdmin)
	w := warningByID(out, "trust-ca")
	if st != 200 || w == nil || w["variant"] != "127.0.0.1" || w["severity"] != "error" || w["href"] != "/system/trust#occulited" {
		t.Fatalf("warning: %d %v", st, out)
	}
	p := w["params"].(map[string]any)
	if p["store"] != "occulited" || p["candidate"] != false || len(p["hosts"].([]any)) != 1 {
		t.Errorf("params: %v", p)
	}
	_, out = warnCall(t, mux, "GET", "/api/system/v1/trust", "", "admin", auth.RoleAdmin)
	pending := out["pending"].([]any)
	if len(pending) != 1 || pending[0].(map[string]any)["host"] != "127.0.0.1" || len(pending[0].(map[string]any)["chain"].([]any)) != 1 {
		t.Fatalf("pending: %v", pending)
	}
	// the server's own certificate added to occulited's store: the call passes, the record goes
	leaf := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
	body, _ := json.Marshal(map[string]string{"pem": leaf})
	if st, out := warnCall(t, mux, "POST", "/api/system/v1/trust/occulited", string(body), "admin", auth.RoleAdmin); st != 200 {
		t.Fatalf("add: %d %v", st, out)
	}
	if res, err := client.Get(srv.URL); err != nil || res.StatusCode != 204 {
		t.Fatalf("after the add: %v %v", res, err)
	}
	_, out = warnCall(t, mux, "GET", "/api/system/v1/warnings", "", "admin", auth.RoleAdmin)
	if warningByID(out, "trust-ca") != nil {
		t.Errorf("the warning stays: %v", out)
	}
}
