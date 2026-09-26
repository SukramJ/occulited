package catalog

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimited(t *testing.T) {
	now := time.Date(2026, 9, 26, 21, 0, 0, 0, time.UTC)
	reset := strconv.FormatInt(now.Add(14*time.Minute+10*time.Second).Unix(), 10)
	for _, c := range []struct {
		name    string
		status  int
		headers map[string]string
		body    string
		limited bool
		minutes int
	}{
		{"403, budget spent, reset", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}, "", true, 15},
		{"429 with Retry-After seconds", 429, map[string]string{"Retry-After": "90"}, "", true, 2},
		{"403 with a Retry-After date", 403, map[string]string{"Retry-After": now.Add(5 * time.Minute).Format(http.TimeFormat)}, "", true, 5},
		{"Retry-After wins over the reset", 403, map[string]string{"Retry-After": "60", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": reset}, "", true, 1},
		{"403, the words only", 403, nil, `{"message":"API rate limit exceeded for 198.51.100.7."}`, true, 0},
		{"403, a reset already past", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": strconv.FormatInt(now.Add(-time.Minute).Unix(), 10)}, "", true, 0},
		{"403, something else", 403, map[string]string{"X-RateLimit-Remaining": "12"}, `{"message":"Repository access blocked"}`, false, 0},
		{"500", 500, nil, "", false, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			res := &http.Response{StatusCode: c.status, Header: http.Header{}}
			for k, v := range c.headers {
				res.Header.Set(k, v)
			}
			limited, at := rateLimited(res, []byte(c.body), now)
			if limited != c.limited {
				t.Fatalf("limited %v, want %v", limited, c.limited)
			}
			e := &ReleasesError{Repo: "o/x", RateLimited: limited, RetryAt: at, Status: c.status}
			if m := e.RetryMinutes(now); m != c.minutes {
				t.Errorf("minutes %d, want %d", m, c.minutes)
			}
		})
	}
}

func TestReleasesErrorMessages(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		e    *ReleasesError
		code string
		want string
	}{
		{&ReleasesError{Repo: "o/x", RateLimited: true, RetryAt: now.Add(12 * time.Minute)}, CodeRateLimit, "GitHub rate limit, try again in 12 min"},
		{&ReleasesError{Repo: "o/x", RateLimited: true}, CodeRateLimit, "GitHub rate limit, try again later"},
		{&ReleasesError{Repo: "o/x", Err: errors.New("dial tcp: no route to host")}, CodeUnreachable, "could not be read: dial tcp"},
		{&ReleasesError{Repo: "o/x", Status: 502}, CodeUnreachable, "GitHub answered HTTP 502"},
	} {
		if c.e.Code() != c.code || !strings.Contains(c.e.message(now), c.want) || !strings.Contains(c.e.Error(), "o/x") {
			t.Errorf("%s / %q, want %s / %q", c.e.Code(), c.e.message(now), c.code, c.want)
		}
	}
	inner := errors.New("x")
	if !errors.Is(&ReleasesError{Err: inner}, inner) {
		t.Error("Unwrap")
	}
}

// ghStub is a GitHub whose releases answer can be switched between the list and a refusal; it
// counts the downloads, so a test can say that nothing was fetched.
type ghStub struct {
	srv       *httptest.Server
	mode      atomic.Value // "ok", "limited", "limited429"
	reset     time.Time
	downloads atomic.Int32
	version   atomic.Value // the newest tag
}

