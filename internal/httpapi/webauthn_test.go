package httpapi

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/meta"
)

// openccu-lite task 262: the security keys, with a software authenticator - an ECDSA P-256 key
// that answers a registration with a "none" attestation and a login with a signed assertion, as
// a FIDO2 key or a platform passkey does, so the ceremonies run end to end without a browser.

const (
	rpHost   = "box.lan"
	rpOrigin = "http://box.lan"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

type softKey struct {
	priv    *ecdsa.PrivateKey
	id      []byte
	counter uint32
	rk, uv  bool
	// noCredProps leaves the credProps result out, as a browser that does not report it does
	noCredProps bool
}

func newSoftKey(t *testing.T, rk, uv bool) *softKey {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	_, _ = rand.Read(id)
	return &softKey{priv: priv, id: id, rk: rk, uv: uv}
}

func (k *softKey) flags(attested bool) byte {
	f := byte(0x01) // UP
	if k.uv {
		f |= 0x04
	}
	if attested {
		f |= 0x40 // AT
	}
	return f
}

// attest answers a creation challenge: the browser's PublicKeyCredential.toJSON().
func (k *softKey) attest(challenge, origin, rpID string) []byte {
	cd, _ := json.Marshal(map[string]any{"type": "webauthn.create", "challenge": challenge, "origin": origin})
	rpHash := sha256.Sum256([]byte(rpID))
	point, _ := k.priv.PublicKey.Bytes() // 0x04 || X || Y, uncompressed
	x, y := point[1:33], point[33:65]
	cose, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, k.flags(true))
	auth = binary.BigEndian.AppendUint32(auth, k.counter)
	auth = append(auth, make([]byte, 16)...) // AAGUID
	auth = binary.BigEndian.AppendUint16(auth, uint16(len(k.id)))
	auth = append(auth, k.id...)
	auth = append(auth, cose...)
	att, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": auth})
	ext := map[string]any{"credProps": map[string]any{"rk": k.rk}}
	if k.noCredProps {
		ext = map[string]any{}
	}
	out, _ := json.Marshal(map[string]any{
		"id": b64(k.id), "rawId": b64(k.id), "type": "public-key",
		"response":               map[string]any{"clientDataJSON": b64(cd), "attestationObject": b64(att), "transports": []string{"usb"}},
		"clientExtensionResults": ext,
	})
	return out
}

