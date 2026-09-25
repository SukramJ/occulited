package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// `occulited token` refuses before the first user exists, with a message that says what to do,
// and writes nothing; afterwards it creates the token.
func TestTokenCommand(t *testing.T) {
	dir := t.TempDir()
	args := []string{"charly-setup", "--role", "admin", "--state-dir", dir}
	for _, tc := range []struct {
		name    string
		setup   bool
		wantErr string
	}{
		{"before setup", false, "no user exists"},
		{"after setup", true, ""},
	} {
		if tc.setup {
			if err := auth.ConsoleSetPassword(dir, "admin", "secret123"); err != nil {
				t.Fatal(err)
			}
		}
		err := token(args)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "occulited passwd")):
			t.Errorf("%s: %v, want %q", tc.name, err, tc.wantErr)
		}
		if !tc.setup {
			if _, err := os.Stat(filepath.Join(dir, "users.json")); !os.IsNotExist(err) {
				t.Errorf("%s: users.json written: %v", tc.name, err)
			}
		}
	}
	store, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if list := store.Tokens(); len(list) != 1 || list[0].Name != "charly-setup" || !list[0].Scopes.Full() {
		t.Errorf("tokens %+v", list)
	}
	// task 66: --scope (repeatable), --expires and --ip; --role is the alias, and a token without
	// either is refused
	if err := token([]string{"reader", "--scope", "meta:read", "--scope", "logs:read", "--expires", "30d", "--ip", "192.168.1.0/24", "--ip", "10.0.0.5", "--state-dir", dir}); err != nil {
		t.Fatal(err)
	}
	if err := token([]string{"nothing", "--state-dir", dir}); err == nil || !strings.Contains(err.Error(), "--scope") {
		t.Errorf("a token without scopes: %v", err)
	}
	if err := token([]string{"bad", "--scope", "root", "--state-dir", dir}); err == nil {
		t.Error("an unknown scope")
	}
	if err := token([]string{"led-alias", "--role", "led", "--state-dir", dir}); err != nil {
		t.Fatal(err)
	}
	store, _ = auth.Open(dir, auth.Options{})
	byName := map[string]auth.Token{}
	for _, tk := range store.Tokens() {
		byName[tk.Name] = tk
	}
	if r := byName["reader"]; strings.Join(r.Scopes.Strings(), ",") != "logs:read,meta:read" || r.Expires == nil || r.Expires.Before(time.Now().Add(29*24*time.Hour)) || strings.Join(r.IPs, " ") != "192.168.1.0/24 10.0.0.5/32" {
		t.Errorf("reader: %+v", r)
	}
	if l := byName["led-alias"]; strings.Join(l.Scopes.Strings(), ",") != "led" {
		t.Errorf("led alias: %+v", l)
	}
	if len(byName) != 3 {
		t.Errorf("tokens: %v", byName)
	}
}

func TestParseExpiry(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"2027-01-01T00:00:00Z", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"30d", now.Add(30 * 24 * time.Hour), true},
		{"12h", now.Add(12 * time.Hour), true},
		{"0d", time.Time{}, false},
		{"soon", time.Time{}, false},
	} {
		got, err := parseExpiry(tc.in, now)
		if (err == nil) != tc.ok || (tc.ok && !got.Equal(tc.want)) {
			t.Errorf("%q: %v %v", tc.in, got, err)
		}
	}
	if got, err := parseExpiry("2027-01-01", now); err != nil || got.Year() != 2027 || got.Month() != 1 || got.Day() != 1 {
		t.Errorf("date: %v %v", got, err)
	}
}
