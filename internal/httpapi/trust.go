package httpapi

// openccu-lite task 231: the Trust stores page's API - the four stores (system, occulited,
// OAuth/OIDC, ACME) listed together, add by PEM or DER, remove, copy between stores, restore a
// removed base certificate, and the pending strict failures with their one-click copy. The OIDC
// settings' own routes (oidctrust.go) work on the same store.

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/trust"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

func (a *SystemAPI) registerTrust(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/trust", a.trustView)
	route(mux, auth.ScopeSystemRead, "GET "+p+"/trust/{store}/{id}/pem", a.trustPEM)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/trust/{store}", a.trustAdd)
	route(mux, auth.ScopeSystemWrite, "DELETE "+p+"/trust/{store}/{id}", a.trustRemove)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/trust/{store}/{id}/copy", a.trustCopy)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/trust/{store}/{id}/restore", a.trustRestore)
}

func (a *SystemAPI) trustLog() *slog.Logger {
	if a.Trust != nil && a.Trust.Log != nil {
		return a.Trust.Log
	}
	return slog.Default()
}

func (a *SystemAPI) trustReady(w http.ResponseWriter) bool {
	if a.Trust == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "no-trust-store", Message: "this daemon runs without a state directory for trust stores"})
		return false
	}
	return true
}

// trustStoreError maps the store's refusals; the certificate ones are trustError's (oidctrust.go).
func trustStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, trust.ErrStore):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: err.Error()})
	case errors.Is(err, trust.ErrReadOnly):
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "read_only", Message: err.Error()})
	case errors.Is(err, trust.ErrSame), errors.Is(err, trust.ErrNoRemove):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
	case errors.Is(err, trust.ErrPresent):
		writeJSON(w, http.StatusConflict, apiError{Error: "present", Message: err.Error()})
	default:
		trustError(w, err)
	}
}

func (a *SystemAPI) trustView(w http.ResponseWriter, _ *http.Request) {
	if !a.trustReady(w) {
		return
	}
	v, err := a.Trust.View()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "trust", Message: err.Error()})
		return
	}
	writeJSON(w, 200, v)
}

type trustAdded struct {
	Added []trust.Info    `json:"added"`
	Store trust.StoreView `json:"store"`
}

func (a *SystemAPI) storeView(id string) trust.StoreView {
	v, _ := a.Trust.View()
	for _, s := range v.Stores {
		if s.ID == id {
			return s
		}
	}
	return trust.StoreView{ID: id, Certificates: []trust.Info{}}
}

// trustAdd takes {pem} (pasted or an uploaded PEM file) or {der} (an uploaded .der/.cer, base64).
func (a *SystemAPI) trustAdd(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	var body struct {
		PEM string `json:"pem"`
		DER string `json:"der"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	text := []byte(body.PEM)
	if strings.TrimSpace(body.PEM) == "" && body.DER != "" {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body.DER))
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "bad_certificate", Message: "the DER upload is not base64: " + err.Error()})
			return
		}
		text = b
	}
	store := r.PathValue("store")
	added, err := a.Trust.AddTo(r.Context(), store, text, SessionFrom(r).User, trust.OriginPage)
	if err != nil {
		trustStoreError(w, err)
		return
	}
	for _, i := range added {
		withCaller(r, a.trustLog()).Info("trust: certificate added", "store", store, "subject", i.Subject, "fingerprint", i.Fingerprint)
	}
	a.trustChanged()
	writeJSON(w, 200, trustAdded{Added: added, Store: a.storeView(store)})
}

func (a *SystemAPI) trustRemove(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	store, id := r.PathValue("store"), r.PathValue("id")
	if err := a.Trust.RemoveFrom(r.Context(), store, id); err != nil {
		trustStoreError(w, err)
		return
	}
	withCaller(r, a.trustLog()).Info("trust: certificate removed", "store", store, "id", id)
	a.trustChanged()
	writeJSON(w, 200, map[string]any{"store": a.storeView(store)})
}

func (a *SystemAPI) trustRestore(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	store, id := r.PathValue("store"), r.PathValue("id")
	if err := a.Trust.Restore(r.Context(), store, id); err != nil {
		trustStoreError(w, err)
		return
	}
	withCaller(r, a.trustLog()).Info("trust: certificate trusted again", "store", store, "id", id)
	a.trustChanged()
	writeJSON(w, 200, map[string]any{"store": a.storeView(store)})
}

// trustCopy: {to} - the certificate of this store into another, a copy (the maintainer).
func (a *SystemAPI) trustCopy(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	var body struct {
		To string `json:"to"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	from, id := r.PathValue("store"), r.PathValue("id")
	added, err := a.Trust.Copy(r.Context(), from, body.To, id, SessionFrom(r).User)
	if err != nil {
		trustStoreError(w, err)
		return
	}
	for _, i := range added {
		withCaller(r, a.trustLog()).Info("trust: certificate copied", "from", from, "to", body.To, "subject", i.Subject)
	}
	a.trustChanged()
	writeJSON(w, 200, trustAdded{Added: added, Store: a.storeView(body.To)})
}

// trustPEM is one certificate as a download.
func (a *SystemAPI) trustPEM(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	p, err := a.Trust.PEMOf(r.PathValue("store"), r.PathValue("id"))
	if err != nil {
		trustStoreError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("id")+`.pem"`)
	_, _ = w.Write([]byte(p))
}

// trustChanged re-evaluates the Status page's warnings at once: a failure fixed by a copy shows
// no more.
func (a *SystemAPI) trustChanged() {
	if a.Warnings != nil {
		go a.Warnings.Evaluate(context.Background())
	}
}

// trustWarnings: the strict failures - one warning per store, its variant the hosts, so a silence
// covers the hosts it was set for (Lists). Severity error: the call does not go through.
func (a *SystemAPI) trustWarnings(context.Context) ([]warnings.Warning, bool) {
	if a.Trust == nil {
		return nil, true
	}
	byStore := map[string][]trust.Failure{}
	for _, f := range a.Trust.Failures() {
		byStore[f.Store] = append(byStore[f.Store], f)
	}
	var out []warnings.Warning
	for _, id := range trust.StoreIDs {
		fs := byStore[id]
		if len(fs) == 0 {
			continue
		}
		sort.Slice(fs, func(i, j int) bool { return fs[i].Host < fs[j].Host })
		hosts := make([]string, 0, len(fs))
		var issuer string
		candidate := false
		for _, f := range fs {
			hosts = append(hosts, f.Host)
			if issuer == "" {
				issuer = f.Issuer
			}
			if f.Candidate != nil {
				candidate = true
			}
		}
		out = append(out, warnings.Warning{ID: "trust-ca", Variant: strings.Join(hosts, ","), Severity: warnings.SeverityError, Href: "/system/trust#" + id,
			Params: map[string]any{"store": id, "hosts": hosts, "issuer": issuer, "candidate": candidate}})
	}
	return out, true
}
