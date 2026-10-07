package auth

import "testing"

// occulited task 22: the console's credential - an ephemeral token for `occulited update`, with
// the update's scopes, accepted from the loopback alone (behind lighttpd a request carries the
// browser's address), and minted anew at every start.
func TestConsoleToken(t *testing.T) {
	s, err := Open(t.TempDir(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.MintConsoleToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		remote string
		ok     bool
	}{{"127.0.0.1", true}, {"::1", true}, {"192.168.1.20", false}, {"", false}} {
		sess := s.ValidateFrom(secret, c.remote)
		if (sess != nil) != c.ok {
			t.Errorf("from %q: %+v, want accepted %v", c.remote, sess, c.ok)
			continue
		}
		if sess != nil && (sess.User != "token:console" || !sess.Scopes.Has(ScopePower) || !sess.Scopes.Has(ScopeBackup) || !sess.Scopes.Has(ScopeSystemRead) || sess.Scopes.Has(ScopeSystemWrite)) {
			t.Errorf("from %q: %+v", c.remote, sess)
		}
	}
	// a new start: the old secret is gone
	again, err := s.MintConsoleToken()
	if err != nil || again == secret || s.ValidateFrom(secret, "127.0.0.1") != nil || s.ValidateFrom(again, "127.0.0.1") == nil {
		t.Errorf("re-mint: %v", err)
	}
}
