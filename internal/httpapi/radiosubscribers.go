package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// subscriberSettle is how long the remove route watches the handlers file for the entry to go.
// rfd and the VirtualDevices process rewrite their file before init answers; hmipserver writes
// LegacyService.handlers a moment later (an added entry appeared after about 4 s on the Charly, a
// removed one was gone at once, 2026-09-12). The tests make it short.
var (
	subscriberSettle = 3 * time.Second
	subscriberPoll   = 200 * time.Millisecond
)

// radioSubscriberRemove removes one registered callback from an interface process (task 76): the
// XML-RPC init(url, "") every client uses to deregister, on the process named in
// InterfacesList.xml. Only a pair that is in the process's handlers file right now is sent - the
// url is never taken from the request alone - and the answer says whether the entry went.
func (a *SystemAPI) radioSubscriberRemove(w http.ResponseWriter, r *http.Request) {
	s := SessionFrom(r)
	if s == nil || !s.Has(auth.ScopeSystemWrite) {
		forbiddenScope(w, auth.ScopeSystemWrite)
		return
	}
	var body struct {
		Interface string `json:"interface"`
		ID        string `json:"id"`
		URL       string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		badBody(w, err)
		return
	}
	if body.Interface == "" || body.ID == "" || body.URL == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "bad-request", Message: "interface, id and url are required"})
		return
	}
	known := false
	for _, i := range a.Root.ReadRadio().Interfaces {
		if i.Name == body.Interface {
			known = true
			break
		}
	}
	if !known {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "unknown-interface", Message: "no such interface in InterfacesList.xml"})
		return
	}
	if !a.Root.HasSubscriber(body.Interface, body.ID, body.URL) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "not-subscribed", Message: "this subscription is not registered with " + body.Interface + " (any more)"})
		return
	}
	if a.InitInterface == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no interface processes on this system"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	err := a.InitInterface(ctx, body.Interface, body.URL, "")
	cancel()
	if err != nil {
		slog.Warn("radio: removing a subscription failed", "user", s.User, "interface", body.Interface, "id", body.ID, "url", body.URL, "err", err)
		writeJSON(w, http.StatusBadGateway, apiError{Error: "interface-call", Message: body.Interface + ": " + err.Error()})
		return
	}
	removed := waitRemoved(r.Context(), a.Root, body.Interface, body.ID, body.URL)
	slog.Info("radio: subscription removed", "user", s.User, "interface", body.Interface, "id", body.ID, "url", body.URL, "removed", removed)
	writeJSON(w, 200, map[string]any{"removed": removed, "subscribers": a.Root.InterfaceSubscribers(body.Interface)})
}

// waitRemoved re-reads the handlers file until the pair is gone or subscriberSettle has passed.
func waitRemoved(ctx context.Context, root system.Root, iface, id, url string) bool {
	deadline := time.Now().Add(subscriberSettle)
	for {
		if !root.HasSubscriber(iface, id, url) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return !root.HasSubscriber(iface, id, url)
		case <-time.After(subscriberPoll):
		}
	}
}
