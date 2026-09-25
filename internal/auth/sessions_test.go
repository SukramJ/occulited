package auth

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// B-102, D-67: sessions survive a restart and a reboot, stored by hash only.

var bothMethods = []string{MethodPassword, MethodOIDC}

// openAt opens a store over dir with its mirror in mirror and the session store in
// dir/sessions/sessions.json, on the clock *now - a new occulited process over the same state.
func openAt(t *testing.T, dir, mirror string, now *time.Time, methods []string) *Store {
	t.Helper()
	s, err := Open(dir, Options{SessionDir: mirror, SessionFile: storeFile(dir), RestoreMethods: methods, Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func storeFile(dir string) string { return filepath.Join(dir, "sessions", "sessions.json") }

func readStore(t *testing.T, dir string) sessionsDoc {
	t.Helper()
	var doc sessionsDoc
	b, err := os.ReadFile(storeFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the store is not JSON: %v %q", err, b)
	}
	return doc
}

func storedHashes(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	for _, e := range readStore(t, dir).Sessions {
		out = append(out, e.Hash)
	}
	sort.Strings(out)
	return out
}

func keysOf(ids ...string) []string {
	var out []string
	for _, id := range ids {
		out = append(out, sessionKey(id))
	}
	sort.Strings(out)
	return out
}

func mirrorNames(t *testing.T, mirror string) []string {
	t.Helper()
	entries, err := os.ReadDir(mirror)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func mustLogin(t *testing.T, s *Store, name, pw string) *Session {
	t.Helper()
	sess, err := s.Login(name, pw, "192.168.0.24", "Firefox")
	if err != nil {
		t.Fatalf("login %s: %v", name, err)
	}
	return sess
}

func TestSessionSurvivesRestart(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	a := openAt(t, dir, filepath.Join(dir, "run1"), &now, bothMethods)
	if err := a.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	sess := mustLogin(t, a, "admin", "secret123")

	// a reboot: a new process over the same state directory, an empty tmpfs
	now = now.Add(3 * time.Minute)
	run2 := filepath.Join(dir, "run2")
	b := openAt(t, dir, run2, &now, bothMethods)
	if got := mirrorNames(t, run2); strings.Join(got, ",") != sessionKey(sess.ID) {
		t.Fatalf("mirror rebuilt before serving: %v", got)
	}
	if body, _ := os.ReadFile(filepath.Join(run2, sessionKey(sess.ID))); string(body) != "admin\n" {
		t.Fatalf("mirror content %q", body)
	}
	v := b.Validate(sess.ID)
	if v == nil || v.ID != sess.ID || v.User != "admin" || v.Role != RoleAdmin || v.Method != MethodPassword || !v.Created.Equal(sess.Created) || v.Remote != "192.168.0.24" || v.Agent != "Firefox" {
		t.Fatalf("restored session: %+v", v)
	}

	// a restart of occulited alone: the old mirror is still there, with a file of a session that is
	// gone and one in the old shape (named by an id)
	for _, name := range []string{"LEGACY0000", sessionKey("GONE000000")} {
		if err := os.WriteFile(filepath.Join(run2, name), []byte("admin\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := openAt(t, dir, run2, &now, bothMethods)
	if got := mirrorNames(t, run2); strings.Join(got, ",") != sessionKey(sess.ID) {
		t.Errorf("mirror after a restart: %v", got)
	}
	if c.Validate(sess.ID) == nil {
		t.Error("the session did not survive the restart")
	}
	if c.Validate("GONE000000") != nil || c.Validate("LEGACY0000") != nil {
		t.Error("a stale mirror file made a session")
	}
}

// Every way a session ends, or a user's sessions change, is in the store: a new process agrees.
func TestSessionChangesArePersisted(t *testing.T) {
	cur := "bobsecret1"
	for _, tc := range []struct {
		name string
		act  func(s *Store, a1, b1, b2 *Session) error
		// which of admin's a1 and bob's b1, b2 are valid afterwards, in a new process
		a1, b1, b2 bool
		bobRole    Role
	}{
		{"logout", func(s *Store, _, b1, _ *Session) error { s.Logout(b1.ID); return nil }, true, false, true, RoleUser},
		{"sign out everywhere", func(s *Store, _, b1, _ *Session) error { s.LogoutUser("bob", b1.ID); return nil }, true, true, false, RoleUser},
		{"one session ended from the list", func(s *Store, _, b1, b2 *Session) error {
			s.EndSessions(SessionHandle(b2.ID), "bob", b1.ID)
			return nil
		}, true, true, false, RoleUser},
		{"own password change", func(s *Store, _, b1, _ *Session) error { return s.SetPassword("bob", "newsecret1", &cur, b1.ID) }, true, true, false, RoleUser},
		{"password reset by an administrator", func(s *Store, _, _, _ *Session) error { return s.SetPassword("bob", "newsecret1", nil, "") }, true, false, false, RoleUser},
		{"user deleted", func(s *Store, _, _, _ *Session) error { return s.DeleteUser("bob") }, true, false, false, ""},
		{"role change", func(s *Store, _, _, _ *Session) error { return s.SetRole("bob", RoleAdmin) }, true, true, true, RoleAdmin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
			dir := t.TempDir()
			s := openAt(t, dir, filepath.Join(dir, "run1"), &now, bothMethods)
			_ = s.Setup("admin", "secret123")
			if err := s.CreateUser("bob", cur, RoleUser, false); err != nil {
				t.Fatal(err)
			}
			a1, b1, b2 := mustLogin(t, s, "admin", "secret123"), mustLogin(t, s, "bob", cur), mustLogin(t, s, "bob", cur)
			if err := tc.act(s, a1, b1, b2); err != nil {
				t.Fatal(err)
			}
			var want []string
			for _, c := range []struct {
				sess *Session
				ok   bool
			}{{a1, tc.a1}, {b1, tc.b1}, {b2, tc.b2}} {
				if c.ok {
					want = append(want, c.sess.ID)
				}
			}
			// the file right after the change, before anything else writes it
			if got, w := storedHashes(t, dir), keysOf(want...); strings.Join(got, ",") != strings.Join(w, ",") {
				t.Errorf("store %v, want %v", got, w)
			}
			run2 := filepath.Join(dir, "run2")
			fresh := openAt(t, dir, run2, &now, bothMethods)
			if got, w := mirrorNames(t, run2), keysOf(want...); strings.Join(got, ",") != strings.Join(w, ",") {
				t.Errorf("mirror %v, want %v", got, w)
			}
			for name, c := range map[string]struct {
				sess *Session
				ok   bool
			}{"a1": {a1, tc.a1}, "b1": {b1, tc.b1}, "b2": {b2, tc.b2}} {
				v := fresh.Validate(c.sess.ID)
				if (v != nil) != c.ok {
					t.Errorf("%s valid %v, want %v", name, v != nil, c.ok)
				}
				if v != nil && v.User == "bob" && v.Role != tc.bobRole {
					t.Errorf("%s role %s, want %s", name, v.Role, tc.bobRole)
				}
			}
		})
	}
}

// A restored session keeps the expiry it had: the idle timeout from its last stored activity, the
// maximum age from its login. Expiry itself is written: by Validate and by the sweep.
func TestSessionExpiryAcrossRestarts(t *testing.T) {
	t0 := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	now := t0
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	active := mustLogin(t, s, "admin", "secret123")
	idle := mustLogin(t, s, "admin", "secret123")

	// idle: 24 h without a request, the sweep drops it and writes the store
	now = t0.Add(23 * time.Hour)
	if s.Validate(active.ID) == nil {
		t.Fatal("active session gone at 23 h")
	}
	now = t0.Add(24*time.Hour + time.Second)
	s.Sweep()
	if got := storedHashes(t, dir); strings.Join(got, ",") != sessionKey(active.ID) {
		t.Fatalf("after the sweep the store holds %v", got)
	}

	// the idle time runs from the stored last-seen time across a restart
	now = t0.Add(23*time.Hour + 23*time.Hour)
	s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if s.Validate(active.ID) == nil {
		t.Fatal("restored session idled out although it was used 23 h ago")
	}
	if s.Validate(idle.ID) != nil {
		t.Fatal("the swept session is back")
	}
	now = now.Add(24*time.Hour + time.Second)
	s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if s.Validate(active.ID) != nil {
		t.Fatal("a session idle for more than 24 h was restored")
	}
	if got := readStore(t, dir).Sessions; len(got) != 0 {
		t.Fatalf("the start did not write out the expired session: %+v", got)
	}

	// the maximum age: 30 days from the login, however active, and however often restarted
	now = t0
	dir = t.TempDir()
	s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	sess := mustLogin(t, s, "admin", "secret123")
	for now.Before(t0.Add(30*24*time.Hour - 20*time.Hour)) {
		now = now.Add(20 * time.Hour)
		s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		if v := s.Validate(sess.ID); v == nil || !v.Created.Equal(t0) {
			t.Fatalf("at %s: %+v", now.Sub(t0), v)
		}
	}
	if e := readStore(t, dir).Sessions[0]; !e.Expires.Equal(t0.Add(30*24*time.Hour)) || !e.Created.Equal(t0) {
		t.Fatalf("stored expiry moved: %+v", e)
	}
	now = t0.Add(30*24*time.Hour + time.Second)
	if s.Validate(sess.ID) != nil {
		t.Fatal("valid past its maximum age")
	}
	if got := readStore(t, dir).Sessions; len(got) != 0 {
		t.Fatalf("the expiry was not written: %+v", got)
	}
	now = t0.Add(30*24*time.Hour - time.Minute) // the clock set back does not bring it back either
	if s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods); s.Validate(sess.ID) != nil {
		t.Fatal("an expired session came back")
	}
}

// No session id - in any file name or file content - under the state directory or in the mirror.
func TestNoSessionIDOnDisk(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	mirror := filepath.Join(dir, "run")
	s := openAt(t, dir, mirror, &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	_ = s.CreateUser("bob", "bobsecret1", RoleUser, false)
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, mustLogin(t, s, "admin", "secret123").ID, mustLogin(t, s, "bob", "bobsecret1").ID)
	}
	_ = s.CreateUserWithoutPassword("carol", RoleUser)
	ext, err := s.LoginExternal("oidc", "carol", "10.0.0.2", "curl")
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, ext.ID)
	now = now.Add(time.Hour)
	for _, id := range ids {
		_ = s.Validate(id) // a last-seen write
	}
	s.Logout(ids[0])
	s.Close()
	_ = openAt(t, dir, mirror, &now, bothMethods) // and a restart's writes
	files := 0
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		for _, id := range ids {
			if strings.Contains(p, id) {
				t.Errorf("a path holds session id %s: %s", id, p)
			}
		}
		if d.IsDir() {
			return nil
		}
		files++
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if bytes.Contains(b, []byte(id)) {
				t.Errorf("%s holds session id %s", p, id)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files < len(ids)-1+3 { // users.json, sessions.json, .nobackup and the live sessions' mirror files
		t.Fatalf("only %d files looked at", files)
	}
}

func TestSessionStoreFiles(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if _, err := os.Stat(filepath.Join(dir, "sessions", ".nobackup")); err != nil {
		t.Fatalf(".nobackup at the start: %v", err)
	}
	_ = s.Setup("admin", "secret123")
	sess := mustLogin(t, s, "admin", "secret123")
	st, err := os.Stat(filepath.Join(dir, "sessions"))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the store's directory: %v %v", err, st)
	}
	if st, err := os.Stat(storeFile(dir)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the store: %v %v", err, st)
	}
	if st, err := os.Stat(filepath.Join(dir, "run")); err != nil || st.Mode().Perm() != 0o711 {
		t.Fatalf("the mirror: %v %v", err, st)
	}
	var raw map[string]any
	b, _ := os.ReadFile(storeFile(dir))
	_ = json.Unmarshal(b, &raw)
	entry := raw["sessions"].([]any)[0].(map[string]any)
	for _, k := range []string{"hash", "user", "role", "method", "created", "last_seen", "idle_expires", "expires"} {
		if _, ok := entry[k]; !ok {
			t.Errorf("stored session without %s: %v", k, entry)
		}
	}
	if raw["version"] != float64(sessionsVersion) || entry["hash"] != sessionKey(sess.ID) || entry["method"] != MethodPassword {
		t.Errorf("stored: %s", b)
	}
	if _, ok := entry["id"]; ok {
		t.Error("an id field in the store")
	}
	// the marker comes back before the next write, and no temporary file is left over
	_ = os.Remove(filepath.Join(dir, "sessions", ".nobackup"))
	s.Logout(sess.ID)
	names := mirrorNames(t, filepath.Join(dir, "sessions"))
	if strings.Join(names, ",") != ".nobackup,sessions.json" {
		t.Errorf("the store's directory holds %v", names)
	}
	// auth-off's anonymous session is never stored
	s.EnsureAnonymous()
	s.Close()
	if got := readStore(t, dir).Sessions; len(got) != 0 {
		t.Errorf("stored: %+v", got)
	}
	// the console commands open the store without it and leave it alone
	if err := ConsoleSetPassword(dir, "admin", "console-pw1"); err != nil {
		t.Fatal(err)
	}
	if names := mirrorNames(t, filepath.Join(dir, "sessions")); strings.Join(names, ",") != ".nobackup,sessions.json" {
		t.Errorf("after a console command: %v", names)
	}
}

// A request writes the last-seen time only when the stored one is LastSeenEvery old; a clean
// shutdown writes the rest.
func TestLastSeenWriteThrottle(t *testing.T) {
	t0 := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	now := t0
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	sess := mustLogin(t, s, "admin", "secret123")
	other := mustLogin(t, s, "admin", "secret123")
	identity := func() os.FileInfo {
		st, err := os.Stat(storeFile(dir))
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	lastSeen := func() time.Time {
		for _, e := range readStore(t, dir).Sessions {
			if e.Hash == sessionKey(sess.ID) {
				return e.LastSeen
			}
		}
		t.Fatal("the session is not in the store")
		return time.Time{}
	}
	written := identity()
	step := func(at time.Duration, wantWrite bool, wantStored time.Duration) {
		t.Helper()
		now = t0.Add(at)
		if s.Validate(sess.ID) == nil {
			t.Fatalf("at %s: invalid", at)
		}
		st := identity()
		if wrote := !os.SameFile(st, written); wrote != wantWrite {
			t.Errorf("at %s: written %v, want %v", at, wrote, wantWrite)
		}
		written = st
		if got := lastSeen(); !got.Equal(t0.Add(wantStored)) {
			t.Errorf("at %s: stored last-seen %s, want %s", at, got.Sub(t0), wantStored)
		}
	}
	step(time.Minute, false, 0)
	step(5*time.Minute, false, 0)
	step(10*time.Minute-time.Second, false, 0)
	step(10*time.Minute, true, 10*time.Minute)
	step(15*time.Minute, false, 10*time.Minute)
	step(19*time.Minute, false, 10*time.Minute)
	if s.Validate(other.ID) == nil { // the other session, 19 min after its login: written too
		t.Fatal("other session")
	}
	if os.SameFile(identity(), written) {
		t.Error("the other session's stale last-seen was not written")
	}
	written = identity()
	step(20*time.Minute, false, 19*time.Minute) // the other session's write carried this one's 19 min
	now = t0.Add(25 * time.Minute)
	s.Validate(sess.ID)
	s.Close()
	if os.SameFile(identity(), written) || !lastSeen().Equal(t0.Add(25*time.Minute)) {
		t.Errorf("shutdown: stored last-seen %s", lastSeen().Sub(t0))
	}
	written = identity()
	s.Close()
	if !os.SameFile(identity(), written) {
		t.Error("a second shutdown wrote again with nothing new")
	}

	// a box without a real-time clock starts behind the stored time: the last-seen time does not
	// go back, so the jump to the right time is not idle time
	now = t0.Add(-40 * 24 * time.Hour)
	s = openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if s.Validate(sess.ID) == nil {
		t.Fatal("invalid with the clock behind")
	}
	written = identity()
	now = t0.Add(30 * time.Minute)
	if s.Validate(sess.ID) == nil {
		t.Fatal("the clock's jump forward counted as idle time")
	}
	if got := lastSeen(); !got.Equal(t0.Add(25*time.Minute)) && !got.Equal(t0.Add(30*time.Minute)) {
		t.Errorf("stored last-seen %s", got.Sub(t0))
	}
}

// A store occulited cannot use restores nothing, says so, and never stops the start.
func TestUnusableSessionStore(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	prepare := func(t *testing.T) (string, *Session) {
		dir := t.TempDir()
		s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		_ = s.Setup("admin", "secret123")
		return dir, mustLogin(t, s, "admin", "secret123")
	}
	for _, tc := range []struct {
		name    string
		content string
		log     string
	}{
		{"corrupt", `{"version":2,"sessions":[{"hash":`, "corrupt"},
		{"empty", "", "corrupt"},
		{"not an object", `[1,2,3]`, "corrupt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, sess := prepare(t)
			if err := os.WriteFile(storeFile(dir), []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			logs := captureLog(t)
			s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
			if s.Validate(sess.ID) != nil {
				t.Error("a session from an unusable store")
			}
			if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), tc.log) {
				t.Errorf("log: %s", logs)
			}
			if got := readStore(t, dir).Sessions; len(got) != 0 {
				t.Errorf("the corrupt store was not replaced: %+v", got)
			}
			if names := mirrorNames(t, filepath.Join(dir, "run")); len(names) != 0 {
				t.Errorf("mirror %v", names)
			}
			if again := mustLogin(t, s, "admin", "secret123"); openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods).Validate(again.ID) == nil {
				t.Error("a login after an unusable store does not survive")
			}
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a file of mode 0")
		}
		dir, sess := prepare(t)
		if err := os.Chmod(storeFile(dir), 0); err != nil {
			t.Fatal(err)
		}
		logs := captureLog(t)
		s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		if s.Validate(sess.ID) != nil || !strings.Contains(logs.String(), "cannot read the session store") {
			t.Errorf("unreadable: log %s", logs)
		}
		again := mustLogin(t, s, "admin", "secret123") // replaces the file
		if openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods).Validate(again.ID) == nil {
			t.Error("the next login's store is not usable")
		}
	})
	t.Run("the store's directory is a file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "sessions"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		logs := captureLog(t)
		s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		_ = s.Setup("admin", "secret123")
		if sess := mustLogin(t, s, "admin", "secret123"); s.Validate(sess.ID) == nil {
			t.Error("a login without a usable store")
		}
		if !strings.Contains(logs.String(), "not usable") || !strings.Contains(logs.String(), "cannot write the session store") {
			t.Errorf("log: %s", logs)
		}
	})
	t.Run("entries that do not fit", func(t *testing.T) {
		dir, sess := prepare(t)
		s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		_ = s.CreateUserWithoutPassword("carol", RoleUser)
		ext, err := s.LoginExternal("oidc", "carol", "", "")
		if err != nil {
			t.Fatal(err)
		}
		// task 19: the provider signs in any account, so admin's provider session is a good one
		viaProvider, err := s.LoginExternal("oidc", "admin", "", "")
		if err != nil {
			t.Fatal(err)
		}
		doc := readStore(t, dir)
		good := map[string]bool{sessionKey(sess.ID): true, sessionKey(ext.ID): true, sessionKey(viaProvider.ID): true}
		var base storedSession // admin's password session
		for _, e := range doc.Sessions {
			if e.Hash == sessionKey(sess.ID) {
				base = e
			}
		}
		bad := func(f func(e *storedSession)) {
			e := base
			e.Hash = sessionKey(time.Now().String() + e.Hash + string(rune(len(doc.Sessions))))
			f(&e)
			doc.Sessions = append(doc.Sessions, e)
		}
		bad(func(e *storedSession) { e.Hash = "ABCDEFGHIJ" })                  // an id, not a hash
		bad(func(e *storedSession) { e.Hash = strings.ToUpper(e.Hash) })       // not lower-case hex
		bad(func(e *storedSession) { e.User = "mallory" })                     // no such user
		bad(func(e *storedSession) { e.Method = "magic" })                     // no such method
		bad(func(e *storedSession) { e.User, e.Method = "carol", "password" }) // a password session of an account without a password
		bad(func(e *storedSession) { e.Expires = time.Time{} })
		bad(func(e *storedSession) { e.IdleExpires = now.Add(-time.Second) })
		bad(func(e *storedSession) { e.Expires = now.Add(-time.Second) })
		bad(func(e *storedSession) {
			e.Role = RoleAdmin
			e.User = "carol"
			e.Method = MethodOIDC
			e.Hash = sessionKey(ext.ID)
		}) // a duplicate
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(storeFile(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
		fresh := openAt(t, dir, filepath.Join(dir, "run2"), &now, bothMethods)
		if fresh.Validate(sess.ID) == nil {
			t.Error("the good password session was dropped")
		}
		if v := fresh.Validate(ext.ID); v == nil || v.Role != RoleUser {
			t.Errorf("the good provider session: %+v", v)
		}
		if v := fresh.Validate(viaProvider.ID); v == nil || v.Role != RoleAdmin || v.Method != MethodOIDC {
			t.Errorf("admin's provider session: %+v", v)
		}
		if n := len(fresh.Sessions("")); n != 3 {
			t.Errorf("%d sessions restored: %+v", n, fresh.Sessions(""))
		}
		for _, h := range storedHashes(t, dir) {
			if !good[h] {
				t.Errorf("a dropped entry stayed in the store: %s", h)
			}
		}
		if names := mirrorNames(t, filepath.Join(dir, "run2")); len(names) != 3 {
			t.Errorf("mirror %v", names)
		}
	})
}

// The running authentication mode decides which stored sessions come back: a provider's only
// while it is configured, none with authentication off.
func TestRestoreMethods(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	pw := mustLogin(t, s, "admin", "secret123")
	_ = s.CreateUserWithoutPassword("carol", RoleAdmin)
	ext, err := s.LoginExternal("oidc", "carol", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if v := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods).Validate(ext.ID); v == nil || v.Method != MethodOIDC {
		t.Fatalf("provider session with the provider configured: %+v", v)
	}
	local := openAt(t, dir, filepath.Join(dir, "run"), &now, []string{MethodPassword})
	if local.Validate(pw.ID) == nil || local.Validate(ext.ID) != nil {
		t.Error("local mode: the password session stays, the provider's goes")
	}
	if got := storedHashes(t, dir); strings.Join(got, ",") != sessionKey(pw.ID) {
		t.Errorf("store %v", got)
	}
	off := openAt(t, dir, filepath.Join(dir, "run"), &now, nil)
	if off.Validate(pw.ID) != nil {
		t.Error("auth off restored a session")
	}
	if got := readStore(t, dir).Sessions; len(got) != 0 {
		t.Errorf("auth off left sessions in the store: %+v", got)
	}
	if again := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods); again.Validate(pw.ID) != nil {
		t.Error("switching authentication off and on again restored a session")
	}
}

