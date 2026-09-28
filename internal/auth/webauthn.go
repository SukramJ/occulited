package auth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/go-webauthn/webauthn/webauthn"
)

// Security keys and passkeys (openccu-lite task 262, ASVS 5.0 V6.5): an account may register
// WebAuthn credentials, each user for their own account. One or more keys make the key the second
// factor after the password - the password alone is refused - and a key registered as a
// discoverable credential with user verification signs in alone (a passkey). The store keeps the
// library's credential record (id, public key, sign count, flags, AAGUID, transports) beside a
// name, when it was made and last used, in users.json next to the password hash; the console
// writes the same file. The WebAuthn ceremonies - the challenges and their verification - are
// the API's (internal/httpapi/webauthn.go) with github.com/go-webauthn/webauthn; the store
// holds the pending state between a ceremony's two halves, so a login that is half done has one
// owner and one time limit.

// WebAuthnCredential is one security key or passkey of an account as users.json holds it.
type WebAuthnCredential struct {
	// Key is the library's record: id, public key, sign count, flags, AAGUID, transports.
	Key webauthn.Credential `json:"key"`
	// Name is what the user called it ("YubiKey blue", "iPhone").
	Name     string     `json:"name"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
}

// ID is the credential id as the API and the console name it: base64url, unpadded.
func (c *WebAuthnCredential) ID() string { return base64.RawURLEncoding.EncodeToString(c.Key.ID) }

// Passkey reports whether the key can sign in alone: it was made as a discoverable credential
// (resident key) and the authenticator verified the user (PIN or biometric) when it was made.
// Anything else - an old U2F key, a key made without a PIN - is a second factor only.
func (c *WebAuthnCredential) Passkey() bool {
	resident := c.Key.Extensions.RK != nil && *c.Key.Extensions.RK
	return resident && c.Key.Flags.UserVerified
}

// WebAuthnView is a credential as the API lists it: never the public key or the sign count.
type WebAuthnView struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"last_used,omitempty"`
	// Passkey: signs in alone; false: a second factor only.
	Passkey bool `json:"passkey"`
	// Transports the authenticator reported (usb, nfc, ble, internal, hybrid), for the list.
	Transports []string `json:"transports,omitempty"`
}

func (c *WebAuthnCredential) view() WebAuthnView {
	v := WebAuthnView{ID: c.ID(), Name: c.Name, Created: c.Created, LastUsed: c.LastUsed, Passkey: c.Passkey()}
	for _, t := range c.Key.Transport {
		v.Transports = append(v.Transports, string(t))
	}
	return v
}

var (
	// ErrWebAuthnName is a key name that is empty, too long or carries a control character.
	ErrWebAuthnName = errors.New("the key's name is 1 to 64 printable characters")
	// ErrWebAuthnDuplicate is a credential id the account already has.
	ErrWebAuthnDuplicate = errors.New("this key is registered already")
	// ErrWebAuthnUnknown is a credential id the account does not have.
	ErrWebAuthnUnknown = errors.New("no such key")
	// ErrWebAuthnLimit is the tenth key: enough for anyone, and a bound on users.json.
	ErrWebAuthnLimit = errors.New("an account has at most 10 keys")
	// ErrWebAuthnClone is a sign count that went backwards: the authenticator may have been
	// cloned. The login is refused and the event logged; the key stays, for the owner to remove.
	ErrWebAuthnClone = errors.New("the key's signature counter went backwards: it may have been cloned")
	// ErrPendingLogin is a pending login that is unknown, spent or run out.
	ErrPendingLogin = errors.New("the login is unknown, spent or run out: start again")
	// ErrSecondFactor is the password alone for an account that has a key.
	ErrSecondFactor = errors.New("this account signs in with its security key as well")
)

// MaxWebAuthnKeys is the number of keys an account may register.
const MaxWebAuthnKeys = 10

// MethodPasskey is the login method of a session a passkey opened alone; a password-and-key
// login is MethodPassword (the password was used, and its rules - must_change_password - apply).
const MethodPasskey = "passkey"

// PendingTTL is how long a login may stay half done: the password verified, the key not yet
// presented; or a passkey ceremony started and not finished.
const PendingTTL = 2 * time.Minute

// pendingLogin is a ceremony's state between its two halves.
type pendingLogin struct {
	user    string // "" for a passkey login, which names its user in the assertion
	remote  string
	expires time.Time
	data    any // the API's ceremony state (webauthn.SessionData)
}

func validKeyName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// WebAuthnUser is the account as the library sees it: the stable account id as the user handle
// (task 193's id survives a rename, so a passkey does too), the name for display, the keys.
type WebAuthnUser struct {
	Name        string
	AccountID   string
	Credentials []webauthn.Credential
}

