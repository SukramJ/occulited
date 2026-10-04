package auth

import (
	"regexp"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

// occulited task 14: the console's way back in, and what a passkey login does to a key whose
// registration did not report a resident credential.

func TestOneTimePassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		pw, err := OneTimePassword()
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[a-hjkmnp-z2-9]{5}(-[a-hjkmnp-z2-9]{5}){3}$`).MatchString(pw) {
			t.Fatalf("shape: %q", pw)
		}
		if seen[pw] {
			t.Fatalf("repeated: %q", pw)
		}
		seen[pw] = true
	}
}

func TestConsoleResetAuth(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword, MethodPasskey}})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Setup("admin", "secret123")
	if err := s.CreateUser("bob", "bobsecret1", RoleUser, false); err != nil {
		t.Fatal(err)
	}
	_, _ = s.AddWebAuthn("admin", keyRecord("k1", false), "old")
	_, _ = s.AddWebAuthn("admin", keyRecord("k2", true), "phone")
	sess, err := s.Login("admin", "secret123", "r", "a")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()

	accounts, err := ConsoleAccounts(dir)
	if err != nil || len(accounts) != 2 || accounts[0].Name != "admin" || accounts[0].WebAuthnKeys != 2 || accounts[0].WebAuthnUnusable != 1 || accounts[0].Hash != "" || accounts[1].Name != "bob" {
		t.Fatalf("list: %+v %v", accounts, err)
	}
	if _, _, err := ConsoleResetAuth(dir, "nobody", time.Now()); err != ErrUnknownUser {
		t.Errorf("unknown: %v", err)
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	pw, removed, err := ConsoleResetAuth(dir, "admin", at)
	if err != nil || removed != 2 || pw == "" {
		t.Fatalf("reset: %q %d %v", pw, removed, err)
	}

	s2, err := Open(dir, Options{SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword, MethodPasskey}, Now: func() time.Time { return at.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Validate(sess.ID) != nil {
		t.Error("the session outlived the reset")
	}
	if s2.HasWebAuthn("admin") || !s2.MustChangePassword("admin") {
		t.Error("keys left, or no must_change_password")
	}
	if _, err := s2.Login("admin", "secret123", "r", "a"); err != ErrInvalidCredentials {
		t.Errorf("the old password: %v", err)
	}
	if got, err := s2.Login("admin", pw, "r", "a"); err != nil || got == nil {
		t.Errorf("the one-time password: %v", err)
	}
	// the Status page's notice: the reset within the week, not before it
	if r := s2.ConsoleResets(at.Add(-ConsoleResetNotice)); len(r) != 1 || r[0].User != "admin" || !r[0].At.Equal(at) {
		t.Errorf("resets: %v", r)
	}
	if r := s2.ConsoleResets(at); len(r) != 0 {
		t.Errorf("resets after it: %v", r)
	}
}

func TestPasskeyLoginMarksResident(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Setup("admin", "secret123")
	// a key whose registration did not say whether it is resident (no credProps), with UV
	k := webauthn.Credential{ID: []byte("k1"), PublicKey: []byte("pk")}
	k.Flags.UserVerified = true
	if v, _ := s.AddWebAuthn("admin", k, "laptop"); v.Passkey {
		t.Fatal("a key of unknown residency is no passkey yet")
	}
	if s.AnyPasskey() {
		t.Fatal("no passkey yet")
	}
	// a discoverable login found it: it is resident, a passkey from now on
	if _, err := s.CompleteLogin("admin", &k, MethodPasskey, "r", "a"); err != nil {
		t.Fatal(err)
	}
	if keys := s.WebAuthnKeys("admin"); len(keys) != 1 || !keys[0].Passkey || !s.AnyPasskey() {
		t.Errorf("after the login: %v", keys)
	}
}
