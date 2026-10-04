package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// openccu-lite task 262: the store's half of the security keys and the session lengths.

func keyRecord(id string, passkey bool) webauthn.Credential {
	c := webauthn.Credential{ID: []byte(id), PublicKey: []byte("pk-" + id)}
	if passkey {
		rk := true
		c.Extensions.RK = &rk
		c.Flags.UserVerified = true
	}
	return c
}

func TestWebAuthnStore(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionDir: filepath.Join(dir, "run"), SessionFile: filepath.Join(dir, "sessions", "sessions.json"), RestoreMethods: []string{MethodPassword, MethodPasskey}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Setup("admin", "secret123")
	if s.HasWebAuthn("admin") || s.AnyPasskey() || s.AnyWebAuthn() {
		t.Fatal("keys before any was added")
	}
	if _, err := s.AddWebAuthn("admin", keyRecord("k1", false), " "); err != ErrWebAuthnName {
		t.Errorf("an empty name: %v", err)
	}
	if _, err := s.AddWebAuthn("nobody", keyRecord("k1", false), "x"); err != ErrUnknownUser {
		t.Errorf("unknown user: %v", err)
	}
	v, err := s.AddWebAuthn("admin", keyRecord("k1", false), "  YubiKey ")
	if err != nil || v.Name != "YubiKey" || v.Passkey || v.ID != "azE" {
		t.Fatalf("add: %v %v", v, err)
	}
	if _, err := s.AddWebAuthn("admin", keyRecord("k1", true), "again"); err != ErrWebAuthnDuplicate {
		t.Errorf("duplicate: %v", err)
	}
	if _, err := s.AddWebAuthn("admin", keyRecord("k2", true), "phone"); err != nil {
		t.Fatal(err)
	}
	if !s.HasWebAuthn("admin") || !s.AnyPasskey() || !s.AnyWebAuthn() || len(s.WebAuthnKeys("admin")) != 2 {
		t.Fatal("two keys, one a passkey")
	}
	// occulited task 14: the keys do not touch the password login - no second factor
	sess, _, err := s.LoginDetail("admin", "secret123", "192.0.2.1", "t")
	if err != nil || sess == nil || sess.Method != MethodPassword {
		t.Fatalf("password with keys: %v %v", sess, err)
	}
	if _, _, err := s.LoginDetail("admin", "wrong-one", "192.0.2.1", "t"); err != ErrInvalidCredentials {
		t.Errorf("wrong password: %v", err)
	}
	user := s.WebAuthnUser("admin")
	if user == nil || user.Name != "admin" || len(user.Credentials) != 2 || string(user.WebAuthnID()) != user.AccountID {
		t.Fatalf("the ceremony's account: %v", user)
	}
	// the list counts the key that cannot sign in (a second factor from before task 14)
	for _, a := range s.Users() {
		if a.Name == "admin" && (a.WebAuthnKeys != 2 || a.WebAuthnUnusable != 1) {
			t.Errorf("account counts: %+v", a)
		}
	}
	// the pending state is bound to the address and spent once
	id, err := s.StorePending("admin", "192.0.2.1", "state")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.TakePending(id, "192.0.2.2"); err != ErrPendingLogin {
		t.Errorf("another address: %v", err)
	}
	if u, d, err := s.TakePending(id, "192.0.2.1"); err != nil || u != "admin" || d != "state" {
		t.Errorf("take: %v %v %v", u, d, err)
	}
	if _, _, err := s.TakePending(id, "192.0.2.1"); err != ErrPendingLogin {
		t.Errorf("spent: %v", err)
	}
	id, _ = s.StorePending("", "192.0.2.1", "p")
	now = now.Add(PendingTTL + time.Second)
	if _, _, err := s.TakePending(id, "192.0.2.1"); err != ErrPendingLogin {
		t.Errorf("run out: %v", err)
	}
	// the handle lookup: the account id and one of its keys
	if u, err := s.WebAuthnUserByHandle([]byte("k2"), []byte(user.AccountID)); err != nil || u.Name != "admin" {
		t.Errorf("by handle: %v %v", u, err)
	}
	if _, err := s.WebAuthnUserByHandle([]byte("k9"), []byte(user.AccountID)); err != ErrWebAuthnUnknown {
		t.Errorf("a foreign key id: %v", err)
	}
	if _, err := s.WebAuthnUserByHandle([]byte("k2"), []byte("nobody")); err != ErrUnknownUser {
		t.Errorf("an unknown handle: %v", err)
	}
	// a completed login records the counter and the time, opens the session with the method
	k := keyRecord("k2", true)
	k.Authenticator.SignCount = 7
	sess, err = s.CompleteLogin("admin", &k, MethodPasskey, "192.0.2.1", "t")
	if err != nil || sess == nil || sess.Method != MethodPasskey {
		t.Fatalf("complete: %v %v", sess, err)
	}
	keys := s.WebAuthnKeys("admin")
	if keys[1].LastUsed == nil || !keys[1].LastUsed.Equal(now) {
		t.Errorf("last used: %v", keys[1])
	}
	// a clone warning is refused and counts as a failure
	k.Authenticator.CloneWarning = true
	if _, err := s.CompleteLogin("admin", &k, MethodPasskey, "192.0.2.1", "t"); err != ErrWebAuthnClone {
		t.Errorf("clone: %v", err)
	}
	// the key step's failures lock like wrong passwords
	lockedUser, lockedRemote := false, false
	for i := 0; i < 8; i++ {
		why := s.FailKeyStep("admin", "192.0.2.9")
		lockedUser, lockedRemote = lockedUser || why.LockedUser, lockedRemote || why.LockedRemote
	}
	if !lockedUser || !lockedRemote || !s.LockedRemote("192.0.2.9") {
		t.Errorf("lockout: user %v remote %v", lockedUser, lockedRemote)
	}
	if _, _, err := s.LoginDetail("admin", "secret123", "192.0.2.1", "t"); err != ErrLockedOut {
		t.Errorf("locked user: %v", err)
	}
	now = now.Add(16 * time.Minute)
	// removal: by the owner keeps the sessions, by an administrator ends them
	other, _ := s.Setup("x", "y"), 0
	_ = other
	sess2, err := s.CompleteLogin("admin", &webauthn.Credential{ID: []byte("k1"), PublicKey: []byte("pk-k1")}, MethodPasskey, "192.0.2.1", "t")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveWebAuthn("admin", "nope", false, ""); err != ErrWebAuthnUnknown {
		t.Errorf("remove unknown: %v", err)
	}
	if err := s.RemoveWebAuthn("admin", "azE", false, ""); err != nil || len(s.WebAuthnKeys("admin")) != 1 {
		t.Errorf("owner removes: %v", err)
	}
	if s.Validate(sess2.ID) == nil {
		t.Error("the owner's removal ended the session")
	}
	if err := s.RemoveWebAuthn("admin", "azI", true, ""); err != nil || s.HasWebAuthn("admin") {
		t.Errorf("admin removes: %v", err)
	}
	if s.Validate(sess2.ID) != nil {
		t.Error("the administrator's removal kept the session")
	}
	// users.json round-trips the keys
	_, _ = s.AddWebAuthn("admin", keyRecord("k3", true), "back")
	s2, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if keys := s2.WebAuthnKeys("admin"); len(keys) != 1 || keys[0].Name != "back" || !keys[0].Passkey {
		t.Errorf("after a reopen: %v", keys)
	}
	// the limit
	for i := 0; i < MaxWebAuthnKeys; i++ {
		if _, err := s2.AddWebAuthn("admin", keyRecord(string(rune('a'+i))+"x", false), "n"); err != nil && err != ErrWebAuthnLimit {
			t.Fatal(err)
		}
	}
	if _, err := s2.AddWebAuthn("admin", keyRecord("zz", false), "one more"); err != ErrWebAuthnLimit {
		t.Errorf("limit: %v", err)
	}
}

