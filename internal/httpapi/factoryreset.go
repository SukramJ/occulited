package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The factory reset (task 109, D-106): the marker the firmware's boot-time check reads, set from
// the Backup page and followed by a reboot. Nothing is kept. The GET is the page's warning: which
// interfaces still list devices, whether the BidCos security key is the default (task 81's check)
// and whether the HmIP network key is on the system (task 149) - the reset erases both, and a
// BidCos device paired under an individual key keeps that key. No key is read out here.

func (a *SystemAPI) registerFactoryReset(mux *http.ServeMux, p string) {
	route(mux, auth.ScopePower, "GET "+p+"/factory-reset", a.factoryResetView)
	route(mux, auth.ScopePower, "POST "+p+"/factory-reset", a.factoryReset)
	route(mux, auth.ScopePower, "DELETE "+p+"/factory-reset", a.factoryResetCancel)
}

// factoryResetView answers what the page shows before it asks: the host name the confirmation has
// to repeat, the marker's state, the paired devices per interface (unknown when the interface does
// not answer), the key states. Administrator only, like the routes that act.
func (a *SystemAPI) factoryResetView(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), system.PairedDevicesTimeout+time.Second)
	defer cancel()
	paired := map[string]system.PairedDevices{}
	if a.RadioInterfaces != nil {
		paired = system.CountPaired(ctx, a.RadioInterfaces())
	}
	keySet, keyKnown, keyErr := a.Root.SecurityKeyState(ctx)
	out := map[string]any{
		"hostname":           a.Root.Hostname(),
		"armed":              a.Root.FactoryResetArmed(),
		"container":          a.Root.Container(),
		"update_staged":      a.Root.UpdateStaged(),
		"interfaces":         paired,
		"security_key_set":   keySet,
		"security_key_known": keyKnown && keyErr == nil,
		"hmip_local_key":     a.Root.HmIPLocalKeyEnabled(),
	}
	if keyErr != nil {
		out["security_key_error"] = keyErr.Error()
	}
	writeJSON(w, 200, out)
}

// factoryReset arms the reset and reboots. {"confirm": true} as every power route, and "hostname":
// the system's name typed by the user (case and surrounding space do not matter) - a wrong one is
// 400 hostname and nothing happens. 409 update-staged while an update waits for the recovery
// system. When the reboot fails the marker goes again.
func (a *SystemAPI) factoryReset(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
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
	if a.Manager == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no addon manager"})
		return
	}
	if err := a.Root.ArmFactoryReset(); err != nil {
		if errors.Is(err, system.ErrUpdateStaged) {
			detail := map[string]any{}
			if u := a.Root.StagedSystemUpdate(); u != nil {
				detail["file"] = u.File
			}
			writeJSON(w, http.StatusConflict, apiError{Error: "update-staged", Message: err.Error(), Detail: detail})
			return
		}
		writeErr(w, err)
		return
	}
	// no boot-timing marker: the boot after a reset makes the userfs anew and is nobody's
	// measurement; the page counts down with the restore's figures
	if err := a.Manager.Reboot(context.Background()); err != nil {
		_ = a.Root.DisarmFactoryReset()
		writeErr(w, err)
		return
	}
	slog.Warn("power: factory reset set, rebooting", "user", SessionFrom(r).User)
	writeJSON(w, 200, map[string]any{"ok": true, "rebooting": true, "reset": true})
}

// factoryResetCancel takes a set marker away: a reset that was armed but whose reboot did not
// happen (or was stopped) would otherwise wipe the userfs at the next boot, whenever that is.
func (a *SystemAPI) factoryResetCancel(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	if !a.Root.FactoryResetArmed() {
		writeJSON(w, 200, map[string]any{"ok": true, "armed": false})
		return
	}
	if err := a.Root.DisarmFactoryReset(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "armed": false})
}

// sameHostname compares the typed name with the system's: case and surrounding space do not
// matter, and the short name matches a typed FQDN's first label, since the page shows the short one.
func sameHostname(typed, host string) bool {
	typed = strings.ToLower(strings.TrimSpace(typed))
	host = strings.ToLower(strings.TrimSpace(host))
	if typed == "" || host == "" {
		return false
	}
	if typed == host {
		return true
	}
	if i := strings.IndexByte(typed, '.'); i > 0 && !strings.Contains(host, ".") {
		return typed[:i] == host
	}
	return false
}
