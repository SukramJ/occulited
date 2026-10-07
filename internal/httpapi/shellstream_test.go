package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/servicemsg"
)

// occulited B-53: the shell's streams in one connection.

type fakePairingFeed struct {
	mu sync.Mutex
	n  int
	ch chan struct{}
}

func (f *fakePairingFeed) PairingView() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return map[string]any{"enabled": true, "requests": make([]any, f.n)}
}

func (f *fakePairingFeed) PairingWatch() (<-chan struct{}, func()) { return f.ch, func() {} }

func (f *fakePairingFeed) add() {
	f.mu.Lock()
	f.n++
	f.mu.Unlock()
	f.ch <- struct{}{}
}

type sseEvent struct{ name, data string }

func shellEvents(t *testing.T, url string) <-chan sseEvent {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	out := make(chan sseEvent, 32)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		name := ""
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				out <- sseEvent{name, strings.TrimPrefix(line, "data: ")}
			}
		}
		close(out)
	}()
	return out
}

func nextShellEvent(t *testing.T, ch <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case e, ok := <-ch:
		if !ok {
			t.Fatal("the stream ended")
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return sseEvent{}
}

func TestShellStream(t *testing.T) {
	store := &servicemsg.Store{OnChange: ServiceMessageWatchers.Changed}
	feed := &fakePairingFeed{ch: make(chan struct{}, 1)}
	api := &SystemAPI{Root: fakeRoot(t), ServiceMessages: store, PairingFeed: feed}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	for _, q := range []string{"", "?topics=", "?topics=addons,log", "?topics=events"} {
		if st, _, body := do(t, srv, "GET", "/api/system/v1/stream"+q, "", nil); st != 400 {
			t.Fatalf("%q: %d %s", q, st, body)
		}
	}

	// all three: each topic's event at once, sorted by topic
	ev := shellEvents(t, srv.URL+"/api/system/v1/stream?topics=service-messages,pairing&topics=addons,addons")
	var names []string
	for range 3 {
		names = append(names, nextShellEvent(t, ev).name)
	}
	if strings.Join(names, " ") != "addons pairing messages" {
		t.Fatalf("first events %v", names)
	}
	// each change is its own topic's event, and only that one
	api.addonsChanged()
	if e := nextShellEvent(t, ev); e.name != "addons" || !strings.Contains(e.data, `"revision"`) {
		t.Fatalf("after an addon change: %+v", e)
	}
	store.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "00010000000A10:0", Key: "LOW_BAT", Value: true, Time: time.Unix(1000, 0)})
	if e := nextShellEvent(t, ev); e.name != "messages" || !strings.Contains(e.data, `"LOW_BAT"`) {
		t.Fatalf("after a service message: %+v", e)
	}
	feed.add()
	if e := nextShellEvent(t, ev); e.name != "pairing" || !strings.Contains(e.data, `"requests":[null]`) {
		t.Fatalf("after a pairing request: %+v", e)
	}

	// one topic: nothing of the others
	one := shellEvents(t, srv.URL+"/api/system/v1/stream?topics=addons")
	if e := nextShellEvent(t, one); e.name != "addons" {
		t.Fatalf("addons alone: %+v", e)
	}
	store.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "00010000000A10:0", Key: "UNREACH", Value: true, Time: time.Unix(1001, 0)})
	if e := nextShellEvent(t, ev); e.name != "messages" {
		t.Fatalf("the three-topic stream: %+v", e)
	}
	select {
	case e := <-one:
		t.Fatalf("the addons stream sent %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
}

// A system without the service-message store or pairing: those topics send nothing, the others work.
func TestShellStreamMissingFeeds(t *testing.T) {
	api := &SystemAPI{Root: fakeRoot(t)}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	ev := shellEvents(t, srv.URL+"/api/system/v1/stream?topics=addons,service-messages,pairing")
	if e := nextShellEvent(t, ev); e.name != "addons" {
		t.Fatalf("%+v", e)
	}
	select {
	case e := <-ev:
		t.Fatalf("a feed this system lacks sent %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	// the auth API without pairing: no channel, the topic is quiet
	api.PairingFeed = &AuthAPI{}
	ev = shellEvents(t, srv.URL+"/api/system/v1/stream?topics=pairing,addons")
	if e := nextShellEvent(t, ev); e.name != "addons" {
		t.Fatalf("%+v", e)
	}
}

// The pairing topic is the administrator's (auth:admin), as GET /pairing/stream is.
func TestShellStreamPairingScope(t *testing.T) {
	api := &SystemAPI{Root: fakeRoot(t), PairingFeed: &fakePairingFeed{ch: make(chan struct{}, 1)}}
	mux := http.NewServeMux()
	api.Register(mux)
	for _, c := range []struct {
		role auth.Role
		want int
	}{{auth.RoleUser, 403}, {auth.RoleAdmin, 200}} {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest("GET", "/api/system/v1/stream?topics=addons,pairing", nil)
		req = req.WithContext(context.WithValue(ctx, ctxKey{}, &auth.Session{ID: "s", User: "u", Role: c.role, Scopes: auth.RoleScopes(c.role)}))
		w := httptest.NewRecorder()
		done := make(chan struct{})
		go func() { mux.ServeHTTP(w, req); close(done) }()
		if c.want == 200 {
			time.Sleep(100 * time.Millisecond)
		}
		cancel()
		<-done
		if w.Code != c.want {
			t.Fatalf("%s: %d %s", c.role, w.Code, w.Body)
		}
		if c.want == 403 && !strings.Contains(w.Body.String(), "auth:admin") {
			t.Fatalf("the 403 names no scope: %s", w.Body)
		}
	}
	// a user's stream without the pairing topic
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/api/system/v1/stream?topics=addons", nil)
	req = req.WithContext(context.WithValue(ctx, ctxKey{}, &auth.Session{ID: "s", User: "u", Role: auth.RoleUser, Scopes: auth.RoleScopes(auth.RoleUser)}))
	w := httptest.NewRecorder()
	cancel()
	mux.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("a user's addons: %d", w.Code)
	}
}

// The public mode may hold the stream for the service messages alone.
func TestPublicShellStream(t *testing.T) {
	for q, want := range map[string]bool{
		"topics=service-messages":                  true,
		"topics=service-messages,service-messages": true,
		"topics=service-messages,addons":           false,
		"topics=pairing":                           false,
		"topics=":                                  false,
		"topics=nope":                              false,
		"":                                         false,
	} {
		for method, ok := range map[string]bool{"GET": want, "POST": false} {
			r := httptest.NewRequest(method, "/api/system/v1/stream?"+q, nil)
			if got := publicPath(r); got != ok {
				t.Errorf("%s %q: %v, want %v", method, q, got, ok)
			}
		}
	}
}
