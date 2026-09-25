package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// The route table (task 66, D-85): every route of /api/ is registered through route, with the
// scope it needs, and the middleware refuses a request whose session lacks that scope with a 403
// that names it. The default is deny: a route the table does not know is not reached by anybody
// - which scopes_test.go turns into a failing test, so it never ships that way - and a pattern
// registered twice with different scopes is a bug found at start-up (a panic).
//
// One scope per route. GET /led/state is the one exception with a second: the status LED's
// state belongs to the scope led (Home Assistant's token reads what it sets) and is a system
// read like the LED's configuration (every account's LED page loads it); the first is the
// route's own, named in a 403.
//
// scopeOpen marks the routes that need no session at all: feature detection, health, the auth
// flow itself. open() is the same list by path, for the middleware's first look and for what is
// registered outside this package (the HTTP-01 challenge, the health route in main).

// scopeOpen is the table's mark for a route without a session.
const scopeOpen auth.Scope = ""

var (
	routesMu    sync.Mutex
	routeScopes = map[string][]auth.Scope{}
)

// route registers pattern on mux with the scope it needs (and, where a route belongs to two,
// the other one after it). The pattern is the mux's own form, "METHOD /path".
func route(mux *http.ServeMux, scope auth.Scope, pattern string, h func(http.ResponseWriter, *http.Request), also ...auth.Scope) {
	scopes := append([]auth.Scope{scope}, also...)
	routesMu.Lock()
	if have, ok := routeScopes[pattern]; ok && fmt.Sprint(have) != fmt.Sprint(scopes) {
		routesMu.Unlock()
		panic(fmt.Sprintf("httpapi: route %q registered with scopes %v and %v", pattern, have, scopes))
	}
	routeScopes[pattern] = scopes
	routesMu.Unlock()
	if (scope == scopeOpen) != open(strings.TrimPrefix(pattern, methodOf(pattern)+" ")) {
		panic(fmt.Sprintf("httpapi: route %q: the table and open() disagree", pattern))
	}
	mux.HandleFunc(pattern, h)
}

func methodOf(pattern string) string {
	m, _, _ := strings.Cut(pattern, " ")
	return m
}

// scopesOf answers what the route that will serve r needs: its scopes, its pattern, and
// whether the mux has such a route at all (a 404, a 405 or a redirect has none - the mux
// answers those itself, after the session check).
func scopesOf(mux *http.ServeMux, r *http.Request) ([]auth.Scope, string, bool) {
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return nil, "", false
	}
	routesMu.Lock()
	defer routesMu.Unlock()
	scopes, ok := routeScopes[pattern]
	return scopes, pattern, ok
}

// allowed reports whether the session holds one of the route's scopes.
func allowed(sess *auth.Session, scopes []auth.Scope) bool {
	for _, s := range scopes {
		if s == scopeOpen || sess.Has(s) {
			return true
		}
	}
	return false
}

// forbiddenScope is the 403 of a session that lacks the scope: the answer names it, so a program
// can tell its operator which scope its token needs.
func forbiddenScope(w http.ResponseWriter, scope auth.Scope) {
	writeJSON(w, http.StatusForbidden, apiError{Error: "forbidden", Message: "the scope " + string(scope) + " is required", Scope: string(scope)})
}

// RouteScopes is the table as the tests and the documentation read it: pattern → scopes.
func RouteScopes() map[string][]auth.Scope {
	routesMu.Lock()
	defer routesMu.Unlock()
	out := make(map[string][]auth.Scope, len(routeScopes))
	for k, v := range routeScopes {
		out[k] = append([]auth.Scope(nil), v...)
	}
	return out
}