func TestWebAuthnConsole(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Setup("admin", "secret123")
	_, _ = s.AddWebAuthn("admin", keyRecord("k1", false), "one")
	_, _ = s.AddWebAuthn("admin", keyRecord("k2", true), "two")
	sess, _ := s.CompleteLogin("admin", &webauthn.Credential{ID: []byte("k1"), PublicKey: []byte("x")}, MethodPassword, "r", "a")
	s.Close()
	keys, err := ConsoleWebAuthnKeys(dir, "admin")
	if err != nil || len(keys) != 2 {
		t.Fatalf("list: %v %v", keys, err)
	}
	if _, err := ConsoleWebAuthnKeys(dir, "nobody"); err != ErrUnknownUser {
		t.Errorf("unknown: %v", err)
	}
	if n, err := ConsoleRemoveWebAuthn(dir, "admin", "nope"); err != ErrWebAuthnUnknown || n != 0 {
		t.Errorf("remove unknown: %d %v", n, err)
	}
	if n, err := ConsoleRemoveWebAuthn(dir, "admin", "azE"); err != nil || n != 1 {
		t.Errorf("remove one: %d %v", n, err)
	}
	if n, err := ConsoleRemoveWebAuthn(dir, "admin", ""); err != nil || n != 1 {
		t.Errorf("remove all: %d %v", n, err)
	}
	if n, err := ConsoleRemoveWebAuthn(dir, "admin", ""); err != nil || n != 0 {
		t.Errorf("remove none: %d %v", n, err)
	}
	// the stored session ended with the keys, as a console password ends them
	s3, err := Open(dir, Options{SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword}})
	if err != nil {
		t.Fatal(err)
	}
	if s3.Validate(sess.ID) != nil {
		t.Error("the session outlived the console removal")
	}
	if s3.HasWebAuthn("admin") {
		t.Error("keys left")
	}
}

