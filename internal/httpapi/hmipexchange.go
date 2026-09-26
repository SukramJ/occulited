package httpapi

import (
	"log/slog"
	"net/http"
)

// Task 155 (D-106): the adapter exchange's diagnosis and what the page offers. The view says why
// hmipserver stopped (the key server unreachable, or this module refused), which previous module's
// network is on the system and whether local key mode is on; the retry restarts HmIP-RF for a key
// server that was not reached; the fresh start is the guided action for a refused module - admin
// only, with the host name typed as the confirmation, like the factory reset - and answers 202
// with the view while the identity moves aside and HmIP-RF restarts.

func (a *SystemAPI) exchangeView(w http.ResponseWriter, _ *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	writeJSON(w, 200, a.exchange())
}

// exchange is the view plus the host name the confirmation has to repeat.
func (a *SystemAPI) exchange() map[string]any {
	v := a.HmIPLocalKey.Exchange()
	return map[string]any{
		"fatal": v.Fatal, "module": v.Module, "previous": v.Previous, "replaces_snapshots": v.ReplacesSnapshots,
		"local_key": v.LocalKey, "exchange_id": v.ExchangeID, "switching": v.Switching, "error": v.Error,
		"hostname": a.Root.Hostname(),
	}
}

func (a *SystemAPI) exchangeRetry(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	if err := a.HmIPLocalKey.RetryExchange(); err != nil {
		localKeyError(w, err)
		return
	}
	if s := SessionFrom(r); s != nil {
		reqLog(r).Info("adapter exchange: retry")
	}
	writeJSON(w, http.StatusAccepted, a.exchange())
}

// exchangeFreshStart takes {"confirm": true, "hostname": <the system's name as typed>, "local_key":
// bool}. A wrong name is 400 hostname and nothing happens; what the service refuses is 409.
func (a *SystemAPI) exchangeFreshStart(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var body struct {
		Confirm  bool   `json:"confirm"`
		Hostname string `json:"hostname"`
		LocalKey bool   `json:"local_key"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	if !body.Confirm {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "confirm", Message: "confirm: true is required"})
		return
	}
	if !sameHostname(body.Hostname, a.Root.Hostname()) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "hostname", Message: "the host name typed does not match this system's"})
		return
	}
	if err := a.HmIPLocalKey.FreshStart(body.LocalKey); err != nil {
		localKeyError(w, err)
		return
	}
	user := ""
	if s := SessionFrom(r); s != nil {
		user = s.User
	}
	slog.Warn("adapter exchange: fresh start with this module", "user", user, "local_key", body.LocalKey)
	writeJSON(w, http.StatusAccepted, a.exchange())
}