// assert answers a login challenge with the counter given and this key's UV flag.
func (k *softKey) assert(challenge, origin, rpID string, handle []byte, counter uint32) []byte {
	cd, _ := json.Marshal(map[string]any{"type": "webauthn.get", "challenge": challenge, "origin": origin})
	rpHash := sha256.Sum256([]byte(rpID))
	auth := append([]byte{}, rpHash[:]...)
	auth = append(auth, k.flags(false))
	auth = binary.BigEndian.AppendUint32(auth, counter)
	cdHash := sha256.Sum256(cd)
	digest := sha256.Sum256(append(append([]byte{}, auth...), cdHash[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, k.priv, digest[:])
	resp := map[string]any{"clientDataJSON": b64(cd), "authenticatorData": b64(auth), "signature": b64(sig)}
	if handle != nil {
		resp["userHandle"] = b64(handle)
	}
	out, _ := json.Marshal(map[string]any{"id": b64(k.id), "rawId": b64(k.id), "type": "public-key", "response": resp})
	return out
}

// webauthnServer is authServer with a configuration file (for /config) and the routes on the
// host box.lan.
func webauthnServer(t *testing.T, fqdn string) (*httptest.Server, *AuthAPI) {
	t.Helper()
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := meta.New(nil, nil)
	mux := http.NewServeMux()
	cfgFile := filepath.Join(dir, "occulited.json")
	if err := os.WriteFile(cfgFile, []byte(`{"auth":{"mode":"local"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &AuthAPI{Store: store, ConfigFile: cfgFile}
	if fqdn != "" {
		a.FQDN = func() string { return fqdn }
	}
	a.Register(mux)
	(&MetaAPI{Store: ms}).Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	return srv, a
}

// call is do with the Host the page was opened on.
func call(t *testing.T, srv *httptest.Server, host, method, path, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	shellHeader(req, hdr)
	req.Host = host
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func challengeOf(t *testing.T, out map[string]any) string {
	t.Helper()
	opts, _ := out["options"].(map[string]any)
	c, _ := opts["challenge"].(string)
	if c == "" {
		t.Fatalf("no challenge in %v", out)
	}
	return c
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// register adds a key to the account of the session cookie, through the confirmed ticket.
func register(t *testing.T, srv *httptest.Server, cookie, password, name string, k *softKey) map[string]any {
	t.Helper()
	hdr := map[string]string{"Cookie": CookieName + "=" + cookie}
	st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/ticket", `{"path":"/api/auth/v1/me/webauthn","confirm":true,"password":"`+password+`"}`, hdr)
	if st != 200 {
		t.Fatalf("confirm: %d %v", st, out)
	}
	hdr["X-Occulite-Confirm"], _ = out["ticket"].(string)
	st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/begin", `{"name":"`+name+`"}`, hdr)
	if st != 200 {
		t.Fatalf("begin: %d %v", st, out)
	}
	opts := out["options"].(map[string]any)
	if rp, _ := opts["rp"].(map[string]any); rp["id"] != rpHost {
		t.Fatalf("rp id %v", opts["rp"])
	}
	delete(hdr, "X-Occulite-Confirm")
	st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/finish", mustJSON(map[string]any{"registration": out["registration"], "response": json.RawMessage(k.attest(challengeOf(t, out), rpOrigin, rpHost))}), hdr)
	if st != 201 {
		t.Fatalf("finish: %d %v", st, out)
	}
	key, _ := out["key"].(map[string]any)
	return key
}

func TestWebAuthnRegisterAndLogin(t *testing.T) {
	srv, _ := webauthnServer(t, "")
	if st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil); st != 200 {
		t.Fatalf("setup: %d %v", st, out)
	}
	cookie := cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)
	hdr := map[string]string{"Cookie": CookieName + "=" + cookie}

	// no keys yet, and the feature route says so
	if st, out := call(t, srv, rpHost, "GET", "/api/auth/v1/me/webauthn", "", hdr); st != 200 || len(out["keys"].([]any)) != 0 {
		t.Fatalf("list: %d %v", st, out)
	}
	if st, out := call(t, srv, rpHost, "GET", "/api/auth/v1/webauthn", "", nil); st != 200 || out["passkeys"] != false || out["registered"] != false {
		t.Fatalf("info: %d %v", st, out)
	}
	// adding needs the confirmed ticket
	if st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/begin", `{"name":"blue"}`, hdr); st != 403 || out["error"] != "confirm-required" {
		t.Fatalf("begin without a confirmation: %d %v", st, out)
	}
	passkey := newSoftKey(t, true, true)
	key := register(t, srv, cookie, "secret123", "blue", passkey)
	if key["name"] != "blue" || key["passkey"] != true || key["id"] != b64(passkey.id) {
		t.Fatalf("key: %v", key)
	}
	if st, out := call(t, srv, rpHost, "GET", "/api/auth/v1/webauthn", "", nil); st != 200 || out["passkeys"] != true || out["registered"] != true {
		t.Fatalf("info with a passkey: %d %v", st, out)
	}
	// the same key again is a duplicate (the exclude list would stop a browser first)
	hdr2 := map[string]string{"Cookie": CookieName + "=" + cookie}
	st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/ticket", `{"path":"/api/auth/v1/me/webauthn","confirm":true,"password":"secret123"}`, hdr2)
	if st != 200 {
		t.Fatalf("confirm again: %d %v", st, out)
	}
	hdr2["X-Occulite-Confirm"] = out["ticket"].(string)
	st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/begin", `{"name":"again"}`, hdr2)
	if st != 200 {
		t.Fatalf("begin again: %d %v", st, out)
	}
	if ex, _ := out["options"].(map[string]any)["excludeCredentials"].([]any); len(ex) != 1 {
		t.Errorf("exclude list: %v", out["options"])
	}
	delete(hdr2, "X-Occulite-Confirm")
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/finish", mustJSON(map[string]any{"registration": out["registration"], "response": json.RawMessage(passkey.attest(challengeOf(t, out), rpOrigin, rpHost))}), hdr2); st != 409 {
		t.Errorf("a duplicate key: %d %v", st, out)
	}

	// occulited task 14: the password alone signs in whatever keys the account has - no second
	// factor, and the key step's route is gone
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/logout", "", hdr); st != 200 {
		t.Fatalf("logout: %d %v", st, out)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/auth/v1/login", strings.NewReader(`{"username":"admin","password":"secret123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Host = rpHost
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var fin map[string]any
	_ = json.NewDecoder(res.Body).Decode(&fin)
	res.Body.Close()
	sid, _ := fin["sid"].(string)
	if res.StatusCode != 200 || sid == "" || fin["second_factor"] != nil || len(res.Cookies()) < 2 {
		t.Fatalf("password login with a passkey: %d %v %v", res.StatusCode, fin, res.Cookies())
	}
	if st, o := call(t, srv, rpHost, "GET", "/api/auth/v1/state", "", map[string]string{"Cookie": CookieName + "=" + sid}); st != 200 || o["authenticated"] != true || o["method"] != "password" {
		t.Errorf("state after the password: %d %v", st, o)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/webauthn", `{"login":"x","response":{}}`, nil); st == 200 {
		t.Errorf("the key step's route answers: %d %v", st, o)
	}
	// a wrong password still fails as before
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login", `{"username":"admin","password":"wrong-one-1"}`, nil); st != 401 || o["sid"] != nil {
		t.Errorf("wrong password: %d %v", st, o)
	}

	// the passkey signs in without name and password, with user verification
	st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil)
	if st != 200 || out["login"] == "" {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	if uv := out["options"].(map[string]any)["userVerification"]; uv != "required" {
		t.Errorf("user verification %v, want required", uv)
	}
	handle := []byte(fin["account_id"].(string))
	// a sign count that went backwards: refused
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(passkey.assert(challengeOf(t, out), rpOrigin, rpHost, handle, 0))}), nil); st != 200 {
		t.Errorf("a first use (count 0): %d %v", st, o)
	}
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(passkey.assert(challengeOf(t, out), rpOrigin, rpHost, handle, 3))}), nil); st != 200 {
		t.Errorf("count 3: %d %v", st, o)
	}
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(passkey.assert(challengeOf(t, out), rpOrigin, rpHost, handle, 1))}), nil); st != 401 {
		t.Errorf("a cloned key's count: %d %v", st, o)
	}
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(passkey.assert(challengeOf(t, out), rpOrigin, rpHost, handle, 5))}), nil)
	if st != 200 || out["user"] != "admin" {
		t.Fatalf("passkey login: %d %v", st, out)
	}
	if st, o := call(t, srv, rpHost, "GET", "/api/auth/v1/state", "", map[string]string{"Cookie": CookieName + "=" + out["sid"].(string)}); st != 200 || o["method"] != auth.MethodPasskey {
		t.Errorf("state after the passkey: %d %v", st, o)
	}
	// without user verification a key signs nobody in
	noUV := *passkey
	noUV.uv = false
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(noUV.assert(challengeOf(t, out), rpOrigin, rpHost, handle, 6))}), nil); st != 401 {
		t.Errorf("passkey without UV: %d %v", st, o)
	}
	// an unknown handle
	if st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Fatalf("passkey begin: %d %v", st, out)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(passkey.assert(challengeOf(t, out), rpOrigin, rpHost, []byte("nobody00"), 7))}), nil); st != 401 {
		t.Errorf("unknown handle: %d %v", st, o)
	}

	// removal by the owner: the confirmed ticket again; then the password alone signs in
	own := map[string]string{"Cookie": CookieName + "=" + sid}
	if st, o := call(t, srv, rpHost, "DELETE", "/api/auth/v1/me/webauthn/"+b64(passkey.id), "", own); st != 403 {
		t.Errorf("remove without a confirmation: %d %v", st, o)
	}
	_, o := call(t, srv, rpHost, "POST", "/api/auth/v1/ticket", `{"path":"/api/auth/v1/me/webauthn","confirm":true,"password":"secret123"}`, own)
	own["X-Occulite-Confirm"] = o["ticket"].(string)
	if st, o := call(t, srv, rpHost, "DELETE", "/api/auth/v1/me/webauthn/"+b64(passkey.id), "", own); st != 200 || len(o["keys"].([]any)) != 0 {
		t.Fatalf("remove: %d %v", st, o)
	}
	if st, o := call(t, srv, rpHost, "POST", "/api/auth/v1/login", `{"username":"admin","password":"secret123"}`, nil); st != 200 || o["sid"] == nil {
		t.Errorf("login without keys: %d %v", st, o)
	}
	if _, o := call(t, srv, rpHost, "GET", "/api/auth/v1/webauthn", "", nil); o["passkeys"] != false {
		t.Errorf("info without keys: %v", o)
	}
}

