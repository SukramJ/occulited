package httpapi

import (
	"errors"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// The caller's own shell preferences (task 59): GET /api/auth/v1/me/preferences and PUT, any
// role - a plain user's tab bar is that user's own business, which is why the path is in
// selfService. The anonymous administrator of auth mode off and a token have no account to keep
// them with: GET answers the empty set and PUT 409 no-account; the shell keeps them in the
// browser then.

func (a *AuthAPI) preferences(w http.ResponseWriter, r *http.Request) {
	p, _ := a.Store.Preferences(SessionFrom(r).User)
	writeJSON(w, 200, p)
}

func (a *AuthAPI) putPreferences(w http.ResponseWriter, r *http.Request) {
	var p auth.Preferences
	if err := readJSON(r, &p); err != nil {
		badBody(w, err)
		return
	}
	name := SessionFrom(r).User
	if err := a.Store.SetPreferences(name, p); err != nil {
		switch {
		case errors.Is(err, auth.ErrUnknownUser):
			writeJSON(w, http.StatusConflict, apiError{Error: "no-account", Message: "this session has no account to keep preferences with"})
		case errors.Is(err, auth.ErrBadPreferences):
			badBody(w, err)
		default:
			authErr(w, err)
		}
		return
	}
	stored, _ := a.Store.Preferences(name)
	writeJSON(w, 200, stored)
}
