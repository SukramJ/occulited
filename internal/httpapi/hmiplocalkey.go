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

// identityConfirmed answers the refusal of a request that changes the HmIP identity files without the
// user's word (openccu-lite task 317, D-120): 400 confirm without "confirm": true, 400 hostname when
// the name is asked for (typed) and does not match this system's. True when the request may go on.
func (a *SystemAPI) identityConfirmed(w http.ResponseWriter, confirm bool, hostname string, typed bool) bool {
	if !confirm {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "confirm", Message: "confirm: true is required"})
		return false
	}
	if typed && !sameHostname(hostname, a.Root.Hostname()) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "hostname", Message: "the host name typed does not match this system's"})
		return false
	}
	return true
}

// localKeyEnable switches local key mode on: {mode, network_key?, backbone_key?, confirm,
// hostname?}. The switch makes hmipserver write the module's identity files under the new key
// (D-120): confirm is required, and for mode generate - every paired device has to be taught in
// again - the host name typed as well, unless hmipserver lists no device.
func (a *SystemAPI) localKeyEnable(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var b struct {
		Mode        string `json:"mode"`
		NetworkKey  string `json:"network_key"`
		BackboneKey string `json:"backbone_key"`
		Confirm     bool   `json:"confirm"`
		Hostname    string `json:"hostname"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if !a.identityConfirmed(w, b.Confirm, "", false) {
		return
	}
	// a generated key: every paired device has to be taught in again - the host name typed, unless
	// hmipserver lists no device (the welcome page's step on an empty network)
	if b.Mode == "generate" && !sameHostname(b.Hostname, a.Root.Hostname()) && a.HmIPLocalKey.DeviceCount(r.Context()) != 0 {
		a.identityConfirmed(w, true, b.Hostname, true)
		return
	}
	if err := a.HmIPLocalKey.Enable(b.Mode, b.NetworkKey, b.BackboneKey); err != nil {
		localKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.HmIPLocalKey.Status())
}

// localKeyDisable goes back to eQ-3's key server: {confirm, hostname}. The snapshot's identity
// files replace the ones in hmipserver's data directory (a copy of those is kept first), and the
// devices taught in under the local key since have to be taught in again (D-120: the host name
// typed).
func (a *SystemAPI) localKeyDisable(w http.ResponseWriter, r *http.Request) {
	if !a.localKeyReady(w) {
		return
	}
	var b struct {
		Confirm  bool   `json:"confirm"`
		Hostname string `json:"hostname"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if !a.identityConfirmed(w, b.Confirm, b.Hostname, true) {
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
