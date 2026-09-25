package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/oidc"
	"github.com/hobbyquaker/occulited/internal/system"
)

// dkServices is hmipserver's unit for the device keys: running, restarted on Apply.
type dkServices struct{ restarts int }

func (s *dkServices) List() ([]system.Service, error) {
	return []system.Service{{ID: "hmipserver", Running: true, Since: "2026-09-19T10:00:00Z"}}, nil
}

func (s *dkServices) Control(_ context.Context, id, action string) (string, error) {
	s.restarts++
	return "", nil
}

// deviceKeysServer: the auth routes and the system API with task 154's device keys (no hmipserver
// to ask, so every stored key is listed as not paired), admin with a password, bob a user.
func deviceKeysServer(t *testing.T) (*httptest.Server, *auth.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "run", "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser("bob", "bobsecret1", auth.RoleUser, false); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/config/crRFD"), 0o755); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store}
	a.Register(mux)
	dk := &system.HmIPDeviceKeys{Root: system.Root(root), Services: &dkServices{}, StateDir: filepath.Join(root, "state")}
	(&SystemAPI{Root: system.Root(root), HmIPDeviceKeys: dk, ConfirmTicket: store.RedeemConfirmed}).Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	return srv, store, root
}

const dkBase = "/api/system/v1/radio/hmip/device-keys"

func TestDeviceKeysOverHTTP(t *testing.T) {
	srv, store, root := deviceKeysServer(t)
	admin := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)}
	bob := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"bob","password":"bobsecret1"}`)}

	// a user has no radio:keys: every route is refused
	for _, r := range [][2]string{{"GET", dkBase}, {"POST", dkBase}, {"DELETE", dkBase + "/3014F711A0000A1B2C3D4E5F"}, {"POST", dkBase + "/apply"}, {"GET", dkBase + "/export"}} {
		if st, out, _ := do(t, srv, r[0], r[1], `{}`, bob); st != 403 || out["scope"] != "radio:keys" {
			t.Errorf("bob %s %s: %d %v", r[0], r[1], st, out)
		}
	}
	// the administrator stores two keys: a code, and a typed SGTIN with the printed key
	st, out, _ := do(t, srv, "POST", dkBase, `{"code":"EQ01SG3014F711A0000A1B2C3D4E5FDLKCA477C71EC12F9BDD289046D34012259"}`, admin)
	if st != 200 || out["sgtin"] != "3014F711A0000A1B2C3D4E5F" || out["paired"] != false {
		t.Fatalf("add: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", dkBase, `{"sgtin":"3014-F711-A000-0EDD-89A8-1DBA","key":"6A8XY-73U0K-Z6YX5-284EM-T028KS"}`, admin); st != 200 {
		t.Fatalf("add typed: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", dkBase, `{"code":"WIFI:S:x;;"}`, admin); st != 422 || out["error"] != "invalid-code" {
		t.Errorf("a wrong code: %d %v", st, out)
	}
	st, out, body := do(t, srv, "GET", dkBase, "", admin)
	if st != 200 || out["stored"] != float64(2) || out["pending"] != float64(2) || out["devices_known"] != false || strings.Contains(body, "CA477C71") {
		t.Fatalf("view: %d %s", st, body)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/crRFD/sgtin.map")); !strings.Contains(string(b), "3014F711A0000A1B2C3D4E5F=CA477C71EC12F9BDD289046D34012259\n") {
		t.Fatalf("the map: %s", b)
	}

	// the export: a valid session is not enough
	if st, out, _ := do(t, srv, "GET", dkBase+"/export", "", admin); st != 403 || out["error"] != "confirm-required" {
		t.Errorf("export without a confirmation: %d %v", st, out)
	}
	// the confirmation needs the password; a wrong one is 401 and counts like a failed login
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+DeviceKeysExportPath+`","confirm":true,"password":"wrong"}`, admin); st != 401 || out["ticket"] != nil {
		t.Errorf("a wrong password: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "GET", "/api/auth/v1/confirm", "", admin); st != 200 || out["method"] != "password" {
		t.Errorf("the method: %d %v", st, out)
	}
	confirm := func() string {
		t.Helper()
		st, out, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+DeviceKeysExportPath+`","confirm":true,"password":"secret123"}`, admin)
		tk, _ := out["ticket"].(string)
		if st != 200 || tk == "" {
			t.Fatalf("confirm: %d %v", st, out)
		}
		return tk
	}
	tk := confirm()
	// a confirmed ticket is no download ticket
	if st, _, _ := do(t, srv, "GET", dkBase+"/export?ticket="+tk, "", nil); st != 401 {
		t.Errorf("the confirmed ticket as a download ticket: %d", st)
	}
	tk = confirm()
	// another session of the same account cannot spend it
	other := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`), "X-Occulite-Confirm": tk}
	if st, _, _ := do(t, srv, "GET", dkBase+"/export", "", other); st != 403 {
		t.Errorf("another session spent the ticket: %d", st)
	}
	tk = confirm()
	withTk := map[string]string{"Cookie": admin["Cookie"], "X-Occulite-Confirm": tk}
	st, out, body = do(t, srv, "GET", dkBase+"/export", "", withTk)
	keys, _ := out["keys"].([]any)
	if st != 200 || len(keys) != 2 || !strings.Contains(body, `"payload":"EQ01SG3014F711A0000A1B2C3D4E5FDLKCA477C71EC12F9BDD289046D34012259"`) {
		t.Fatalf("export: %d %s", st, body)
	}
	// nothing is remembered: the same ticket again, or none, asks again
	if st, _, _ := do(t, srv, "GET", dkBase+"/export", "", withTk); st != 403 {
		t.Errorf("a ticket spent twice: %d", st)
	}

	// a token with radio:keys is not asked; one with system:write has no radio:keys
	secret, err := store.CreateToken("keys", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeRadioKeys}})
	if err != nil {
		t.Fatal(err)
	}
	if st, out, _ := do(t, srv, "GET", dkBase+"/export", "", map[string]string{"Authorization": "Bearer " + secret}); st != 200 || len(out["keys"].([]any)) != 2 {
		t.Errorf("a token's export: %d %v", st, out)
	}
	sw, err := store.CreateToken("sw", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeSystemWrite}})
	if err != nil {
		t.Fatal(err)
	}
	if st, out, _ := do(t, srv, "GET", dkBase, "", map[string]string{"Authorization": "Bearer " + sw}); st != 403 || out["scope"] != "radio:keys" {
		t.Errorf("system:write: %d %v", st, out)
	}
	// a token gets no confirmed ticket, it needs none
	if st, _, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+DeviceKeysExportPath+`","confirm":true}`, map[string]string{"Authorization": "Bearer " + secret}); st != 403 && st != 409 {
		t.Errorf("a token's confirmation: %d", st)
	}

	// remove one, apply
	if st, out, _ := do(t, srv, "DELETE", dkBase+"/3014-F711-A000-0EDD-89A8-1DBA", "", admin); st != 200 || out["stored"] != float64(1) {
		t.Errorf("delete: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "DELETE", dkBase+"/3014F711A0000EDD89A81DBA", "", admin); st != 404 {
		t.Errorf("delete twice: %d", st)
	}
	if st, _, _ := do(t, srv, "POST", dkBase+"/apply", "", admin); st != 202 {
		t.Errorf("apply: %d", st)
	}
}

