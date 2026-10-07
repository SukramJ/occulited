package catalog

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// B-52: a fresh system shows the image's copy of the catalogue, marked as such, and a check that
// cannot reach the published file says so - in the view, across a restart, counted until a check
// works - instead of a "checked" over a list nobody fetched.
func TestCheckFailureAndBundledFallback(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	var manifests404 atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case "/catalog/catalog.json":
			_, _ = w.Write([]byte(`{"format": 1, "addons": [{"git": "https://github.com/o/a", "manifest": "openccu-lite.json"},
				{"git": "https://github.com/o/e", "manifest": "catalog/manifests/e.json"}]}`))
		case "/catalog/manifests/e.json":
			_, _ = w.Write([]byte(`{"format": 1, "id": "e", "name": "E", "release": {"github": "o/e", "asset": "e.tgz"}}`))
		case "/repos/o/a/releases":
			_, _ = w.Write([]byte(`[]`))
		case "/o/a/HEAD/openccu-lite.json":
			if manifests404.Load() {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"format": 1, "id": "a", "name": "A", "release": {"github": "o/a", "asset": "a.tgz"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	bundled := writeFile(t, dir, "etc/catalog.json", `{"format": 1, "addons": [
		{"git": "https://github.com/o/a", "manifest": "openccu-lite.json"},
		{"git": "https://github.com/o/e", "manifest": "catalog/manifests/e.json", "untested": true}]}`)
	writeFile(t, dir, "etc/manifests/e.json", `{"format": 1, "id": "e", "name": "E", "release": {"github": "o/e", "asset": "e.tgz"}}`)
	stamp := time.Date(2026, 10, 4, 5, 37, 0, 0, time.UTC)
	if err := os.Chtimes(strings.TrimPrefix(bundled, "file://"), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	newService := func() *Service {
		s := New([]string{srv.URL + "/catalog/catalog.json", bundled}, "x86_64", nil)
		s.GitHubAPI, s.RawGitHub = srv.URL, srv.URL
		s.BundledManifests = filepath.Join(dir, "etc/manifests")
		s.CacheFile = filepath.Join(dir, "catalog-cache.json")
		return s
	}
	s := newService()

	// the first visit: the bundled list, marked with its date, no check yet, no failure
	v, err := s.Fetch(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Addons) != 2 || v.Source != "bundled" || v.BundledDate == nil || !v.BundledDate.Equal(stamp) || v.Checked != nil || v.CheckError != nil {
		t.Fatalf("first visit: %d addons, source %q, bundled %v, checked %v, error %+v", len(v.Addons), v.Source, v.BundledDate, v.Checked, v.CheckError)
	}

	// the published file unreachable: the bundled list stays, the check failed and names the host
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatalf("a check with the bundled copy standing in is no error of the request: %v", err)
	}
	v, _ = s.Fetch(t.Context(), false)
	host := strings.TrimPrefix(srv.URL, "http://")
	f := v.CheckError
	if f == nil || f.Host != host || !strings.HasPrefix(f.Message, host+": ") || f.Failures != 1 || !f.Since.Equal(f.At) {
		t.Fatalf("failure: %+v", f)
	}
	if strings.Contains(f.Message, `"http`) {
		t.Errorf("the message carries Go's quoted URL: %q", f.Message)
	}
	if v.Checked != nil || v.Source != "bundled" || len(v.Addons) != 2 {
		t.Fatalf("after the failed check: checked %v, source %q, %d addons", v.Checked, v.Source, len(v.Addons))
	}
	if f.Persistent() {
		t.Error("one failed check is no Status warning")
	}

	// counted across a restart, until it keeps failing
	s = newService()
	if got := s.CheckError(); got == nil || got.Failures != 1 {
		t.Fatalf("after a restart: %+v", got)
	}
	_ = s.Refresh(t.Context())
	_ = s.Refresh(t.Context())
	f = s.CheckError()
	if f == nil || f.Failures != 3 || !f.Since.Equal(v.CheckError.Since) || !f.Persistent() {
		t.Fatalf("three in a row: %+v", f)
	}

	// back: the published list, checked, the failure gone
	down.Store(false)
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Fetch(t.Context(), false)
	if v.CheckError != nil || v.Checked == nil || v.Source != "published" {
		t.Fatalf("after a check that worked: error %+v, checked %v, source %q", v.CheckError, v.Checked, v.Source)
	}
	checked := *v.Checked

	// the catalogue fetched but no addon's manifest from its repository (the adapter beside the
	// catalogue still reads, as under GitHub's rate limit): a failure too, and checked stays the
	// last good one
	manifests404.Store(true)
	if err := s.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Fetch(t.Context(), false)
	if v.CheckError == nil || !strings.Contains(v.CheckError.Message, "no addon's manifest could be read (o/a: HTTP 404)") || v.CheckError.Host != "" {
		t.Fatalf("no manifest: %+v", v.CheckError)
	}
	if v.Checked == nil || !v.Checked.Equal(checked) {
		t.Errorf("checked moved on a failed check: %v, was %v", v.Checked, checked)
	}

	// the daily run's fetch that works ends the failures; one that fails counts
	s.daily(t.Context())
	if s.CheckError() != nil {
		t.Errorf("the daily fetch worked: %+v", s.CheckError())
	}
	down.Store(true)
	s.daily(t.Context())
	if f := s.CheckError(); f == nil || f.Failures != 1 || f.Host != host {
		t.Errorf("the daily fetch failed: %+v", f)
	}
}

// Nothing loads at all (no bundled copy, the published file unreachable): the request fails, and
// the failure is recorded for the page and the warning.
func TestCheckFailureNothingLoads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	s := New([]string{srv.URL + "/catalog.json"}, "x86_64", nil)
	s.CacheFile = filepath.Join(t.TempDir(), "cache.json")
	if v, err := s.Fetch(t.Context(), false); err != nil || len(v.Addons) != 0 || v.Source != "" {
		t.Fatalf("before a check: %v %+v", err, v)
	}
	err := s.Refresh(t.Context())
	if err == nil {
		t.Fatal("nothing loaded, the check must fail")
	}
	if f := s.CheckError(); f == nil || f.Failures != 1 || !strings.Contains(f.Message, "HTTP 503") {
		t.Fatalf("%+v", f)
	}
}

// A check the user left (cancelled) is no outcome; one cut off by the time limit is a failure in
// plain words.
func TestNoteCheckCancelAndDeadline(t *testing.T) {
	s := New(nil, "x86_64", nil)
	s.noteCheck(context.Canceled)
	if s.CheckError() != nil {
		t.Error("a cancelled check was counted")
	}
	s.noteCheck(context.DeadlineExceeded)
	if f := s.CheckError(); f == nil || f.Message != "the check did not finish in time" || f.Host != "" {
		t.Errorf("%+v", f)
	}
	s.noteCheck(newSourceError("https://raw.githubusercontent.com/x", errors.New("no route to host")))
	if f := s.CheckError(); f == nil || f.Failures != 2 || f.Host != "raw.githubusercontent.com" || f.Message != "raw.githubusercontent.com: no route to host" {
		t.Errorf("%+v", f)
	}
	var nilf *CheckFailure
	if nilf.Persistent() {
		t.Error("nil is no failure")
	}
	day := &CheckFailure{Failures: 2, Since: time.Now().Add(-25 * time.Hour), At: time.Now()}
	if !day.Persistent() {
		t.Error("two failures over a day keep failing")
	}
}
