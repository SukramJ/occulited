package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/meta"
	"github.com/hobbyquaker/occulited/internal/system"
)

// task 269: a change of the system names its caller in the journal at the default level - the
// handler's own line through reqLog, or the middleware's "api: change" when the handler wrote
// none - and the noisy routes (the RPC proxy, the metadata) stay out of Info.
func TestChangeLinesNameTheCaller(t *testing.T) {
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	ms, err := meta.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := logRoot(t)
	svc := scriptBox{system.AddonScripts{Root: r}}
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store}
	a.Register(mux)
	(&MetaAPI{Store: ms, Root: string(r)}).Register(mux)
	(&SystemAPI{Root: r, Services: svc, Log: testLog, Addons: svc, Manager: svc, Nav: svc, Run: system.DryRunner}).Register(mux)
	const p = "/api/audit-test" // outside the three APIs: TestRouteTable leaves another test's fixture alone
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/silent", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/own-line", func(w http.ResponseWriter, r *http.Request) {
		reqLog(r).Info("audit-test: switched", "on", true)
		writeJSON(w, 200, map[string]any{"ok": true})
	})
	route(mux, auth.ScopeSystemWrite, "DELETE "+p+"/refused", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid"})
	})
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/bad", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad-request"})
	})
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/conflict", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusConflict, apiError{Error: "conflict"})
	})
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/forbidden", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "forbidden"})
	})
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/failed", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal"})
	})
	srv := httptest.NewServer(a.Middleware(mux))
	defer srv.Close()

	st, out, _ := do(t, srv, "POST", "/api/auth/v1/login", `{"username":"admin","password":"secret123"}`, nil)
	sid, _ := out["sid"].(string)
	if st != 200 || sid == "" {
		t.Fatalf("login: %d %v", st, out)
	}
	hdr := map[string]string{"Authorization": "Bearer " + sid, "X-Forwarded-For": "198.51.100.23"}
	reader, err := store.CreateToken("reader", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeSystemRead}})
	if err != nil {
		t.Fatal(err)
	}
	readerHdr := map[string]string{"Authorization": "Bearer " + reader, "X-Forwarded-For": "198.51.100.24"}
	debugLines := func(msg string) int {
		n := 0
		for _, l := range strings.Split(logs.String(), "\n") {
			if strings.Contains(l, "level=DEBUG") && strings.Contains(l, msg) {
				n++
			}
		}
		return n
	}
	infoLines := func() []string {
		var ls []string
		for _, l := range strings.Split(logs.String(), "\n") {
			if strings.Contains(l, "level=INFO") {
				ls = append(ls, l)
			}
		}
		return ls
	}

	for _, c := range []struct {
		name, method, path, body string
		want                     []string // the one Info line; nil: none
		reader                   bool     // with a token of system:read alone
		debug                    bool     // the middleware's line at Debug
	}{
		{"a change without a line of its own", "POST", p + "/silent", "{}",
			[]string{`msg="api: change"`, "method=POST", "route=" + p + "/silent", "status=200", "user=admin", "remote=198.51.100.23"}, false, false},
		{"a change with its own line", "PUT", p + "/own-line", "{}",
			[]string{`msg="audit-test: switched"`, "on=true", "user=admin", "remote=198.51.100.23"}, false, false},
		{"a failed change", "POST", p + "/failed", "{}",
			[]string{`msg="api: change"`, "status=500", "user=admin", "remote=198.51.100.23"}, false, false},
		{"a refused change: nothing changed", "DELETE", p + "/refused", "", nil, false, true},
		{"a bad request: Debug", "POST", p + "/bad", "{}", nil, false, true},
		{"a conflict: Debug", "PUT", p + "/conflict", "{}", nil, false, true},
		{"a change the handler refused with 403: Info", "POST", p + "/forbidden", "{}",
			[]string{`msg="api: change refused"`, "method=POST", "route=" + p + "/forbidden", "status=403", "user=admin", "remote=198.51.100.23"}, false, false},
		{"a change the scope check refused: Info", "POST", p + "/silent", "{}",
			[]string{`msg="api: change refused"`, "method=POST", "route=" + p + "/silent", "status=403", "user=token:reader", "remote=198.51.100.24"}, true, false},
		{"the RPC proxy refused by the scope check: not Info", "POST", "/api/rpc/v1/json/BidCos-RF", `{"jsonrpc":"2.0","id":1,"method":"setValue","params":[]}`, nil, true, false},
		{"a read", "GET", "/api/system/v1/status", "", nil, false, false},
		{"the RPC proxy: setValue", "POST", "/api/rpc/v1/json/BidCos-RF", `{"jsonrpc":"2.0","id":1,"method":"setValue","params":["ABC0000001:1","STATE",true]}`, nil, false, false},
		{"the RPC proxy: XML-RPC", "POST", "/api/rpc/v1/xmlrpc/BidCos-RF", `<?xml version="1.0"?><methodCall><methodName>setValue</methodName></methodCall>`, nil, false, false},
		{"the metadata", "PATCH", "/api/meta/v1/objects/BidCos-RF.X%3A1", `{"name":"Kitchen"}`, nil, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs.Reset()
			h := hdr
			if c.reader {
				h = readerHdr
			}
			st, _, raw := do(t, srv, c.method, c.path, c.body, h)
			// past the middleware: the handler answered, whatever it made of the body
			if !c.reader && (st == 401 || st == 404 || (st == 403 && !strings.HasSuffix(c.path, "/forbidden"))) {
				t.Fatalf("the request did not reach its handler: %d %s", st, raw)
			}
			if c.reader && st != 403 {
				t.Fatalf("the scope check let it through: %d %s", st, raw)
			}
			if c.debug && debugLines(`msg="api: change"`) != 1 {
				t.Errorf("want the middleware's line at Debug:\n%s", logs.String())
			}
			got := infoLines()
			if c.want == nil {
				if len(got) != 0 {
					t.Fatalf("Info lines, want none:\n%s", strings.Join(got, "\n"))
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want one Info line, got %d:\n%s", len(got), logs.String())
			}
			for _, w := range c.want {
				if !strings.Contains(got[0], w) {
					t.Errorf("%q not in %s", w, got[0])
				}
			}
		})
	}
}

