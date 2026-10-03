package httpapi

import (
	"crypto/tls"
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

// openccu-lite task 232: the pin routes - the peer's chain from the configured server or a given
// URL, a pin from a certificate or a bare hash in either mode, the refusals, remove, Re-pin with
// replace - and the Status page's warning when a connection matches none of the pins.
// selfSignedServer answers 204 under a self-signed certificate with a key of its own (httptest's
// NewTLSServer gives every server the same built-in key, which no pinning test can tell apart).
func selfSignedServer(t *testing.T) *httptest.Server {
	t.Helper()
	certPEM, keyPEM := testcert.SelfSigned([]string{"localhost"}, []string{"127.0.0.1"}, time.Now().Add(24*time.Hour))
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestPinRoutes(t *testing.T) {
	mux, a, _ := trustRig(t)
	srv := selfSignedServer(t)
	leaf := srv.Certificate()
	leafPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}))
	spki := trust.SPKI(leaf)
	target := ""
	a.PinTarget = func(purpose string) string {
		if purpose == trust.PurposeACME {
			return target
		}
		return ""
	}
	body := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	admin := func(method, path, b string) (int, map[string]any) {
		return warnCall(t, mux, method, path, b, "admin", auth.RoleAdmin)
	}

	// only the two purposes take pins; a user may not
	if st, out := admin("POST", "/api/system/v1/trust/occulited/pins", body(map[string]string{"spki": spki})); st != 404 || out["error"] != "not_found" {
		t.Errorf("occulited: %d %v", st, out)
	}
	// the peer: nothing configured, then a URL given, then the configured one
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins/peer", ""); st != 422 || out["error"] != "invalid" {
		t.Errorf("peer without a target: %d %v", st, out)
	}
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins/peer", body(map[string]string{"url": "http://plain.example/"})); st != 422 {
		t.Errorf("peer http: %d %v", st, out)
	}
	st, out := admin("POST", "/api/system/v1/trust/acme/pins/peer", body(map[string]string{"url": srv.URL}))
	chain, _ := out["chain"].([]any)
	if st != 200 || out["host"] != "127.0.0.1" || len(chain) != 1 || out["verified"] != false || chain[0].(map[string]any)["spki"] != spki || chain[0].(map[string]any)["pinned"] != false || chain[0].(map[string]any)["pem"] == "" {
		t.Fatalf("peer: %d %v", st, out)
	}
	target = srv.URL + "/directory"
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins/peer", ""); st != 200 || out["url"] != target || out["host"] != "127.0.0.1" {
		t.Errorf("peer from the target: %d %v", st, out)
	}
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins/peer", body(map[string]string{"url": "https://127.0.0.1:1/"})); st != 502 || out["error"] != "unreachable" {
		t.Errorf("peer unreachable: %d %v", st, out)
	}
	// the refusals of a pin
	for name, b := range map[string]string{
		"mode":      body(map[string]string{"pem": leafPEM, "mode": "always"}),
		"spki":      body(map[string]string{"spki": "xyz"}),
		"two certs": body(map[string]string{"pem": leafPEM + leafPEM}),
	} {
		if st, out := admin("POST", "/api/system/v1/trust/acme/pins", b); st != 422 || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", name, st, out)
		}
	}
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins", body(map[string]string{"pem": "-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----"})); st != 422 || out["error"] != "private_key" {
		t.Errorf("a key: %d %v", st, out)
	}
	// pin the leaf, with-ca by default; the peer now says pinned, but not verified: the self-signed
	// server is under no CA of the store
	st, out = admin("POST", "/api/system/v1/trust/acme/pins", body(map[string]string{"pem": leafPEM}))
	pin, _ := out["pin"].(map[string]any)
	if st != 200 || pin == nil || pin["mode"] != "with-ca" || pin["spki"] != spki || pin["added_by"] != "admin" || pin["fingerprint"] != trust.Fingerprint(leaf) || len(out["pins"].([]any)) != 1 {
		t.Fatalf("pin: %d %v", st, out)
	}
	if st, out := admin("POST", "/api/system/v1/trust/acme/pins", body(map[string]string{"pem": leafPEM})); st != 409 || out["error"] != "present" {
		t.Errorf("twice: %d %v", st, out)
	}
	_, out = admin("POST", "/api/system/v1/trust/acme/pins/peer", "")
	if c := out["chain"].([]any)[0].(map[string]any); c["pinned"] != true || out["verified"] != false || !strings.Contains(out["error"].(string), "unknown authority") {
		t.Errorf("peer with the with-ca pin: %v", out)
	}
	_, out = admin("GET", "/api/system/v1/trust", "")
	if ps := storeOf(out, "acme")["pins"].([]any); len(ps) != 1 || ps[0].(map[string]any)["id"] != pin["id"] {
		t.Errorf("view: %v", storeOf(out, "acme"))
	}
	if storeOf(out, "system")["pins"] != nil || len(out["pin_failures"].([]any)) != 0 {
		t.Errorf("view: %v", out)
	}
	// the connection itself: the with-ca pin fails on the CA, no pin warning; re-pinned as only
	// (the danger path) it passes
	client := a.Trust.HTTPClient(trust.PurposeACME, 5*time.Second)
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("with-ca passed a self-signed server")
	}
	_, out = admin("GET", "/api/system/v1/warnings", "")
	if warningByID(out, "trust-pin") != nil || warningByID(out, "trust-ca") == nil {
		t.Errorf("warnings after the CA failure: %v", out)
	}
	st, out = admin("POST", "/api/system/v1/trust/acme/pins", body(map[string]any{"pem": leafPEM, "mode": "only", "replace": true}))
	if st != 200 || out["pin"].(map[string]any)["mode"] != "only" || len(out["pins"].([]any)) != 1 {
		t.Fatalf("re-pin as only: %d %v", st, out)
	}
	if res, err := client.Get(srv.URL); err != nil || res.StatusCode != 204 {
		t.Fatalf("pin only: %v %v", res, err)
	}
	_, out = admin("POST", "/api/system/v1/trust/acme/pins/peer", "")
	if out["verified"] != true || out["pin_only"] != true {
		t.Errorf("peer under the only pin: %v", out)
	}
	_, out = admin("GET", "/api/system/v1/warnings", "")
	if warningByID(out, "trust-ca") != nil {
		t.Errorf("the CA failure stays after the pass: %v", out)
	}
	// another server with another key: the mismatch, its warning with the presented fingerprint,
	// the page's record; a bare backup hash of that key clears it
	other := selfSignedServer(t)
	if _, err := client.Get(other.URL); err == nil {
		t.Fatal("another key passed")
	}
	_, out = admin("GET", "/api/system/v1/warnings", "")
	w := warningByID(out, "trust-pin")
	if w == nil || w["variant"] != "acme:127.0.0.1" || w["severity"] != "error" || w["href"] != "/system/trust#acme" {
		t.Fatalf("pin warning: %v", out)
	}
	if p := w["params"].(map[string]any); p["purpose"] != "acme" || p["host"] != "127.0.0.1" || p["spki"] != trust.SPKI(other.Certificate()) || p["fingerprint"] != trust.Fingerprint(other.Certificate()) {
		t.Errorf("params: %v", p)
	}
	_, out = admin("GET", "/api/system/v1/trust", "")
	if fs := out["pin_failures"].([]any); len(fs) != 1 || fs[0].(map[string]any)["host"] != "127.0.0.1" || len(fs[0].(map[string]any)["chain"].([]any)) != 1 {
		t.Fatalf("pin failures: %v", out["pin_failures"])
	}
	hexSPKI := strings.ToLower(hexOf(t, trust.SPKI(other.Certificate())))
	st, out = admin("POST", "/api/system/v1/trust/acme/pins", body(map[string]string{"spki": hexSPKI, "mode": "only"}))
	if st != 200 || out["pin"].(map[string]any)["spki"] != trust.SPKI(other.Certificate()) || out["pin"].(map[string]any)["subject"] != nil || len(out["pins"].([]any)) != 2 {
		t.Fatalf("backup pin from hex: %d %v", st, out)
	}
	_, out = admin("GET", "/api/system/v1/warnings", "")
	if warningByID(out, "trust-pin") != nil {
		t.Errorf("the mismatch stays after its key was pinned: %v", out)
	}
	if res, err := client.Get(other.URL); err != nil || res.StatusCode != 204 {
		t.Fatalf("after the backup pin: %v %v", res, err)
	}
	// remove: unknown, then both; without pins the CA check alone applies again
	if st, out := admin("DELETE", "/api/system/v1/trust/acme/pins/nope", ""); st != 404 {
		t.Errorf("remove unknown: %d %v", st, out)
	}
	_, out = admin("GET", "/api/system/v1/trust", "")
	for _, p := range storeOf(out, "acme")["pins"].([]any) {
		if st, out := admin("DELETE", "/api/system/v1/trust/acme/pins/"+p.(map[string]any)["id"].(string), ""); st != 200 {
			t.Fatalf("remove: %d %v", st, out)
		}
	}
	_, out = admin("GET", "/api/system/v1/trust", "")
	if ps := storeOf(out, "acme")["pins"]; ps != nil {
		t.Errorf("pins after the removes: %v", ps)
	}
	if _, err := client.Get(srv.URL); err == nil {
		t.Error("without pins the self-signed server passed")
	}
}