// occulited task 14: a registration asks for a resident key and user verification, both required,
// and refuses a credential that is not a passkey; one whose browser does not report credProps is
// taken as the resident credential it was asked for.
func TestWebAuthnRegisterOnlyPasskeys(t *testing.T) {
	srv, _ := webauthnServer(t, "")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	cookie := cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)
	attempt := func(k *softKey) (int, map[string]any) {
		hdr := map[string]string{"Cookie": CookieName + "=" + cookie}
		_, o := call(t, srv, rpHost, "POST", "/api/auth/v1/ticket", `{"path":"/api/auth/v1/me/webauthn","confirm":true,"password":"secret123"}`, hdr)
		hdr["X-Occulite-Confirm"] = o["ticket"].(string)
		st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/begin", `{"name":"k"}`, hdr)
		if st != 200 {
			t.Fatalf("begin: %d %v", st, out)
		}
		sel, _ := out["options"].(map[string]any)["authenticatorSelection"].(map[string]any)
		if sel["residentKey"] != "required" || sel["requireResidentKey"] != true || sel["userVerification"] != "required" {
			t.Errorf("selection: %v", sel)
		}
		delete(hdr, "X-Occulite-Confirm")
		return call(t, srv, rpHost, "POST", "/api/auth/v1/me/webauthn/finish", mustJSON(map[string]any{"registration": out["registration"], "response": json.RawMessage(k.attest(challengeOf(t, out), rpOrigin, rpHost))}), hdr)
	}
	if st, out := attempt(newSoftKey(t, false, true)); st != 422 || out["error"] != "webauthn-not-passkey" {
		t.Errorf("not resident: %d %v", st, out)
	}
	if st, out := attempt(newSoftKey(t, true, false)); st != 422 || out["error"] != "webauthn-refused" {
		t.Errorf("no user verification: %d %v", st, out)
	}
	quiet := newSoftKey(t, true, true)
	quiet.noCredProps = true
	if st, out := attempt(quiet); st != 201 || out["key"].(map[string]any)["passkey"] != true {
		t.Errorf("no credProps: %d %v", st, out)
	}
}

