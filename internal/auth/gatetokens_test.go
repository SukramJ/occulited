package auth

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// openccu-lite task 307: the gate's token mirror - one file per stored token, named by the hash of
// its secret, with the token's name, the segments its ingress scopes open, its expiry and its ranges;
// written on every change, gone with the token, the previous secret of a rotation kept for its minute.
func TestGateTokenMirror(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	now := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	gate := filepath.Join(dir, "run", "gate-tokens")
	segments := func(id string) []string {
		if id == "redmatic" {
			return []string{"red", "bad seg", "a/b"} // the drop-in's segment; the two others cannot be written
		}
		return nil
	}
	s, err := Open(dir, Options{SessionDir: filepath.Join(dir, "run", "sessions"), AddonSegments: segments, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(gate); err != nil || st.Mode().Perm() != 0o711 {
		t.Fatalf("the mirror directory: %v %v", err, st)
	}
	read := func(secret string) string {
		b, err := os.ReadFile(filepath.Join(gate, hashToken(secret)))
		if err != nil {
			return ""
		}
		return string(b)
	}
	files := func() int {
		entries, _ := os.ReadDir(gate)
		return len(entries)
	}

	// a token with two ingress scopes: its own ids and the drop-in's segment, sorted; no expiry line
	exp := now.Add(time.Hour)
	red, err := s.CreateToken("loom", TokenOptions{Scopes: Scopes{AddonScope("redmatic"), AddonScope("hmm")}, IPs: []string{"192.0.2.0/24", "198.51.100.7"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := read(red), "name loom\naddons hmm red redmatic\nip 192.0.2.0/24\nip 198.51.100.7/32\n"; got != want {
		t.Errorf("mirror of the ingress token:\n%s\nwant\n%s", got, want)
	}
	if st, _ := os.Stat(filepath.Join(gate, hashToken(red))); st.Mode().Perm() != 0o644 {
		t.Errorf("mode %o, want 644 under a strict umask", st.Mode().Perm())
	}
	// a token without an ingress scope is in the mirror (the gate answers 403, not 401) and opens nothing
	plain, _ := s.CreateToken("plain", TokenOptions{Scopes: Scopes{ScopeMetaRead}, Expires: &exp})
	if got, want := read(plain), "name plain\nexpires "+itoa(exp.Unix())+"\n"; got != want {
		t.Errorf("mirror of a plain token:\n%s\nwant\n%s", got, want)
	}
	// Full access opens every addon
	full, _ := s.CreateToken("full", TokenOptions{Scopes: Scopes{ScopeAll}})
	if got := read(full); got != "name full\naddons *\n" {
		t.Errorf("mirror of a Full-access token:\n%s", got)
	}
	// the three tokens, nothing else (the local token is main's, EnsureLocalToken)
	if n := files(); n != 3 {
		t.Errorf("%d files, want 3 (loom, plain, full)", n)
	}

	// a rotation: both secrets have a file for the grace minute, the old one with its end
	fresh, err := s.RotateToken("loom", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if read(fresh) == "" || !strings.Contains(read(red), "expires "+itoa(now.Add(time.Minute).Unix())+"\n") {
		t.Fatalf("after the rotation: new %q old %q", read(fresh), read(red))
	}
	now = now.Add(2 * time.Minute)
	s.Sweep()
	if read(red) != "" || read(fresh) == "" {
		t.Errorf("after the grace minute: old %q new %q", read(red), read(fresh))
	}
	// narrowing drops a segment
	if _, err := s.UpdateToken("loom", nil, []string{"addon:hmm"}); err != nil {
		t.Fatal(err)
	}
	if got := read(fresh); got != "name loom\naddons hmm\nip 192.0.2.0/24\nip 198.51.100.7/32\n" {
		t.Errorf("after narrowing:\n%s", got)
	}
	// an expired token leaves the mirror at the sweep; a deleted one at once
	now = exp.Add(time.Second)
	s.Sweep()
	if read(plain) != "" {
		t.Error("an expired token is still mirrored")
	}
	if err := s.DeleteToken("loom"); err != nil || read(fresh) != "" {
		t.Errorf("deleted: %v %q", err, read(fresh))
	}
	// a file nobody owns is swept; a changed users.json (the console's token) is mirrored on reload
	stray := filepath.Join(gate, strings.Repeat("ab", 32))
	_ = os.WriteFile(stray, []byte("name stray\n"), 0o644)
	other, err := Open(dir, Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	console, err := other.CreateToken("console", TokenOptions{Scopes: Scopes{AddonScope("hmm")}})
	if err != nil {
		t.Fatal(err)
	}
	// make the change visible as another file (mtime equality would hide it on a fast machine)
	stale := now.Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "users.json"), stale, stale)
	s.Sweep()
	if _, err := os.Stat(stray); err == nil {
		t.Error("a stray file survived the sync")
	}
	if got := read(console); got != "name console\naddons hmm\n" {
		t.Errorf("the console's token after the reload:\n%s", got)
	}
}

// Without a session directory there is no mirror at all, and an ephemeral token never has a file.
func TestGateTokenMirrorOff(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken("xx", TokenOptions{Scopes: Scopes{AddonScope("hmm")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.MintEphemeral("eph", Scopes{ScopeMetaRead}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() == "gate-tokens" {
			t.Fatal("a mirror directory without a session directory")
		}
	}
	gate := filepath.Join(dir, "run", "gate-tokens")
	s2, err := Open(dir, Options{SessionDir: filepath.Join(dir, "run", "sessions")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.MintEphemeral("eph", Scopes{ScopeAll}); err != nil {
		t.Fatal(err)
	}
	entries, _ = os.ReadDir(gate)
	if len(entries) != 1 { // xx
		t.Errorf("%d files, want 1 (the ephemeral token has none)", len(entries))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
