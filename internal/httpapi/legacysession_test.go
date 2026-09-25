package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

// Task 125 (D-77): which addons get the session's ?sid=@..@ alias in their URLs - the ones the box
// cannot vouch for reading the header, while the global switch and the addon's own are on - and
// the Status warning that names them.

// memSwitches is LegacySessions in memory.
type memSwitches struct {
	on  bool
	off []string
}

func (m *memSwitches) LegacySession() (bool, []string) { return m.on, m.off }
func (m *memSwitches) SetLegacySession(on bool, off []string) error {
	m.on, m.off = on, off
	return nil
}

// legacyBox is a box with RedMatic 9.7.3 (its manifest declares the header), Mosquitto (does not)
// and hmm at beta.15 (a version before its manifest), RedMatic's frontend at /addons/red/, and an
// admin and a user logged in.
func legacyBox(t *testing.T) (*httptest.Server, *SystemAPI, *memSwitches, map[string]string, map[string]string) {
	t.Helper()
	root := system.Root(t.TempDir())
	w := func(p, c string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(string(root), p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	w("usr/local/etc/config/rc.d/redmatic", "#!/bin/sh\ncase $1 in info) printf 'Name: RedMatic\\nVersion: 9.7.3\\nConfig-Url: /addons/redmatic/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/rc.d/mosquitto", "#!/bin/sh\ncase $1 in info) printf 'Name: Mosquitto\\nVersion: 2.1.2+3\\nConfig-Url: /addons/mosquitto/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/rc.d/hmm", "#!/bin/sh\ncase $1 in info) printf 'Name: Homematic Manager\\nVersion: 3.0.0-beta.15\\nConfig-Url: /addons/hmm/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/lighttpd/redmatic.conf", "$HTTP[\"url\"] =~ \"^/(addons/red/).*\" {\n  proxy.server = (\"/addons/red/\" => (( \"host\" => \"127.0.0.1\", \"port\" => 1880 )))\n}\n", 0o644)
	storeManifest(t, root, "redmatic", `{"format": 1, "id": "redmatic", "name": "RedMatic", "ui": {"session_header": true}}`)
	svc := scriptBox{system.AddonScripts{Root: root}}
	sw := &memSwitches{on: true}
	store, err := auth.Open(t.TempDir(), auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser("bob", "bobsecret1", auth.RoleUser, false); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &SystemAPI{Root: root, Addons: svc, Nav: svc, Legacy: sw}
	a.Register(mux)
	a.Warnings = &warnings.Tracker{Sources: a.warningSources()}
	auth := &AuthAPI{Store: store}
	auth.Register(mux)
	srv := httptest.NewServer(auth.Middleware(mux))
	t.Cleanup(srv.Close)
	admin := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"admin","password":"secret123"}`)}
	user := map[string]string{"Cookie": CookieName + "=" + cookieOf(t, srv, `{"username":"bob","password":"bobsecret1"}`)}
	return srv, a, sw, admin, user
}

func TestLegacySessionPerAddon(t *testing.T) {
	srv, a, sw, admin, user := legacyBox(t)
	// who gets the alias: an addon without the header for its installed version, while on
	legacyOf := func() map[string]bool {
		t.Helper()
		_, out, raw := do(t, srv, "GET", "/api/system/v1/addons", "", user)
		got := map[string]bool{}
		for _, x := range out["addons"].([]any) {
			m := x.(map[string]any)
			got[m["id"].(string)] = m["legacy_session"] == true
		}
		_, out, raw2 := do(t, srv, "GET", "/api/system/v1/nav", "", user)
		for _, x := range out["entries"].([]any) {
			m := x.(map[string]any)
			got["nav:"+m["id"].(string)] = m["legacy_session"] == true
		}
		_, _ = raw, raw2
		return got
	}
	want := func(name string, exp map[string]bool) {
		t.Helper()
		got := legacyOf()
		for k, v := range exp {
			if got[k] != v {
				t.Errorf("%s: %s legacy_session %v, want %v", name, k, got[k], v)
			}
		}
	}
	want("the default", map[string]bool{"redmatic": false, "mosquitto": true, "hmm": true, "nav:red": false})
	names := func() []string {
		var ids []string
		for _, w := range a.Warnings.Evaluate(context.Background()) {
			if w.ID == "legacy-session" {
				for _, x := range w.Params["addons"].([]warnAddon) {
					ids = append(ids, x.ID)
				}
				if w.Href != "/addons" || w.Severity != warnings.SeverityWarning || w.Variant != strings.Join(ids, ",") {
					t.Errorf("warning %+v", w)
				}
			}
		}
		return ids
	}
	if got := names(); !slices.Equal(got, []string{"hmm", "mosquitto"}) {
		t.Errorf("the warning names %v", got)
	}

	// the per-addon switch, administrators only; the global one untouched
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/addons/mosquitto/legacy-session", `{"enabled":false}`, user); st != 403 {
		t.Errorf("a user switched: %d", st)
	}
	st, out, _ := do(t, srv, "PUT", "/api/system/v1/addons/mosquitto/legacy-session", `{"enabled":false}`, admin)
	if st != 200 || out["enabled"] != true || out["addon_enabled"] != false || out["id"] != "mosquitto" {
		t.Fatalf("switch off: %d %v", st, out)
	}
	if !sw.on || !slices.Equal(sw.off, []string{"mosquitto"}) {
		t.Errorf("stored: %v %v", sw.on, sw.off)
	}
	want("mosquitto off", map[string]bool{"redmatic": false, "mosquitto": false, "hmm": true})
	if got := names(); !slices.Equal(got, []string{"hmm"}) {
		t.Errorf("the warning names %v", got)
	}
	if st, out, _ := do(t, srv, "PUT", "/api/system/v1/addons/mosquitto/legacy-session", `{"enabled":true}`, admin); st != 200 || out["addon_enabled"] != true || len(out["off"].([]any)) != 0 {
		t.Errorf("switch on again: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/addons/../x/legacy-session", `{"enabled":false}`, admin); st != 404 && st != 422 {
		t.Errorf("a bad id: %d", st)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/addons/x%2Fy/legacy-session", `{"enabled":false}`, admin); st != 422 && st != 404 {
		t.Errorf("a bad id: %d", st)
	}

	// the global switch: off for everyone, and the warning goes away
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/legacy-session", `{"enabled":false}`, user); st != 403 {
		t.Errorf("a user switched globally: %d", st)
	}
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/legacy-session", `{"enabled":false}`, admin)
	if st != 200 || out["enabled"] != false {
		t.Fatalf("global off: %d %v", st, out)
	}
	want("global off", map[string]bool{"redmatic": false, "mosquitto": false, "hmm": false, "nav:red": false})
	if got := names(); len(got) != 0 {
		t.Errorf("the warning stayed: %v", got)
	}
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/legacy-session", "", user); st != 200 || out["enabled"] != false {
		t.Errorf("GET: %d %v", st, out)
	}
	// on again with a list
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/legacy-session", `{"enabled":true,"off":["hmm","hmm","../x",""]}`, admin)
	if st != 200 || out["enabled"] != true || len(out["off"].([]any)) != 1 || out["off"].([]any)[0] != "hmm" {
		t.Fatalf("global on with a list: %d %v", st, out)
	}
	want("hmm off", map[string]bool{"mosquitto": true, "hmm": false})

	// without switches: on for every undeclared addon, the routes 501
	a.Legacy = nil
	want("no switches", map[string]bool{"redmatic": false, "mosquitto": true, "hmm": true})
	if st, _, _ := do(t, srv, "GET", "/api/system/v1/legacy-session", "", admin); st != 501 {
		t.Errorf("GET without switches: %d", st)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/legacy-session", `{"enabled":false}`, admin); st != 501 {
		t.Errorf("PUT without switches: %d", st)
	}
}

// The nav entries: an older RedMatic's frontend gets the alias like its settings page, and a nav.d
// page that is no addon gets it by the global switch alone.
func TestLegacySessionNav(t *testing.T) {
	root := system.Root(t.TempDir())
	w := func(p, c string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(string(root), p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	w("usr/local/etc/config/rc.d/redmatic", "#!/bin/sh\ncase $1 in info) printf 'Name: RedMatic\\nVersion: 9.7.2\\nConfig-Url: /addons/redmatic/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/lighttpd/redmatic.conf", "$HTTP[\"url\"] =~ \"^/(addons/red/).*\" {\n  proxy.server = (\"/addons/red/\" => (( \"host\" => \"127.0.0.1\", \"port\" => 1880 )))\n}\n", 0o644)
	w("usr/local/etc/config/nav.d/docs.json", `{"id": "docs", "label": "Docs", "href": "/addons/docs/", "target": "iframe", "order": 10}`, 0o644)
	w("usr/local/etc/config/nav.d/ext.json", `{"id": "ext", "label": "Ext", "href": "https://example.org/", "target": "blank", "order": 20}`, 0o644)
	// an installed addon whose own nav.d drop-in points at a CGI lighttpd serves itself (no proxy
	// drop-in): a page that lives by the CCU convention, so it gets the alias
	w("usr/local/etc/config/rc.d/cuxd", "#!/bin/sh\ncase $1 in info) printf 'Name: CUxD\\nVersion: 2.11\\nConfig-Url: /addons/cuxd/index.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/nav.d/cuxd.json", `{"id": "cuxd", "label": "CUxD", "href": "/addons/cuxd/index.cgi", "target": "iframe", "order": 30}`, 0o644)
	svc := scriptBox{system.AddonScripts{Root: root}}
	sw := &memSwitches{on: true}
	mux := http.NewServeMux()
	(&SystemAPI{Root: root, Addons: svc, Nav: svc, Legacy: sw}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	nav := func() map[string]bool {
		_, out, _ := do(t, srv, "GET", "/api/system/v1/nav", "", nil)
		got := map[string]bool{}
		for _, x := range out["entries"].([]any) {
			m := x.(map[string]any)
			got[m["id"].(string)] = m["legacy_session"] == true
		}
		return got
	}
	// B-133: RedMatic's frontend is a page its own server answers behind lighttpd's proxy - it has
	// the gate's header and the API refuses the alias, so the shell opens it without ?sid= even with
	// every switch on; the nav.d page under /addons/ and the addon's CGI page keep the alias
	if got := nav(); got["red"] || !got["docs"] || !got["cuxd"] || got["ext"] {
		t.Errorf("on: %v", got)
	}
	sw.off = []string{"cuxd"}
	if got := nav(); got["red"] || got["cuxd"] || !got["docs"] {
		t.Errorf("cuxd off: %v", got)
	}
	sw.on, sw.off = false, nil
	if got := nav(); got["red"] || got["docs"] || got["cuxd"] || got["ext"] {
		t.Errorf("global off: %v", got)
	}
}

// B-133 as found on the Charly: Homematic Manager's frontend, proxied to its own server, was opened
// with ?sid=@alias@; it checked the alias against the API, which refuses it (D-77), and sent the
// frame to the box's page. GET /nav must not mark such a frontend legacy_session, while the same
// addon's settings page (a CGI lighttpd serves itself, which asks tclrega) keeps the alias - and the
// Status warning still names the addon for that page.
func TestLegacySessionProxiedFrontend(t *testing.T) {
	root := system.Root(t.TempDir())
	w := func(p, c string, mode os.FileMode) {
		t.Helper()
		full := filepath.Join(string(root), p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), mode); err != nil {
			t.Fatal(err)
		}
	}
	w("usr/local/etc/config/rc.d/hmm", "#!/bin/sh\ncase $1 in info) printf 'Name: Homematic Manager\\nVersion: 3.0.0-beta.15\\nConfig-Url: /addons/hmm/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/lighttpd/hmm.conf", "$HTTP[\"url\"] =~ \"^/addons/hmm/(?!settings\\.cgi|service\\.cgi)\" {\n    proxy.server = (\"\" => ((\"host\" => \"127.0.0.1\", \"port\" => 8090)))\n}\n", 0o644)
	svc := scriptBox{system.AddonScripts{Root: root}}
	sw := &memSwitches{on: true}
	api := &SystemAPI{Root: root, Addons: svc, Nav: svc, Legacy: sw}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, out, _ := do(t, srv, "GET", "/api/system/v1/nav", "", nil)
	entries := out["entries"].([]any)
	if len(entries) != 1 {
		t.Fatalf("entries: %v", entries)
	}
	e := entries[0].(map[string]any)
	if e["href"] != "/addons/hmm/" || e["legacy_session"] == true {
		t.Errorf("the proxied frontend must not get the alias: %v", e)
	}
	_, out, _ = do(t, srv, "GET", "/api/system/v1/addons", "", nil)
	ad := out["addons"].([]any)[0].(map[string]any)
	if ad["config_url"] != "/addons/hmm/settings.cgi" || ad["legacy_session"] != true {
		t.Errorf("the settings CGI keeps the alias: %v", ad)
	}
	ws, _ := api.legacySessionWarning(context.Background())
	if len(ws) != 1 || ws[0].Variant != "hmm" {
		t.Errorf("the warning names hmm for its settings page: %+v", ws)
	}
	// with the settings page's alias switched off for hmm nothing of hmm receives it
	sw.off = []string{"hmm"}
	if ws, _ := api.legacySessionWarning(context.Background()); len(ws) != 0 {
		t.Errorf("no warning once nothing receives the alias: %+v", ws)
	}
}