// occulited task 14's upgrade: a key stored as a second factor by task 262 stays, listed as one
// that cannot sign in; the password alone opens the session, and the login page has no button.
func TestWebAuthnLegacySecondFactorKey(t *testing.T) {
	srv, a := webauthnServer(t, "")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	old := newSoftKey(t, false, false)
	rk := false
	cred := webauthn.Credential{ID: old.id, PublicKey: []byte("pk")}
	cred.Extensions.RK = &rk
	if _, err := a.Store.AddWebAuthn("admin", cred, "blue"); err != nil {
		t.Fatal(err)
	}
	if _, o := call(t, srv, rpHost, "GET", "/api/auth/v1/webauthn", "", nil); o["passkeys"] != false || o["registered"] != true {
		t.Errorf("info: %v", o)
	}
	st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/login", `{"username":"admin","password":"secret123"}`, nil)
	if st != 200 || out["sid"] == nil {
		t.Fatalf("password login: %d %v", st, out)
	}
	hdr := map[string]string{"Cookie": CookieName + "=" + out["sid"].(string)}
	if st, o := call(t, srv, rpHost, "GET", "/api/auth/v1/me/webauthn", "", hdr); st != 200 || o["keys"].([]any)[0].(map[string]any)["passkey"] != false {
		t.Errorf("list: %d %v", st, o)
	}
	if st, o := call(t, srv, rpHost, "GET", "/api/auth/v1/users", "", hdr); st != 200 || !strings.Contains(mustJSON(o), `"webauthn_unusable":1`) {
		t.Errorf("users: %d %v", st, o)
	}
}

