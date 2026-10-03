package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// openccu-lite task 100: the user's check fetches the images an entry's manifest declares from the
// repository at the tag the manifest was read at, beside the manifest (its directory is the package
// root there), with the manifest's rules - at most MaxSize bytes,
// an image by content - into the images directory under their hash. The page and the image route
// answer from the files; a check whose manifest is unchanged keeps them, and fetches again only what
// is missing; a file no entry names any more is removed; an adapter's manifest gets none.
func TestImagesFetchedWithTheManifest(t *testing.T) {
	var iconCalls atomic.Int32
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="8" height="8"/></svg>`
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog/catalog.json":
			_, _ = w.Write([]byte(`{"format": 1, "addons": [
				{"git": "https://github.com/hc/mosq", "manifest": "addon_files/openccu-lite.json"},
				{"git": "https://github.com/x/loom", "manifest": "catalog/manifests/openccu-loom.json"}]}`))
		case "/catalog/manifests/openccu-loom.json":
			_, _ = w.Write([]byte(`{"format": 1, "id": "openccu-loom", "name": "Loom", "release": {"github": "x/loom", "asset": "loom-{version}.tar.gz"}, "ui": {"icon": "loom/www/icon.svg"}}`))
		case "/repos/hc/mosq/releases":
			_, _ = w.Write([]byte(`[{"tag_name": "v2.1.2", "assets": [{"name": "mosquitto-x86_64-2.1.2.tar.gz", "size": 10, "browser_download_url": "` + srv.URL + `/pkg"}]}]`))
		case "/repos/hc/mosq", "/repos/x/loom":
			_, _ = w.Write([]byte(`{"stargazers_count": 1}`))
		case "/repos/x/loom/releases":
			_, _ = w.Write([]byte(`[]`))
		case "/hc/mosq/v2.1.2/addon_files/openccu-lite.json":
			w.Header().Set("ETag", `"m1"`)
			if r.Header.Get("If-None-Match") == `"m1"` {
				w.WriteHeader(304)
				return
			}
			_, _ = w.Write([]byte(`{"format": 1, "id": "mosquitto", "name": "Mosquitto", "release": {"github": "hc/mosq", "asset": "mosquitto-{arch}-{version}.tar.gz"},
				"ui": {"icon": "mosquitto/www/icon.svg", "icon_dark": "mosquitto/www/icon-dark.svg", "logo": "mosquitto/www/logo.png", "logo_dark": "mosquitto/www/big.png"}}`))
		case "/hc/mosq/v2.1.2/addon_files/mosquitto/www/icon.svg":
			iconCalls.Add(1)
			_, _ = w.Write([]byte(svg))
		case "/hc/mosq/v2.1.2/addon_files/mosquitto/www/icon-dark.svg":
			_, _ = w.Write([]byte(`<!DOCTYPE html><html>not an image</html>`))
		case "/hc/mosq/v2.1.2/addon_files/mosquitto/www/big.png":
			_, _ = w.Write(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 256<<10)...))
		case "/x/loom/HEAD/loom/www/icon.svg":
			t.Error("an adapter's image was fetched from the repository")
			http.NotFound(w, r)
		default:
			http.NotFound(w, r) // logo.png among them
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	newService := func() *Service {
		s := New([]string{srv.URL + "/catalog/catalog.json"}, "x86_64", nil)
		s.GitHubAPI, s.RawGitHub = srv.URL, srv.URL
		s.CacheFile = filepath.Join(dir, "catalog-cache.json")
		s.ImagesDir = filepath.Join(dir, "images")
		return s
	}
	s := newService()
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(svg))
	hash := hex.EncodeToString(sum[:])
	v, _ := s.Fetch(t.Context(), false)
	var mosq, loom Item
	for _, it := range v.Addons {
		switch it.Git {
		case "https://github.com/hc/mosq":
			mosq = it
		case "https://github.com/x/loom":
			loom = it
		}
	}
	// the icon is kept under its hash; the html, the missing logo and the oversized one are not
	if len(mosq.ImageHashes) != 1 || mosq.ImageHashes["icon"] != hash {
		t.Fatalf("mosq images: %v", mosq.ImageHashes)
	}
	if loom.ImageHashes != nil {
		t.Errorf("loom (an adapter) has images: %v", loom.ImageHashes)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "images", hash)); err != nil || string(b) != svg {
		t.Fatalf("the file: %q %v", b, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "images")); len(entries) != 1 {
		t.Errorf("%d files in the images directory", len(entries))
	}
	// the route's read
	if b, err := s.Image("mosquitto", "icon"); err != nil || string(b) != svg {
		t.Errorf("Image: %q %v", b, err)
	}
	for _, c := range []struct{ id, kind string }{{"mosquitto", "logo"}, {"mosquitto", "icon-dark"}, {"openccu-loom", "icon"}, {"nope", "icon"}, {"mosquitto", "x"}} {
		if _, err := s.Image(c.id, c.kind); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Image(%s, %s): %v", c.id, c.kind, err)
		}
	}
	// a second check: the manifest is unchanged (304) and the icon on disk - not fetched again
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if iconCalls.Load() != 1 {
		t.Errorf("the icon was fetched %d times", iconCalls.Load())
	}
	// a new process answers from the files; the icon gone from disk is fetched again at the next check
	s2 := newService()
	if b, err := s2.Image("mosquitto", "icon"); err != nil || string(b) != svg {
		t.Errorf("Image from the cache: %q %v", b, err)
	}
	if err := os.Remove(filepath.Join(dir, "images", hash)); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Image("mosquitto", "icon"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Image without the file: %v", err)
	}
	if err := s2.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if iconCalls.Load() != 2 {
		t.Errorf("the missing icon was not fetched again: %d", iconCalls.Load())
	}
	// a stray file in the directory goes with the next save; a foreign name stays
	stray := filepath.Join(dir, "images", strings.Repeat("ff", 32))
	if err := os.WriteFile(stray, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "images", "README"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s2.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stray); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the stray file stayed")
	}
	if _, err := os.Stat(filepath.Join(dir, "images", "README")); err != nil {
		t.Error("a foreign file was removed")
	}
	// without an images directory nothing is fetched or kept
	s3 := New([]string{srv.URL + "/catalog/catalog.json"}, "x86_64", nil)
	s3.GitHubAPI, s3.RawGitHub = srv.URL, srv.URL
	s3.CacheFile = filepath.Join(t.TempDir(), "c.json")
	if err := s3.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	if it := s3.Item(t.Context(), "mosquitto"); it == nil || it.ImageHashes != nil {
		t.Errorf("without a directory: %+v", it)
	}
}
