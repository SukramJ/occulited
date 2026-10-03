package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/catalog"
	"github.com/hobbyquaker/occulited/internal/manifest"
	"github.com/hobbyquaker/occulited/internal/system"
)

// imageServices lists one generated addon unit, so GET /services can carry the addon's images.
type imageServices struct{ scriptBox }

func (s imageServices) List() ([]system.Service, error) {
	return []system.Service{{ID: "addon-mosquitto", Kind: "addon", Category: "addon", Running: true}, {ID: "rfd", Kind: "system"}}, nil
}
func (imageServices) Control(context.Context, string, string) (string, error) { return "", nil }

func get(t *testing.T, srv *httptest.Server, path string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	shellHeader(req, hdr)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

// openccu-lite task 100: the addons' icons and logos are served from occulited's own origin with a
// type the content says, nosniff and a Content-Security-Policy, so an SVG with a script in it is a
// picture and nothing more; a kind the manifest does not declare, a missing file and a file that is
// no image are 404; and the lists say which images each addon has.
func TestAddonImageRoutes(t *testing.T) {
	r := fakeRoot(t)
	w := func(rel, content string) {
		t.Helper()
		p := filepath.Join(string(r), rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script><rect width="8" height="8"/></svg>`
	storeManifest(t, r, "mosquitto", `{"format": 1, "id": "mosquitto", "name": "Mosquitto", "ui": {"icon": "mosquitto/www/icon.svg", "icon_dark": "mosquitto/www/icon-dark.svg", "logo": "mosquitto/www/logo.png"}}`)
	w("usr/local/addons/mosquitto/www/icon.svg", svg)
	w("usr/local/addons/mosquitto/www/icon-dark.svg", `<!DOCTYPE html><html><body>not an svg</body></html>`)
	// the fixture box lists mosquitto as an installed addon (its rc.d entry is in fakeRoot)
	svc := scriptBox{system.AddonScripts{Root: r}}
	tm := &manifest.Manifest{Format: 1, ID: "tm-devices", Name: manifest.Text{"en": "TM Devices"}}
	cat := &fakeCatalog{
		view:   &catalog.View{Addons: []catalog.Item{{Git: "https://github.com/x/tm", Manifest: tm, ImageHashes: map[string]string{"icon": strings.Repeat("ab", 32)}}, {Git: "https://github.com/x/none"}}},
		images: map[string][]byte{"tm-devices/icon": []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")},
	}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Services: imageServices{svc}, Log: testLog, Addons: svc, Manager: svc, Nav: svc, Catalog: cat}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// the image, with the headers that keep it a picture
	res := get(t, srv, "/api/system/v1/addons/mosquitto/images/icon?v=2.1.2", nil)
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != svg {
		t.Fatalf("icon: %d %q", res.StatusCode, body)
	}
	for k, want := range map[string]string{
		"Content-Type":                 "image/svg+xml",
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "default-src 'none'; style-src 'unsafe-inline'",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "private, max-age=3600",
	} {
		if got := res.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	if res := get(t, srv, "/api/system/v1/addons/mosquitto/images/icon", map[string]string{"If-None-Match": etag}); res.StatusCode != 304 {
		t.Errorf("If-None-Match: %d", res.StatusCode)
	}
	// 404: html named .svg, a declared file that is not there, an undeclared kind, an unknown addon,
	// a kind that is none
	for _, p := range []string{"mosquitto/images/icon-dark", "mosquitto/images/logo", "mosquitto/images/logo-dark", "redmatic/images/icon", "mosquitto/images/etc", "mosquitto/images/..%2Fetc"} {
		res := get(t, srv, "/api/system/v1/addons/"+p, nil)
		if res.StatusCode != 404 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s: %d %s", p, res.StatusCode, res.Header.Get("Content-Type"))
		}
	}
	// the lists name the declared images, kind → URL, with the version
	st, out, _ := do(t, srv, "GET", "/api/system/v1/addons", "", nil)
	if st != 200 {
		t.Fatalf("addons: %d", st)
	}
	var mosq map[string]any
	for _, a := range out["addons"].([]any) {
		if a.(map[string]any)["id"] == "mosquitto" {
			mosq = a.(map[string]any)
		}
	}
	images, _ := mosq["images"].(map[string]any)
	if len(images) != 3 || images["icon"] != "/api/system/v1/addons/mosquitto/images/icon?v="+mosq["version"].(string) || images["logo"] == nil || images["icon-dark"] == nil || images["logo-dark"] != nil {
		t.Errorf("addons images: %v", images)
	}
	st, out, _ = do(t, srv, "GET", "/api/system/v1/services", "", nil)
	if st != 200 {
		t.Fatalf("services: %d", st)
	}
	for _, s := range out["services"].([]any) {
		s := s.(map[string]any)
		img, _ := s["images"].(map[string]any)
		switch s["id"] {
		case "addon-mosquitto":
			if len(img) != 3 || img["icon"] != "/api/system/v1/addons/mosquitto/images/icon?v=" {
				t.Errorf("services images: %v", img)
			}
		default:
			if img != nil {
				t.Errorf("%s has images: %v", s["id"], img)
			}
		}
	}
	// the catalogue: the item names its fetched images by hash, the route serves them
	st, out, _ = do(t, srv, "GET", "/api/system/v1/catalog", "", nil)
	if st != 200 {
		t.Fatalf("catalog: %d", st)
	}
	items := out["catalog"].(map[string]any)["addons"].([]any)
	img, _ := items[0].(map[string]any)["images"].(map[string]any)
	if len(img) != 1 || img["icon"] != "/api/system/v1/catalog/tm-devices/images/icon?v=abababababab" {
		t.Errorf("catalog images: %v", img)
	}
	if _, has := items[1].(map[string]any)["images"]; has {
		t.Error("an entry without a manifest has images")
	}
	res = get(t, srv, "/api/system/v1/catalog/tm-devices/images/icon?v=abababababab", nil)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("catalog icon: %d %v", res.StatusCode, res.Header)
	}
	for _, p := range []string{"tm-devices/images/logo", "nope/images/icon"} {
		if res := get(t, srv, "/api/system/v1/catalog/"+p, nil); res.StatusCode != 404 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	// a catalogue fake that answers bytes that are no image: 404, never served
	cat.images["tm-devices/logo"] = []byte("<html>")
	cat.view.Addons[0].ImageHashes["logo"] = strings.Repeat("cd", 32)
	if res := get(t, srv, "/api/system/v1/catalog/tm-devices/images/logo", nil); res.StatusCode != 404 {
		t.Errorf("not an image: %d", res.StatusCode)
	}
}
