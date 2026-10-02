package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/argon2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionDir: filepath.Join(dir, "sessions"), LockAfter: 3})
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func TestSetupAndLogin(t *testing.T) {
	s, dir := open(t)
	if !s.SetupRequired() {
		t.Fatal("fresh store must require setup")
	}
	if _, err := s.Login("admin", "whatever1", "127.0.0.1", ""); err != ErrSetupRequired {
		t.Fatalf("login before setup: %v", err)
	}
	if err := s.Setup("Admin", "secret123"); err != ErrBadUsername {
		t.Fatalf("uppercase username must be refused: %v", err)
	}
	if err := s.Setup("admin", "short"); err != ErrWeakPassword {
		t.Fatalf("weak password: %v", err)
	}
	if err := s.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("admin2", "secret123"); err != ErrSetupDone {
		t.Fatalf("second setup: %v", err)
	}
	if _, err := s.Login("admin", "wrong-pw", "127.0.0.1", ""); err != ErrInvalidCredentials {
		t.Fatalf("wrong password: %v", err)
	}
	sess, err := s.Login("admin", "secret123", "127.0.0.1", "curl")
	if err != nil || !IsSessionID(sess.ID) || sess.Role != RoleAdmin {
		t.Fatalf("login: %v %+v", err, sess)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "sessions", sessionKey(sess.ID))); err != nil || string(b) != "admin\n" {
		t.Fatalf("session mirror: %v %q", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", sess.ID)); !os.IsNotExist(err) {
		t.Fatalf("a mirror file named by the id itself: %v", err)
	}
	if v := s.Validate(sess.ID); v == nil || v.User != "admin" {
		t.Fatal("validate")
	}
	if v := s.Validate("nope123456"); v != nil {
		t.Fatal("unknown sid must not validate")
	}
	s.Logout(sess.ID)
	if v := s.Validate(sess.ID); v != nil {
		t.Fatal("logged-out sid must not validate")
	}
	if _, err := os.Stat(filepath.Join(dir, "sessions", sessionKey(sess.ID))); !os.IsNotExist(err) {
		t.Fatal("mirror must be removed on logout")
	}
	// the hash is never listed
	if u := s.Users(); len(u) != 1 || u[0].Hash != "" || u[0].Role != RoleAdmin {
		t.Fatalf("users: %+v", u)
	}
	// users.json is private
	if st, _ := os.Stat(filepath.Join(dir, "users.json")); st.Mode().Perm() != 0o600 {
		t.Fatalf("users.json mode %o", st.Mode().Perm())
	}
}

