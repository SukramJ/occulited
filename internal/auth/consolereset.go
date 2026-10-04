package auth

import (
	"crypto/rand"
	"sort"
	"strings"
	"time"
)

// The console's way back in (occulited task 14): `occulited admin list` shows the accounts and
// `occulited admin reset-auth <user>` resets one - its passkeys removed, a one-time password set
// that must be changed at the next login, its sessions ended. Both run as root on the system
// (ssh, or keyboard and display), the trust anchor of an appliance, and edit users.json the way
// `occulited passwd` does; a running occulited follows the changed file at the next request. The
// reset is written into the account (console_reset), so the Status page can say for a while that
// access was reset on the console.

// ConsoleResetNotice is how long the Status page shows a console reset.
const ConsoleResetNotice = 7 * 24 * time.Hour

// oneTimeAlphabet leaves out what is easily misread (0/O, 1/l/I): the password is read off a
// console and typed into a login page.
const oneTimeAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// OneTimePassword is a password for a console reset: four groups of five characters (about 99
// bits), to be changed at the first login.
func OneTimePassword() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%5 == 0 {
			sb.WriteByte('-')
		}
		// 256 % 31 leaves a bias of under 1 % on the first few letters; it costs no strength
		// worth counting at this length
		sb.WriteByte(oneTimeAlphabet[int(c)%len(oneTimeAlphabet)])
	}
	return sb.String(), nil
}

// ConsoleAccounts lists the accounts in users.json for `occulited admin list`, by name.
func ConsoleAccounts(dir string) ([]Account, error) {
	s, err := Open(dir, Options{})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Account, 0, len(s.users))
	for _, u := range s.users {
		out = append(out, accountOf(u))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ConsoleResetAuth resets an account's access from the console: every key removed, a new
// one-time password set with must_change_password, the reset's time recorded, the account's
// stored sessions ended. It answers the password and how many keys went.
func ConsoleResetAuth(dir, name string, now time.Time) (password string, removed int, err error) {
	s, err := Open(dir, Options{})
	if err != nil {
		return "", 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return "", 0, ErrUnknownUser
	}
	pw, err := OneTimePassword()
	if err != nil {
		return "", 0, err
	}
	h, err := HashPassword(pw)
	if err != nil {
		return "", 0, err
	}
	removed = len(u.WebAuthn)
	at := now.UTC()
	u.Hash, u.MustChangePW, u.WebAuthn, u.ConsoleReset = h, true, nil, &at
	if err := s.save(); err != nil {
		return "", 0, err
	}
	// a running daemon ends the sessions when it sees the hash change; for one that is not
	// running they leave the session store now
	if err := endStoredSessions(SessionStorePath(dir), name); err != nil {
		return pw, removed, err
	}
	return pw, removed, nil
}

// ConsoleReset is one account's console reset, for the Status page.
type ConsoleReset struct {
	User string    `json:"user"`
	At   time.Time `json:"at"`
}

// ConsoleResets lists the console resets after since, oldest first.
func (s *Store) ConsoleResets(since time.Time) []ConsoleReset {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	var out []ConsoleReset
	for _, u := range s.users {
		if u.ConsoleReset != nil && u.ConsoleReset.After(since) {
			out = append(out, ConsoleReset{User: u.Name, At: *u.ConsoleReset})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].User < out[j].User
	})
	return out
}
