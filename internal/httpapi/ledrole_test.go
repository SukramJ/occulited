package httpapi

import (
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// A token with the scope led (task 95's role, task 66's scope) reaches the status LED's state,
// an override and locate and a 403 everywhere else - the addon pages included; a token with the
// read scopes reads the state and changes nothing. The LED routes answer 501 here (no
// controller), which is past the middleware and all that matters.
func TestLEDScopeRoutes(t *testing.T) {
	srv, store := fullAPI(t)
	ledTok, err := store.CreateToken("hass", auth.TokenOptions{Scopes: auth.Scopes{auth.ScopeLED}})
	if err != nil {
		t.Fatal(err)
	}
	userTok, err := store.CreateToken("reader", auth.TokenOptions{Scopes: auth.RoleScopes(auth.RoleUser)})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path string
		want         int
		scope        string
	}{
		{"GET", "/api/system/v1/led/state", 501, ""},
		{"POST", "/api/system/v1/led/override", 501, ""},
		{"DELETE", "/api/system/v1/led/override", 501, ""},
		{"DELETE", "/api/system/v1/led/override/node-red:abc", 501, ""},
		{"POST", "/api/system/v1/led/locate", 501, ""},
		{"DELETE", "/api/system/v1/led/locate", 501, ""},
		{"GET", "/api/system/v1/led", 403, "system:read"},
		{"PUT", "/api/system/v1/led", 403, "system:write"},
		{"POST", "/api/system/v1/led/preview", 403, "system:write"},
		{"DELETE", "/api/system/v1/led/overrides", 403, "system:write"},
		{"GET", "/api/system/v1/status", 403, "system:read"},
		{"GET", "/api/meta/v1/snapshot", 403, "meta:read"},
		{"POST", "/api/auth/v1/password", 403, "self"},
		{"GET", "/api/auth/v1/sessions", 403, "self"},
		{"GET", "/api/auth/v1/state", 200, ""},
	} {
		if st, scope := callScoped(t, srv, c.method, c.path, ledTok); st != c.want || scope != c.scope {
			t.Errorf("led token %s %s: %d %q, want %d %q", c.method, c.path, st, scope, c.want, c.scope)
		}
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/system/v1/led/state", 501},
		{"GET", "/api/system/v1/led", 501},
		{"POST", "/api/system/v1/led/override", 403},
		{"DELETE", "/api/system/v1/led/override", 403},
		{"POST", "/api/system/v1/led/locate", 403},
	} {
		if st, _ := callScoped(t, srv, c.method, c.path, userTok); st != c.want {
			t.Errorf("user token %s %s: %d, want %d", c.method, c.path, st, c.want)
		}
	}
}

// The role led is nobody's account role; as a token's it is the alias of the scope led.
func TestLEDRoleNotForUsers(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateUser("flow", "secret123", auth.RoleLED, false); err == nil {
		t.Error("a user account with the role led was created")
	}
	if err := store.CreateUser("monitor", "secret123", auth.RoleUser, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRole("monitor", auth.RoleLED); err == nil {
		t.Error("a user account was given the role led")
	}
	if err := store.SetRole("monitor", auth.Role("root")); err == nil {
		t.Error("an unknown role was set")
	}
	secret, err := store.CreateToken("hass", auth.TokenOptions{Scopes: auth.RoleScopes(auth.RoleLED)})
	if err != nil {
		t.Fatal(err)
	}
	if s := store.Validate(secret); s == nil || s.Role != "" || !s.Has(auth.ScopeLED) || s.Has(auth.ScopeSystemRead) {
		t.Errorf("token session: %+v", s)
	}
	if _, err := store.CreateToken("bad", auth.TokenOptions{Scopes: auth.RoleScopes(auth.Role("root"))}); err == nil {
		t.Error("a token with an unknown role was created")
	}
}
