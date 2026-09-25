package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/firmware"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The Firmware page's viewer: a bundle's changelog and info come back as UTF-8 text, everything
// else is refused with the status that says why.
func TestFirmwareBundleFileRoute(t *testing.T) {
	dir := t.TempDir()
	b := filepath.Join(dir, "4107")
	_ = os.MkdirAll(b, 0o755)
	w := func(name, data string) {
		if err := os.WriteFile(filepath.Join(b, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("info", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.0.0\n")
	w("changelog.txt", "Version 1.2.6\n")
	w("latin1.txt", "f\xfcr\n")
	w("HmIPW-DRS8_update_V1_2_6_220928.efw", "\x00\x01")
	w("big.log", strings.Repeat("x", firmware.MaxViewSize+10))
	_ = os.WriteFile(filepath.Join(dir, "secret"), []byte("root only"), 0o644)
	_ = os.Symlink(filepath.Join(dir, "secret"), filepath.Join(b, "notes.txt"))

	mux := http.NewServeMux()
	(&SystemAPI{Root: system.Root(t.TempDir()), Firmware: firmware.NewService(nil, dir, nil, nil)}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		name   string
		path   string
		status int
		body   string // the text of a 200, the error code otherwise
	}{
		{"the changelog", "4107/files/changelog.txt", 200, "Version 1.2.6\n"},
		{"the info file", "4107/files/info", 200, "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.0.0\n"},
		{"a Latin-1 file as UTF-8", "4107/files/latin1.txt", 200, "für\n"},
		{"a name not in the bundle", "4107/files/readme.txt", 404, "not-found"},
		{"a symlink", "4107/files/notes.txt", 404, "not-found"},
		{"an escaped slash", "4107/files/..%2Fsecret", 404, ""},
		{"a firmware image", "4107/files/HmIPW-DRS8_update_V1_2_6_220928.efw", 415, "not-viewable"},
		{"over the cap", "4107/files/big.log", 413, "too-large"},
		{"an unknown bundle", "310/files/info", 404, "not-found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := http.Get(srv.URL + "/api/system/v1/firmware/bundles/" + c.path)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != c.status {
				t.Fatalf("status %d, want %d: %s", res.StatusCode, c.status, raw)
			}
			if strings.Contains(string(raw), "root only") {
				t.Fatal("the file outside the bundle was served")
			}
			if c.status == 200 {
				if ct := res.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
					t.Errorf("content type %q", ct)
				}
				if res.Header.Get("X-Content-Type-Options") != "nosniff" {
					t.Error("no nosniff")
				}
				if string(raw) != c.body {
					t.Errorf("body %q, want %q", raw, c.body)
				}
				return
			}
			if c.body == "" {
				return
			}
			var e apiError
			if err := json.Unmarshal(raw, &e); err != nil || e.Error != c.body {
				t.Fatalf("error %q (%v), want %q: %s", e.Error, err, c.body, raw)
			}
			if c.status == 413 && (e.Detail["size"] != float64(firmware.MaxViewSize+10) || e.Detail["limit"] != float64(firmware.MaxViewSize)) {
				t.Errorf("the size is not in the answer: %v", e.Detail)
			}
		})
	}

	// without a firmware service the route says so
	mux2 := http.NewServeMux()
	(&SystemAPI{Root: system.Root(t.TempDir())}).Register(mux2)
	rec := httptest.NewRecorder()
	mux2.ServeHTTP(rec, httptest.NewRequest("GET", "/api/system/v1/firmware/bundles/4107/files/info", nil))
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("no service: %d", rec.Code)
	}
}
