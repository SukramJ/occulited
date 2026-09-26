package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/outboundpin"
)

// What the addon catalogue sends (task 266, the fork's docs/privacy.md): the catalogue file, each
// addon's manifest, GitHub's release list and repository (for the star count) with a GitHub Accept
// header, and at an install the package and its checksum - no version, serial, address or list of
// installed addons of the system; Go's own User-Agent. A change here is a change to the privacy
// statement.
func TestOutboundRequestsArePinned(t *testing.T) {
	pkg := []byte(strings.Repeat("addon-bytes", 100))
	sum := sha256.Sum256(pkg)
	var srv *httptest.Server
	h, log := outboundpin.Recorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog/catalog.json":
			_, _ = w.Write([]byte(`{"format": 1, "addons": [{"git": "https://github.com/hc/mosq", "manifest": "addon_files/openccu-lite.json"}]}`))
		case "/repos/hc/mosq/releases":
			_, _ = w.Write([]byte(`[{"tag_name": "v2.1.2", "assets": [{"name": "mosquitto-x86_64-2.1.2.tar.gz", "size": 1100, "browser_download_url": "` + srv.URL + `/pkg"},{"name": "mosquitto-x86_64-2.1.2.tar.gz.sha256", "size": 80, "browser_download_url": "` + srv.URL + `/pkg.sha256"}]}]`))
		case "/repos/hc/mosq":
			_, _ = w.Write([]byte(`{"stargazers_count": 34}`))
		case "/hc/mosq/v2.1.2/addon_files/openccu-lite.json":
			_, _ = w.Write([]byte(`{"format": 1, "id": "mosquitto", "name": "Mosquitto", "release": {"github": "hc/mosq", "asset": "mosquitto-{arch}-{version}.tar.gz"}}`))
		case "/pkg":
			_, _ = w.Write(pkg)
		case "/pkg.sha256":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  mosquitto-x86_64-2.1.2.tar.gz\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	srv = httptest.NewServer(h)
	t.Cleanup(srv.Close)
	s := New([]string{srv.URL + "/catalog/catalog.json"}, "x86_64", &fakeInstaller{})
	s.GitHubAPI, s.RawGitHub = srv.URL, srv.URL
	s.CacheFile = filepath.Join(t.TempDir(), "catalog-cache.json")
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Install(t.Context(), "mosquitto"); err != nil || p.Phase != "done" {
		t.Fatalf("install: %v %+v", err, p)
	}
	const plain = "\nAccept-Encoding: gzip\nUser-Agent: Go-http-client/1.1"
	const gh = "\nAccept: application/vnd.github+json" + plain
	want := []string{
		"GET /catalog/catalog.json" + plain,
		"GET /hc/mosq/v2.1.2/addon_files/openccu-lite.json" + plain,
		"GET /pkg" + plain,
		"GET /pkg.sha256" + plain,
		"GET /repos/hc/mosq" + gh,
		"GET /repos/hc/mosq/releases?per_page=10" + gh,
	}
	got := log.Requests()
	sort.Strings(got)
	got = dedupe(got)
	if strings.Join(got, "\n\n") != strings.Join(want, "\n\n") {
		t.Errorf("the requests changed - update docs/privacy.md in the fork with this test:\n%s", strings.Join(got, "\n\n"))
	}
}