func (u WebAuthnUser) WebAuthnID() []byte                         { return []byte(u.AccountID) }
func (u WebAuthnUser) WebAuthnName() string                       { return u.Name }
func (u WebAuthnUser) WebAuthnDisplayName() string                { return u.Name }
func (u WebAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }

func (s *Store) webauthnUserLocked(u *User) WebAuthnUser {
	out := WebAuthnUser{Name: u.Name, AccountID: u.ID}
	for _, c := range u.WebAuthn {
		out.Credentials = append(out.Credentials, c.Key)
	}
	return out
}

// WebAuthnUser is the account for a ceremony: nil when there is no such account. Its keys are
// what the library excludes at a registration and allows at a login.
func (s *Store) WebAuthnUser(name string) *WebAuthnUser {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[name]
	if !ok {
		return nil
	}
	wu := s.webauthnUserLocked(u)
	return &wu
}

// WebAuthnUserByHandle finds the account a discoverable credential names: by the user handle
// (the account id) and, as a check, the credential id must be one of its keys.
func (s *Store) WebAuthnUserByHandle(rawID, handle []byte) (*WebAuthnUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	for _, u := range s.users {
		if u.ID != string(handle) {
			continue
		}
		for _, c := range u.WebAuthn {
			if string(c.Key.ID) == string(rawID) {
				wu := s.webauthnUserLocked(u)
				return &wu, nil
			}
		}
		return nil, ErrWebAuthnUnknown
	}
	return nil, ErrUnknownUser
}

// WebAuthnKeys lists an account's keys, newest last; nil for an unknown account too.
func (s *Store) WebAuthnKeys(name string) []WebAuthnView {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[name]
	if !ok {
		return nil
	}
	out := make([]WebAuthnView, 0, len(u.WebAuthn))
	for _, c := range u.WebAuthn {
		out = append(out, c.view())
	}
	return out
}

// HasWebAuthn reports whether the account has at least one key: its password alone is then
// refused at the login.
func (s *Store) HasWebAuthn(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[name]
	return ok && len(u.WebAuthn) > 0
}

// AnyPasskey reports whether any account has a key that signs in alone: the login page offers
// the passkey button then.
func (s *Store) AnyPasskey() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	for _, u := range s.users {
		for _, c := range u.WebAuthn {
			if c.Passkey() {
				return true
			}
		}
	}
	return false
}

// AnyWebAuthn reports whether any account has a key at all: a rename of the host or the domain
// orphans them (the RP ID is the system's name), so the Network page warns before one.
func (s *Store) AnyWebAuthn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	for _, u := range s.users {
		if len(u.WebAuthn) > 0 {
			return true
		}
	}
	return false
}

// AddWebAuthn stores a key the API has just verified for the account, under name.
func (s *Store) AddWebAuthn(user string, key webauthn.Credential, name string) (WebAuthnView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[user]
	if !ok {
		return WebAuthnView{}, ErrUnknownUser
	}
	name = strings.TrimSpace(name)
	if !validKeyName(name) {
		return WebAuthnView{}, ErrWebAuthnName
	}
	if len(u.WebAuthn) >= MaxWebAuthnKeys {
		return WebAuthnView{}, ErrWebAuthnLimit
	}
	for _, c := range u.WebAuthn {
		if string(c.Key.ID) == string(key.ID) {
			return WebAuthnView{}, ErrWebAuthnDuplicate
		}
	}
	c := &WebAuthnCredential{Key: key, Name: name, Created: s.opt.Now()}
	u.WebAuthn = append(u.WebAuthn, c)
	if err := s.save(); err != nil {
		u.WebAuthn = u.WebAuthn[:len(u.WebAuthn)-1]
		return WebAuthnView{}, err
	}
	return c.view(), nil
}

// RemoveWebAuthn removes one key (id as the list names it) of the account. endSessions ends the
// account's other sessions as well - what an administrator's removal does, like a password reset
// - except the one called keep.
func (s *Store) RemoveWebAuthn(user, id string, endSessions bool, keep string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	u, ok := s.users[user]
	if !ok {
		return ErrUnknownUser
	}
	i := -1
	for j, c := range u.WebAuthn {
		if c.ID() == id {
			i = j
		}
	}
	if i < 0 {
		return ErrWebAuthnUnknown
	}
	u.WebAuthn = append(u.WebAuthn[:i], u.WebAuthn[i+1:]...)
	if len(u.WebAuthn) == 0 {
		u.WebAuthn = nil
	}
	if endSessions && s.dropSessionsOf(user, keep) > 0 {
		s.saveSessions(true)
	}
	return s.save()
}