// A write that ends a session and fails leaves no store that would bring the session back.
func TestFailedRevocationWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into a directory of mode 0500")
	}
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	gone := mustLogin(t, s, "admin", "secret123")
	kept := mustLogin(t, s, "admin", "secret123")
	store := filepath.Join(dir, "sessions")
	if err := os.Chmod(store, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(store, 0o700) })
	logs := captureLog(t)
	s.Logout(gone.ID)
	if !strings.Contains(logs.String(), "cannot write the session store") {
		t.Errorf("log: %s", logs)
	}
	if err := os.Chmod(store, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if fresh.Validate(gone.ID) != nil {
		t.Fatal("the logged-out session came back after a failed write")
	}
	_ = kept // lost with the store: logging in again is the safe side
}

// Task 125: a session id is 26 characters of base32 (130 bits), never the ten-character shape.
func TestSessionIDs(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		id, err := newSID()
		if err != nil {
			t.Fatal(err)
		}
		if len(id) != 26 || !IsSessionID(id) || IsLegacyID(id) || seen[id] {
			t.Fatalf("id %q (seen %v)", id, seen[id])
		}
		seen[id] = true
	}
	for _, bad := range []string{"", "ABCDEFGHIJ", "abcdefghijklmnopqrstuvwxyz", "ABCDEFGHIJKLMNOPQRSTUVWXY1", "ABCDEFGHIJKLMNOPQRSTUVWXYZA", "@ABCDEFGHIJKLMNOPQRSTUVWXYZ@"} {
		if IsSessionID(bad) {
			t.Errorf("%q counts as a session id", bad)
		}
	}
	if !IsSessionID(AnonymousSID) || !IsLegacyID(AnonymousLegacySID) || IsSessionID(AnonymousLegacySID) {
		t.Error("the anonymous ids have the wrong shapes")
	}
	if h := SessionHandle("ABCDEFGHIJKLMNOPQRSTUVWXYZ"); h != sessionKey("ABCDEFGHIJKLMNOPQRSTUVWXYZ")[:6]+"……" || strings.Contains(h, "ABCD") {
		t.Errorf("handle %q", h)
	}
	if SessionStorePath("/usr/local/etc/occulite") != "/usr/local/etc/occulite/sessions/sessions.json" {
		t.Error(SessionStorePath("/usr/local/etc/occulite"))
	}
}

