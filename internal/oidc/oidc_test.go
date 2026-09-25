package oidc

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// a fake provider: discovery, an authorize endpoint that redirects straight back with a code,
// a token endpoint that checks PKCE, userinfo with the claims of `userinfo`
func fakeProvider(t *testing.T, userinfo string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	var challenge string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Write([]byte(`{"issuer":"` + srv.URL + `","authorization_endpoint":"` + srv.URL + `/authorize","token_endpoint":"` + srv.URL + `/token","userinfo_endpoint":"` + srv.URL + `/userinfo"}`))
		case "/authorize":
			challenge = r.URL.Query().Get("code_challenge")
			if r.URL.Query().Get("code_challenge_method") != "S256" || r.URL.Query().Get("nonce") == "" {
				w.WriteHeader(400)
				return
			}
			u, _ := url.Parse(r.URL.Query().Get("redirect_uri"))
			q := u.Query()
			q.Set("code", "thecode")
			q.Set("state", r.URL.Query().Get("state"))
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
		case "/token":
			_ = r.ParseForm()
			if r.Form.Get("code") != "thecode" || r.Form.Get("code_verifier") == "" || r.Form.Get("client_secret") != "s3cret" || challenge == "" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			w.Write([]byte(`{"access_token":"at","token_type":"Bearer"}`))
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer at" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(userinfo))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var noRedirect = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// login runs the flow against the provider once and returns what Finish makes of it.
func login(t *testing.T, c *Client) (*Identity, error) {
	t.Helper()
	start, err := c.Start(context.Background(), "http://box/api/auth/v1/oidc/callback", "/addons/hmm/")
	if err != nil {
		t.Fatal(err)
	}
	// the browser follows the authorize redirect and comes back with code + state
	res, err := noRedirect.Get(start)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := url.Parse(res.Header.Get("Location"))
	if !strings.HasPrefix(loc.String(), "http://box/api/auth/v1/oidc/callback?") {
		t.Fatalf("redirect: %s", loc)
	}
	return c.Finish(context.Background(), loc.Query().Get("state"), loc.Query().Get("code"))
}

func TestFlow(t *testing.T) {
	srv := fakeProvider(t, `{"sub":"u-123","preferred_username":"basti","email":"s@example.org","groups":["ccu-admins","users"]}`)
	c := New(Config{Issuer: srv.URL, ClientID: "occulite", ClientSecret: "s3cret"})
	id, err := login(t, c)
	if err != nil {
		t.Fatal(err)
	}
	if id.Subject != "u-123" || id.Username != "basti" || id.Email != "s@example.org" || id.ReturnTo != "/addons/hmm/" {
		t.Errorf("%+v", id)
	}
	// a state is spent by its Finish, and an unknown one is refused
	start, _ := c.Start(context.Background(), "http://box/cb", "")
	res, _ := noRedirect.Get(start)
	loc, _ := url.Parse(res.Header.Get("Location"))
	if _, err := c.Finish(context.Background(), loc.Query().Get("state"), "thecode"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Finish(context.Background(), loc.Query().Get("state"), "thecode"); err == nil {
		t.Error("state reused")
	}
	if _, err := c.Finish(context.Background(), "nope", "thecode"); err == nil {
		t.Error("unknown state accepted")
	}
}

// task 19 (D-53): the user name is the claim's value, verbatim. Nothing is folded, cleaned or
// taken from another claim: an account is matched by exactly this string, or not at all.
func TestUsernameClaimVerbatim(t *testing.T) {
	cases := []struct {
		userinfo, claim, want, err string
	}{
		{`{"sub":"u-1","preferred_username":"Sebastian Raff"}`, "", "Sebastian Raff", ""},
		{`{"sub":"u-1","preferred_username":"basti","email":"s@example.org"}`, "email", "s@example.org", ""},
		{`{"sub":"u-1","email":"s@example.org"}`, "", "", `the claim "preferred_username" is missing`},
		{`{"sub":"u-1","preferred_username":""}`, "", "", `the claim "preferred_username" is missing`},
		{`{"sub":"u-1","preferred_username":["basti"]}`, "", "", "not a string"},
		{`{"sub":"u-1","nickname":"alice"}`, "nickname", "alice", ""},
		{`{"preferred_username":"basti"}`, "", "", "no subject"},
	}
	for _, tc := range cases {
		srv := fakeProvider(t, tc.userinfo)
		id, err := login(t, New(Config{Issuer: srv.URL, ClientID: "occulite", ClientSecret: "s3cret", UsernameClaim: tc.claim}))
		switch {
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: err %v, want %q", tc.userinfo, err, tc.err)
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v", tc.userinfo, err)
		case tc.err == "" && id.Username != tc.want:
			t.Errorf("%s: user name %q, want %q", tc.userinfo, id.Username, tc.want)
		}
	}
}

func TestJWTClaim(t *testing.T) {
	tok := "x." + base64.RawURLEncoding.EncodeToString([]byte(`{"nonce":"n1","sub":"a"}`)) + ".y"
	if jwtClaim(tok, "nonce") != "n1" || jwtClaim("garbage", "nonce") != nil {
		t.Error("claim")
	}
}

// openccu-lite task 230: a provider with a certificate the system does not know - the test fails
// on the system's pool, passes with the certificate added, and names the chain it verified; the
// running client takes the roots and fetches the discovery again; the peer's chain is read
// without verification for the administrator to compare.
func TestCheckAndRoots(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/a", "token_endpoint": srv.URL + "/t", "userinfo_endpoint": srv.URL + "/u"})
	}))
	defer srv.Close()
	ctx := context.Background()
	if r := Check(ctx, srv.URL, nil); r.OK || !strings.Contains(r.Error, "certificate") {
		t.Fatalf("the system's pool: %+v", r)
	}
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	r := Check(ctx, srv.URL+"/", pool)
	if !r.OK || r.IssuerMismatch || r.TokenEndpoint != srv.URL+"/t" || len(r.Verified) == 0 || !r.Verified[len(r.Verified)-1].Equal(srv.Certificate()) {
		t.Fatalf("with the certificate: %+v", r)
	}
	if r := Check(ctx, srv.URL+"/other", pool); r.OK || !strings.Contains(r.Error, "HTTP 404") {
		t.Errorf("a wrong path: %+v", r)
	}
	c := New(Config{Issuer: srv.URL, ClientID: "x"})
	if _, err := c.discover(ctx); err == nil {
		t.Fatal("the running client trusted an unknown certificate")
	}
	c.SetRootCAs(pool)
	if _, err := c.discover(ctx); err != nil {
		t.Fatalf("after SetRootCAs: %v", err)
	}
	c.SetRootCAs(nil)
	if _, err := c.discover(ctx); err == nil {
		t.Fatal("the discovery was kept across a change of roots")
	}
	chain, err := PeerChain(ctx, srv.URL)
	if err != nil || len(chain) == 0 || !chain[0].Equal(srv.Certificate()) {
		t.Fatalf("peer chain: %v %v", chain, err)
	}
	if _, err := PeerChain(ctx, "http://example.org"); err == nil {
		t.Error("a plain http issuer has no chain")
	}
}