// usedWebAuthnLocked takes the library's updated record after a successful assertion: the sign
// count and the flags, and the time. A clone warning - a sign count that went backwards - is
// refused before anything is written.
func (s *Store) usedWebAuthnLocked(u *User, key *webauthn.Credential) error {
	for _, c := range u.WebAuthn {
		if string(c.Key.ID) != string(key.ID) {
			continue
		}
		if key.Authenticator.CloneWarning {
			return ErrWebAuthnClone
		}
		now := s.opt.Now()
		c.Key.Authenticator.SignCount = key.Authenticator.SignCount
		c.Key.Flags = key.Flags
		c.LastUsed = &now
		return nil
	}
	return ErrWebAuthnUnknown
}

// --- pending logins ------------------------------------------------------------------------

func (s *Store) sweepPendingLocked() {
	now := s.opt.Now()
	for id, p := range s.pending {
		if now.After(p.expires) {
			delete(s.pending, id)
		}
	}
}

func (s *Store) newPendingLocked(user, remote string, data any) (string, error) {
	if s.pending == nil {
		s.pending = map[string]pendingLogin{}
	}
	s.sweepPendingLocked()
	id, err := newSID()
	if err != nil {
		return "", err
	}
	s.pending[id] = pendingLogin{user: user, remote: remote, expires: s.opt.Now().Add(PendingTTL), data: data}
	return id, nil
}

// BeginPasswordLogin is the first half of a login: the password is verified as Login verifies
// it (the lockout included), and the account's keys decide what follows. Without keys the session
// is opened at once and returned; with keys nothing is opened - the caller gets the account (for
// the assertion's allow list) and hands the ceremony's state to StorePending, then CompleteLogin.
func (s *Store) BeginPasswordLogin(name, pw, remote, agent string) (sess *Session, user *WebAuthnUser, why Refusal, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	if len(s.users) == 0 {
		return nil, nil, why, ErrSetupRequired
	}
	if s.lockedOut("u:"+name) || s.lockedOut("r:"+remote) {
		why.Reason = "locked"
		return nil, nil, why, ErrLockedOut
	}
	u, ok := s.users[name]
	if !verifyOrBurn(u, ok, pw) {
		switch {
		case !ok:
			why.Reason = "unknown user"
		case u.Hash == "":
			why.Reason = "no password"
		default:
			why.Reason = "wrong password"
		}
		s.failLogin(name, remote, &why)
		return nil, nil, why, ErrInvalidCredentials
	}
	delete(s.failures, "u:"+name)
	if len(u.WebAuthn) == 0 {
		sess, err = s.openSession(name, u.Level, MethodPassword, remote, agent)
		return sess, nil, why, err
	}
	wu := s.webauthnUserLocked(u)
	return nil, &wu, why, nil
}

// StorePending keeps a ceremony's state (the library's SessionData) for PendingTTL and answers
// its id: the client's handle for the second half. user is the account for a second-factor
// login, "" for a passkey login or a registration's user is not a login.
func (s *Store) StorePending(user, remote string, data any) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if user != "" {
		if _, ok := s.users[user]; !ok {
			return "", ErrUnknownUser
		}
	}
	return s.newPendingLocked(user, remote, data)
}

// TakePending spends a pending ceremony: its state and user, or ErrPendingLogin. It is spent
// whatever the second half's outcome - a failed assertion starts over with a new challenge.
func (s *Store) TakePending(id, remote string) (user string, data any, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepPendingLocked()
	p, ok := s.pending[id]
	if !ok || p.remote != remote {
		return "", nil, ErrPendingLogin
	}
	delete(s.pending, id)
	return p.user, p.data, nil
}

// CompleteLogin is the second half of a login with a key: key is the library's record after
// the assertion verified. The sign count is checked and written, the session opened with
// method - MethodPassword when the password came first, MethodPasskey when the key signed in
// alone (the account's other rules stay the account's).
func (s *Store) CompleteLogin(name string, key *webauthn.Credential, method, remote, agent string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.reload()
	if s.lockedOut("u:"+name) || s.lockedOut("r:"+remote) {
		return nil, ErrLockedOut
	}
	u, ok := s.users[name]
	if !ok {
		return nil, ErrUnknownUser
	}
	if err := s.usedWebAuthnLocked(u, key); err != nil {
		var why Refusal
		s.failLogin(name, remote, &why)
		return nil, err
	}
	if err := s.save(); err != nil {
		return nil, err
	}
	delete(s.failures, "u:"+name)
	return s.openSession(name, u.Level, method, remote, agent)
}

