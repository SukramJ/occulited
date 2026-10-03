package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite B-297: an open shell learns that the addons changed - from the route that changed
// them at once, and from the files for what changed beside occulited.

func addonEvents(t *testing.T, url string) <-chan int64 {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan int64, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "addons":
				var v struct{ Revision int64 }
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v) == nil {
					out <- v.Revision
				}
			}
		}
		close(out)
	}()
	return out
}

func nextRevision(t *testing.T, ch <-chan int64, after int64) int64 {
	t.Helper()
	select {
	case r, ok := <-ch:
		if !ok {
			t.Fatal("the stream ended")
		}
		if r <= after {
			t.Fatalf("revision %d after %d", r, after)
		}
		return r
	case <-time.After(5 * time.Second):
		t.Fatalf("no event after revision %d", after)
	}
	return 0
}

func noEvent(t *testing.T, ch <-chan int64, d time.Duration) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("an event without a change: %d", r)
	case <-time.After(d):
	}
}

func TestAddonsStream(t *testing.T) {
	r := fakeRoot(t)
	svc := scriptBox{system.AddonScripts{Root: r}}
	mux := http.NewServeMux()
	api := &SystemAPI{Root: r, Services: svc, Log: testLog, Addons: svc, Manager: svc, Nav: svc}
	api.addonFeed.every = 50 * time.Millisecond
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	ev := addonEvents(t, srv.URL+"/api/system/v1/addons/stream")
	first := nextRevision(t, ev, 0)
	if first < time.Now().Add(-time.Minute).UnixMilli() {
		t.Fatalf("the first revision does not start from the clock: %d", first)
	}
	noEvent(t, ev, 300*time.Millisecond)

	// the uninstall route: at once
	if st, out, _ := do(t, srv, "POST", "/api/system/v1/addons/mosquitto/uninstall", "", nil); st != 200 {
		t.Fatalf("uninstall: %d %v", st, out)
	}
	rev := nextRevision(t, ev, first)

	// an install beside occulited (the console's install_addon): the files say it
	rc := filepath.Join(string(r), "usr/local/etc/config/rc.d")
	if err := os.WriteFile(filepath.Join(rc, "hm2mqtt"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rev = nextRevision(t, ev, rev)
	// disabled beside occulited: the executable bit
	if err := os.Chmod(filepath.Join(rc, "hm2mqtt"), 0o644); err != nil {
		t.Fatal(err)
	}
	rev = nextRevision(t, ev, rev)

	// a second stream starts at the current revision; both see the next change
	ev2 := addonEvents(t, srv.URL+"/api/system/v1/addons/stream")
	if got := nextRevision(t, ev2, 0); got != rev {
		t.Fatalf("a new stream's revision %d, want %d", got, rev)
	}
	api.addonsChanged()
	r1, r2 := nextRevision(t, ev, rev), nextRevision(t, ev2, rev)
	if r1 != r2 {
		t.Fatalf("two streams, two revisions: %d %d", r1, r2)
	}
}

// The look at the files ends with the last stream.
func TestAddonChangesPollEnds(t *testing.T) {
	c := addonChanges{every: 10 * time.Millisecond}
	calls := make(chan struct{}, 100)
	fp := func() string { calls <- struct{}{}; return "x" }
	ch := c.add(fp)
	<-calls
	c.remove(ch)
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		polling := c.polling
		c.mu.Unlock()
		if !polling {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the poll did not end")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// and starts again with the next one
	ch = c.add(fp)
	defer c.remove(ch)
	c.mu.Lock()
	polling := c.polling
	c.mu.Unlock()
	if !polling {
		t.Fatal("no poll for a new stream")
	}
}
