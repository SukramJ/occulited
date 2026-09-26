// Package outboundpin records what an outbound request carries, for the tests that pin every
// call the system makes to an outside source (openccu-lite task 266, the fork's docs/privacy.md):
// the method, the path and the query, and every header except Host (which names the test server).
// A change to what leaves the system is then a visible diff in a test, and the privacy statement
// is changed with it.
package outboundpin

import (
	"net/http"
	"sort"
	"strings"
	"sync"
)

// Log is the requests a Recorder saw, one block each.
type Log struct {
	mu   sync.Mutex
	reqs []string
}

// Recorder wraps next and writes every request into the returned log first.
func Recorder(next http.Handler) (http.Handler, *Log) {
	l := &Log{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.add(r)
		next.ServeHTTP(w, r)
	}), l
}

// Describe is the block a request is pinned as: "GET /path?query", then "Name: value" for every
// header but Host, sorted by name.
func Describe(r *http.Request) string {
	lines := []string{r.Method + " " + r.URL.RequestURI()}
	keys := make([]string, 0, len(r.Header))
	for k := range r.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range r.Header[k] {
			lines = append(lines, k+": "+v)
		}
	}
	return strings.Join(lines, "\n")
}

func (l *Log) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = append(l.reqs, Describe(r))
}

// Requests is every request so far, in order.
func (l *Log) Requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.reqs...)
}

// Reset forgets what was recorded.
func (l *Log) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.reqs = nil
}
