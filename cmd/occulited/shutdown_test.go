package main

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/httpapi"
	"github.com/hobbyquaker/occulited/internal/meta"
	"github.com/hobbyquaker/occulited/internal/servicemsg"
	"github.com/hobbyquaker/occulited/internal/system"
)

// startServer serves h the way run does and returns the base URL and Serve's result.
func startServer(t *testing.T, h http.Handler) (*http.Server, func(), string, chan error) {
	t.Helper()
	srv, endRequests := newHTTPServer("127.0.0.1:0", logRequests(slog.New(slog.DiscardHandler), h))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	t.Cleanup(func() { endRequests(); _ = srv.Close() })
	return srv, endRequests, "http://" + ln.Addr().String(), served
}

// openStream opens a long-lived answer and waits for its first bytes, so the request is in
// flight when the stop comes.
func openStream(t *testing.T, url string) io.ReadCloser {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		t.Fatalf("%s: %d %s", url, res.StatusCode, b)
	}
	if _, err := bufio.NewReader(res.Body).ReadString('\n'); err != nil {
		t.Fatalf("%s: no first line: %v", url, err)
	}
	return res.Body
}

// TestStopEndsTheStreams is B-151: every long-lived answer ends on its request's context, so a
// stop with open streams (a browser on the Log page, the shell's change and service-message
// streams) is over in well under the limit, with no connection left to close.
func TestStopEndsTheStreams(t *testing.T) {
	// the live log runs journalctl --follow: a stand-in that never ends by itself
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "journalctl"), []byte("#!/bin/sh\necho '{}'\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	store, err := meta.New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&httpapi.MetaAPI{Store: store}).Register(mux)
	(&httpapi.SystemAPI{Root: system.Root(t.TempDir()), ServiceMessages: &servicemsg.Store{}, Journal: &system.JournalLog{}}).Register(mux)
	srv, endRequests, base, served := startServer(t, mux)

	var bodies []io.ReadCloser
	for _, p := range []string{"/api/meta/v1/events/sse", "/api/system/v1/service-messages/stream", "/api/system/v1/log/stream"} {
		bodies = append(bodies, openStream(t, base+p))
	}

	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, nil))
	start := time.Now()
	stopHTTPServer(srv, endRequests, shutdownLimit, log)
	if took := time.Since(start); took > time.Second {
		t.Errorf("the stop took %v, want well under the %v limit", took, shutdownLimit)
	}
	if logged.Len() > 0 {
		t.Errorf("a clean stop logs nothing: %s", logged.String())
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve: %v", err)
	}
	for i, b := range bodies {
		done := make(chan error, 1)
		go func() { _, err := io.Copy(io.Discard, b); done <- err }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Errorf("stream %d still open after the stop", i)
		}
		_ = b.Close()
	}
}

// TestStopClosesWhatOutlivesTheLimit: a handler that ignores its context is closed after the
// limit, with a warning, and the stop still returns (run then exits 0).
func TestStopClosesWhatOutlivesTheLimit(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stuck", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "open\n")
		w.(http.Flusher).Flush()
		<-release
	})
	srv, endRequests, base, served := startServer(t, mux)
	body := openStream(t, base+"/stuck")
	defer body.Close()

	var logged strings.Builder
	log := slog.New(slog.NewTextHandler(&logged, nil))
	start := time.Now()
	stopHTTPServer(srv, endRequests, 200*time.Millisecond, log)
	if took := time.Since(start); took < 200*time.Millisecond || took > 2*time.Second {
		t.Errorf("the stop took %v, want the 200ms limit", took)
	}
	if !strings.Contains(logged.String(), "connections still open were closed") {
		t.Errorf("no warning: %q", logged.String())
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve: %v", err)
	}
	_, _ = io.Copy(io.Discard, body) // closed by the server: returns rather than hangs
}
