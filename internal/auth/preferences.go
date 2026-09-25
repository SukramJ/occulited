package auth

import (
	"errors"
	"fmt"
	"regexp"
)

// Preferences are what the shell remembers per account (task 59, D-54): the addons with a web
// frontend in the order the user gave them, and which of them are pinned to the tab bar. They
// are kept with the account in users.json, so a phone and a desktop show the same tab bar; the
// shell reconciles the list against the addons that are installed, so a stale entry does no harm.
type Preferences struct {
	// Addons with a web frontend of their own, in the user's order; a pinned one is a tab.
	Addons []AddonPreference `json:"addons"`
	// StartPage is where the UI opens for this account (task 193): "app" or "status"; "" is the
	// shell's default (status).
	StartPage string `json:"start_page,omitempty"`
	// AppFullscreen shows the App without the shell's top bar (task 193): the App is the whole
	// window, its drawer carries the way back to Status.
	AppFullscreen bool `json:"app_fullscreen,omitempty"`
}

// AddonPreference is one addon's place in the dropdown and whether it has a tab.
type AddonPreference struct {
	ID     string `json:"id"`
	Pinned bool   `json:"pinned,omitempty"`
}

// ErrBadPreferences is a preferences body the store refuses; the message says why.
var ErrBadPreferences = errors.New("invalid preferences")

// maxPreferenceAddons bounds the list: no box has that many addons, and users.json is read on
// every login.
const maxPreferenceAddons = 100

// an addon id is its directory name under /usr/local/addons
var addonIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// normalised returns a copy the store can keep: never a nil list, every id well-formed and
// named once.
func (p Preferences) normalised() (Preferences, error) {
	out := Preferences{Addons: make([]AddonPreference, 0, len(p.Addons))}
	out.AppFullscreen = p.AppFullscreen
	switch p.StartPage {
	case "", "app", "status":
		out.StartPage = p.StartPage
	default:
		return out, fmt.Errorf("%w: start_page is app or status", ErrBadPreferences)
	}
	if len(p.Addons) > maxPreferenceAddons {
		return out, fmt.Errorf("%w: at most %d addons", ErrBadPreferences, maxPreferenceAddons)
	}
	seen := map[string]bool{}
	for _, a := range p.Addons {
		if !addonIDRe.MatchString(a.ID) {
			return out, fmt.Errorf("%w: addon id %q", ErrBadPreferences, a.ID)
		}
		if seen[a.ID] {
			return out, fmt.Errorf("%w: addon %q named twice", ErrBadPreferences, a.ID)
		}
		seen[a.ID] = true
		out.Addons = append(out.Addons, AddonPreference{ID: a.ID, Pinned: a.Pinned})
	}
	return out, nil
}

// Preferences returns an account's preferences and whether the account exists. An account
// without any, and a name that is no account (the anonymous administrator, a token), get an
// empty set.
func (s *Store) Preferences(name string) (Preferences, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[name]
	if !ok || u.Preferences == nil {
		return Preferences{Addons: []AddonPreference{}}, ok
	}
	p, _ := u.Preferences.normalised()
	return p, true
}

// SetPreferences replaces an account's preferences. ErrUnknownUser for a name that is no
// account, ErrBadPreferences for a list the store will not keep.
func (s *Store) SetPreferences(name string, p Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[name]
	if !ok {
		return ErrUnknownUser
	}
	n, err := p.normalised()
	if err != nil {
		return err
	}
	if len(n.Addons) == 0 && n.StartPage == "" && !n.AppFullscreen {
		u.Preferences = nil // nothing to keep: the account reads as before
	} else {
		u.Preferences = &n
	}
	return s.save()
}
