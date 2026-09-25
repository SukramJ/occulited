package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

func storePolicy(t *testing.T, root system.Root, p system.AddonPolicy) {
	t.Helper()
	dir := filepath.Join(string(root), system.AddonPolicyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(dir, p.ID+".json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// storeManifest writes an addon's stored manifest, as its install would (D-119).
func storeManifest(t *testing.T, root system.Root, id, body string) {
	t.Helper()
	dir := filepath.Join(string(root), system.AddonPolicyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+system.AddonManifestSuffix), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Task 88 (D-67, D-119): ?sid= stops only for an addon whose stored manifest declares the header;
// a policy the old catalogue wrote still counts with its header_since version; everything else keeps it.
func TestSessionHeader(t *testing.T) {
	header := &system.AddonRuntime{Session: &system.AddonSession{HeaderSince: "9.7.3"}}
	declares := `{"format": 1, "id": "redmatic", "name": "RedMatic", "ui": {"session_header": true}}`
	cases := []struct {
		name              string
		manifest          string // "" for none
		policy            *system.AddonPolicy
		id, version, path string
		want              bool
	}{
		{"the manifest declares it", declares, nil, "redmatic", "9.7.3", "/addons/red/", true},
		{"RedMatic's settings page", declares, nil, "redmatic", "9.7.3", "/addons/redmatic/settings.cgi", true},
		{"the manifest is per version: any installed version counts", declares, nil, "redmatic", "git-a1b2c3d", "/addons/red/", true},
		{"a manifest without the field keeps ?sid=", `{"format": 1, "id": "mosquitto", "name": "Mosquitto"}`, nil, "mosquitto", "2.1.2+3", "/addons/mosquitto/settings.cgi", false},
		{"a manifest wins over an old policy block", `{"format": 1, "id": "redmatic", "name": "RedMatic"}`, &system.AddonPolicy{ID: "redmatic", Mode: "confined", Source: "catalog", Runtime: header}, "redmatic", "9.7.3", "/addons/red/", false},
		{"an unknown version keeps ?sid=", declares, nil, "redmatic", "", "/addons/red/", false},
		{"a path outside /addons/ keeps ?sid=", declares, nil, "redmatic", "9.7.3", "/red/", false},
		{"no manifest, no policy keeps ?sid=", "", nil, "cuxd", "9.7.3", "/addons/cuxd/", false},
		// the old catalogue's block in the policy, for an addon installed before the manifest
		{"the old block, a new enough RedMatic", "", &system.AddonPolicy{ID: "redmatic", Mode: "confined", Source: "catalog", Runtime: header}, "redmatic", "9.7.3", "/addons/red/", true},
		{"the old block, a newer RedMatic", "", &system.AddonPolicy{ID: "redmatic", Mode: "confined", Source: "catalog", Runtime: header}, "redmatic", "9.10.0", "/addons/red/", true},
		{"the old block, an older RedMatic keeps ?sid=", "", &system.AddonPolicy{ID: "redmatic", Mode: "confined", Source: "catalog", Runtime: header}, "redmatic", "9.7.2", "/addons/red/", false},
		{"the old block without the field keeps ?sid=", "", &system.AddonPolicy{ID: "redmatic", Mode: "confined", Source: "catalog", Runtime: &system.AddonRuntime{}}, "redmatic", "9.7.3", "/addons/red/", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := system.Root(t.TempDir())
			if c.policy != nil {
				storePolicy(t, root, *c.policy)
			}
			if c.manifest != "" {
				storeManifest(t, root, c.id, c.manifest)
			}
			if got := SessionHeader(root, c.id, c.version, c.path); got != c.want {
				t.Errorf("SessionHeader = %v, want %v", got, c.want)
			}
		})
	}
}

// GET /nav and GET /addons carry session_header where the shell leaves ?sid= off, from the installed
// addon's stored manifest: RedMatic has it for its frontend and its settings page, Mosquitto (no
// declaration) has neither, and the installed version never leaks into the nav answer.
func TestNavAndAddonsSessionHeader(t *testing.T) {
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
	version := "9.7.3"
	w("usr/local/etc/config/rc.d/redmatic", "#!/bin/sh\ncase $1 in info) printf 'Name: RedMatic\\nVersion: "+version+"\\nConfig-Url: /addons/redmatic/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/rc.d/mosquitto", "#!/bin/sh\ncase $1 in info) printf 'Name: Mosquitto\\nVersion: 2.1.2+3\\nConfig-Url: /addons/mosquitto/settings.cgi\\n';; esac\n", 0o755)
	w("usr/local/etc/config/lighttpd/redmatic.conf", "$HTTP[\"url\"] =~ \"^/(addons/red/).*\" {\n  proxy.server = (\"/addons/red/\" => (( \"host\" => \"127.0.0.1\", \"port\" => 1880 )))\n}\n", 0o644)
	svc := scriptBox{system.AddonScripts{Root: root}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: root, Addons: svc, Nav: svc}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	check := func(wantHeader bool) {
		t.Helper()
		st, out, raw := do(t, srv, "GET", "/api/system/v1/nav", "", nil)
		entries, _ := out["entries"].([]any)
		if st != 200 || len(entries) != 1 {
			t.Fatalf("nav: %d %s", st, raw)
		}
		red := entries[0].(map[string]any)
		if red["id"] != "red" || red["addon"] != "redmatic" || (red["session_header"] == true) != wantHeader {
			t.Errorf("nav entry %v, session_header want %v", red, wantHeader)
		}
		if strings.Contains(raw, "AddonVersion") || strings.Contains(raw, version) {
			t.Errorf("the installed version is part of the nav answer: %s", raw)
		}
		st, out, raw = do(t, srv, "GET", "/api/system/v1/addons", "", nil)
		addons, _ := out["addons"].([]any)
		if st != 200 || len(addons) != 2 {
			t.Fatalf("addons: %d %s", st, raw)
		}
		for _, x := range addons {
			a := x.(map[string]any)
			want := a["id"] == "redmatic" && wantHeader
			if (a["session_header"] == true) != want {
				t.Errorf("addon %v: session_header want %v", a["id"], want)
			}
		}
	}
	check(false) // nothing declared yet
	storeManifest(t, root, "redmatic", `{"format": 1, "id": "redmatic", "name": "RedMatic", "ui": {"session_header": true}}`)
	check(true)
}

// B-134: the manifest names Homematic Manager's settings page (ui.settings_url), since its Config-Url
// is the CCU's button into the app; GET /addons answers it as config_url, so the shell's gear frames
// the settings. A policy the old catalogue wrote still counts for an addon without a manifest.
func TestSettingsURL(t *testing.T) {
	stored := &system.AddonRuntime{SettingsURL: "/addons/hmm/settings.cgi?cmd=config"}
	cases := []struct {
		name     string
		manifest string
		policy   *system.AddonPolicy
		id       string
		want     string
	}{
		{"the manifest names it", `{"format": 1, "id": "hmm", "name": "HMM", "ui": {"settings_url": "/addons/hmm/settings.cgi?cmd=config"}}`, nil, "hmm", "/addons/hmm/settings.cgi?cmd=config"},
		{"a manifest without the field", `{"format": 1, "id": "mosquitto", "name": "Mosquitto"}`, nil, "mosquitto", ""},
		{"a manifest without the field wins over the old block", `{"format": 1, "id": "hmm", "name": "HMM"}`, &system.AddonPolicy{ID: "hmm", Mode: "confined", Source: "catalog", Runtime: stored}, "hmm", ""},
		{"no manifest, no policy", "", nil, "cuxd", ""},
		{"the old block", "", &system.AddonPolicy{ID: "hmm", Mode: "confined", Source: "catalog", Runtime: stored}, "hmm", "/addons/hmm/settings.cgi?cmd=config"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := system.Root(t.TempDir())
			if c.policy != nil {
				storePolicy(t, root, *c.policy)
			}
			if c.manifest != "" {
				storeManifest(t, root, c.id, c.manifest)
			}
			if got := SettingsURL(root, c.id); got != c.want {
				t.Errorf("SettingsURL = %q, want %q", got, c.want)
			}
		})
	}

	// GET /addons: hmm's config_url is the manifest's page, and it is the page the legacy alias
	// goes to; the raw Config-Url stays under settings (hm_addons.cfg's word)
	root := system.Root(t.TempDir())
	full := filepath.Join(string(root), "usr/local/etc/config/rc.d/hmm")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("#!/bin/sh\ncase $1 in info) printf 'Name: Homematic Manager\\nVersion: 3.0.0-beta.17\\nConfig-Url: /addons/hmm/settings.cgi\\n';; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	storeManifest(t, root, "hmm", `{"format": 1, "id": "hmm", "name": "HMM", "ui": {"settings_url": "/addons/hmm/settings.cgi?cmd=config"}}`)
	svc := scriptBox{system.AddonScripts{Root: root}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: root, Addons: svc, Nav: svc, Legacy: &memSwitches{on: true}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_, out, raw := do(t, srv, "GET", "/api/system/v1/addons", "", nil)
	addons, _ := out["addons"].([]any)
	if len(addons) != 1 {
		t.Fatalf("addons: %s", raw)
	}
	a := addons[0].(map[string]any)
	if a["config_url"] != "/addons/hmm/settings.cgi?cmd=config" || a["legacy_session"] != true {
		t.Errorf("config_url from the manifest: %v", a)
	}
}