func TestLockout(t *testing.T) {
	s, _ := open(t)
	_ = s.Setup("admin", "secret123")
	for i := 0; i < 3; i++ {
		_, _ = s.Login("admin", "wrong", "10.0.0.5", "")
	}
	if _, err := s.Login("admin", "secret123", "10.0.0.5", ""); err != ErrLockedOut {
		t.Fatalf("expected lockout, got %v", err)
	}
	// a different remote is locked too because the user is locked
	if _, err := s.Login("admin", "secret123", "10.0.0.6", ""); err != ErrLockedOut {
		t.Fatalf("user lock: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	now := time.Date(2026, 9, 6, 4, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, _ := Open(dir, Options{IdleTimeout: time.Hour, Now: func() time.Time { return now }})
	_ = s.Setup("admin", "secret123")
	sess, _ := s.Login("admin", "secret123", "", "")
	now = now.Add(30 * time.Minute)
	if s.Validate(sess.ID) == nil {
		t.Fatal("still valid at 30 min")
	}
	now = now.Add(2 * time.Hour)
	if s.Validate(sess.ID) != nil {
		t.Fatal("expired after idle timeout")
	}
}

func TestUsersRolesPasswords(t *testing.T) {
	s, dir := open(t)
	_ = s.Setup("admin", "secret123")
	if err := s.CreateUser("bob", "bobsecret1", RoleUser, true); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser("bob", "x", RoleUser, false); err != ErrDuplicateUser {
		t.Fatalf("duplicate: %v", err)
	}
	if !s.MustChangePassword("bob") {
		t.Fatal("must-change flag")
	}
	if err := s.DeleteUser("admin"); err != ErrLastAdmin {
		t.Fatalf("last admin: %v", err)
	}
	if err := s.SetRole("admin", RoleUser); err != ErrLastAdmin {
		t.Fatalf("demote last admin: %v", err)
	}
	_ = s.SetRole("bob", RoleAdmin)
	if err := s.DeleteUser("admin"); err != nil {
		t.Fatalf("delete admin with another admin present: %v", err)
	}
	wrong := "nope"
	if err := s.SetPassword("bob", "newsecret1", &wrong, ""); err != ErrInvalidCredentials {
		t.Fatalf("wrong current: %v", err)
	}
	cur := "bobsecret1"
	s1, _ := s.Login("bob", "bobsecret1", "a", "")
	s2, _ := s.Login("bob", "bobsecret1", "b", "")
	if err := s.SetPassword("bob", "newsecret1", &cur, s1.ID); err != nil {
		t.Fatal(err)
	}
	if s.Validate(s1.ID) == nil || s.Validate(s2.ID) != nil {
		t.Fatal("password change must keep the current session and end the others")
	}
	if s.MustChangePassword("bob") {
		t.Fatal("flag cleared after change")
	}
	// console reset while the daemon runs: users.json changes on disk, the store reloads
	time.Sleep(10 * time.Millisecond)
	if err := ConsoleSetPassword(dir, "bob", "console-reset1"); err != nil {
		t.Fatal(err)
	}
	// force an mtime difference on coarse filesystems
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(filepath.Join(dir, "users.json"), future, future)
	if _, err := s.Login("bob", "console-reset1", "c", ""); err != nil {
		t.Fatalf("login after console reset: %v", err)
	}
	if n := s.LogoutUser("bob", ""); n < 1 {
		t.Fatal("sign out everywhere")
	}
}

// task 19 (D-53, D-54): a provider login is the account of exactly that name, with the role it
// has here; no account, no session and nothing written. An account without a password signs in
// through the provider only, and a password session of it is never restored.
func TestProviderLoginMatchesAccounts(t *testing.T) {
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionDir: filepath.Join(dir, "run"), SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword, MethodOIDC}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Setup("admin", "secret123")
	before, _ := os.ReadFile(filepath.Join(dir, "users.json"))
	// unknown names, and the near misses of the exact match: nothing is created, the file is as it was
	for _, name := range []string{"alice", "Admin", "admin ", "admin@example.org", "", "ad\nmin"} {
		if _, err := s.LoginExternal("oidc", name, "10.0.0.2", "curl"); err != ErrUnknownUser {
			t.Errorf("%q: %v", name, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(dir, "users.json"))
	if string(before) != string(after) || len(s.Users()) != 1 || len(s.Sessions("")) != 0 {
		t.Fatalf("a refused provider login wrote something: %d users, %d sessions", len(s.Users()), len(s.Sessions("")))
	}
	// an account with a password signs in through the provider with its own role
	sess, err := s.LoginExternal("oidc", "admin", "10.0.0.2", "curl")
	if err != nil || sess.Role != RoleAdmin || sess.Method != MethodOIDC || sess.User != "admin" {
		t.Fatalf("admin via the provider: %v %+v", err, sess)
	}
	if u := s.Users()[0]; !u.PasswordSet || u.LastProviderLogin == nil || !u.LastProviderLogin.Equal(now) {
		t.Errorf("listing after a provider login: %+v", u)
	}
	// an account without a password: created for the provider, refused by the password login
	if err := s.CreateUserWithoutPassword("carol", RoleUser); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUserWithoutPassword("carol", RoleUser); err != ErrDuplicateUser {
		t.Errorf("duplicate: %v", err)
	}
	if err := s.CreateUserWithoutPassword("Carol", RoleUser); err != ErrBadUsername {
		t.Errorf("bad name: %v", err)
	}
	if _, err := s.Login("carol", "", "10.0.0.3", ""); err != ErrInvalidCredentials {
		t.Errorf("password login of a provider-only account: %v", err)
	}
	if _, err := s.Login("carol", "anything1", "10.0.0.3", ""); err != ErrInvalidCredentials {
		t.Errorf("password login of a provider-only account: %v", err)
	}
	carol, err := s.LoginExternal("oidc", "carol", "10.0.0.3", "")
	if err != nil || carol.Role != RoleUser {
		t.Fatalf("carol via the provider: %v %+v", err, carol)
	}
	list := s.Users()
	if len(list) != 2 || list[1].Name != "carol" || list[1].PasswordSet || list[1].Hash != "" || list[1].LastProviderLogin == nil {
		t.Errorf("listing: %+v", list)
	}
	// the role is the account's: a change here is what the next provider login gets
	_ = s.SetRole("carol", RoleAdmin)
	if again, err := s.LoginExternal("oidc", "carol", "", ""); err != nil || again.Role != RoleAdmin {
		t.Errorf("role after SetRole: %v %+v", err, again)
	}
	// she cannot change a password she does not have, an administrator can give her one
	cur := ""
	if err := s.SetPassword("carol", "carolsecret1", &cur, ""); err != ErrNoPassword {
		t.Errorf("own password change without one: %v", err)
	}
	if err := s.SetPassword("carol", "carolsecret1", nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("carol", "carolsecret1", "10.0.0.3", ""); err != nil {
		t.Errorf("password login after the reset: %v", err)
	}
	if !s.Users()[1].PasswordSet {
		t.Error("password_set after the reset")
	}
	if s.Validate(carol.ID) != nil {
		t.Error("the reset must end her sessions, as it does for any account")
	}
	carol, _ = s.LoginExternal("oidc", "carol", "10.0.0.3", "")
	// a password session of an account without a password is not restored (a hand-edited store)
	_ = s.CreateUserWithoutPassword("dave", RoleUser)
	dave, _ := s.LoginExternal("oidc", "dave", "", "")
	doc := readStore(t, dir)
	for i := range doc.Sessions {
		if doc.Sessions[i].Hash == sessionKey(dave.ID) {
			doc.Sessions[i].Method = MethodPassword
		}
	}
	b, _ := json.Marshal(doc)
	if err := os.WriteFile(storeFile(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}
	s.Close()
	fresh, err := Open(dir, Options{SessionDir: filepath.Join(dir, "run"), SessionFile: SessionStorePath(dir), RestoreMethods: []string{MethodPassword, MethodOIDC}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Validate(dave.ID) != nil {
		t.Error("a password session of an account without a password was restored")
	}
	if fresh.Validate(carol.ID) == nil || fresh.Validate(sess.ID) == nil {
		t.Error("the provider sessions were not restored")
	}
	// the fields of the first shape are dropped on the next save, the account stays
	old := `{"users": [{"name": "erin", "role": "user", "hash": "", "created": "2026-09-01T00:00:00Z", "external": "oidc", "subject": "u-9"}]}`
	odir := t.TempDir()
	if err := os.WriteFile(filepath.Join(odir, "users.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	o, err := Open(odir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if e, err := o.LoginExternal("oidc", "erin", "", ""); err != nil || e.Role != RoleUser {
		t.Errorf("an account of the first shape: %v %+v", err, e)
	}
	b, _ = os.ReadFile(filepath.Join(odir, "users.json"))
	if strings.Contains(string(b), "external") || strings.Contains(string(b), "subject") || !strings.Contains(string(b), `"erin"`) {
		t.Errorf("after the save:\n%s", b)
	}
}

func TestTokens(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("admin", "test-passw0rd-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToken("Bad Name", asRole(RoleUser)); err != ErrBadTokenName {
		t.Errorf("name: %v", err)
	}
	secret, err := s.CreateToken("hm2mqtt", asRole(RoleUser))
	if err != nil || !IsToken(secret) {
		t.Fatalf("create: %v %q", err, secret)
	}
	if _, err := s.CreateToken("hm2mqtt", asRole(RoleAdmin)); err != ErrDuplicateToken {
		t.Errorf("duplicate: %v", err)
	}
	sess := s.Validate(secret)
	if sess == nil || sess.Role != "" || !sess.Has(ScopeLogsRead) || sess.Has(ScopeMetaWrite) || sess.User != "token:hm2mqtt" {
		t.Fatalf("validate: %+v", sess)
	}
	if s.Validate(TokenPrefix+"00000000000000000000000000000000") != nil {
		t.Error("unknown token accepted")
	}
	list := s.Tokens()
	if len(list) != 1 || list[0].Hash != "" || list[0].Prefix != secret[4:12] || list[0].LastUsed == nil {
		t.Errorf("list: %+v", list)
	}
	// the file survives a reopen and never contains the secret
	b, _ := os.ReadFile(filepath.Join(dir, "users.json"))
	if strings.Contains(string(b), secret) {
		t.Error("secret stored in clear")
	}
	s2, _ := Open(dir, Options{})
	if s2.Validate(secret) == nil {
		t.Error("token lost on reopen")
	}
	if err := s2.DeleteToken("hm2mqtt"); err != nil {
		t.Fatal(err)
	}
	if s2.Validate(secret) != nil || s2.DeleteToken("hm2mqtt") != ErrUnknownToken {
		t.Error("delete")
	}

	// the local token: provisioned once, regenerated when the file goes missing
	path := filepath.Join(dir, "local-token")
	if err := s2.EnsureLocalToken(path); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	first := strings.TrimSpace(string(b))
	if sess := s2.Validate(first); sess == nil || sess.User != "token:local" || !sess.Has(ScopeMetaRead) || sess.Has(ScopeSystemRead) {
		t.Fatalf("local: %+v", sess)
	}
	if err := s2.EnsureLocalToken(path); err != nil {
		t.Fatal(err)
	}
	if b, _ = os.ReadFile(path); strings.TrimSpace(string(b)) != first {
		t.Error("stable token rewritten")
	}
	os.Remove(path)
	if err := s2.EnsureLocalToken(path); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if second := strings.TrimSpace(string(b)); second == first || s2.Validate(first) != nil || s2.Validate(second) == nil {
		t.Error("regeneration")
	}
	if n := len(s2.Tokens()); n != 1 {
		t.Errorf("local token duplicated: %d", n)
	}
}

func TestPasswordHashFormats(t *testing.T) {
	h, err := HashPassword("test-passw0rd-1")
	if err != nil || !strings.HasPrefix(h, "argon2id$m=32768,t=3,p=1$") {
		t.Fatalf("%v %q", err, h)
	}
	if !verifyPassword(h, "test-passw0rd-1") || verifyPassword(h, "test-passw0rd-2") {
		t.Error("verify")
	}
	// a hash written before the parameters were recorded: 64 MiB, t=3, p=2
	salt := []byte("0123456789abcdef")
	legacyKey := argon2.IDKey([]byte("oldpass99"), salt, 3, 64*1024, 2, 32)
	legacy := "argon2id$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(legacyKey)
	if !verifyPassword(legacy, "oldpass99") || verifyPassword(legacy, "oldpass98") {
		t.Error("legacy verify")
	}
	for _, bad := range []string{"", "md5$x$y", "argon2id$m=0,t=3,p=1$x$y", "argon2id$m=99999999,t=3,p=1$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(legacyKey)} {
		if verifyPassword(bad, "whatever") {
			t.Errorf("accepted %q", bad)
		}
	}
}

// A token made on the console while the daemon runs: refused before the first user exists,
// found by the daemon afterwards - also when the file's time went backwards - and never lost to
// the daemon's own saves.
func TestConsoleTokenWhileDaemonRuns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	daemon, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// the daemon provisions its local token before anyone has set the box up
	if err := daemon.EnsureLocalToken(filepath.Join(dir, "local-token")); err != nil {
		t.Fatal(err)
	}
	if _, err := ConsoleCreateToken(dir, "charly-setup", asRole(RoleAdmin)); err != ErrSetupRequired {
		t.Fatalf("before setup: %v", err)
	}
	if n := len(daemon.Tokens()); n != 1 {
		t.Fatalf("the refused command changed the tokens: %d", n)
	}
	if err := daemon.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}

	secret, err := ConsoleCreateToken(dir, "charly-setup", asRole(RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	// a clock set back: the console's file is older than the one the daemon last saw
	past := time.Now().Add(-24 * time.Hour)
	_ = os.Chtimes(path, past, past)
	if sess := daemon.Validate(secret); sess == nil || !sess.Scopes.Full() || sess.User != "token:charly-setup" {
		t.Fatalf("the daemon does not know the console token: %+v", sess)
	}
	// the daemon saves (a token through the API, then a user), the console adds a second token
	// in between; all of it survives, in the daemon and in the file
	if _, err := daemon.CreateToken("api", asRole(RoleUser)); err != nil {
		t.Fatal(err)
	}
	secret2, err := ConsoleCreateToken(dir, "charly-setup2", asRole(RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.CreateUser("bob", "bobsecret1", RoleUser, false); err != nil {
		t.Fatal(err)
	}
	if err := daemon.DeleteToken("api"); err != nil {
		t.Fatal(err)
	}
	fresh, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Store{daemon, fresh} {
		if tokenNames(s) != "charly-setup,charly-setup2,local" || s.Validate(secret) == nil || s.Validate(secret2) == nil || len(s.Users()) != 2 {
			t.Errorf("tokens %s, users %+v", tokenNames(s), s.Users())
		}
	}
}

func tokenNames(s *Store) string {
	var names []string
	for _, t := range s.Tokens() {
		names = append(names, t.Name)
	}
	return strings.Join(names, ",")
}

// save merges what another writer put into users.json between the store's last look and its
// write - here within one mutation, which reload alone cannot cover.
func TestSaveMergesAnotherWriter(t *testing.T) {
	dir := t.TempDir()
	daemon, _ := Open(dir, Options{})
	if err := daemon.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	gone, _ := daemon.CreateToken("gone", asRole(RoleUser))
	console, _ := Open(dir, Options{})

	daemon.mu.Lock()
	// the daemon has reloaded and is busy with its mutation when the console writes
	consoleSecret, err := console.CreateToken("console", asRole(RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	if err := console.CreateUser("carol", "carolsecret1", RoleUser, false); err != nil {
		t.Fatal(err)
	}
	daemon.tokens = nil // the daemon removes "gone"
	mine, err := daemon.createTokenLocked("daemon", asRole(RoleUser))
	daemon.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := Open(dir, Options{})
	for name, s := range map[string]*Store{"daemon": daemon, "file": fresh} {
		if got := tokenNames(s); got != "console,daemon" {
			t.Errorf("%s: tokens %s", name, got)
		}
		if s.Validate(consoleSecret) == nil || s.Validate(mine) == nil || s.Validate(gone) != nil {
			t.Errorf("%s: validation", name)
		}
		if u := s.Users(); len(u) != 2 || u[1].Name != "carol" {
			t.Errorf("%s: users %+v", name, u)
		}
	}
}

// The merge rule, record by record, for users and tokens alike: what this store changed since it
// last synced stands, the rest comes from the file.
func TestMerge(t *testing.T) {
	// "name:role" entries
	build := func(spec string) (map[string]*User, []*Token) {
		users := map[string]*User{}
		var tokens []*Token
		for _, e := range strings.Fields(spec) {
			name, role, _ := strings.Cut(e, ":")
			users[name] = &User{Name: name, Role: Role(role), Hash: "h"}
			tokens = append(tokens, &Token{Name: name, Scopes: RoleScopes(Role(role)), Hash: "h"})
		}
		return users, tokens
	}
	for _, tc := range []struct {
		name, base, disk, mem, want string
	}{
		{"nothing changed", "a:user", "a:user", "a:user", "a:user"},
		{"added on disk", "a:user", "a:user b:user", "a:user", "a:user b:user"},
		{"added here", "a:user", "a:user", "a:user c:user", "a:user c:user"},
		{"added on both sides", "a:user", "a:user b:user", "a:user c:user", "a:user b:user c:user"},
		{"removed here", "a:user b:user", "a:user b:user", "a:user", "a:user"},
		{"removed here, another added on disk", "a:user b:user", "a:user b:user c:admin", "a:user", "a:user c:admin"},
		{"removed on disk", "a:user b:user", "a:user", "a:user b:user", "a:user"},
		{"changed on disk", "a:user", "a:admin", "a:user", "a:admin"},
		{"changed here", "a:user", "a:user", "a:admin", "a:admin"},
		{"changed on both sides: this store wins", "a:user b:user", "a:admin b:user", "a:user b:admin", "a:admin b:admin"},
		{"removed here, changed on disk", "a:user b:user", "a:user b:admin", "a:user", "a:user"},
		{"no file before (all of memory is new)", "", "d:user", "a:user", "a:user d:user"},
	} {
		s := &Store{}
		s.users, s.tokens = build(tc.base)
		s.synced(nil)
		s.users, s.tokens = build(tc.mem)
		du, dt := build(tc.disk)
		doc := usersDoc{Tokens: dt}
		for _, u := range du {
			doc.Users = append(doc.Users, u)
		}
		s.merge(doc)
		var users, tokens []string
		for _, u := range s.users {
			users = append(users, u.Name+":"+string(u.Role))
		}
		sort.Strings(users)
		for _, t := range s.tokens {
			tokens = append(tokens, t.Name+":"+roleOf(t.Scopes))
		}
		if got := strings.Join(users, " "); got != tc.want {
			t.Errorf("%s: users %q, want %q", tc.name, got, tc.want)
		}
		if got := strings.Join(tokens, " "); got != tc.want {
			t.Errorf("%s: tokens %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A users.json the daemon cannot read is not overwritten by its next save.
func TestSaveKeepsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 0")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	daemon, _ := Open(dir, Options{})
	_ = daemon.Setup("admin", "secret123")
	secret, err := ConsoleCreateToken(dir, "console", asRole(RoleAdmin))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if daemon.Validate(secret) != nil {
		t.Fatal("a token from an unreadable file")
	}
	if _, err := daemon.CreateToken("daemon", asRole(RoleUser)); err == nil {
		t.Fatal("the daemon overwrote a file it cannot read")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if daemon.Validate(secret) == nil {
		t.Fatal("the token is not found once the file is readable")
	}
	if _, err := daemon.CreateToken("daemon", asRole(RoleUser)); err != nil {
		t.Fatal(err)
	}
	if fresh, _ := Open(dir, Options{}); tokenNames(fresh) != "console,daemon" {
		t.Errorf("tokens %s", tokenNames(fresh))
	}
}

// Written as root (the console), users.json belongs to the state directory's owner (the
// daemon's user), so the daemon can still read it.
func TestConsoleWriteKeepsOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir := t.TempDir()
	if err := os.Chown(dir, 4242, 4243); err != nil {
		t.Fatal(err)
	}
	if err := ConsoleSetPassword(dir, "admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	if sys := st.Sys().(*syscall.Stat_t); sys.Uid != 4242 || sys.Gid != 4243 {
		t.Errorf("owner %d:%d", sys.Uid, sys.Gid)
	}
}

// B-233: a login for an unknown account or one without a password runs the same argon2 as a wrong
// password, so the answer's time does not tell which names exist.
func TestLoginUnknownAccountCostsAHash(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Options{SessionDir: filepath.Join(dir, "sessions"), LockAfter: 100}) // no lockout's fast path in the timing
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUserWithoutPassword("carol", RoleUser); err != nil {
		t.Fatal(err)
	}
	// every refusal - a wrong password, an unknown name, an account without a password - is exactly
	// one argon2 run of the current parameters (B-36: this replaces a wall-clock comparison that
	// failed on a busy CI runner; the work done, not the time it took, is what the test can see)
	want := argonCost{t: argonTime, m: argonMemory, p: argonThreads, n: argonKeyLen}
	for _, name := range []string{"admin", "nobody", "carol"} {
		before := hashes.Load()
		lastCost.Store(argonCost{})
		if _, err := s.Login(name, "wrong-pw1", "192.0.2."+name[:1], ""); err != ErrInvalidCredentials {
			t.Fatalf("%s: %v", name, err)
		}
		if n := hashes.Load() - before; n != 1 {
			t.Errorf("%s: %d argon2 runs for a refusal, want 1", name, n)
		}
		if got, _ := lastCost.Load().(argonCost); got != want {
			t.Errorf("%s: the refusal's argon2 run cost %+v, want %+v", name, got, want)
		}
	}
	// the dummy hash has the current parameters and parses: its run costs what a real one does
	if !strings.HasPrefix(dummyHash, fmt.Sprintf("argon2id$m=%d,t=%d,p=%d$", argonMemory, argonTime, argonThreads)) {
		t.Errorf("dummy hash parameters: %s", dummyHash)
	}
}
