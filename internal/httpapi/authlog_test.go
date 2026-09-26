package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// B-231: logins, refused logins and lockouts are in the journal at the default level, with the
// name as given, the address and the method, never the password or a token.
func TestAuthAuditLines(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{SessionDir: filepath.Join(dir, "s"), LockAfter: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUserWithoutPassword("carol", auth.RoleUser); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	mux := http.NewServeMux()
	a := &AuthAPI{Store: store, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	a.Register(mux)
	srv := httptest.NewServer(a.Middleware(mux))
	defer srv.Close()

	line := func(t *testing.T, want ...string) string {
		t.Helper()
		for _, l := range strings.Split(logs.String(), "\n") {
			ok := true
			for _, w := range want {
				ok = ok && strings.Contains(l, w)
			}
			if ok {
				return l
			}
		}
		t.Errorf("no line with %q in:\n%s", want, logs.String())
		return ""
	}
	for _, c := range []struct {
		name, body string
		status     int
		want       []string
	}{
		{"login", `{"username":"admin","password":"secret123"}`, 200, []string{"level=INFO", `msg="auth: login"`, "user=admin", "method=password", "remote=127.0.0.1"}},
		{"wrong password", `{"username":"admin","password":"wrong-pw-1"}`, 401, []string{"level=WARN", `msg="auth: login refused"`, "user=admin", "method=password", "remote=127.0.0.1", `reason="wrong password"`}},
		{"unknown user", `{"username":"mallory","password":"wrong-pw-1"}`, 401, []string{"level=WARN", "user=mallory", `reason="unknown user"`}},
		{"no password", `{"username":"carol","password":"wrong-pw-1"}`, 401, []string{"level=WARN", "user=carol", `reason="no password"`}},
		{"control characters", `{"username":"ev\nil\u001b[31m` + strings.Repeat("x", 100) + `","password":"wrong-pw-1"}`, 401, []string{"level=WARN", "user=ev?il?[31mxxx", "…"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs.Reset()
			if st, out, _ := do(t, srv, "POST", "/api/auth/v1/login", c.body, nil); st != c.status {
				t.Fatalf("%d %v", st, out)
			}
			line(t, c.want...)
			if strings.Contains(logs.String(), "secret123") || strings.Contains(logs.String(), "wrong-pw-1") {
				t.Errorf("a password in the log: %s", logs.String())
			}
		})
	}
	// the fifth failure from the address (four above, one of "mallory2") starts a lockout of the
	// address: one Warn; the attempts refused while it holds are Debug
	logs.Reset()
	for i := 0; i < 2; i++ {
		do(t, srv, "POST", "/api/auth/v1/login", `{"username":"mallory2","password":"wrong-pw-1"}`, nil)
	}
	line(t, "level=WARN", `msg="auth: login locked after repeated failures"`, "remote=127.0.0.1", "until=")
	if strings.Count(logs.String(), "login locked") != 1 {
		t.Errorf("lockout announced more than once: %s", logs.String())
	}
	logs.Reset()
	if st, _, _ := do(t, srv, "POST", "/api/auth/v1/login", `{"username":"admin","password":"secret123"}`, nil); st != 429 {
		t.Fatalf("locked: %d", st)
	}
	line(t, "level=DEBUG", `msg="auth: login refused"`, "reason=locked")
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a refusal inside the lockout at Warn: %s", logs.String())
	}
	// a refused token: Warn, without the token
	logs.Reset()
	tok := "olt_" + strings.Repeat("a", 32)
	if !auth.IsToken(tok) {
		t.Fatal("the token shape changed")
	}
	do(t, srv, "GET", "/api/auth/v1/users", "", map[string]string{"Authorization": "Bearer " + tok})
	line(t, "level=WARN", "method=token", "remote=127.0.0.1")
	if strings.Contains(logs.String(), tok) {
		t.Errorf("the token in the log: %s", logs.String())
	}
	// a stale session cookie is no refused login
	logs.Reset()
	do(t, srv, "GET", "/api/auth/v1/users", "", map[string]string{"Cookie": CookieName + "=" + strings.Repeat("A", 26)})
	if strings.Contains(logs.String(), "refused") {
		t.Errorf("a stale cookie logged as a refused login: %s", logs.String())
	}
}

// the flood limit: beyond refusedPerMinute Warn lines a minute, one notice and then Debug
func TestRefusedLimit(t *testing.T) {
	var l refusedLimit
	now := time.Unix(1000, 0)
	for i := 1; i <= refusedPerMinute; i++ {
		if ok, first := l.allow(now); !ok || first {
			t.Fatalf("%d: %v %v", i, ok, first)
		}
	}
	if ok, first := l.allow(now.Add(time.Second)); ok || !first {
		t.Fatalf("over the limit: %v %v", ok, first)
	}
	if ok, first := l.allow(now.Add(2 * time.Second)); ok || first {
		t.Fatalf("further: %v %v", ok, first)
	}
	if ok, _ := l.allow(now.Add(time.Minute)); !ok {
		t.Fatal("the next minute")
	}
}
