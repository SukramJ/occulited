package httpapi

import (
	"github.com/hobbyquaker/occulited/internal/auth"

	"context"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/hobbyquaker/occulited/internal/system"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

// Task 125 (D-77): the legacy session in addon URLs. The shell puts the session's ?sid=@..@ alias
// into the URLs of the addons that live by the CCU convention - those the box cannot vouch for
// reading the gate's X-Occulite-Session (SessionHeader false: no runtime.session.header_since for
// the installed version, or outside the catalogue) - on by default, with a warning on the Status
// page, and switched off globally or per addon on the Addons page. Off, the
// shell opens the addon without ?sid=, and the addon's CGIs refuse the page in their own way. The
// alias is only ever a credential at lighttpd's gate and in the tclrega shim; the API never takes
// it, so what is in a URL is no longer the session.

// LegacySessions keeps the two switches (occulited.json's addons block); nil on SystemAPI means on
// for every addon and no way to change it (the routes answer 501).
type LegacySessions interface {
	LegacySession() (on bool, off []string)
	SetLegacySession(on bool, off []string) error
}

// legacyOn says whether the legacy session is switched on for an addon: globally, and not off
// for the addon itself.
func (a *SystemAPI) legacyOn(id string) bool {
	if a.Legacy == nil {
		return true
	}
	on, off := a.Legacy.LegacySession()
	return on && !slices.Contains(off, id)
}

// LegacySession says whether the shell opens path - the URL of addon id, installed at version -
// with the session's alias: a path under /addons/ of an addon that does not read the header
// (SessionHeader), with the legacy session on for it.
func (a *SystemAPI) LegacySession(id, version, path string) bool {
	if !strings.HasPrefix(path, "/addons/") {
		return false
	}
	return !SessionHeader(a.Root, id, version, path) && a.legacyOn(id)
}

// legacySessionView is GET /legacy-session and the answer of the PUTs.
func (a *SystemAPI) legacySessionView() map[string]any {
	on, off := a.Legacy.LegacySession()
	if off == nil {
		off = []string{}
	}
	return map[string]any{"enabled": on, "off": off}
}

func (a *SystemAPI) registerLegacySession(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/legacy-session", a.legacySessionGet)
	route(mux, auth.ScopeAuthAdmin, "PUT "+p+"/legacy-session", a.legacySessionPut)
	route(mux, auth.ScopeAddonsWrite, "PUT "+p+"/addons/{id}/legacy-session", a.addonLegacySessionPut)
}

func (a *SystemAPI) legacySessionReady(w http.ResponseWriter) bool {
	if a.Legacy == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "this daemon was started without a configuration file"})
		return false
	}
	return true
}

// legacySessionGet is GET /legacy-session, any session: the global switch and the addons it is
// switched off for.
func (a *SystemAPI) legacySessionGet(w http.ResponseWriter, _ *http.Request) {
	if !a.legacySessionReady(w) {
		return
	}
	writeJSON(w, 200, a.legacySessionView())
}

// legacySessionPut is PUT /legacy-session {enabled, off?}, administrators only: the global switch,
// and the per-addon list when the body carries one.
func (a *SystemAPI) legacySessionPut(w http.ResponseWriter, r *http.Request) {
	if !a.legacySessionReady(w) {
		return
	}
	if !SessionFrom(r).Has(auth.ScopeAuthAdmin) {
		forbiddenScope(w, auth.ScopeAuthAdmin)
		return
	}
	var body struct {
		Enabled *bool     `json:"enabled"`
		Off     *[]string `json:"off"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	on, off := a.Legacy.LegacySession()
	if body.Enabled != nil {
		on = *body.Enabled
	}
	if body.Off != nil {
		off = cleanIDs(*body.Off)
	}
	if err := a.Legacy.SetLegacySession(on, off); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "config", Message: err.Error()})
		return
	}
	a.reevaluateWarnings()
	writeJSON(w, 200, a.legacySessionView())
}

// addonLegacySessionPut is PUT /addons/{id}/legacy-session {enabled}, administrators only: the
// switch of one addon. It does not touch the global one.
func (a *SystemAPI) addonLegacySessionPut(w http.ResponseWriter, r *http.Request) {
	if !a.legacySessionReady(w) {
		return
	}
	if !SessionFrom(r).Has(auth.ScopeAddonsWrite) {
		forbiddenScope(w, auth.ScopeAddonsWrite)
		return
	}
	id := r.PathValue("id")
	if !validAddonID(id) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "invalid addon id"})
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	on, off := a.Legacy.LegacySession()
	off = slices.DeleteFunc(slices.Clone(off), func(x string) bool { return x == id })
	if !body.Enabled {
		off = append(off, id)
	}
	if err := a.Legacy.SetLegacySession(on, cleanIDs(off)); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "config", Message: err.Error()})
		return
	}
	a.reevaluateWarnings()
	out := a.legacySessionView()
	out["id"] = id
	out["addon_enabled"] = a.legacyOn(id)
	writeJSON(w, 200, out)
}

// validAddonID is the shape of an addon id the policy files accept.
func validAddonID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '_':
		case (c == '.' || c == '-') && i > 0:
		default:
			return false
		}
	}
	return true
}

// cleanIDs keeps the valid ids, each once, sorted.
func cleanIDs(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if validAddonID(id) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// reevaluateWarnings runs the warnings once after a switch, so the Status page follows at once.
func (a *SystemAPI) reevaluateWarnings() {
	if a.Warnings != nil {
		a.Warnings.Evaluate(context.Background())
	}
}

// legacySessionWarning: the installed addons that receive the alias in a URL the shell opens -
// their settings page or their frontend. The variant is the sorted ids, so one more addon is a new
// warning; it goes away when no addon receives the alias (all declare the header, or the switches
// are off).
func (a *SystemAPI) legacySessionWarning(ctx context.Context) ([]warnings.Warning, bool) {
	if a.Addons == nil {
		return nil, true
	}
	list, err := a.Addons.ListAddons(ctx)
	if err != nil {
		return nil, false
	}
	var entries []system.NavEntry
	if a.Nav != nil {
		entries = a.Nav.NavEntries(ctx)
	}
	var out []warnAddon
	for _, ad := range list {
		if u := SettingsURL(a.Root, ad.ID); u != "" {
			ad.ConfigURL = u // B-134
		}
		gets := a.LegacySession(ad.ID, ad.Version, addonSettingsURL(ad))
		for _, e := range entries {
			if e.Addon == ad.ID && !e.Proxied && a.LegacySession(ad.ID, ad.Version, e.Href) {
				gets = true
			}
		}
		if gets {
			out = append(out, warnAddon{ID: ad.ID, Name: ad.Name, Enabled: ad.Enabled})
		}
	}
	if len(out) == 0 {
		return nil, true
	}
	return []warnings.Warning{addonListWarning("legacy-session", warnings.SeverityWarning, "/addons", out)}, true
}
