package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// openccu-lite B-297: the shell's Addons menu and the pinned addon tabs are the nav and addon lists
// it read at sign-in, so an addon uninstalled (or installed) stayed (or stayed missing) until a
// reload - in the tab that did it and in every other. GET /addons/stream tells an open shell when
// those lists may have changed: a revision, sent at once and again on every change, and the shell
// reads both lists again when it differs from the one it has.
//
// A change is reported by the routes that make one (an uninstall, an install's end, enable and
// disable), at once; and, for what changes beside them (an install from the console, an addon's
// own update), by a look at the addons' files every few seconds while a stream is open
// (system.Root.AddonsFingerprint: names, modes, sizes, times - no script runs).

// addonPollEvery is how often the files are looked at while a stream is open.
const addonPollEvery = 3 * time.Second

// addonChanges is the revision and the open streams. The zero value is ready.
type addonChanges struct {
	mu      sync.Mutex
	rev     int64
	subs    map[chan struct{}]struct{}
	polling bool
	every   time.Duration // the look's interval; 0 is addonPollEvery (a test sets it)
}

// revision is a number that changes with every change; it starts from the clock, so a restarted
// service never says the revision a shell already has.
func (c *addonChanges) revision() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rev == 0 {
		c.rev = time.Now().UnixMilli()
	}
	return c.rev
}

// changed counts a change and wakes every open stream.
func (c *addonChanges) changed() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rev == 0 {
		c.rev = time.Now().UnixMilli()
	}
	c.rev++
	for ch := range c.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// add opens a stream; the first one starts the look at the files, which ends with the last one.
func (c *addonChanges) add(fingerprint func() string) chan struct{} {
	ch := make(chan struct{}, 1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs == nil {
		c.subs = map[chan struct{}]struct{}{}
	}
	c.subs[ch] = struct{}{}
	if !c.polling && fingerprint != nil {
		c.polling = true
		every := c.every
		if every <= 0 {
			every = addonPollEvery
		}
		go c.poll(fingerprint, every)
	}
	return ch
}

func (c *addonChanges) remove(ch chan struct{}) {
	c.mu.Lock()
	delete(c.subs, ch)
	c.mu.Unlock()
}

func (c *addonChanges) poll(fingerprint func() string, every time.Duration) {
	last := fingerprint()
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		c.mu.Lock()
		if len(c.subs) == 0 {
			c.polling = false
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		if fp := fingerprint(); fp != last {
			last = fp
			c.changed()
		}
	}
}

// addonsChanged is what a route that changed the addons calls.
func (a *SystemAPI) addonsChanged() { a.addonFeed.changed() }

func (a *SystemAPI) addonsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	ch := a.addonFeed.add(a.Root.AddonsFingerprint)
	defer a.addonFeed.remove(ch)
	send := func() {
		b, _ := json.Marshal(map[string]any{"revision": a.addonFeed.revision()})
		fmt.Fprintf(w, "event: addons\ndata: %s\n\n", b)
		flusher.Flush()
	}
	send()
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			send()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