// The alias: ten of [0-9a-zA-Z], each drawn uniformly. A chi-square test over the characters of
// many aliases: with 61 degrees of freedom the statistic stays below 100 with a probability of
// 0.999 (the 0.001 quantile is 99.6); a modulo bias of the 62-letter alphabet over 256 values -
// the first 8 letters 1.25 times as likely - would put it in the thousands.
func TestAliasShapeAndDistribution(t *testing.T) {
	const draws = 20000
	counts := map[byte]int{}
	seen := map[string]bool{}
	for i := 0; i < draws; i++ {
		a, err := newAlias()
		if err != nil {
			t.Fatal(err)
		}
		if len(a) != 10 || !IsLegacyID(a) || seen[a] {
			t.Fatalf("alias %q (seen %v)", a, seen[a])
		}
		seen[a] = true
		for j := 0; j < len(a); j++ {
			counts[a[j]]++
		}
	}
	expected := float64(draws*10) / float64(len(aliasAlphabet))
	chi := 0.0
	for i := 0; i < len(aliasAlphabet); i++ {
		d := float64(counts[aliasAlphabet[i]]) - expected
		chi += d * d / expected
	}
	if len(counts) != len(aliasAlphabet) || chi > 100 {
		t.Errorf("chi-square %.1f over %d letters", chi, len(counts))
	}
	for _, bad := range []string{"", "ABCDEFGHI", "ABCDEFGHIJK", "@ABCDEFGHIJ@", "ABCDEFGH-J", "ABCDEFGHIJKLMNOPQRSTUVWXYZ"} {
		if IsLegacyID(bad) {
			t.Errorf("%q counts as an alias", bad)
		}
	}
}

