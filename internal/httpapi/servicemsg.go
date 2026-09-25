package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/servicemsg"
)

// The service messages (task 75): read-only, the count and the list with the device's name and
// its rooms from the metadata store, and a stream that pushes the list to an open Status page
// whenever it changes. Acknowledging is the WebUI's (D-80); persisting the times is task 82's.

// ServiceMessageView is one message as the page shows it.
type ServiceMessageView struct {
	servicemsg.Message
	// Name and Enums come from the metadata store's device object, where there is one; the page
	// says "<type> <address>" otherwise, as the CCU WebUI does
	Name  string   `json:"name,omitempty"`
	Enums []string `json:"enums,omitempty"`
}

func (a *SystemAPI) serviceMessageView() map[string]any {
	snap := a.ServiceMessages.List()
	list := make([]ServiceMessageView, 0, len(snap.Messages))
	for _, m := range snap.Messages {
		v := ServiceMessageView{Message: m}
		if a.Names != nil {
			if name, enums, ok := a.Names(m.Interface + "." + m.Address); ok {
				v.Name, v.Enums = name, enums
			}
		}
		list = append(list, v)
	}
	body := map[string]any{"count": snap.Count, "messages": list, "swept": snap.Swept, "errors": snap.Errors}
	if a.RPC != nil {
		body["feed"] = a.RPC.View()
	}
	return body
}

func (a *SystemAPI) serviceMessages(w http.ResponseWriter, _ *http.Request) {
	if a.ServiceMessages == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no service-message store"})
		return
	}
	writeJSON(w, 200, a.serviceMessageView())
}

// serviceMessageWatchers fans the store's OnChange out to the open streams.
type serviceMessageWatchers struct {
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
}

func (s *serviceMessageWatchers) Changed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *serviceMessageWatchers) add() chan struct{} {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[chan struct{}]struct{}{}
	}
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	return ch
}

func (s *serviceMessageWatchers) remove(ch chan struct{}) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

// ServiceMessageWatchers is what the store's OnChange should call; the streams hang off it.
var ServiceMessageWatchers = &serviceMessageWatchers{}

// serviceMessagesStream sends the whole view as one SSE event: at once, on every change, and
// a comment every 30 s so the connection is known to be alive.
func (a *SystemAPI) serviceMessagesStream(w http.ResponseWriter, r *http.Request) {
	if a.ServiceMessages == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no service-message store"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, fmt.Errorf("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	ch := ServiceMessageWatchers.add()
	defer ServiceMessageWatchers.remove(ch)
	send := func() {
		b, _ := json.Marshal(a.serviceMessageView())
		fmt.Fprintf(w, "event: messages\ndata: %s\n\n", b)
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