// dedupe drops repeats of a sorted list: how often a request goes out is not what is pinned.
func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// B-240 (D-90): nothing goes out unless the user runs a check or *Check daily* is on. A page load
// (Fetch without force), the start of the service (Manifest, Item) and an install's lookup answer
// from what the system holds - the bundled copy, and after a check the kept copy of the published
// file - and make no request; a new process answers from the cache the same way. Run's daily
// refresh fetches the catalogue file again (with force), so the daily switch is the one gate.
func TestNothingGoesOutWithoutTheUsersCheck(t *testing.T) {
	h, log := outboundpin.Recorder(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog/catalog.json":
			_, _ = w.Write([]byte(`{"format": 1, "addons": [{"git": "https://github.com/hc/mosq", "manifest": "addon_files/openccu-lite.json"}, {"git": "https://github.com/x/loom", "manifest": "catalog/manifests/openccu-loom.json", "untested": true}]}`))
		case "/catalog/manifests/openccu-loom.json":
			_, _ = w.Write([]byte(`{"format": 1, "id": "openccu-loom", "name": "Loom", "release": {"github": "x/loom", "asset": "loom.tgz"}}`))
		case "/repos/hc/mosq/releases", "/repos/x/loom/releases":
			_, _ = w.Write([]byte(`[]`))
		case "/hc/mosq/HEAD/addon_files/openccu-lite.json":
			_, _ = w.Write([]byte(`{"format": 1, "id": "mosquitto", "name": "Mosquitto", "release": {"github": "hc/mosq", "asset": "mosquitto-{arch}-{version}.tar.gz"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	// the bundled copy the image carries, with an adapter manifest beside it
	bundled := writeFile(t, dir, "etc/catalog.json", `{"format": 1, "addons": [{"git": "https://github.com/x/loom", "manifest": "catalog/manifests/openccu-loom.json", "untested": true}]}`)
	writeFile(t, dir, "etc/manifests/openccu-loom.json", `{"format": 1, "id": "openccu-loom", "name": "Loom", "release": {"github": "x/loom", "asset": "loom.tgz"}}`)
	newService := func() *Service {
		s := New([]string{srv.URL + "/catalog/catalog.json", bundled}, "x86_64", &fakeInstaller{})
		s.GitHubAPI, s.RawGitHub = srv.URL, srv.URL
		s.CacheFile = filepath.Join(dir, "catalog-cache.json")
		s.BundledManifests = filepath.Join(dir, "etc/manifests")
		return s
	}
	none := func(what string) {
		t.Helper()
		if got := log.Requests(); len(got) != 0 {
			t.Fatalf("%s must make no request (B-240), made:\n%s", what, strings.Join(got, "\n\n"))
		}
	}

	// before any check: the bundled copy answers everything
	s := newService()
	v, err := s.Fetch(t.Context(), false)
	if err != nil || len(v.Addons) != 1 || v.Addons[0].Manifest == nil || v.Addons[0].ID != "openccu-loom" {
		t.Fatalf("bundled view: %v %+v", err, v)
	}
	none("a page load before the first check")
	if s.Manifest("openccu-loom") == nil || s.Manifest("mosquitto") != nil {
		t.Fatal("the start-time fallback must know the bundled adapter and nothing else")
	}
	none("the start-time manifest fallback")
	if s.Item(t.Context(), "openccu-loom") == nil {
		t.Fatal("Item from the bundled copy")
	}
	none("Item")
	if _, err := s.Install(t.Context(), "mosquitto"); err == nil || !strings.Contains(err.Error(), "run a check first") {
		t.Fatalf("an install of an unknown addon must ask for a check, not fetch: %v", err)
	}
	none("an install's lookup of an unknown addon")

	// the user's check goes out and keeps the published copy
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := log.Requests(); len(got) == 0 || !strings.HasPrefix(got[0], "GET /catalog/catalog.json") {
		t.Fatalf("the check fetches the catalogue file first: %v", got)
	}
	log.Reset()
	v, _ = s.Fetch(t.Context(), false)
	if len(v.Addons) != 2 {
		t.Fatalf("after the check both entries: %+v", v.Addons)
	}
	none("a page load after the check")

	// a new process: the kept copy of the published file and the cached manifests, no request
	s2 := newService()
	v2, err := s2.Fetch(t.Context(), false)
	if err != nil || len(v2.Addons) != 2 {
		t.Fatalf("new process: %v %+v", err, v2)
	}
	for _, it := range v2.Addons {
		if it.Manifest == nil {
			t.Fatalf("not from the cache: %+v", it)
		}
	}
	if s2.Manifest("mosquitto") == nil {
		t.Fatal("the start-time fallback knows the fetched manifest from the cache")
	}
	none("a new process's page load and manifest fallback")

	// Run's daily refresh is the one scheduled fetch: Fetch with force goes out
	if _, err := s2.Fetch(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if got := log.Requests(); len(got) != 1 || !strings.HasPrefix(got[0], "GET /catalog/catalog.json") {
		t.Fatalf("the daily refresh fetches the catalogue file and nothing else: %v", got)
	}
}
