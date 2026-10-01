package httpapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/meta"
)

// openccu-lite task 307: the guard of the addon pages takes a token with the ingress scope of the
// addon behind the path - redmatic's for /addons/red/ (its drop-in's segment) and /addons/redmatic/,
// not hmm's - beside system:read (B-2); a token without either is 403 naming the scope, a path
// nobody's is 403 naming system:read; the cookie and the session are untouched.
func TestRequireSessionAddonToken(t *testing.T) {
	store, err := auth.Open(t.TempDir(), auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Setup("admin", "secret123")
	red, _ := store.CreateToken("loom", auth.TokenOptions{Scopes: auth.Scopes{auth.AddonScope("redmatic")}})
	hmm, _ := store.CreateToken("hass", auth.TokenOptions{Scopes: auth.Scopes{auth.AddonScope("hmm"), auth.ScopeMetaRead}})
	sys, _ := store.CreateToken("updater", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeSystemRead}})
	full, _ := store.CreateToken("full", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeAll}})
	meta, _ := store.CreateToken("names", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeMetaRead}})
	a := &AuthAPI{Store: store, AddonForSegment: func(seg string) string {
		switch seg {
		case "red", "redmatic":
			return "redmatic"
		case "hmm":
			return "hmm"
		}
		return ""
	}}
	srv := httptest.NewServer(a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(SessionFrom(r).User))
	})))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		name, token, path string
		want              int
		scope             string
	}{
		{"redmatic's token on its proxied segment", red, "/addons/red/flows", 200, ""},
		{"redmatic's token on its own id", red, "/addons/redmatic/settings.cgi", 200, ""},
		{"redmatic's token on another addon", red, "/addons/hmm/", 403, "addon:hmm"},
		{"hmm's token on its pages", hmm, "/addons/hmm/api/x", 200, ""},
		{"hmm's token on redmatic", hmm, "/addons/red/", 403, "addon:redmatic"},
		{"a token on a segment nobody has", hmm, "/addons/nope/x.cgi", 403, "system:read"},
		{"system:read opens every addon page (B-2)", sys, "/addons/hmm/x.cgi", 200, ""},
		{"Full access", full, "/addons/red/", 200, ""},
		{"meta:read alone", meta, "/addons/red/", 403, "addon:redmatic"},
		{"an unknown token", "olt_" + strings.Repeat("0", 32), "/addons/red/", 401, ""},
	} {
		st, out, raw := do(t, srv, "GET", tc.path, "", map[string]string{"Authorization": "Bearer " + tc.token})
		if st != tc.want {
			t.Errorf("%s: %d, want %d (%s)", tc.name, st, tc.want, raw)
		}
		if tc.scope != "" && out["scope"] != tc.scope {
			t.Errorf("%s: the 403 names %v, want %s", tc.name, out["scope"], tc.scope)
		}
		if st == 200 && !strings.HasPrefix(raw, "token:") {
			t.Errorf("%s: the handler saw %q", tc.name, raw)
		}
	}
	// without the hook a token needs system:read, as before
	a.AddonForSegment = nil
	if st, _, _ := do(t, srv, "GET", "/addons/red/", "", map[string]string{"Authorization": "Bearer " + red}); st != 403 {
		t.Errorf("without the hook: %d", st)
	}
}

// GET /tokens lists the installed addons' ingress scopes with their names; POST /tokens takes one
// for an installed addon and refuses one for an addon that is not (422 naming it); a system
// without addons lists none and refuses every ingress scope.
func TestTokenAddonScopes(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := meta.New(nil, nil)
	mux := http.NewServeMux()
	testAuthAPI := &AuthAPI{Store: store}
	testAuthAPI.Register(mux)
	(&MetaAPI{Store: ms}).Register(mux)
	srv := httptest.NewServer(testAuthAPI.Middleware(mux))
	t.Cleanup(srv.Close)
	if st, _, _ := do(t, srv, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"test-passw0rd-1"}`, nil); st != 200 {
		t.Fatal("setup")
	}
	cookie := cookieOf(t, srv, `{"username":"admin","password":"test-passw0rd-1"}`)
	hdr := map[string]string{"Cookie": CookieName + "=" + cookie, "X-Occulite-Request": "1"}
	st, out, raw := do(t, srv, "GET", "/api/auth/v1/tokens", "", hdr)
	if st != 200 || len(out["addons"].([]any)) != 0 {
		t.Fatalf("no addons: %d %s", st, raw)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/tokens", `{"name":"loom","scopes":["addon:openccu-loom"]}`, hdr); st != 422 || out["error"] != "invalid-scope" || !strings.Contains(out["message"].(string), "openccu-loom") {
		t.Fatalf("an ingress scope without addons: %d %v", st, out)
	}
	testAuthAPI.Addons = func() map[string]string {
		return map[string]string{"redmatic": "RedMatic", "openccu-loom": "OpenCCU-Loom"}
	}
	st, out, raw = do(t, srv, "GET", "/api/auth/v1/tokens", "", hdr)
	addons, _ := out["addons"].([]any)
	if st != 200 || len(addons) != 2 {
		t.Fatalf("addons: %d %s", st, raw)
	}
	first := addons[0].(map[string]any)
	if first["id"] != "openccu-loom" || first["name"] != "OpenCCU-Loom" || first["scope"] != "addon:openccu-loom" {
		t.Errorf("the first addon: %v", first)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/tokens", `{"name":"loom","scopes":["addon:openccu-loom","meta:read"]}`, hdr); st != 201 || !strings.Contains(out["token"].(string), "olt_") {
		t.Fatalf("create: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/tokens", `{"name":"mosq","scopes":["addon:mosquitto"]}`, hdr); st != 422 || !strings.Contains(out["message"].(string), `"mosquitto"`) {
		t.Fatalf("an addon that is not installed: %d %v", st, out)
	}
	// the ingress scope opens no API route: 403 naming the route's scope
	var secret string
	st, out, _ = do(t, srv, "POST", "/api/auth/v1/tokens", `{"name":"only","scopes":["addon:redmatic"]}`, hdr)
	if st != 201 {
		t.Fatal(st)
	}
	secret = out["token"].(string)
	if st, out, _ := do(t, srv, "GET", "/api/meta/v1/snapshot", "", map[string]string{"Authorization": "Bearer " + secret}); st != 403 || out["scope"] != "meta:read" {
		t.Errorf("the ingress scope on the API: %d %v", st, out)
	}
}
