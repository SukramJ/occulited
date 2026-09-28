package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// openccu-lite task 213: the one cross-site rule (the gate's Lua mirrors it).
func TestCrossSite(t *testing.T) {
	nav := map[string]string{"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}
	with := func(base map[string]string, kv ...string) map[string]string {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name     string
		method   string
		hdr      map[string]string
		navigate bool
		refused  bool
	}{
		{"same-origin POST", "POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, false, false},
		{"a typed URL", "GET", map[string]string{"Sec-Fetch-Site": "none"}, false, false},
		{"cross-site POST", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, false, true},
		{"same-site POST (another port)", "POST", map[string]string{"Sec-Fetch-Site": "same-site"}, false, true},
		{"cross-site form POST as a navigation", "POST", with(nav, "Sec-Fetch-Site", "cross-site"), false, true},
		{"cross-site link", "GET", with(nav, "Sec-Fetch-Site", "cross-site"), true, false},
		{"same-site link", "GET", with(nav, "Sec-Fetch-Site", "same-site"), true, false},
		{"cross-site frame", "GET", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "iframe"}, false, true},
		{"cross-site image", "GET", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, false, true},
		{"no Sec-Fetch-Site, foreign Origin", "POST", map[string]string{"Origin": "https://evil.example"}, false, true},
		{"no Sec-Fetch-Site, Origin null", "POST", map[string]string{"Origin": "null"}, false, true},
		{"no Sec-Fetch-Site, own Origin", "PUT", map[string]string{"Origin": "https://BOX.lan"}, false, false},
		{"no Sec-Fetch-Site nor Origin, foreign Referer", "DELETE", map[string]string{"Referer": "https://evil.example/x"}, false, true},
		{"no Sec-Fetch-Site nor Origin, own Referer", "PATCH", map[string]string{"Referer": "https://box.lan/system"}, false, false},
		{"a program: no header", "POST", nil, false, false},
		{"an old browser's GET", "GET", map[string]string{"Origin": "https://evil.example"}, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "https://box.lan/addons/hmm/settings.cgi", nil)
			for k, v := range c.hdr {
				r.Header.Set(k, v)
			}
			v := crossSite(r)
			if v.navigate != c.navigate || (v.refuse != "") != c.refused {
				t.Errorf("got %+v", v)
			}
		})
	}
}

// The API refuses a cross-site state change made with the cookie, and nothing else: the same call
// with the session as a bearer, a token's, a same-origin one and a cross-site GET go through. The
// addon CGIs: refused from elsewhere, a link opens the page without its query.
func TestCrossSiteRoutes(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Setup("admin", "correct horse battery")
	sess, _ := store.Login("admin", "correct horse battery", "127.0.0.1", "test")
	tok, err := store.CreateToken("t213", auth.TokenOptions{Scopes: auth.RoleScopes(auth.RoleAdmin)})
	if err != nil {
		t.Fatal(err)
	}
	a := &AuthAPI{Store: store}
	mux := http.NewServeMux()
	a.Register(mux)
	route(mux, auth.ScopeSystemWrite, "POST /api/x", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	route(mux, auth.ScopeSystemRead, "GET /api/x", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"ok": true}) })
	mux.Handle("/addons/", a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("addon " + r.URL.RawQuery)) })))
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, hdr map[string]string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := noRedirect.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	cookie := "occulite_session=" + sess.ID
	xs := map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
	for _, c := range []struct {
		name   string
		method string
		path   string
		hdr    map[string]string
		want   int
		loc    string
	}{
		{"API: cross-site POST with the cookie", "POST", "/api/x", xs, 403, ""},
		{"API: same-site POST with the cookie", "POST", "/api/x", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-site"}, 403, ""},
		{"API: foreign Origin, no Sec-Fetch-Site", "POST", "/api/x", map[string]string{"Cookie": cookie, "Origin": "https://evil.example"}, 403, ""},
		{"API: logout from elsewhere", "POST", "/api/auth/v1/logout", xs, 403, ""},
		{"API: same-origin POST with the cookie and the header", "POST", "/api/x", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-origin", RequestHeader: "1"}, 200, ""},
		// task 259: same-origin, but without the header credential - a form post from the shell's own origin would look like this
		{"API: same-origin POST with the cookie alone", "POST", "/api/x", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-origin"}, 403, ""},
		{"API: cross-site POST with the session as a bearer", "POST", "/api/x", map[string]string{"Authorization": "Bearer " + sess.ID, "Sec-Fetch-Site": "cross-site"}, 200, ""},
		{"API: a token from anywhere", "POST", "/api/x", map[string]string{"Authorization": "Bearer " + tok, "Sec-Fetch-Site": "cross-site"}, 200, ""},
		{"API: a cross-site GET with the cookie", "GET", "/api/x", xs, 200, ""},
		{"API: a program with the cookie, no browser headers, no header credential", "POST", "/api/x", map[string]string{"Cookie": cookie}, 403, ""},
		{"API: a program with the cookie and the header credential", "POST", "/api/x", map[string]string{"Cookie": cookie, RequestHeader: "1"}, 200, ""},
		{"API: a program with the gate cookie: no credential", "POST", "/api/x", map[string]string{"Cookie": "occulite_gate=" + sess.ID, RequestHeader: "1"}, 401, ""},
		{"addon: cross-site POST", "POST", "/addons/hmm/service.cgi", xs, 403, ""},
		{"addon: cross-site image GET", "GET", "/addons/hmm/service.cgi?cmd=stop", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}, 403, ""},
		{"addon: a link with a query opens the bare page", "GET", "/addons/hmm/settings.cgi?cmd=config&auth=off", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 302, "/addons/hmm/settings.cgi"},
		{"addon: a link without a query", "GET", "/addons/hmm/", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 200, ""},
		{"addon: its own page's POST", "POST", "/addons/hmm/settings.cgi", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "same-origin"}, 200, ""},
		// task 259: the gate cookie is what a browser carries to /addons/, under the same rule; a form post needs no header credential there
		{"addon: its own page's POST with the gate cookie", "POST", "/addons/hmm/settings.cgi", map[string]string{"Cookie": "occulite_gate=" + sess.ID, "Sec-Fetch-Site": "same-origin"}, 200, ""},
		{"addon: cross-site POST with the gate cookie", "POST", "/addons/hmm/service.cgi", map[string]string{"Cookie": "occulite_gate=" + sess.ID, "Sec-Fetch-Site": "cross-site"}, 403, ""},
		{"addon: a link with a query and the gate cookie opens the bare page", "GET", "/addons/hmm/settings.cgi?cmd=config", map[string]string{"Cookie": "__Secure-occulite_gate=" + sess.ID, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"}, 302, "/addons/hmm/settings.cgi"},
		{"addon: a typed URL with a query", "GET", "/addons/hmm/settings.cgi?cmd=config", map[string]string{"Cookie": cookie, "Sec-Fetch-Site": "none"}, 200, ""},
		{"addon: a token from anywhere", "POST", "/addons/hmm/x.cgi", map[string]string{"Authorization": "Bearer " + tok, "Sec-Fetch-Site": "cross-site"}, 200, ""},
	} {
		st, loc := call(c.method, c.path, c.hdr)
		if st != c.want || loc != c.loc {
			t.Errorf("%s: %d %q, want %d %q", c.name, st, loc, c.want, c.loc)
		}
	}
}
