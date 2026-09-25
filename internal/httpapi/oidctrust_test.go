package httpapi

import (
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/oidc"
	"github.com/hobbyquaker/occulited/internal/trust"
)

// openccu-lite task 230: a provider whose certificate the system does not know. Trusting it by
// PEM makes the test pass and the running client log in against it; a key is refused; the
// issuer's chain is fetched for a confirmation by fingerprint; removing it undoes it; a user may
// do none of it.
func TestOIDCTrust(t *testing.T) {
	var idp *httptest.Server
	idp = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/a", "token_endpoint": idp.URL + "/t", "userinfo_endpoint": idp.URL + "/u"})
	}))
	t.Cleanup(idp.Close)
	idpPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: idp.Certificate().Raw}))
	fp := trust.Fingerprint(idp.Certificate())

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "occulited.json")
	cfg := config.Default()
	cfg.Auth.Mode, cfg.Auth.OIDC.Issuer, cfg.Auth.OIDC.ClientID = "oidc", idp.URL, "lite"
	if err := config.Save(cfgFile, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Setup("admin", "correct horse battery")
	sess, _ := store.Login("admin", "correct horse battery", "127.0.0.1", "test")
	client := oidc.New(oidc.Config{Issuer: idp.URL, ClientID: "lite"})
	api := &AuthAPI{Store: store, ConfigFile: cfgFile, OIDC: client, Trust: trust.Open(dir)}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(api.Middleware(mux))
	t.Cleanup(srv.Close)
	hdr := map[string]string{"Authorization": "Bearer " + sess.ID}
	body := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	start := func() int {
		req := httptest.NewRequest("GET", "/api/auth/v1/oidc/start", nil)
		_, err := client.Start(req.Context(), "https://box/cb", "/")
		if err != nil {
			return 502
		}
		return 200
	}

	st, out, _ := do(t, srv, "GET", "/api/auth/v1/oidc/trust", "", hdr)
	if st != 200 || len(out["anchors"].([]any)) != 0 {
		t.Fatalf("list: %d %v", st, out)
	}
	// the test with the stored issuer fails on the system's pool; so does the login
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/test", "", hdr)
	if st != 200 || out["ok"] != false || !strings.Contains(out["error"].(string), "certificate") || out["verified_by"] != nil {
		t.Fatalf("test before: %d %v", st, out)
	}
	if start() != 502 {
		t.Fatal("the login trusted an unknown certificate")
	}
	// a key is refused, and so is a certificate that is not the one confirmed
	_, key, _ := testcert.Issued([]string{"x"}, time.Now().Add(time.Hour))
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": idpPEM + string(key)}), hdr); st != 422 || out["error"] != "private_key" {
		t.Errorf("a key: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": "hello"}), hdr); st != 422 || out["error"] != "no_certificate" {
		t.Errorf("no certificate: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": idpPEM, "fingerprint": strings.Repeat("AB:", 31) + "AB"}), hdr); st != 422 || out["error"] != "fingerprint_mismatch" {
		t.Errorf("a wrong fingerprint: %d %v", st, out)
	}
	// the chain the server presents, not trusted yet
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/peer-chain", body(map[string]string{"issuer": idp.URL}), hdr)
	chain, _ := out["chain"].([]any)
	if st != 200 || len(chain) == 0 || out["verified"] != false || chain[0].(map[string]any)["fingerprint"] != fp || chain[0].(map[string]any)["trusted"] != false {
		t.Fatalf("peer chain: %d %v", st, out)
	}
	// confirmed by its fingerprint (lower case, without colons: the same)
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": chain[0].(map[string]any)["pem"].(string), "fingerprint": strings.ToLower(strings.ReplaceAll(fp, ":", ""))}), hdr)
	if st != 200 || len(out["anchors"].([]any)) != 1 || len(out["added"].([]any)) != 1 {
		t.Fatalf("trust: %d %v", st, out)
	}
	anchor := out["anchors"].([]any)[0].(map[string]any)
	if anchor["fingerprint"] != fp || anchor["purposes"].([]any)[0] != "oidc" || anchor["added_by"] != "admin" {
		t.Errorf("anchor: %v", anchor)
	}
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/test", body(map[string]string{"issuer": idp.URL + "/"}), hdr)
	vb, _ := out["verified_by"].(map[string]any)
	if st != 200 || out["ok"] != true || out["tls"] != true || vb == nil || vb["trusted_here"] != true || vb["fingerprint"] != fp || out["token_endpoint"] != idp.URL+"/t" {
		t.Fatalf("test after: %d %v", st, out)
	}
	if start() != 200 {
		t.Fatal("the running client did not take the certificate")
	}
	if _, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/peer-chain", "", hdr); out["verified"] != true || out["chain"].([]any)[0].(map[string]any)["trusted"] != true {
		t.Errorf("peer chain after: %v", out)
	}
	// pasting the same again doubles nothing
	if _, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": idpPEM}), hdr); len(out["anchors"].([]any)) != 1 {
		t.Errorf("pasted twice: %v", out)
	}
	// a user may not read or change it
	_ = store.CreateUser("bob", "bobs password 123", auth.RoleUser, false)
	bob, _ := store.Login("bob", "bobs password 123", "127.0.0.1", "test")
	bh := map[string]string{"Authorization": "Bearer " + bob.ID}
	for _, c := range [][3]string{{"GET", "/api/auth/v1/oidc/trust", ""}, {"POST", "/api/auth/v1/oidc/trust", body(map[string]string{"pem": idpPEM})}, {"DELETE", "/api/auth/v1/oidc/trust/" + anchor["id"].(string), ""}, {"POST", "/api/auth/v1/oidc/test", ""}} {
		if st, _, _ := do(t, srv, c[0], c[1], c[2], bh); st != 403 {
			t.Errorf("a user: %s %s: %d", c[0], c[1], st)
		}
	}
	// removed: the login fails again
	if st, out, _ := do(t, srv, "DELETE", "/api/auth/v1/oidc/trust/"+anchor["id"].(string), "", hdr); st != 200 || len(out["anchors"].([]any)) != 0 {
		t.Fatalf("remove: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "DELETE", "/api/auth/v1/oidc/trust/"+anchor["id"].(string), "", hdr); st != 404 {
		t.Errorf("remove twice: %d", st)
	}
	if start() != 502 {
		t.Fatal("the removed certificate is still trusted")
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/test", body(map[string]string{"issuer": "ftp://x"}), hdr); st != 422 {
		t.Errorf("a bad issuer: %d %v", st, out)
	}
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://auth.example.org/application/o/lite/": "auth.example.org",
		"https://auth.example.org:9443/x":              "auth.example.org",
		"https://[fd00::1]:443/x":                      "fd00::1",
		"https://127.0.0.1":                            "127.0.0.1",
	} {
		if got := hostOf(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
