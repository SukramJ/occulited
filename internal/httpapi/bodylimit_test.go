package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

// B-232: every JSON body is read through readJSON, decodeSmall or a MaxBytesReader of its own; a
// bare decoder or ReadAll on the request body lets a caller grow the heap without end.
func TestNoUnboundedBody(t *testing.T) {
	bare := regexp.MustCompile(`(json\.NewDecoder|io\.ReadAll)\(\s*(r|req)\.Body\s*\)`)
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if bare.MatchString(line) {
				t.Errorf("%s:%d reads the request body without a limit: %s", name, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// a body over its limit is 413 too-large with the limit, through decodeSmall and readJSON alike
func TestBodyTooLarge(t *testing.T) {
	h := func(read func(http.ResponseWriter, *http.Request, any) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var v map[string]any
			if err := read(w, r, &v); err != nil {
				badBody(w, err)
				return
			}
			writeJSON(w, 200, v)
		}
	}
	small := h(decodeSmall)
	large := h(func(_ http.ResponseWriter, r *http.Request, v any) error { return readJSON(r, v) })
	big := `{"x":"` + strings.Repeat("a", 2<<20) + `"}`
	for _, c := range []struct {
		name    string
		h       http.HandlerFunc
		body    string
		status  int
		limit   float64
		errCode string
	}{
		{"small ok", small, `{"a":1}`, 200, 0, ""},
		{"small over", small, `{"x":"` + strings.Repeat("a", smallBody) + `"}`, 413, smallBody, "too-large"},
		{"small broken", small, `{"a":`, 422, 0, "invalid-body"},
		{"readJSON ok", large, `{"a":1}`, 200, 0, ""},
		{"readJSON over", large, big, 413, maxBody, "too-large"},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c.h(rec, httptest.NewRequest("POST", "/", strings.NewReader(c.body)))
			if rec.Code != c.status {
				t.Fatalf("%d %s", rec.Code, rec.Body.String())
			}
			if c.errCode == "" {
				return
			}
			var e apiError
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error != c.errCode {
				t.Fatalf("%v %s", err, rec.Body.String())
			}
			if c.limit != 0 && e.Detail["limit"] != c.limit {
				t.Errorf("limit: %v", e.Detail)
			}
		})
	}
	// and through a route: the login (open) with a 2 MiB body
	srv := authServer(t)
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/login", big, nil); st != 413 || out["error"] != "too-large" {
		t.Errorf("login with a 2 MiB body: %d %v", st, out)
	}
}