// A refused passkey counts towards the lockout of the address like a wrong password.
func TestWebAuthnLockout(t *testing.T) {
	srv, _ := webauthnServer(t, "")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	cookie := cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)
	register(t, srv, cookie, "secret123", "phone", newSoftKey(t, true, true))
	other := newSoftKey(t, true, true)
	locked := false
	for i := 0; i < 10 && !locked; i++ {
		st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey", "{}", nil)
		if st == 429 {
			locked = true
			break
		}
		if st != 200 {
			t.Fatalf("begin %d: %d %v", i, st, out)
		}
		st, out = call(t, srv, rpHost, "POST", "/api/auth/v1/login/passkey/finish", mustJSON(map[string]any{"login": out["login"], "response": json.RawMessage(other.assert(challengeOf(t, out), rpOrigin, rpHost, []byte("nobody00"), uint32(i+1)))}), nil)
		if st != 401 && st != 429 {
			t.Fatalf("finish %d: %d %v", i, st, out)
		}
	}
	if !locked {
		t.Fatal("refused passkeys did not lock the address")
	}
}

// An administrator removes another account's key, which ends that account's sessions.
func TestWebAuthnAdminRemoval(t *testing.T) {
	srv, _ := webauthnServer(t, "")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	admin := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)}
	if st, out := call(t, srv, rpHost, "POST", "/api/auth/v1/users", `{"username":"bob","password":"bobsecret1","level":"operate"}`, admin); st != 201 {
		t.Fatalf("create bob: %d %v", st, out)
	}
	bobCookie := cookieOf(t, srv, `{"username":"bob","password":"bobsecret1"}`)
	key := newSoftKey(t, true, true)
	kv := register(t, srv, bobCookie, "bobsecret1", "phone", key)
	bob := map[string]string{"Cookie": CookieName + "=" + bobCookie}
	// bob cannot see or remove through the admin routes
	if st, _ := call(t, srv, rpHost, "GET", "/api/auth/v1/users/bob/webauthn", "", bob); st != 403 {
		t.Errorf("bob on the admin route: %d", st)
	}
	if st, out := call(t, srv, rpHost, "GET", "/api/auth/v1/users/bob/webauthn", "", admin); st != 200 || len(out["keys"].([]any)) != 1 {
		t.Fatalf("admin lists bob's keys: %d %v", st, out)
	}
	if st, _ := call(t, srv, rpHost, "GET", "/api/auth/v1/users/nobody/webauthn", "", admin); st != 404 {
		t.Errorf("unknown user: %d", st)
	}
	if st, out := call(t, srv, rpHost, "DELETE", "/api/auth/v1/users/bob/webauthn/"+kv["id"].(string), "", admin); st != 200 || len(out["keys"].([]any)) != 0 {
		t.Fatalf("admin removes: %d %v", st, out)
	}
	if st, _ := call(t, srv, rpHost, "GET", "/api/auth/v1/state", "", bob); st != 200 {
		t.Errorf("state: %d", st)
	} else if _, out := call(t, srv, rpHost, "GET", "/api/auth/v1/state", "", bob); out["authenticated"] != false {
		t.Errorf("bob's session outlived the removal: %v", out)
	}
	if st, _ := call(t, srv, rpHost, "DELETE", "/api/auth/v1/users/bob/webauthn/"+kv["id"].(string), "", admin); st != 404 {
		t.Errorf("removing it again: %d", st)
	}
}

