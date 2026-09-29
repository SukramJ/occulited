package httpapi

import (
	"errors"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/system"
)

// Task 149's local key mode (D-103): the HmIP network key on the box. Every route is the
// administrator's - the answer never carries a key, but switching the network's key is not the
// read-only box token's business, and neither is knowing which module's identity is kept aside.

func (a *SystemAPI) localKeyReady(w http.ResponseWriter) bool {
	if a.HmIPLocalKey == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no local key service"})
		return false
	}
	return true
}

func localKeyError(w http.ResponseWriter, err error) {
	var lk system.ErrLocalKey
	if errors.As(err, &lk) {
		writeJSON(w, http.StatusConflict, apiError{Error: "local-key", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, apiError{Error: "local-key", Message: err.Error()})
}

func (a *SystemAPI) localKeyStatus(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	st := a.HmIPLocalKey.Status()
	if r.URL.Query().Get("devices") == "1" {
		n := a.HmIPLocalKey.DeviceCount(r.Context())
		st.Devices = &n
	}
	writeJSON(w, 200, st)
}

func (a *SystemAPI) localKeyEnable(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var b struct {
		Mode        string `json:"mode"`
		NetworkKey  string `json:"network_key"`
		BackboneKey string `json:"backbone_key"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if err := a.HmIPLocalKey.Enable(b.Mode, b.NetworkKey, b.BackboneKey); err != nil {
		localKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.HmIPLocalKey.Status())
}

func (a *SystemAPI) localKeyDisable(w http.ResponseWriter, _ *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	if err := a.HmIPLocalKey.Disable(); err != nil {
		localKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.HmIPLocalKey.Status())
}

func (a *SystemAPI) localKeyOverride(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var b struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if err := a.HmIPLocalKey.Override(b.On); err != nil {
		localKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.HmIPLocalKey.Status())
}

func (a *SystemAPI) localKeyDiscard(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	if err := a.HmIPLocalKey.DiscardSnapshot(r.PathValue("sgtin")); err != nil {
		localKeyError(w, err)
		return
	}
	writeJSON(w, 200, a.HmIPLocalKey.Status())
}

// localKeyRestore puts a fresh-start snapshot back (openccu-lite task 212): {"confirm": true,
// "hostname": <the system's name as typed>}, the confirmation the fresh start takes. A wrong name
// is 400 hostname and nothing happens; what the service refuses is 409; 202 with the status while
// the files return and HmIP-RF restarts.
func (a *SystemAPI) localKeyRestore(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var body struct {
		Confirm  bool   `json:"confirm"`
		Hostname string `json:"hostname"`
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
	if err := a.HmIPLocalKey.RestoreSnapshot(r.PathValue("sgtin")); err != nil {
		localKeyError(w, err)
		return
	}
	reqLog(r).Warn("local key mode: fresh-start snapshot restored", "sgtin", r.PathValue("sgtin"))
	writeJSON(w, http.StatusAccepted, a.HmIPLocalKey.Status())
}