func newGHStub(t *testing.T) *ghStub {
	g := &ghStub{}
	g.mode.Store("ok")
	g.version.Store("1.0.0")
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/x/releases":
			switch g.mode.Load().(string) {
			case "limited":
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(g.reset.Unix(), 10))
				w.WriteHeader(403)
				_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
				return
			case "limited429":
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(429)
				return
			}
			v := g.version.Load().(string)
			_, _ = w.Write([]byte(`[{"tag_name":"v` + v + `","assets":[{"name":"x-x86_64-` + v + `.tar.gz","size":3,"browser_download_url":"` + g.srv.URL + `/pkg"}]}]`))
		case "/pkg":
			g.downloads.Add(1)
			_, _ = w.Write([]byte("pkg"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

// newStubService is a catalogue of one bundled adapter, "x", released on the stub.
func newStubService(t *testing.T, g *ghStub, inst *fakeInstaller) *Service {
	dir := t.TempDir()
	cat := writeFile(t, dir, "catalog.json", `{"format": 1, "addons": [{"git": "https://github.com/o/x", "manifest": "catalog/manifests/x.json"}]}`)
	writeFile(t, dir, "manifests/x.json", `{"format": 1, "id": "x", "name": "X", "release": {"github": "o/x", "asset": "x-{arch}-{version}.tar.gz"}}`)
	s := New([]string{cat}, "x86_64", inst)
	s.GitHubAPI = g.srv.URL
	s.BundledManifests = filepath.Join(dir, "manifests")
	s.CacheFile = filepath.Join(dir, "cache.json")
	if _, err := s.Fetch(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	return s
}

// B-21: an install whose release list cannot be read refuses with the reason and installs nothing -
// also when an earlier answer is cached, which used to be installed in its place.
func TestInstallRefusesWithoutTheReleaseList(t *testing.T) {
	now := time.Date(2026, 9, 26, 21, 10, 0, 0, time.UTC)
	g := newGHStub(t)
	g.reset = now.Add(23*time.Minute + 5*time.Second)
	inst := &fakeInstaller{}
	s := newStubService(t, g, inst)
	s.now = func() time.Time { return now }

	// the page's check reads the list once: 1.0.0 is cached in memory and on disk
	s.RefreshReleases(t.Context())
	if it := s.Item(t.Context(), "x"); it == nil || it.Latest == nil || it.Latest.Version != "1.0.0" {
		t.Fatalf("the first check: %+v", it)
	}
	// 1.1.0 is published, and the shared address is out of budget
	g.version.Store("1.1.0")
	g.mode.Store("limited")
	p, err := s.Install(t.Context(), "x")
	if err == nil || p.Phase != "failed" || p.Error != CodeRateLimit || p.RetryMinutes != 24 {
		t.Fatalf("a rate-limited install must refuse: %v %+v", err, p)
	}
	if !strings.Contains(p.Message, "GitHub rate limit, try again in 24 min") || !strings.Contains(p.Message, "nothing was installed") {
		t.Errorf("message: %q", p.Message)
	}
	if inst.got != nil || g.downloads.Load() != 0 {
		t.Fatalf("nothing may be downloaded or installed: %d downloads, installer got %q", g.downloads.Load(), inst.got)
	}
	if s.Progress().Error != CodeRateLimit {
		t.Error("the progress the page polls carries the code")
	}

	// 429 with Retry-After
	g.mode.Store("limited429")
	if p, err := s.Install(t.Context(), "x"); err == nil || p.Error != CodeRateLimit || p.RetryMinutes != 2 {
		t.Fatalf("429: %v %+v", err, p)
	}

	// no network at all
	g.mode.Store("ok")
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	s.GitHubAPI = gone.URL
	if p, err := s.Install(t.Context(), "x"); err == nil || p.Error != CodeUnreachable || p.RetryMinutes != 0 {
		t.Fatalf("unreachable: %v %+v", err, p)
	}
	if inst.got != nil || g.downloads.Load() != 0 {
		t.Fatal("still nothing installed")
	}

	// the budget is back: the install reads the list and installs the newest release
	s.GitHubAPI = g.srv.URL
	if p, err := s.Install(t.Context(), "x"); err != nil || p.Phase != "done" || p.Error != "" || p.Message != "x-x86_64-1.1.0.tar.gz" {
		t.Fatalf("after the limit: %v %+v", err, p)
	}
}

// B-21: a check that cannot read a list keeps the version the page showed and names why, across a
// restart; the next check that reads everything clears it.
func TestCheckNamesAnUnreadReleaseList(t *testing.T) {
	now := time.Date(2026, 9, 26, 21, 10, 0, 0, time.UTC)
	g := newGHStub(t)
	g.reset = now.Add(9 * time.Minute)
	s := newStubService(t, g, &fakeInstaller{})
	s.now = func() time.Time { return now }
	s.dailyReleases(t.Context())
	if v := s.Cached(); v == nil || v.ReleasesError != nil || v.Addons[0].Latest.Version != "1.0.0" {
		t.Fatalf("a clean check: %+v", v)
	}

	g.version.Store("1.1.0")
	g.mode.Store("limited")
	s.dailyReleases(t.Context())
	v := s.Cached()
	n := v.ReleasesError
	if n == nil || n.Code != CodeRateLimit || n.Repo != "o/x" || n.RetryMinutes != 9 || !strings.Contains(n.Message, "try again in 9 min") || !n.At.Equal(now) {
		t.Fatalf("the notice: %+v", n)
	}
	if v.Addons[0].Latest.Version != "1.0.0" {
		t.Error("the version the page had stays")
	}
	// the wait counts down from the moment the page asks
	now = now.Add(5 * time.Minute)
	if n := s.Cached().ReleasesError; n.RetryMinutes != 4 || !strings.Contains(n.Message, "try again in 4 min") {
		t.Errorf("four minutes later: %+v", n)
	}

	// a restart reads the notice from the cache file
	s2 := New(s.URLs, "x86_64", &fakeInstaller{})
	s2.GitHubAPI, s2.BundledManifests, s2.CacheFile, s2.now = s.GitHubAPI, s.BundledManifests, s.CacheFile, s.now
	if _, err := s2.Fetch(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if n := s2.Cached().ReleasesError; n == nil || n.Code != CodeRateLimit {
		t.Fatalf("after a restart: %+v", n)
	}

	// the budget is back: the next check reads 1.1.0 and the notice goes
	g.mode.Store("ok")
	s.dailyReleases(t.Context())
	if v := s.Cached(); v.ReleasesError != nil || v.Addons[0].Latest.Version != "1.1.0" {
		t.Fatalf("after the limit: %+v %+v", v.ReleasesError, v.Addons[0].Latest)
	}

	// a rate limit wins over a network error in the notice; other errors are not noted
	s.noteReleasesError(errors.New("no package for this box"))
	if s.Cached().ReleasesError != nil {
		t.Fatal("an error that is not about the list")
	}
	s.noteReleasesError(&ReleasesError{Repo: "a/b", Err: errors.New("timeout")})
	s.noteReleasesError(&ReleasesError{Repo: "c/d", RateLimited: true})
	s.noteReleasesError(&ReleasesError{Repo: "e/f", Status: 502})
	if n := s.Cached().ReleasesError; n.Repo != "c/d" || n.RetryMinutes != 0 || !strings.Contains(n.Message, "try again later") {
		t.Fatalf("the rate limit wins: %+v", n)
	}
}

// B-21 in the check's manifest pass: a rate-limited tag lookup keeps the last manifest and its tag
// and names the limit in the page's notice; without a last manifest the entry says it itself.
func TestManifestPassUnderTheRateLimit(t *testing.T) {
	g := newGHStub(t)
	g.mode.Store("limited")
	g.reset = time.Now().Add(30 * time.Minute)
	s := newStubService(t, g, &fakeInstaller{})
	e := Entry{Git: "https://github.com/o/x", Manifest: "openccu-lite.json"}
	if c := s.fetchManifest(t.Context(), e, ""); c.Manifest != nil || !strings.Contains(c.Error, "GitHub rate limit") {
		t.Fatalf("no last manifest: %+v", c)
	}
	prev := s.Manifest("x")
	s.mu.Lock()
	s.cache.Entries[entryKey(e.Git)] = cached{Manifest: prev, Tag: "v1.0.0"}
	s.cache.ReleasesError = nil
	s.mu.Unlock()
	c := s.fetchManifest(t.Context(), e, "")
	if c.Manifest != prev || c.Tag != "v1.0.0" || c.Error != "" {
		t.Fatalf("the last manifest stays: %+v", c)
	}
	if n := s.Cached().ReleasesError; n == nil || n.Code != CodeRateLimit {
		t.Fatalf("the notice: %+v", n)
	}
}