// FailKeyStep counts a key step that failed - an assertion the library refused - towards the
// lockout of the name (when known) and the address, as a wrong password counts. It answers
// whether this attempt started a lockout, and until when.
func (s *Store) FailKeyStep(name, remote string) Refusal {
	s.mu.Lock()
	defer s.mu.Unlock()
	var why Refusal
	why.Reason = "key refused"
	if name != "" {
		why.LockedUser = s.recordFailure("u:" + name)
	}
	why.LockedRemote = s.recordFailure("r:" + remote)
	if why.LockedUser || why.LockedRemote {
		why.Until = s.opt.Now().Add(s.opt.LockFor)
	}
	return why
}

// LockedRemote reports whether the address is locked out: a passkey login names no user, so the
// address is all there is to check before its challenge goes out.
func (s *Store) LockedRemote(remote string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lockedOut("r:" + remote)
}

// --- the console ---------------------------------------------------------------------------

// ConsoleWebAuthnKeys lists an account's keys from users.json, for `occulited webauthn list`.
func ConsoleWebAuthnKeys(dir, name string) ([]WebAuthnView, error) {
	s, err := Open(dir, Options{})
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return nil, ErrUnknownUser
	}
	out := make([]WebAuthnView, 0, len(u.WebAuthn))
	for _, c := range u.WebAuthn {
		out = append(out, c.view())
	}
	return out, nil
}

// ConsoleRemoveWebAuthn removes one key (by its id) or, with id "", every key of the account in
// users.json - the recovery for an administrator who lost the key - and ends the account's stored
// sessions as a console password does. It answers how many keys went.
func ConsoleRemoveWebAuthn(dir, name, id string) (int, error) {
	s, err := Open(dir, Options{})
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[name]
	if !ok {
		return 0, ErrUnknownUser
	}
	kept := u.WebAuthn[:0]
	removed := 0
	for _, c := range u.WebAuthn {
		if id == "" || c.ID() == id {
			removed++
			continue
		}
		kept = append(kept, c)
	}
	if removed == 0 {
		if id == "" {
			return 0, nil
		}
		return 0, ErrWebAuthnUnknown
	}
	u.WebAuthn = kept
	if len(kept) == 0 {
		u.WebAuthn = nil
	}
	if err := s.save(); err != nil {
		return 0, err
	}
	if err := endStoredSessions(SessionStorePath(dir), name); err != nil {
		return removed, fmt.Errorf("the keys are removed, but the account's stored sessions could not be ended: %w", err)
	}
	return removed, nil
}

// --- session limits --------------------------------------------------------------------------

// Session length bounds (task 262): the idle timeout and the absolute lifetime an administrator
// may set. The defaults are ASVS's Level 2 numbers (12 hours, 30 minutes); the old 24 h / 30 d
// are the values a household can set back, within these bounds.
const (
	MinIdleTimeout = 5 * time.Minute
	MaxIdleTimeout = 30 * 24 * time.Hour
	MinMaxAge      = time.Hour
	MaxMaxAge      = 90 * 24 * time.Hour
	// DefaultIdleTimeout and DefaultMaxAge are the store's defaults since task 262.
	DefaultIdleTimeout = 30 * time.Minute
	DefaultMaxAge      = 12 * time.Hour
)

// ErrSessionLimits is a pair of limits outside the bounds, or an idle timeout above the lifetime.
var ErrSessionLimits = fmt.Errorf("the idle timeout is %s to %s, the lifetime %s to %s, and the idle timeout is not above the lifetime", MinIdleTimeout, MaxIdleTimeout, MinMaxAge, MaxMaxAge)

// ValidSessionLimits reports whether the pair is within the bounds.
func ValidSessionLimits(idle, maxAge time.Duration) bool {
	return idle >= MinIdleTimeout && idle <= MaxIdleTimeout && maxAge >= MinMaxAge && maxAge <= MaxMaxAge && idle <= maxAge
}

// SessionLimits answers the idle timeout and the absolute lifetime in force.
func (s *Store) SessionLimits() (idle, maxAge time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opt.IdleTimeout, s.opt.MaxAge
}

// SetSessionLimits changes both limits at runtime (the login settings). Running sessions end by
// the new limits at their next check: the idle timeout is read live, and the lifetime is
// measured from the login (expired), so a shorter one ends a long-running session at once.
func (s *Store) SetSessionLimits(idle, maxAge time.Duration) error {
	if !ValidSessionLimits(idle, maxAge) {
		return ErrSessionLimits
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opt.IdleTimeout, s.opt.MaxAge = idle, maxAge
	return nil
}