// The alias is made when asked for, once per session, mirrored by its hash with the user and the
// session's hash, handed out again by the same process, and gone with its session - on a logout,
// a password change, an expiry.
func TestLegacyAlias(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	mirror := filepath.Join(dir, "run", "sessions")
	legacy := filepath.Join(dir, "run", "legacy-sessions")
	s := openAt(t, dir, mirror, &now, bothMethods)
	if st, err := os.Stat(legacy); err != nil || st.Mode().Perm() != 0o711 {
		t.Fatalf("the alias mirror beside the session mirror: %v %v", err, st)
	}
	_ = s.Setup("admin", "secret123")
	sess := mustLogin(t, s, "admin", "secret123")
	other := mustLogin(t, s, "admin", "secret123")
	if v := s.Validate(sess.ID); v == nil || v.Legacy != "" {
		t.Fatalf("an alias before anyone asked: %+v", v)
	}
	if names := mirrorNames(t, legacy); len(names) != 0 {
		t.Fatalf("alias mirror %v", names)
	}
	alias, err := s.LegacyID(sess.ID)
	if err != nil || !IsLegacyID(alias) {
		t.Fatalf("LegacyID: %q %v", alias, err)
	}
	if again, _ := s.LegacyID(sess.ID); again != alias {
		t.Errorf("a second ask made another alias: %q, %q", alias, again)
	}
	if v := s.Validate(sess.ID); v == nil || v.Legacy != alias {
		t.Errorf("Validate does not carry the alias: %+v", v)
	}
	if body, err := os.ReadFile(filepath.Join(legacy, sessionKey(alias))); err != nil || string(body) != "admin\n"+sessionKey(sess.ID)+"\n" {
		t.Errorf("alias mirror file: %q %v", body, err)
	}
	if v := s.ValidateLegacy(alias); v == nil || v.User != "admin" || v.ID != "" || v.Legacy != alias {
		t.Errorf("ValidateLegacy: %+v", v)
	}
	for _, bad := range []string{sess.ID, "@" + alias + "@", "nobody0000", sessionKey(alias)} {
		if s.ValidateLegacy(bad) != nil {
			t.Errorf("ValidateLegacy(%q) found a session", bad)
		}
	}
	// the alias is no session id: the API's lookup does not find it
	if s.Validate(alias) != nil {
		t.Error("the alias validated as a session")
	}
	// the other session has none, and asking for one does not touch the first
	if v := s.Validate(other.ID); v == nil || v.Legacy != "" {
		t.Errorf("the other session: %+v", v)
	}
	otherAlias, _ := s.LegacyID(other.ID)
	if otherAlias == "" || otherAlias == alias {
		t.Errorf("the other session's alias: %q", otherAlias)
	}
	if got := mirrorNames(t, legacy); len(got) != 2 {
		t.Errorf("alias mirror %v", got)
	}
	// stored by hash, restored by hash: after a restart the gate still finds the alias, the
	// process does not know it, and the next ask replaces it
	for _, e := range readStore(t, dir).Sessions {
		if e.LegacyHash == "" || e.LegacyHash == e.Hash {
			t.Errorf("stored alias hash %q for %s", e.LegacyHash, e.Hash[:6])
		}
	}
	now = now.Add(time.Minute)
	run2 := filepath.Join(dir, "run2", "sessions")
	fresh := openAt(t, dir, run2, &now, bothMethods)
	if _, err := os.Stat(filepath.Join(dir, "run2", "legacy-sessions", sessionKey(alias))); err != nil {
		t.Errorf("the alias mirror after a restart: %v", err)
	}
	if v := fresh.ValidateLegacy(alias); v == nil || v.User != "admin" {
		t.Errorf("the alias after a restart: %+v", v)
	}
	if v := fresh.Validate(sess.ID); v == nil || v.Legacy != "" {
		t.Errorf("a restored session knows its alias: %+v", v)
	}
	renewed, _ := fresh.LegacyID(sess.ID)
	if renewed == "" || renewed == alias {
		t.Errorf("renewed alias %q", renewed)
	}
	if fresh.ValidateLegacy(alias) != nil {
		t.Error("the old alias still works after the new one")
	}
	if got := mirrorNames(t, filepath.Join(dir, "run2", "legacy-sessions")); len(got) != 2 {
		t.Errorf("alias mirror after the renewal %v", got)
	}
	// ends with its session: a logout removes the file, and a new process does not know it
	fresh.Logout(sess.ID)
	if fresh.ValidateLegacy(renewed) != nil {
		t.Error("the alias outlived its session")
	}
	if _, err := os.Stat(filepath.Join(dir, "run2", "legacy-sessions", sessionKey(renewed))); err == nil {
		t.Error("the alias file outlived its session")
	}
	if openAt(t, dir, run2, &now, bothMethods).ValidateLegacy(renewed) != nil {
		t.Error("the alias came back after a restart")
	}
	// a password change ends the other session and its alias; an expired session's alias too
	if err := fresh.SetPassword("admin", "newsecret1", nil, ""); err != nil {
		t.Fatal(err)
	}
	if fresh.ValidateLegacy(otherAlias) != nil {
		t.Error("the alias outlived the password change")
	}
	idle := mustLogin(t, fresh, "admin", "newsecret1")
	idleAlias, _ := fresh.LegacyID(idle.ID)
	now = now.Add(25 * time.Hour)
	if fresh.ValidateLegacy(idleAlias) != nil {
		t.Error("the alias of an idle session")
	}
	if got := mirrorNames(t, filepath.Join(dir, "run2", "legacy-sessions")); len(got) != 0 {
		t.Errorf("alias mirror at the end %v", got)
	}
	// an alias for a session nobody has
	if a, err := fresh.LegacyID("NOSUCHSESSIONNOSUCHSESSION"); a != "" || err != nil {
		t.Errorf("an alias for no session: %q %v", a, err)
	}
	// auth off: the fixed alias, mirrored beside the fixed session
	anon := fresh.EnsureAnonymous()
	if anon.Legacy != AnonymousLegacySID || fresh.ValidateLegacy(AnonymousLegacySID) == nil {
		t.Errorf("the anonymous alias: %+v", anon)
	}
	if _, err := os.Stat(filepath.Join(dir, "run2", "legacy-sessions", sessionKey(AnonymousLegacySID))); err != nil {
		t.Errorf("the anonymous alias mirror: %v", err)
	}
	if a, _ := fresh.LegacyID(AnonymousSID); a != AnonymousLegacySID {
		t.Errorf("LegacyID of the anonymous session: %q", a)
	}
}

