package httpapi

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// sbomServer is the system API behind the middleware with a store that has an administrator,
// on a root that carries the SBOM when doc is not empty.
func sbomServer(t *testing.T, doc string) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	if doc != "" {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(doc))
		_ = zw.Close()
		p := filepath.Join(root, system.SBOMFile)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "s")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store}
	a.Register(mux)
	(&SystemAPI{Root: system.Root(root), Run: system.DryRunner}).Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	return srv, root
}

func sbomGet(t *testing.T, srv *httptest.Server, path string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", srv.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultTransport.RoundTrip(req) // no transparent gunzip
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

// task 179: the SBOM without a session, gzip as it lies on disk, with an ETag; unpacked for a
// client that does not take gzip; a 304 for the ETag; an attachment on ?download=1.
func TestSBOMServedWithoutSession(t *testing.T) {
	const doc = `{"bomFormat":"CycloneDX","specVersion":"1.6","components":[]}`
	srv, _ := sbomServer(t, doc)

	res, body := sbomGet(t, srv, "/api/system/v1/sbom", map[string]string{"Accept-Encoding": "gzip, deflate"})
	if res.StatusCode != 200 {
		t.Fatalf("status %d: %s", res.StatusCode, body)
	}
	if res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("Content-Type") != "application/vnd.cyclonedx+json" {
		t.Fatalf("headers: %v", res.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	if string(plain) != doc {
		t.Fatalf("body %q", plain)
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}

	res, body = sbomGet(t, srv, "/api/system/v1/sbom", map[string]string{"Accept-Encoding": "identity"})
	if res.StatusCode != 200 || res.Header.Get("Content-Encoding") != "" || string(body) != doc {
		t.Fatalf("identity: %d %v %q", res.StatusCode, res.Header, body)
	}

	res, _ = sbomGet(t, srv, "/api/system/v1/sbom", map[string]string{"If-None-Match": etag})
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", res.StatusCode)
	}

	res, _ = sbomGet(t, srv, "/api/system/v1/sbom?download=1", nil)
	if cd := res.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Fatalf("Content-Disposition %q", cd)
	}
}

func TestSBOMMissing(t *testing.T) {
	srv, _ := sbomServer(t, "")
	res, body := sbomGet(t, srv, "/api/system/v1/sbom", nil)
	if res.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "not-found") {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}
