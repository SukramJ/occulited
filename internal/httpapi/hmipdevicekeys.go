package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/system"
)

// Task 154's HmIP device keys (D-103, D-104): every route needs the scope radio:keys, which only
// Full access includes and an addon's token never gets. Only the export answers keys, and a
// session - even a valid one - has to confirm it first: a confirmed ticket from POST
// /api/auth/v1/ticket (the password) or from the provider's fresh login, in the header
// X-Occulite-Confirm. A token with the scope is not asked; holding it is the grant.

// DeviceKeysExportPath is the export's path, the one the confirmed ticket is issued for.
const DeviceKeysExportPath = "/api/system/v1/radio/hmip/device-keys/export"

func (a *SystemAPI) deviceKeysReady(w http.ResponseWriter) bool {
	if a.HmIPDeviceKeys == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no device key service"})
		return false
	}
	return true
}

func deviceKeysError(w http.ResponseWriter, err error) {
	var dk system.ErrDeviceKeys
	if errors.As(err, &dk) {
		writeJSON(w, http.StatusConflict, apiError{Error: "device-keys", Message: err.Error()})
		return
	}
	writeJSON(w, http.StatusInternalServerError, apiError{Error: "device-keys", Message: err.Error()})
}

func (a *SystemAPI) deviceKeys(w http.ResponseWriter, r *http.Request) {
	if !a.deviceKeysReady(w) {
		return
	}
	writeJSON(w, 200, a.HmIPDeviceKeys.View(r.Context()))
}

func (a *SystemAPI) deviceKeyAdd(w http.ResponseWriter, r *http.Request) {
	if !a.deviceKeysReady(w) {
		return
	}
	var b struct {
		Code  string `json:"code"`
		SGTIN string `json:"sgtin"`
		Key   string `json:"key"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	res, err := a.HmIPDeviceKeys.Add(r.Context(), b.Code, b.SGTIN, b.Key)
	if err != nil {
		var dk system.ErrDeviceKeys
		if errors.As(err, &dk) {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid-code", Message: err.Error()})
			return
		}
		deviceKeysError(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (a *SystemAPI) deviceKeyDelete(w http.ResponseWriter, r *http.Request) {
	if !a.deviceKeysReady(w) {
		return
	}
	if err := a.HmIPDeviceKeys.Delete(r.PathValue("sgtin")); err != nil {
		var dk system.ErrDeviceKeys
		if errors.As(err, &dk) {
			writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: err.Error()})
			return
		}
		deviceKeysError(w, err)
		return
	}
	writeJSON(w, 200, a.HmIPDeviceKeys.View(r.Context()))
}

func (a *SystemAPI) deviceKeysApply(w http.ResponseWriter, r *http.Request) {
	if !a.deviceKeysReady(w) {
		return
	}
	if err := a.HmIPDeviceKeys.Apply(); err != nil {
		deviceKeysError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, a.HmIPDeviceKeys.View(r.Context()))
}

func (a *SystemAPI) deviceKeysExport(w http.ResponseWriter, r *http.Request) {
	if !a.deviceKeysReady(w) {
		return
	}
	sess := SessionFrom(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "unauthenticated", Message: "login required"})
		return
	}
	if !sess.IsToken() {
		t := r.Header.Get("X-Occulite-Confirm")
		if t == "" || a.ConfirmTicket == nil || !a.ConfirmTicket(t, DeviceKeysExportPath, sess.ID) {
			writeJSON(w, http.StatusForbidden, apiError{Error: "confirm-required", Message: "the key sheet asks for the password (or a fresh login at the provider) every time"})
			return
		}
	}
	keys := a.HmIPDeviceKeys.Export(r.Context())
	slog.Info("hmip device keys: exported", "by", sess.User, "remote", remote(r), "keys", len(keys))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"keys": keys})
}