// A ticket is one credential for one path, once, within TicketTTL, and only while its session
// lives.
func TestTickets(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	sess := mustLogin(t, s, "admin", "secret123")
	tk, err := s.IssueTicket(sess.ID, "/api/system/v1/backup")
	if err != nil || len(tk) != 26 {
		t.Fatalf("ticket %q %v", tk, err)
	}
	if s.Validate(tk) != nil || s.ValidateLegacy(tk) != nil {
		t.Error("a ticket passed as a session id or an alias")
	}
	if s.RedeemTicket(tk, "/api/system/v1/log/download") != nil {
		t.Error("redeemed on another path")
	}
	if s.RedeemTicket(tk, "/api/system/v1/backup") != nil {
		t.Error("a ticket tried on the wrong path was not spent")
	}
	tk, _ = s.IssueTicket(sess.ID, "/api/system/v1/backup")
	if v := s.RedeemTicket(tk, "/api/system/v1/backup"); v == nil || v.User != "admin" || v.Role != RoleAdmin || v.ID != "" {
		t.Errorf("redeemed: %+v", v)
	}
	if s.RedeemTicket(tk, "/api/system/v1/backup") != nil {
		t.Error("redeemed twice")
	}
	// a download ticket lives 60 s, a session hand-over ticket 90 s - the confirm window (D-84)
	tk, _ = s.IssueTicket(sess.ID, "/api/system/v1/backup")
	now = now.Add(TicketTTL + time.Second)
	if s.RedeemTicket(tk, "/api/system/v1/backup") != nil {
		t.Error("a download ticket redeemed after its time")
	}
	tk, _ = s.IssueTicket(sess.ID, TicketSession)
	now = now.Add(TicketTTL + time.Second)
	if v := s.RedeemTicket(tk, TicketSession); v == nil || v.User != "admin" {
		t.Errorf("a session ticket must outlive a download ticket: %+v", v)
	}
	tk, _ = s.IssueTicket(sess.ID, TicketSession)
	now = now.Add(SessionTicketTTL + time.Second)
	if s.RedeemTicket(tk, TicketSession) != nil {
		t.Error("a session ticket redeemed after its time")
	}
	tk, _ = s.IssueTicket(sess.ID, TicketSession)
	s.Logout(sess.ID)
	if s.RedeemTicket(tk, TicketSession) != nil {
		t.Error("redeemed after the session ended")
	}
	if tk, err := s.IssueTicket(sess.ID, TicketSession); tk != "" || err != nil {
		t.Errorf("a ticket for an ended session: %q %v", tk, err)
	}
	if tk, err := s.IssueTicket(mustLogin(t, s, "admin", "secret123").ID, ""); tk != "" || err != nil {
		t.Errorf("a ticket for no path: %q %v", tk, err)
	}
}

