package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// subscriberProbe is the Interfaces page's reachability probe (task 76's follow-up, D-64), shared
// by every request so its verdicts are kept across them; a package variable like subscriberSettle,
// so the tests can give it a fake dial and short limits.
var subscriberProbe = &system.SubscriberProbe{}

// subscriberProbeLimit bounds one request: sixteen connects run at once, a second each.
var subscriberProbeLimit = 5 * time.Second

// radioSubscriberReach answers whether each registered callback accepts a TCP connection - the hint
// "not reachable" on the Interfaces page's process cards. Only the addresses in the handlers files of
// the interfaces in InterfacesList.xml are connected to; nothing is taken from the request. Each
// address at most once per 30 s, at most a second per connect. Any role, like GET /radio, whose
// list it annotates.
func (a *SystemAPI) radioSubscriberReach(w http.ResponseWriter, r *http.Request) {
	names := []string{}
	for _, i := range a.Root.ReadRadio().Interfaces {
		names = append(names, i.Name)
	}
	ctx, cancel := context.WithTimeout(r.Context(), subscriberProbeLimit)
	defer cancel()
	writeJSON(w, 200, map[string]any{"subscribers": subscriberProbe.Probe(ctx, a.Root, names)})
}