// the table: which of the real routes count as changes. The RPC proxies, the metadata, the LED's
// live state and the previews are quiet; every quiet route named exists.
func TestAuditedRoutes(t *testing.T) {
	fullAPI(t)
	table := RouteScopes()
	for pattern := range quietRoutes {
		if _, ok := table[pattern]; !ok {
			t.Errorf("quiet route %q is not in the route table", pattern)
		}
	}
	for pattern, want := range map[string]bool{
		"POST /api/system/v1/network":                   true,
		"POST /api/system/v1/services/{id}/{action}":    true,
		"PUT /api/system/v1/services/{id}/unit":         true,
		"POST /api/system/v1/firewall/apply":            true,
		"POST /api/system/v1/storage/shares":            true,
		"PUT /api/system/v1/backup/schedule":            true,
		"POST /api/system/v1/trust/{store}":             true,
		"POST /api/system/v1/radio/restart":             true,
		"POST /api/system/v1/addons/{id}/uninstall":     true,
		"POST /api/system/v1/system-update/install":     true,
		"POST /api/system/v1/reboot":                    true,
		"POST /api/auth/v1/users":                       true,
		"POST /api/auth/v1/password":                    true,
		"POST /api/rpc/v1/json/{interface}":             false,
		"POST /api/rpc/v1/xmlrpc/{interface}":           false,
		"PATCH /api/meta/v1/objects/{ref}":              false,
		"POST /api/system/v1/led/preview":               false,
		"GET /api/system/v1/network":                    false,
		"POST /api/system/v1/radio/connections/preview": false,
	} {
		scopes, ok := table[pattern]
		if !ok {
			t.Errorf("%q is not in the route table", pattern)
			continue
		}
		m, _, _ := strings.Cut(pattern, " ")
		if got := audited(m, pattern, scopes); got != want {
			t.Errorf("audited(%q) = %v, want %v", pattern, got, want)
		}
	}
}
