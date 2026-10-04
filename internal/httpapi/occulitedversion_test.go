package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// task 133: /status carries occulited's own version beside /VERSION's, for the Status page; the
// version is the commit (occulited task 16), and the bare hash goes beside it
func TestStatusCarriesOcculitedVersion(t *testing.T) {
	r := fakeRoot(t)
	const sha = "fa42dfde1e31fb074df53220dd573ceb92642ff0"
	for _, c := range []struct{ version, commit string }{
		{sha, sha},
		{sha + "-hot", sha},
		{"dev", ""},
		{"", ""}, // a build without -X main.version leaves both fields out
	} {
		mux := http.NewServeMux()
		(&SystemAPI{Root: r, Version: c.version, Commit: c.commit}).Register(mux)
		srv := httptest.NewServer(mux)
		st, out, _ := do(t, srv, "GET", "/api/system/v1/status", "", nil)
		srv.Close()
		for field, want := range map[string]string{"occulited_version": c.version, "occulited_commit": c.commit} {
			got, present := out[field]
			if st != 200 || (want == "" && present) || (want != "" && got != want) {
				t.Fatalf("version %q: status %d, %s %v (present %v)", c.version, st, field, got, present)
			}
		}
	}
}
