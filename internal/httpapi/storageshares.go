package httpapi

// openccu-lite task 228, phase 2: System → Storage - the SMB and NFS shares. Each is mounted by
// PID 1 on demand at /media/net/<name> (an automount the helper renders), tested with a probe
// file, and used later by the journal's copies and the backups through the location picker. The
// work is internal/shares'; here are the parsing and the answers.

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/shares"
)

func (a *SystemAPI) registerShares(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/storage/shares", a.sharesList)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/shares", a.sharesCreate)
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/storage/shares/{id}", a.sharesPut)
	route(mux, auth.ScopeSystemWrite, "DELETE "+p+"/storage/shares/{id}", a.sharesDelete)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/shares/{id}/test", a.sharesTest)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/shares/{id}/mount", a.sharesMount)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/shares/{id}/unmount", a.sharesUnmount)
}

func (a *SystemAPI) sharesReady(w http.ResponseWriter) bool {
	if a.Shares == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "network shares are not available here"})
		return false
	}
	return true
}

func writeShareErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, shares.ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: err.Error()})
	case errors.Is(err, shares.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: err.Error()})
	case errors.Is(err, shares.ErrInUse):
		writeJSON(w, http.StatusConflict, apiError{Error: "in_use", Message: err.Error()})
	case errors.Is(err, shares.ErrExists):
		writeJSON(w, http.StatusConflict, apiError{Error: "exists", Message: err.Error()})
	case errors.Is(err, shares.ErrStale):
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: "stale", Message: err.Error(), Detail: map[string]any{"state": shares.StateStale}})
	default:
		writeErr(w, err)
	}
}

func (a *SystemAPI) sharesList(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	views, err := a.Shares.Views()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"container": a.Root.Container(), "kinds": a.Shares.Kinds(), "shares": views})
}

// shareBody reads a share; derived fields are dropped.
func shareBody(r *http.Request) (shares.Share, error) {
	var s shares.Share
	if err := readJSON(r, &s); err != nil {
		return s, err
	}
	s.HasPassword = false
	s.Created = time.Time{}
	return s, nil
}

func (a *SystemAPI) sharesCreate(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	s, err := shareBody(r)
	if err != nil {
		badBody(w, err)
		return
	}
	saved, err := a.Shares.Store.Create(s)
	if err != nil {
		writeShareErr(w, err)
		return
	}
	a.shareSaved(w, r, saved, true)
}

func (a *SystemAPI) sharesPut(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	s, err := shareBody(r)
	if err != nil {
		badBody(w, err)
		return
	}
	if s.ID != "" && s.ID != r.PathValue("id") {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "a share cannot be renamed; remove it and add it under the new name"})
		return
	}
	s.ID = r.PathValue("id")
	saved, err := a.Shares.Store.Update(s)
	if err != nil {
		writeShareErr(w, err)
		return
	}
	a.shareSaved(w, r, saved, false)
}

func (a *SystemAPI) shareSaved(w http.ResponseWriter, r *http.Request, s shares.Share, created bool) {
	applyErr := ""
	if err := a.Shares.Apply(r.Context(), s); err != nil {
		applyErr = err.Error()
		slog.Warn("shares: the mount units were not written", "share", s.ID, "err", err)
	}
	reqLog(r).Info("shares: saved", "share", s.ID, "kind", s.Kind, "source", s.Source(), "read_only", s.ReadOnly, "created", created)
	v, err := a.Shares.View(s.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"share": v}
	if applyErr != "" {
		out["apply_error"] = applyErr
	}
	status := 200
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

func (a *SystemAPI) sharesDelete(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	id := r.PathValue("id")
	if err := a.Shares.Remove(r.Context(), id); err != nil {
		writeShareErr(w, err)
		return
	}
	reqLog(r).Info("shares: removed (the files on the share stay)", "share", id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *SystemAPI) sharesTest(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	res, err := a.Shares.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		writeShareErr(w, err)
		return
	}
	reqLog(r).Info("shares: tested", "share", r.PathValue("id"), "state", res.State, "step", res.Step)
	writeJSON(w, 200, res)
}

func (a *SystemAPI) sharesMount(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	v, err := a.Shares.Mount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeShareErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *SystemAPI) sharesUnmount(w http.ResponseWriter, r *http.Request) {
	if !a.sharesReady(w) {
		return
	}
	v, err := a.Shares.Unmount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeShareErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}
