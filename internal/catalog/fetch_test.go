package catalog

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// occulited B-37: GitHub's edge answered 500 on one pooled HTTP/2 connection, and every install
// took that connection again until occulited restarted. stubGH serves the release list and the
// package over TLS with HTTP/2, like github.com; the package is answered with a 500 on the
// connection that asked for it first (bad) - or on every connection (always) - and with the package
// on any other.
type staleGH struct {
	srv       *httptest.Server
	mu        sync.Mutex
	bad       string // the RemoteAddr of the connection that keeps failing
	always    atomic.Bool
	downloads atomic.Int32
	failures  atomic.Int32
}

func newStaleGH(t *testing.T) *staleGH {
	g := &staleGH{}
	g.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/x/releases":
			_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0","assets":[{"name":"x-x86_64-1.0.0.tar.gz","size":3,"browser_download_url":"` + g.srv.URL + `/pkg?sig=s3cr3t-signature"}]}]`))
		case "/pkg":
			g.mu.Lock()
			if g.bad == "" {
				g.bad = r.RemoteAddr
			}
			bad := g.bad == r.RemoteAddr || g.always.Load()
			g.mu.Unlock()
			if bad {
				g.failures.Add(1)
				w.Header().Set("X-GitHub-Request-Id", "ABCD:1234")
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
			g.downloads.Add(1)
			_, _ = w.Write([]byte("pkg"))
		default:
			http.NotFound(w, r)
		}
	}))
	g.srv.EnableHTTP2 = true
	g.srv.StartTLS()
	t.Cleanup(g.srv.Close)
	return g
}

func newStaleService(t *testing.T, g *staleGH, inst *fakeInstaller) *Service {
	dir := t.TempDir()
	cat := writeFile(t, dir, "catalog.json", `{"format": 1, "addons": [{"git": "https://github.com/o/x", "manifest": "catalog/manifests/x.json"}]}`)
	writeFile(t, dir, "manifests/x.json", `{"format": 1, "id": "x", "name": "X", "release": {"github": "o/x", "asset": "x-{arch}-{version}.tar.gz"}}`)
	s := New([]string{cat}, "x86_64", inst)
	s.HTTP = g.srv.Client() // HTTP/2 over TLS, one long-lived pool as occulited's own client has
	s.GitHubAPI = g.srv.URL
	s.BundledManifests = filepath.Join(dir, "manifests")
	s.CacheFile = filepath.Join(dir, "cache.json")
	if _, err := s.Fetch(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	return s
}

// captureLog makes slog's default write into a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// A connection that keeps failing is given up for a fresh one: the install succeeds at once, and
// the next ones too. Before B-37 every install took the bad connection again.
func TestInstallLeavesAFailingConnection(t *testing.T) {
	log := captureLog(t)
	g := newStaleGH(t)
	inst := &fakeInstaller{}
	s := newStaleService(t, g, inst)
	for i := range 3 {
		p, err := s.Install(t.Context(), "x")
		if err != nil || p.Phase != "done" {
			t.Fatalf("install %d: %v %+v", i, err, p)
		}
	}
	if string(inst.got) != "pkg" || g.downloads.Load() != 3 || g.failures.Load() != 1 {
		t.Errorf("installed %q, %d downloads, %d failures", inst.got, g.downloads.Load(), g.failures.Load())
	}
	// the failure is in the journal, with what identifies it and without the signature
	out := log.String()
	for _, want := range []string{"trying once more on a fresh connection", "status=500", "request_id=ABCD:1234", "/pkg", "proto=HTTP/2.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("the log carries the query:\n%s", out)
	}
}

// A server that fails on every connection fails the install after one retry, and the error says
// where the answer came from - without the query.
func TestInstallSaysWhereA5xxCameFrom(t *testing.T) {
	log := captureLog(t)
	g := newStaleGH(t)
	g.always.Store(true)
	inst := &fakeInstaller{}
	s := newStaleService(t, g, inst)
	p, err := s.Install(t.Context(), "x")
	if err == nil || p.Phase != "failed" || inst.got != nil {
		t.Fatalf("%v %+v", err, p)
	}
	host := strings.TrimPrefix(g.srv.URL, "https://")
	if !strings.Contains(p.Message, "download: HTTP 500 from https://"+host+"/pkg") || strings.Contains(p.Message, "s3cr3t") {
		t.Errorf("message: %q", p.Message)
	}
	if g.failures.Load() != 2 {
		t.Errorf("%d tries, want 2", g.failures.Load())
	}
	out := log.String()
	if !strings.Contains(out, "failed again on a fresh connection") || !strings.Contains(out, "catalog: install failed") || strings.Contains(out, "s3cr3t") {
		t.Errorf("log:\n%s", out)
	}
}

// do: a 4xx is not retried, a cancelled request is not either, and the query never reaches redact's
// output.
func TestDoRetriesOnlyWhatIsWorthIt(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/404":
			http.NotFound(w, r)
		case "/once":
			if hits.Load() == 1 {
				http.Error(w, "boom", http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	s := &Service{HTTP: srv.Client()}
	get := func(path string) *http.Response {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+path, nil)
		res, err := s.do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := get("/once"); res.StatusCode != 200 || hits.Load() != 2 {
		t.Errorf("a 502 once: %d after %d", res.StatusCode, hits.Load())
	}
	hits.Store(0)
	if res := get("/404"); res.StatusCode != 404 || hits.Load() != 1 {
		t.Errorf("a 404: %d after %d hits", res.StatusCode, hits.Load())
	}
	u, _ := http.NewRequest(http.MethodGet, "https://user:pw@example.com/a/b?sig=x#f", nil)
	if got := redact(u.URL); got != "https://example.com/a/b" {
		t.Errorf("redact: %s", got)
	}
}
