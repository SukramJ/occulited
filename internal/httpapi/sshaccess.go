package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/hobbyquaker/occulited/internal/sshkeys"
	"github.com/hobbyquaker/occulited/internal/system"
)

// Task 185: the Remote access page's SSH. Adding a key and setting root's password ask for the
// user's own password every time (task 154's confirmed ticket, X-Occulite-Confirm); a token is not
// asked, as on the device keys' export.
const (
	SSHKeysPath     = "/api/system/v1/ssh/keys"
	SSHPasswordPath = "/api/system/v1/ssh/password"
)

// confirmed answers 403 confirm-required unless the request carries a confirmed ticket for path.
func (a *SystemAPI) confirmed(w http.ResponseWriter, r *http.Request, path string) bool {
	sess := SessionFrom(r)
	if sess == nil {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "unauthenticated", Message: "login required"})
		return false
	}
	if sess.IsToken() {
		return true
	}
	t := r.Header.Get("X-Occulite-Confirm")
	if t == "" || a.ConfirmTicket == nil || !a.ConfirmTicket(t, path, sess.ID) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "confirm-required", Message: "this asks for the password (or a fresh login at the provider) every time"})
		return false
	}
	return true
}

// journalRun is the journal reader's runner: a test's stand-in, or nil for journalctl run by
// occulited itself.
func (a *SystemAPI) journalRun() system.Runner {
	if a.Journal != nil {
		return a.Journal.Run
	}
	return nil
}

func who(r *http.Request) string {
	if s := SessionFrom(r); s != nil {
		return s.User
	}
	return ""
}

func (a *SystemAPI) sshKeys(w http.ResponseWriter, _ *http.Request) {
	f, err := system.SSHKeys()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	writeJSON(w, 200, f)
}

func (a *SystemAPI) sshKeyAdd(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	// the key is checked before the password is spent on it: what it is, and whether the file holds
	// it already (AddSSHKey checks that again under its lock)
	pasted, err := sshkeys.Parse(body.Key)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	if f, err := system.SSHKeys(); err == nil {
		for _, k := range append(append([]sshkeys.Key{}, f.Managed...), f.Other...) {
			if k.Fingerprint == pasted.Fingerprint {
				writeJSON(w, http.StatusConflict, apiError{Error: "exists", Message: system.ErrKeyPresent.Error()})
				return
			}
		}
	}
	if !a.confirmed(w, r, SSHKeysPath) {
		return
	}
	k, err := system.AddSSHKey(body.Key)
	switch {
	case errors.Is(err, system.ErrKeyPresent):
		writeJSON(w, http.StatusConflict, apiError{Error: "exists", Message: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	slog.Info("ssh: key added", "type", k.Type, "fingerprint", k.Fingerprint, "comment", k.Comment, "by", who(r), "remote", remote(r))
	writeJSON(w, http.StatusCreated, k)
}

func (a *SystemAPI) sshKeyRemove(w http.ResponseWriter, r *http.Request) {
	fp := r.URL.Query().Get("fingerprint")
	if fp == "" {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "fingerprint is missing"})
		return
	}
	k, err := system.RemoveSSHKey(fp)
	switch {
	case errors.Is(err, system.ErrLastSSHKey):
		writeJSON(w, http.StatusConflict, apiError{Error: "last-key", Message: err.Error()})
		return
	case errors.Is(err, system.ErrKeyNotManaged):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	slog.Info("ssh: key removed", "type", k.Type, "fingerprint", k.Fingerprint, "comment", k.Comment, "by", who(r), "remote", remote(r))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// sshSessionView is a session as the page shows it: own marks the one from the page's address.
type sshSessionView struct {
	system.SSHSession
	Own bool `json:"own,omitempty"`
}

func (a *SystemAPI) sshSessions(w http.ResponseWriter, r *http.Request) {
	list, err := a.Root.SSHSessions(r.Context(), a.journalRun())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	me := strings.TrimPrefix(remote(r), "::ffff:")
	out := make([]sshSessionView, 0, len(list))
	for _, s := range list {
		out = append(out, sshSessionView{SSHSession: s, Own: s.From != "" && strings.TrimPrefix(s.From, "::ffff:") == me})
	}
	writeJSON(w, 200, map[string]any{"sessions": out})
}

func (a *SystemAPI) sshSessionEnd(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "the session id is a number"})
		return
	}
	s, err := a.Root.EndSSHSession(r.Context(), a.journalRun(), id)
	switch {
	case errors.Is(err, system.ErrNoSession):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	slog.Info("ssh: session ended", "user", s.User, "from", s.From, "port", s.Port, "by", who(r), "remote", remote(r))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// sshKeyOnly (task 245) switches root's password login off and on: {"on": true} makes SSH take a
// key only - refused (409 no-key) while root has no key - and answers the SSH view.
func (a *SystemAPI) sshKeyOnly(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	switch err := system.SetSSHKeyOnly(body.On); {
	case errors.Is(err, system.ErrNoSSHKey):
		writeJSON(w, http.StatusConflict, apiError{Error: "no-key", Message: err.Error()})
		return
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "internal", Message: err.Error()})
		return
	}
	slog.Info("ssh: only key login switched", "on", body.On, "by", who(r), "remote", remote(r))
	a.ssh(w, r)
}
