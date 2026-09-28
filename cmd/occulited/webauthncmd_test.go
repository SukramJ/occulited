package main

import (
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// openccu-lite task 262: the console's webauthn command - the usage for every wrong shape (a bare
// `occulited webauthn` panicked on a lab system), list and remove against a state directory.
func TestWebAuthnCmd(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{}, {"list"}, {"remove"}, {"remove", "admin"}, {"frobnicate", "admin"}, {"list", "admin", "extra"}} {
		err := webauthnCmd(append(args, "--state-dir", dir))
		if err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("%v: %v, want the usage", args, err)
		}
	}
	// an unknown account, then an account without keys
	if err := webauthnCmd([]string{"list", "nobody", "--state-dir", dir}); err != auth.ErrUnknownUser {
		t.Errorf("list nobody: %v", err)
	}
	if err := auth.ConsoleSetPassword(dir, "admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := webauthnCmd([]string{"list", "admin", "--state-dir", dir}); err != nil {
		t.Errorf("list: %v", err)
	}
	if err := webauthnCmd([]string{"remove", "admin", "--all", "--state-dir", dir}); err != nil {
		t.Errorf("remove --all without keys: %v", err)
	}
	if err := webauthnCmd([]string{"remove", "admin", "nope", "--state-dir", dir}); err != auth.ErrWebAuthnUnknown {
		t.Errorf("remove an unknown id: %v", err)
	}
}