// The store of an occulited before the 128-bit ids (version 1) is dropped at the first start:
// its ids have a shape no request can present any more, and none of them becomes an alias.
func TestOldSessionStoreIsDropped(t *testing.T) {
	now := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	_ = s.Setup("admin", "secret123")
	old := "AbCdEfGhIj" // what a version-1 store held: the hash of a ten-character id
	doc := sessionsDoc{Version: 1, Sessions: []storedSession{{Hash: sessionKey(old), User: "admin", Role: RoleAdmin, Method: MethodPassword,
		Created: now, LastSeen: now, IdleExpires: now.Add(time.Hour), Expires: now.Add(24 * time.Hour)}}}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(storeFile(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run", sessionKey(old)), []byte("admin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs := captureLog(t)
	fresh := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if fresh.Validate(old) != nil || fresh.ValidateLegacy(old) != nil || len(fresh.Sessions("")) != 0 {
		t.Error("a session of the old store survived")
	}
	if !strings.Contains(logs.String(), "before the 128-bit session ids") {
		t.Errorf("log: %s", logs)
	}
	if doc := readStore(t, dir); doc.Version != sessionsVersion || len(doc.Sessions) != 0 {
		t.Errorf("the store after the start: %+v", doc)
	}
	if names := mirrorNames(t, filepath.Join(dir, "run")); len(names) != 0 {
		t.Errorf("mirror %v", names)
	}
	// a stored alias hash that is malformed, taken or the session's own drops that entry only
	sess := mustLogin(t, fresh, "admin", "secret123")
	alias, _ := fresh.LegacyID(sess.ID)
	kept := mustLogin(t, fresh, "admin", "secret123")
	doc = readStore(t, dir)
	for i := range doc.Sessions {
		if doc.Sessions[i].Hash == sessionKey(kept.ID) {
			doc.Sessions[i].LegacyHash = doc.Sessions[i].Hash
		}
	}
	b, _ = json.Marshal(doc)
	if err := os.WriteFile(storeFile(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	again := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
	if again.Validate(kept.ID) != nil {
		t.Error("an entry whose alias hash is its own hash was restored")
	}
	if again.Validate(sess.ID) == nil || again.ValidateLegacy(alias) == nil {
		t.Error("the good entry and its alias were dropped")
	}
}

// A reboot used to end every session, which also cleaned up after the console's `passwd`. Now the
// sessions of an account whose password is set outside the daemon, or which is removed there, end
// explicitly - with the daemon running and without it - and a role changed there is taken over.
func TestAccountsChangedElsewhere(t *testing.T) {
	now := time.Date(2026, 9, 12, 21, 0, 0, 0, time.UTC)
	prepare := func(t *testing.T) (string, *Store, *Session, *Session, *Session) {
		dir := t.TempDir()
		s := openAt(t, dir, filepath.Join(dir, "run"), &now, bothMethods)
		_ = s.Setup("admin", "secret123")
		if err := s.CreateUser("bob", "bobsecret1", RoleUser, false); err != nil {
			t.Fatal(err)
		}
		return dir, s, mustLogin(t, s, "admin", "secret123"), mustLogin(t, s, "bob", "bobsecret1"), mustLogin(t, s, "bob", "bobsecret1")
	}
	t.Run("passwd on the console while the daemon runs", func(t *testing.T) {
		dir, daemon, a1, b1, b2 := prepare(t)
		if err := ConsoleSetPassword(dir, "bob", "console-pw-1"); err != nil {
			t.Fatal(err)
		}
		if got := storedHashes(t, dir); strings.Join(got, ",") != sessionKey(a1.ID) {
			t.Errorf("the console left bob's sessions in the store: %v", got)
		}
		// bob's next request, not his next login
		if daemon.Validate(b1.ID) != nil || daemon.Validate(b2.ID) != nil {
			t.Error("bob's sessions outlived a password set on the console")
		}
		if daemon.Validate(a1.ID) == nil {
			t.Error("admin's session ended with bob's password")
		}
		if got := mirrorNames(t, filepath.Join(dir, "run")); strings.Join(got, ",") != sessionKey(a1.ID) {
			t.Errorf("mirror %v", got)
		}
		if _, err := daemon.Login("bob", "console-pw-1", "", ""); err != nil {
			t.Errorf("the new password: %v", err)
		}
		if fresh := openAt(t, dir, filepath.Join(dir, "run2"), &now, bothMethods); fresh.Validate(b1.ID) != nil || fresh.Validate(a1.ID) == nil {
			t.Error("after a restart")
		}
	})
	t.Run("passwd on the console while the daemon is down", func(t *testing.T) {
		dir, daemon, a1, b1, _ := prepare(t)
		daemon.Close()
		if err := ConsoleSetPassword(dir, "bob", "console-pw-1"); err != nil {
			t.Fatal(err)
		}
		fresh := openAt(t, dir, filepath.Join(dir, "run2"), &now, bothMethods)
		if fresh.Validate(b1.ID) != nil || fresh.Validate(a1.ID) == nil {
			t.Error("the next start restored bob's session after a console password")
		}
	})
	t.Run("passwd on the console without a session store", func(t *testing.T) {
		dir := t.TempDir()
		if err := ConsoleSetPassword(dir, "admin", "console-pw-1"); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "sessions")); !os.IsNotExist(err) {
			t.Errorf("the console created the store's directory: %v", err)
		}
	})
	t.Run("role changed and account removed by another writer", func(t *testing.T) {
		dir, daemon, a1, b1, _ := prepare(t)
		other, err := Open(dir, Options{}) // a second writer of users.json, without the session store
		if err != nil {
			t.Fatal(err)
		}
		if err := other.SetRole("bob", RoleAdmin); err != nil {
			t.Fatal(err)
		}
		if v := daemon.Validate(b1.ID); v == nil || v.Role != RoleAdmin {
			t.Fatalf("role from users.json: %+v", v)
		}
		for _, e := range readStore(t, dir).Sessions {
			if e.User == "bob" && e.Role != RoleAdmin {
				t.Errorf("stored role %s", e.Role)
			}
		}
		if err := other.DeleteUser("bob"); err != nil {
			t.Fatal(err)
		}
		if daemon.Validate(b1.ID) != nil {
			t.Error("the session of an account removed from users.json")
		}
		if got := storedHashes(t, dir); strings.Join(got, ",") != sessionKey(a1.ID) {
			t.Errorf("store %v", got)
		}
		// users.json gone altogether: setup again, and no session
		if err := os.Remove(filepath.Join(dir, "users.json")); err != nil {
			t.Fatal(err)
		}
		if daemon.Validate(a1.ID) != nil {
			t.Error("a session without any account")
		}
	})
	t.Run("a store of another version", func(t *testing.T) {
		dir, daemon, a1, _, _ := prepare(t)
		daemon.Close()
		doc := readStore(t, dir)
		doc.Version = sessionsVersion + 1
		b, _ := json.Marshal(doc)
		if err := os.WriteFile(storeFile(dir), b, 0o600); err != nil {
			t.Fatal(err)
		}
		logs := captureLog(t)
		if openAt(t, dir, filepath.Join(dir, "run2"), &now, bothMethods).Validate(a1.ID) != nil {
			t.Error("restored from a store of an unknown version")
		}
		if !strings.Contains(logs.String(), "version") {
			t.Errorf("log: %s", logs)
		}
	})
}

// The unit runs with UMask=0077 so that what the daemon creates is private by default; the two
// mirrors are the exception, opened by lighttpd's gate and by an addon's tclrega shim, and their
// files stay world-readable whatever the umask says.
func TestMirrorFilesWorldReadableUnderStrictUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	now := time.Date(2026, 9, 23, 18, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	mirror := filepath.Join(dir, "run")
	legacy := filepath.Join(dir, "legacy")
	s, err := Open(dir, Options{SessionDir: mirror, LegacyDir: legacy, SessionFile: storeFile(dir), RestoreMethods: bothMethods, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	sess := mustLogin(t, s, "admin", "secret123")
	st, err := os.Stat(filepath.Join(mirror, sessionKey(sess.ID)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mirror file mode %o, want 644", st.Mode().Perm())
	}
	alias, err := s.LegacyID(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(legacy)
	if err != nil || len(entries) != 1 || alias == "" {
		t.Fatalf("alias mirror: %v %d %q", err, len(entries), alias)
	}
	if info, _ := entries[0].Info(); info.Mode().Perm() != 0o644 {
		t.Errorf("alias mirror file mode %o, want 644", info.Mode().Perm())
	}
	if st, _ := os.Stat(storeFile(dir)); st.Mode().Perm() != 0o600 {
		t.Errorf("session store mode %o, want 600", st.Mode().Perm())
	}
}
