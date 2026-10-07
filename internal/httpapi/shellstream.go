package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// occulited B-53: the shell's streams in one. Each window of the admin UI held three long-lived
// streams - the addons' revision, the service messages and either the pairing requests (the
// Status page) or lite-rpc's events (the Control app) - and a browser gives plain HTTP six
// connections per host across all its tabs: a third window did not load at all. Over https
// lighttpd's HTTP/2 allows eight streams at once per connection (a fixed SETTINGS value), and the
// fourth window's page waited behind the nine long-lived ones.
//
// GET /stream?topics=addons,service-messages,pairing carries the three system feeds in one
// connection, each with the event it has on its own route (`addons`, `messages`, `pairing`), at
// once and on every change. The shell holds one for the whole browser (a SharedWorker, or the tab
// itself where there is none) with the topics its shown pages want. The single routes stay for the
// programs that use them.

// PairingFeed is the pairing requests as the stream needs them: the auth API's.
type PairingFeed interface {
	PairingView() map[string]any
	PairingWatch() (<-chan struct{}, func())
}

// PairingView is the Status page's card: the switch and the pending requests.
func (a *AuthAPI) PairingView() map[string]any { return a.pairingView() }

// PairingWatch is told of every change of the pending requests; nil channel without pairing.
func (a *AuthAPI) PairingWatch() (<-chan struct{}, func()) {
	if a.Pairing == nil {
		return nil, func() {}
	}
	return a.Pairing.Watch()
}

// shellTopics are the stream's topics: the event each sends, and a scope beyond the route's own
// (system:read) where the topic needs one.
var shellTopics = map[string]struct {
	event string
	scope auth.Scope
}{
	"addons":           {event: "addons"},
	"service-messages": {event: "messages"},
	"pairing":          {event: "pairing", scope: auth.ScopeAuthAdmin},
}

// shellStreamTopics reads ?topics= (comma-separated, or the parameter repeated): the known topics,
// each once and sorted, or the first unknown one.
func shellStreamTopics(r *http.Request) ([]string, string) {
	var out []string
	for _, v := range r.URL.Query()["topics"] {
		for _, t := range strings.Split(v, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			if _, ok := shellTopics[t]; !ok {
				return nil, t
			}
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	slices.Sort(out)
	return out, ""
}

// publicShellStream: the public mode's principal may hold the stream for the service messages
// alone (the Control app's badges), as it may GET /service-messages/stream.
func publicShellStream(r *http.Request) bool {
	topics, bad := shellStreamTopics(r)
	return bad == "" && len(topics) == 1 && topics[0] == "service-messages"
}

func (a *SystemAPI) shellStream(w http.ResponseWriter, r *http.Request) {
	topics, bad := shellStreamTopics(r)
	if bad != "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad-request", Message: "unknown topic " + bad + " (addons, service-messages, pairing)"})
		return
	}
	if len(topics) == 0 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad-request", Message: "topics is required (addons, service-messages, pairing)"})
		return
	}
	if s := SessionFrom(r); s != nil {
		for _, t := range topics {
			if sc := shellTopics[t].scope; sc != "" && !s.Has(sc) {
				forbiddenScope(w, sc)
				return
			}
		}
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fmt.Errorf("streaming unsupported"))
		return
	}

	// each topic: what wakes it, and what it sends; a feed this system lacks (no service-message
	// store, no pairing) sends nothing, as its own route would answer 501
	type feed struct {
		wake <-chan struct{}
		send func()
	}
	var feeds []feed
	var expire <-chan time.Time
	event := func(name string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
	}
	for _, t := range topics {
		switch t {
		case "addons":
			ch := a.addonFeed.add(a.Root.AddonsFingerprint)
			defer a.addonFeed.remove(ch)
			feeds = append(feeds, feed{ch, func() { event("addons", map[string]any{"revision": a.addonFeed.revision()}) }})
		case "service-messages":
			if a.ServiceMessages == nil {
				continue
			}
			ch := ServiceMessageWatchers.add()
			defer ServiceMessageWatchers.remove(ch)
			feeds = append(feeds, feed{ch, func() { event("messages", a.serviceMessageView()) }})
		case "pairing":
			if a.PairingFeed == nil {
				continue
			}
			ch, stop := a.PairingFeed.PairingWatch()
			if ch == nil {
				continue
			}
			defer stop()
			feeds = append(feeds, feed{ch, func() { event("pairing", a.PairingFeed.PairingView()) }})
			// a request expires on its own: look again every few seconds (as GET /pairing/stream)
			tick := time.NewTicker(5 * time.Second)
			defer tick.Stop()
			expire = tick.C
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	for _, f := range feeds {
		f.send()
	}
	flusher.Flush()

	// one wake-up channel for all feeds: a goroutine per feed passes its signal on
	wake := make(chan int, len(feeds))
	done := r.Context().Done()
	for i, f := range feeds {
		go func() {
			for {
				select {
				case <-done:
					return
				case _, ok := <-f.wake:
					if !ok {
						return
					}
					select {
					case wake <- i:
					case <-done:
						return
					}
				}
			}
		}()
	}
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-done:
			return
		case i := <-wake:
			feeds[i].send()
			flusher.Flush()
		case <-expire:
			if a.PairingFeed != nil {
				_ = a.PairingFeed.PairingView() // expiry signals the watchers
			}
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