// The relying party is the system's name: no key on an IP address, and a registration only on
// the system's full name where it is known.
func TestWebAuthnNames(t *testing.T) {
	srv, _ := webauthnServer(t, "ccu.home.arpa")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	cookie := cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)
	begin := func(host string) (int, map[string]any) {
		hdr := map[string]string{"Cookie": CookieName + "=" + cookie}
		_, o := call(t, srv, host, "POST", "/api/auth/v1/ticket", `{"path":"/api/auth/v1/me/webauthn","confirm":true,"password":"secret123"}`, hdr)
		hdr["X-Occulite-Confirm"] = o["ticket"].(string)
		return call(t, srv, host, "POST", "/api/auth/v1/me/webauthn/begin", `{"name":"k"}`, hdr)
	}
	for _, tc := range []struct {
		host, want string
	}{
		{"192.0.2.10", "webauthn-needs-name"},
		{"[2001:db8::10]:8443", "webauthn-needs-name"},
		{"ccu", "webauthn-needs-name"},
		{"other.example", "webauthn-wrong-name"},
		{"CCU.home.arpa", ""},
		{"ccu.home.arpa:8443", ""},
		{"localhost:8080", ""},
	} {
		st, out := begin(tc.host)
		switch {
		case tc.want == "" && st != 200:
			t.Errorf("%s: %d %v, want a challenge", tc.host, st, out)
		case tc.want != "" && (st != 409 || out["error"] != tc.want):
			t.Errorf("%s: %d %v, want %s", tc.host, st, out, tc.want)
		case tc.want != "" && out["name"] != "ccu.home.arpa":
			t.Errorf("%s: the answer names %v", tc.host, out["name"])
		}
	}
	// the feature route names it too
	if _, out := call(t, srv, rpHost, "GET", "/api/auth/v1/webauthn", "", nil); out["name"] != "ccu.home.arpa" {
		t.Errorf("info: %v", out)
	}
	// a login is not held to the full name (the browser finds no key on another one), only to a name
	if st, out := call(t, srv, "192.0.2.10", "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 409 {
		t.Errorf("passkey on an address: %d %v", st, out)
	}
	if st, out := call(t, srv, "ccu", "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 409 || out["error"] != "webauthn-needs-name" {
		t.Errorf("passkey on the bare name: %d %v", st, out)
	}
	if st, out := call(t, srv, "other.example", "POST", "/api/auth/v1/login/passkey", "{}", nil); st != 200 {
		t.Errorf("passkey on another full name: %d %v", st, out)
	}
}

// The session lengths on /config: the defaults, a change in force at once, the bounds.
func TestSessionLengthsConfig(t *testing.T) {
	srv, a := webauthnServer(t, "")
	call(t, srv, rpHost, "POST", "/api/auth/v1/setup", `{"username":"admin","password":"secret123"}`, nil)
	admin := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)}
	st, out := call(t, srv, rpHost, "GET", "/api/auth/v1/config", "", admin)
	if st != 200 || out["session_idle"] != "30m0s" || out["session_max"] != "12h0m0s" {
		t.Fatalf("defaults: %d %v", st, out)
	}
	put := func(body string) (int, map[string]any) {
		return call(t, srv, rpHost, "PUT", "/api/auth/v1/config", body, admin)
	}
	st, out = put(`{"mode":"local","session_idle":"45m","session_max":"24h"}`)
	if st != 200 || out["session_idle"] != "45m0s" || out["session_max"] != "24h0m0s" {
		t.Fatalf("set: %d %v", st, out)
	}
	if idle, maxAge := a.Store.SessionLimits(); idle.Minutes() != 45 || maxAge.Hours() != 24 {
		t.Errorf("the store's limits: %s %s", idle, maxAge)
	}
	b, _ := os.ReadFile(a.ConfigFile)
	if !strings.Contains(string(b), `"session_idle": "45m"`) || !strings.Contains(string(b), `"session_max": "24h"`) {
		t.Errorf("occulited.json: %s", b)
	}
	// the cookie's lifetime follows
	st, cookies := cookieExchange(t, srv, "POST", "/api/auth/v1/login", `{"username":"admin","password":"secret123"}`, nil)
	if c := cookies[CookieName]; st != 200 || c == nil || c.MaxAge != 24*3600 {
		t.Errorf("cookie lifetime: %v", cookies[CookieName])
	}
	for _, body := range []string{
		`{"mode":"local","session_idle":"abc"}`,
		`{"mode":"local","session_idle":"1m"}`,
		`{"mode":"local","session_max":"91d"}`,
		`{"mode":"local","session_max":"10m"}`,
		`{"mode":"local","session_idle":"3h","session_max":"2h"}`,
	} {
		if st, out := put(body); st != 422 {
			t.Errorf("%s: %d %v, want 422", body, st, out)
		}
	}
	// absent fields keep what is stored; empty strings restore the defaults
	if st, out := put(`{"mode":"local"}`); st != 200 || out["session_idle"] != "45m0s" {
		t.Errorf("absent keeps: %d %v", st, out)
	}
	if st, out := put(`{"mode":"local","session_idle":"","session_max":""}`); st != 200 || out["session_idle"] != "30m0s" || out["session_max"] != "12h0m0s" {
		t.Errorf("empty restores the defaults: %d %v", st, out)
	}
}
