package httpapi

import (
	"errors"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/system"
	"github.com/hobbyquaker/occulited/internal/wifi"
)

// ---- System -> Network: Wi-Fi (task 89) ------------------------------------------------------
//
// GET /wifi: the chips (also while switched off), the settings, the state and connection, the
// saved networks (never their keys). PUT /wifi: the switch, the country, the addressing, the
// preferred interface. POST /wifi/scan, POST /wifi/networks (connect), DELETE /wifi/networks/{ssid}
// (forget). A change made over the Wi-Fi itself is reverted after 90 s unless POST /wifi/confirm
// comes first; the view carries the deadline.

func (a *SystemAPI) wifiUnavailable(w http.ResponseWriter) bool {
	if a.WiFi == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "Wi-Fi is not available on this system"})
		return true
	}
	return false
}

func wifiErr(w http.ResponseWriter, err error) {
	if errors.Is(err, system.ErrWiFiInvalid) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	writeErr(w, err)
}

func (a *SystemAPI) wifiGet(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	writeJSON(w, 200, a.WiFi.View())
}

func (a *SystemAPI) wifiPut(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	var s wifi.Settings
	if err := readJSON(r, &s); err != nil {
		badBody(w, err)
		return
	}
	if err := a.WiFi.PutSettings(r.Context(), s, a.WiFi.OverWiFi(remote(r))); err != nil {
		wifiErr(w, err)
		return
	}
	writeJSON(w, 200, a.WiFi.View())
}

func (a *SystemAPI) wifiScan(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	res, saved, err := a.WiFi.Scan(r.Context())
	if err != nil {
		wifiErr(w, err)
		return
	}
	if res == nil {
		res = []wifi.ScanResult{}
	}
	if saved == nil {
		saved = []string{}
	}
	writeJSON(w, 200, map[string]any{"networks": res, "saved": saved})
}

type wifiConnectBody struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Password string `json:"password"`
	Hidden   bool   `json:"hidden"`
}

func (a *SystemAPI) wifiConnect(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	var b wifiConnectBody
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if err := a.WiFi.Connect(r.Context(), b.SSID, b.Security, b.Password, b.Hidden, a.WiFi.OverWiFi(remote(r))); err != nil {
		wifiErr(w, err)
		return
	}
	writeJSON(w, 200, a.WiFi.View())
}

func (a *SystemAPI) wifiForget(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	if err := a.WiFi.Forget(r.Context(), r.PathValue("ssid"), a.WiFi.OverWiFi(remote(r))); err != nil {
		wifiErr(w, err)
		return
	}
	writeJSON(w, 200, a.WiFi.View())
}

func (a *SystemAPI) wifiConfirm(w http.ResponseWriter, r *http.Request) {
	if a.wifiUnavailable(w) {
		return
	}
	writeJSON(w, 200, map[string]any{"confirmed": a.WiFi.Confirm()})
}
