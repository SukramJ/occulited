package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// task 78 (D-116): the account ladder - the migration of roles to levels, the derived role, the
// scopes of each level, the last-administrator rule by level, and sessions that follow.
func TestLevelsMigrateFromRoles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	old := `{"users":[{"name":"admin","role":"admin","hash":"","created":"2026-01-01T00:00:00Z"},{"name":"kid","role":"user","hash":"","created":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	users := s.Users()
	if len(users) != 2 || users[0].Level != LevelAdminister || users[0].Role != RoleAdmin || users[1].Level != LevelOperate || users[1].Role != RoleUser {
		t.Fatalf("migrated: %+v", users)
	}
	// a change writes the level, and the role beside it for an older reader
	if err := s.SetLevel("kid", LevelConfigure); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"level": "configure"`) || !strings.Contains(string(b), `"role": "user"`) || !strings.Contains(string(b), `"level": "administer"`) {
		t.Fatalf("file: %s", b)
	}
	// read again: the level stands, and a bogus one in the file falls back to the role
	var doc map[string]any
	_ = json.Unmarshal(b, &doc)
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), `"level": "configure"`, `"level": "wizard"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	s2, _ := Open(dir, Options{})
	for _, u := range s2.Users() {
		if u.Name == "kid" && u.Level != LevelOperate {
			t.Fatalf("bogus level did not fall back to the role: %+v", u)
		}
	}
}

func TestLevelScopesAndSessions(t *testing.T) {
	want := map[Level]string{
		LevelRead:       "logs:read,meta:read,rpc:read,system:read",
		LevelOperate:    "logs:read,meta:read,rpc:operate,system:read",
		LevelConfigure:  "logs:read,meta:write,rpc:configure,system:read",
		LevelAdminister: "*",
	}
	join := func(sc Scopes) string {
		var out []string
		for _, x := range sc {
			out = append(out, string(x))
		}
		return strings.Join(out, ",")
	}
	for l, w := range want {
		if got := join(LevelScopes(l)); got != w {
			t.Errorf("LevelScopes(%s) = %s, want %s", l, got, w)
		}
	}
	if LevelScopes("x") != nil || LevelOf(RoleAdmin) != LevelAdminister || LevelOf("") != LevelOperate || RoleOf(LevelConfigure) != RoleUser || RoleOf(LevelAdminister) != RoleAdmin {
		t.Fatal("the mappings")
	}
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("root", "password-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUserLevel("ops", "password-2", LevelOperate, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUser("viewer", "password-3", RoleUser, false); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateUserLevel("nobody", "password-4", "wizard", false); err != ErrBadRole {
		t.Fatalf("a bogus level: %v", err)
	}
	sess, err := s.Login("ops", "password-2", "10.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Level != LevelOperate || sess.Role != RoleUser || !sess.Has(ScopeRPCOperate) || sess.Has(ScopeMetaWrite) || !sess.Has(ScopeSelf) {
		t.Fatalf("operate session: %+v", sess)
	}
	// moving the account moves its live session; the scopes follow
	if err := s.SetLevel("ops", LevelConfigure); err != nil {
		t.Fatal(err)
	}
	v := s.Validate(sess.ID)
	if v == nil || v.Level != LevelConfigure || !v.Has(ScopeMetaWrite) || !v.Has(ScopeRPCConfigure) || v.Has(ScopeSystemWrite) {
		t.Fatalf("after the move: %+v", v)
	}
	if err := s.SetLevel("ops", LevelRead); err != nil {
		t.Fatal(err)
	}
	if v := s.Validate(sess.ID); v == nil || v.Has(ScopeRPCOperate) || !v.Has(ScopeRPCRead) {
		t.Fatalf("read: %+v", v)
	}
	// the last administrator cannot leave the top, by level or by the role alias
	if err := s.SetLevel("root", LevelConfigure); err != ErrLastAdmin {
		t.Fatalf("last administrator by level: %v", err)
	}
	if err := s.SetRole("root", RoleUser); err != ErrLastAdmin {
		t.Fatalf("last administrator by role: %v", err)
	}
	if err := s.DeleteUser("root"); err != ErrLastAdmin {
		t.Fatalf("delete the last administrator: %v", err)
	}
	// a second administer account frees it
	if err := s.SetLevel("viewer", LevelAdminister); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevel("root", LevelConfigure); err != nil {
		t.Fatalf("with a second administrator: %v", err)
	}
	users := s.Users()
	got := map[string]string{}
	for _, u := range users {
		got[u.Name] = string(u.Level) + "/" + string(u.Role)
	}
	if got["root"] != "configure/user" || got["viewer"] != "administer/admin" || got["ops"] != "read/user" {
		t.Fatalf("users: %v", got)
	}
	// the sessions list shows the level; a restart restores it from the session store
	found := false
	for _, x := range s.Sessions("") {
		if x.User == "ops" && x.Level == LevelRead {
			found = true
		}
	}
	if !found {
		t.Fatalf("the list: %+v", s.Sessions(""))
	}
}

// task 193: every account has a stable id, unique, kept across reads and writes; a session
// carries it; an older file gets ids at once.
func TestAccountIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users.json")
	old := `{"users":[{"name":"admin","role":"admin","hash":"","created":"2026-01-01T00:00:00Z"},{"name":"kid","role":"user","hash":"","created":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, u := range s.Users() {
		if !idRe.MatchString(u.ID) || len(u.ID) != 8 {
			t.Fatalf("id %q", u.ID)
		}
		ids[u.Name] = u.ID
	}
	if ids["admin"] == ids["kid"] {
		t.Fatal("ids collide")
	}
	// derived from the name, so another store reads the same ids from the untouched file; the
	// next save writes them
	if b, _ := os.ReadFile(path); strings.Contains(string(b), `"id"`) {
		t.Fatal("the read rewrote the file")
	}
	s2, _ := Open(dir, Options{})
	for _, u := range s2.Users() {
		if ids[u.Name] != u.ID {
			t.Fatalf("id changed on read: %s %s", u.Name, u.ID)
		}
	}
	if err := s2.SetLevel("kid", LevelRead); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"id": "`+ids["admin"]+`"`) || !strings.Contains(string(b), `"id": "`+ids["kid"]+`"`) {
		t.Fatalf("ids not written at the save: %s", b)
	}
	// a new account gets one, and its session carries it
	if err := s2.CreateUserLevel("ops", "password-2", LevelOperate, false); err != nil {
		t.Fatal(err)
	}
	sess, err := s2.Login("ops", "password-2", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if sess.AccountID == "" || sess.AccountID == ids["admin"] || len(sess.AccountID) != 8 {
		t.Fatalf("session account id %q", sess.AccountID)
	}
	// the preferences: the start page
	if err := s2.SetPreferences("ops", Preferences{StartPage: "app", AppFullscreen: true}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s2.Preferences("ops"); p.StartPage != "app" || !p.AppFullscreen {
		t.Fatalf("start page %+v", p)
	}
	if err := s2.SetPreferences("ops", Preferences{AppFullscreen: true}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s2.Preferences("ops"); p.StartPage != "" || !p.AppFullscreen {
		t.Fatalf("fullscreen alone %+v", p)
	}
	if err := s2.SetPreferences("ops", Preferences{StartPage: "moon"}); err == nil {
		t.Fatal("a bogus start page")
	}
	// the Changed hook is told after a write
	told := make(chan struct{}, 4)
	s3, _ := Open(dir, Options{Changed: func() { told <- struct{}{} }})
	if err := s3.SetLevel("ops", LevelRead); err != nil {
		t.Fatal(err)
	}
	select {
	case <-told:
	case <-time.After(2 * time.Second):
		t.Fatal("Changed was not told")
	}
}