func TestSessionLimits(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s, err := Open(t.TempDir(), Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if idle, maxAge := s.SessionLimits(); idle != 30*time.Minute || maxAge != 12*time.Hour {
		t.Fatalf("defaults %s %s", idle, maxAge)
	}
	for _, tc := range []struct {
		idle, max time.Duration
		ok        bool
	}{
		{30 * time.Minute, 12 * time.Hour, true},
		{24 * time.Hour, 30 * 24 * time.Hour, true},
		{5 * time.Minute, time.Hour, true},
		{4 * time.Minute, time.Hour, false},
		{30 * time.Minute, 59 * time.Minute, false},
		{31 * 24 * time.Hour, 60 * 24 * time.Hour, false},
		{time.Hour, 91 * 24 * time.Hour, false},
		{3 * time.Hour, 2 * time.Hour, false},
	} {
		if got := ValidSessionLimits(tc.idle, tc.max); got != tc.ok {
			t.Errorf("%s/%s: %v", tc.idle, tc.max, got)
		}
	}
	if err := s.SetSessionLimits(time.Minute, time.Hour); err != ErrSessionLimits {
		t.Errorf("out of bounds: %v", err)
	}
	_ = s.Setup("admin", "secret123")
	sess, _ := s.Login("admin", "secret123", "r", "a")
	// idle: 30 minutes by default
	now = now.Add(29 * time.Minute)
	if s.Validate(sess.ID) == nil {
		t.Fatal("gone at 29 min")
	}
	now = now.Add(31 * time.Minute)
	if s.Validate(sess.ID) != nil {
		t.Fatal("alive after 31 min idle")
	}
	// the lifetime, measured from the login, follows a change at once
	sess, _ = s.Login("admin", "secret123", "r", "a")
	for i := 0; i < 5; i++ {
		now = now.Add(20 * time.Minute)
		if s.Validate(sess.ID) == nil {
			t.Fatalf("gone at %d x 20 min", i+1)
		}
	}
	if err := s.SetSessionLimits(2*time.Hour, time.Hour); err == nil {
		t.Fatal("idle above the lifetime accepted")
	}
	if err := s.SetSessionLimits(30*time.Minute, time.Hour); err != nil {
		t.Fatal(err)
	}
	if s.Validate(sess.ID) != nil {
		t.Fatal("a session older than the new lifetime stayed")
	}
}