// An account without a password confirms at the provider; without a provider it cannot.
func TestConfirmWithoutPassword(t *testing.T) {
	srv, store, _ := deviceKeysServer(t)
	if err := store.CreateUserWithoutPassword("carol", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	sess, err := store.LoginExternal("oidc", "carol", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	carol := map[string]string{"Cookie": CookieName + "=" + sess.ID}
	if st, out, _ := do(t, srv, "GET", "/api/auth/v1/confirm", "", carol); st != 200 || out["method"] != "" {
		t.Errorf("no provider: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+DeviceKeysExportPath+`","confirm":true,"password":"x"}`, carol); st != 409 || out["error"] != "no-confirmation" {
		t.Errorf("no provider, a ticket: %d %v", st, out)
	}
}

// confirmProvider is a provider for the confirmation round trip: it insists on prompt=login and
// max_age=0, and its ID token carries the nonce and the auth_time the test sets.
type confirmProvider struct {
	srv      *httptest.Server
	user     string
	authTime time.Time
	nonce    string
	forced   bool
}

func newConfirmProvider(t *testing.T) *confirmProvider {
	t.Helper()
	p := &confirmProvider{user: "carol", authTime: time.Now()}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Write([]byte(`{"issuer":"` + p.srv.URL + `","authorization_endpoint":"` + p.srv.URL + `/authorize","token_endpoint":"` + p.srv.URL + `/token","userinfo_endpoint":"` + p.srv.URL + `/userinfo"}`))
		case "/authorize":
			q := r.URL.Query()
			p.nonce, p.forced = q.Get("nonce"), q.Get("prompt") == "login" && q.Get("max_age") == "0"
			http.Redirect(w, r, q.Get("redirect_uri")+"?code=c&state="+q.Get("state"), http.StatusFound)
		case "/token":
			enc := base64.RawURLEncoding.EncodeToString
			payload, _ := json.Marshal(map[string]any{"nonce": p.nonce, "auth_time": p.authTime.Unix()})
			w.Write([]byte(`{"access_token":"at","token_type":"Bearer","id_token":"` + enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + `.sig"}`))
		case "/userinfo":
			b, _ := json.Marshal(map[string]any{"sub": "u-1", "preferred_username": p.user})
			w.Write(b)
		}
	}))
	t.Cleanup(p.srv.Close)
	return p
}

// the browser's round trip: the confirm route, the provider, the callback, each with the cookie
func confirmRoundTrip(t *testing.T, srv *httptest.Server, cookie, start string) string {
	t.Helper()
	get := func(u string, withCookie bool) *http.Response {
		req, _ := http.NewRequest("GET", u, nil)
		if withCookie {
			req.Header.Set("Cookie", CookieName+"="+cookie)
		}
		res, err := noRedirect.Do(req)
		if err != nil || res.StatusCode != 302 {
			t.Fatalf("%s: %v %v", u, err, res)
		}
		return res
	}
	res := get(srv.URL+start, true)
	res = get(res.Header.Get("Location"), false) // the provider
	return get(res.Header.Get("Location"), true).Header.Get("Location")
}

// task 154 (D-104): an account without a password confirms with a fresh login at the provider:
// prompt=login and max_age=0 go out, and only the session's own account, logged in after the start,
// brings back a ticket - in the fragment, which no server sees.
func TestConfirmAtTheProvider(t *testing.T) {
	prov := newConfirmProvider(t)
	dir := t.TempDir()
	store, _ := auth.Open(dir, auth.Options{})
	_ = store.Setup("admin", "test-passw0rd-1")
	if err := store.CreateUserWithoutPassword("carol", auth.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/config/crRFD"), 0o755)
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store, OIDC: oidc.New(oidc.Config{Issuer: prov.srv.URL, ClientID: "x"})}
	a.Register(mux)
	dk := &system.HmIPDeviceKeys{Root: system.Root(root), Services: &dkServices{}, StateDir: filepath.Join(root, "state")}
	(&SystemAPI{Root: system.Root(root), HmIPDeviceKeys: dk, ConfirmTicket: store.RedeemConfirmed}).Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	sess, err := store.LoginExternal("oidc", "carol", "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	carol := map[string]string{"Cookie": CookieName + "=" + sess.ID}
	st, out, _ := do(t, srv, "GET", "/api/auth/v1/confirm?path="+DeviceKeysExportPath+"&return=/system/interfaces", "", carol)
	start, _ := out["url"].(string)
	if st != 200 || out["method"] != "oidc" || !strings.HasPrefix(start, "/api/auth/v1/oidc/confirm?path=") {
		t.Fatalf("method: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/ticket", `{"path":"`+DeviceKeysExportPath+`","confirm":true}`, carol); st != 409 || out["error"] != "confirm-oidc" {
		t.Errorf("a ticket without the provider: %d %v", st, out)
	}
	loc := confirmRoundTrip(t, srv, sess.ID, start)
	tk, ok := strings.CutPrefix(loc, "/system/interfaces#confirm=")
	if !ok || !prov.forced {
		t.Fatalf("round trip: %q forced %v", loc, prov.forced)
	}
	if st, out, _ := do(t, srv, "GET", dkBase+"/export", "", map[string]string{"Cookie": carol["Cookie"], "X-Occulite-Confirm": tk}); st != 200 || out["keys"] == nil {
		t.Errorf("export: %d %v", st, out)
	}
	// a login from before the round trip (the provider did not ask again): refused
	prov.authTime = time.Now().Add(-10 * time.Minute)
	if loc := confirmRoundTrip(t, srv, sess.ID, start); !strings.HasPrefix(loc, "/system/interfaces#confirm-error=") {
		t.Errorf("an old login: %q", loc)
	}
	// the provider signed in someone else: refused
	prov.authTime, prov.user = time.Now(), "admin"
	if loc := confirmRoundTrip(t, srv, sess.ID, start); !strings.HasPrefix(loc, "/system/interfaces#confirm-error=") {
		t.Errorf("another account: %q", loc)
	}
	// and the confirmation opened no session of its own
	if n := len(store.Sessions("carol")); n != 1 {
		t.Errorf("carol has %d sessions", n)
	}
}
