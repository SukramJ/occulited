package httpapi

// openccu-lite task 227: IPv6 per interface - off, SLAAC, DHCPv6 or static - with a 60 s rollback
// (system.IPv6Tx). The page applies, then confirms from the browser within the window, over the
// new configuration; without the confirmation the previous configuration comes back by itself.

import (
	"errors"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

func (a *SystemAPI) registerIPv6(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/network/ipv6", a.ipv6Get)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/network/ipv6", a.ipv6Begin)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/network/ipv6/confirm", a.ipv6Confirm)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/network/ipv6/revert", a.ipv6Revert)
}

// ipv6View is every live interface's configuration and the open change.
func (a *SystemAPI) ipv6View() map[string]any {
	n := a.Root.ReadNetwork()
	names := make([]string, 0, len(n.Interfaces))
	for _, i := range n.Interfaces {
		names = append(names, i.Name)
	}
	return map[string]any{
		"interfaces":     a.IPv6.Settings(names),
		"pending":        a.IPv6.Pending(),
		"window_seconds": int(system.IPv6Window.Seconds()),
		"writable":       !a.Root.HostManaged(),
	}
}

func (a *SystemAPI) ipv6Ready(w http.ResponseWriter) bool {
	if a.IPv6 == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "IPv6 settings are not available on this system"})
		return false
	}
	return true
}

func (a *SystemAPI) ipv6Get(w http.ResponseWriter, _ *http.Request) {
	if !a.ipv6Ready(w) {
		return
	}
	writeJSON(w, 200, a.ipv6View())
}

func (a *SystemAPI) ipv6Begin(w http.ResponseWriter, r *http.Request) {
	if a.hostManaged(w, "the network") || !a.ipv6Ready(w) {
		return
	}
	var body struct {
		Interface string `json:"interface"`
		system.IPv6Settings
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	p, err := a.IPv6.Begin(r.Context(), body.Interface, body.IPv6Settings)
	switch {
	case errors.Is(err, system.ErrIPv6Pending):
		writeJSON(w, http.StatusConflict, apiError{Error: "pending", Message: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	out := a.ipv6View()
	out["changed"] = p != nil
	writeJSON(w, 200, out)
}

func (a *SystemAPI) ipv6Confirm(w http.ResponseWriter, r *http.Request) { a.ipv6Finish(w, r, true) }
func (a *SystemAPI) ipv6Revert(w http.ResponseWriter, r *http.Request)  { a.ipv6Finish(w, r, false) }

func (a *SystemAPI) ipv6Finish(w http.ResponseWriter, r *http.Request, confirm bool) {
	if a.hostManaged(w, "the network") || !a.ipv6Ready(w) {
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	var err error
	if confirm {
		err = a.IPv6.Confirm(body.Token)
	} else {
		err = a.IPv6.Revert(r.Context(), body.Token)
	}
	if errors.Is(err, system.ErrNoIPv6Tx) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "no such pending IPv6 change: it was confirmed, reverted or rolled back already"})
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	out := a.ipv6View()
	out["confirmed"] = confirm
	writeJSON(w, 200, out)
}