// hexOf is a base64 hash as hex, the way openssl prints a digest.
func hexOf(t *testing.T, b64 string) string {
	t.Helper()
	var raw []byte
	if err := json.Unmarshal([]byte(`"`+b64+`"`), &raw); err != nil {
		t.Fatal(err)
	}
	const d = "0123456789ABCDEF"
	var sb strings.Builder
	for _, x := range raw {
		sb.WriteByte(d[x>>4])
		sb.WriteByte(d[x&15])
	}
	return sb.String()
}

// TestOIDCTestWithPins: the OIDC settings' test answers the presented certificate (for Pin the
// current certificate), the pin that matched, and fails on a mismatch; the running client follows
// the pins.
func TestOIDCTestWithPins(t *testing.T) {
	idp := selfSignedServer(t)
	idp.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/a", "token_endpoint": idp.URL + "/t", "userinfo_endpoint": idp.URL + "/u"})
	})
	leaf := idp.Certificate()
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
	ts := trust.Open(dir)
	api := &AuthAPI{Store: store, ConfigFile: cfgFile, OIDC: client, Trust: ts}
	if err := api.ApplyOIDCTrust(); err != nil {
		t.Fatal(err)
	}
	// as main.go wires it: a change of the store reaches the running client, whose cached discovery
	// is dropped with it
	ts.OnChange(func() { _ = api.ApplyOIDCTrust() })
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(api.Middleware(mux))
	t.Cleanup(srv.Close)
	hdr := map[string]string{"Authorization": "Bearer " + sess.ID}
	start := func() bool {
		req := httptest.NewRequest("GET", "/api/auth/v1/oidc/start", nil)
		_, err := client.Start(req.Context(), "https://box/cb", "/")
		return err == nil
	}
	// the self-signed provider fails on the pool: a lost handshake has no leaf to offer
	st, out, _ := do(t, srv, "POST", "/api/auth/v1/oidc/test", "", hdr)
	if st != 200 || out["ok"] != false || out["leaf"] != nil || out["pinned"] != nil {
		t.Fatalf("test before: %d %v", st, out)
	}
	// pinned as only: the test passes by the pin, no verified_by, the presented leaf named (Pin the
	// current certificate); the login works
	if _, err := ts.AddPin(trust.PurposeOIDC, trust.PinRequest{Cert: leaf, Mode: trust.ModeOnly}); err != nil {
		t.Fatal(err)
	}
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/test", "", hdr)
	pinned, _ := out["pinned"].(map[string]any)
	leafOut, _ := out["leaf"].(map[string]any)
	if st != 200 || out["ok"] != true || pinned == nil || pinned["mode"] != "only" || out["pin_only"] != true || out["verified_by"] != nil || leafOut == nil || leafOut["spki"] != trust.SPKI(leaf) {
		t.Fatalf("test with the only pin: %d %v", st, out)
	}
	if !start() {
		t.Fatal("the running client did not follow the pin")
	}
	// the leaf trusted as an anchor and pinned with-ca: verified_by is the leaf (its own root),
	// and the pin is named
	if _, err := ts.AddTo(t.Context(), trust.PurposeOIDC, []byte(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw})), "admin", trust.OriginOIDCSettings); err != nil {
		t.Fatal(err)
	}
	if _, err := ts.AddPin(trust.PurposeOIDC, trust.PinRequest{Cert: leaf, Mode: trust.ModeWithCA, Replace: true}); err != nil {
		t.Fatal(err)
	}
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/test", "", hdr)
	vb, _ := out["verified_by"].(map[string]any)
	pinned, _ = out["pinned"].(map[string]any)
	if st != 200 || out["ok"] != true || vb == nil || vb["trusted_here"] != true || pinned == nil || pinned["mode"] != "with-ca" || out["pin_only"] != nil {
		t.Fatalf("test with the with-ca pin: %d %v", st, out)
	}
	if !start() {
		t.Fatal("the running client refused the pinned, trusted provider")
	}
	// another key pinned instead: the test fails loudly on the pin, so does the login, and the
	// quiet test records nothing while the login's failure is recorded
	other := selfSignedServer(t)
	if _, err := ts.AddPin(trust.PurposeOIDC, trust.PinRequest{Cert: other.Certificate(), Replace: true}); err != nil {
		t.Fatal(err)
	}
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/test", "", hdr)
	if st != 200 || out["ok"] != false || !strings.Contains(out["error"].(string), "none of the 1 pin") || !strings.Contains(out["error"].(string), trust.Fingerprint(leaf)) {
		t.Fatalf("test with a stale pin: %d %v", st, out)
	}
	if len(ts.PinFailures()) != 0 {
		t.Errorf("the test by hand recorded a failure: %+v", ts.PinFailures())
	}
	if start() {
		t.Fatal("the login passed a stale pin")
	}
	if fs := ts.PinFailures(); len(fs) != 1 || fs[0].Purpose != trust.PurposeOIDC {
		t.Errorf("the login's mismatch is not recorded: %+v", fs)
	}
	// the peer chain route still works through the trust package's fetch
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/oidc/peer-chain", "", hdr)
	if st != 200 || len(out["chain"].([]any)) != 1 || out["chain"].([]any)[0].(map[string]any)["spki"] != trust.SPKI(leaf) {
		t.Errorf("peer chain: %d %v", st, out)
	}
}
