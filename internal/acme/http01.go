package acme

import (
	"net/http"
	"strings"
	"sync"
)

// ChallengePrefix is the path lighttpd hands to occulited for HTTP-01 (deploy/lighttpd/
// occulited.conf) and the one open route outside /api: it answers only the tokens of an order in
// flight, with the key authorisation the CA expects, and 404 for everything else.
const ChallengePrefix = "/.well-known/acme-challenge/"

// TokenStore is lego's challenge.Provider for HTTP-01 and the handler that serves it. Present
// puts a token in, CleanUp takes it out; between the two the CA fetches it through lighttpd.
type TokenStore struct {
	mu     sync.Mutex
	tokens map[string]string
}

// Present stores the key authorisation for token (challenge.Provider).
func (t *TokenStore) Present(_, token, keyAuth string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tokens == nil {
		t.tokens = map[string]string{}
	}
	t.tokens[token] = keyAuth
	return nil
}

// CleanUp forgets token (challenge.Provider).
func (t *TokenStore) CleanUp(_, token, _ string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tokens, token)
	return nil
}

// Pending is how many tokens are in flight (the status answer).
func (t *TokenStore) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.tokens)
}

// ServeHTTP answers GET /.well-known/acme-challenge/<token>.
func (t *TokenStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, ChallengePrefix)
	if token == "" || strings.ContainsAny(token, "/.") {
		http.NotFound(w, r)
		return
	}
	t.mu.Lock()
	keyAuth, ok := t.tokens[token]
	t.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(keyAuth))
}
