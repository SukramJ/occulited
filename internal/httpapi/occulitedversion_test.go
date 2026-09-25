package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// task 133: /status carries occulited's own version beside /VERSION's, for the Status page
func TestStatusCarriesOcculitedVersion(t *testing.T) {
	r := fakeRoot(t)
	for _, c := range []struct{ version, want string }{
		{"1df08bb0101038ac6eb7c08c3f3144a8a91840a1-hot", "1df08bb0101038ac6eb7c08c3f3144a8a91840a1-hot"},
		{"", ""}, // a build without -X main.version leaves the field out
	} {
		mux := http.NewServeMux()
		(&SystemAPI{Root: r, Version: c.version}).Register(mux)
		srv := httptest.NewServer(mux)
		st, out, _ := do(t, srv, "GET", "/api/system/v1/status", "", nil)
		srv.Close()
		got, present := out["occulited_version"]
		if st != 200 || (c.want == "" && present) || (c.want != "" && got != c.want) {
			t.Fatalf("version %q: status %d, occulited_version %v (present %v)", c.version, st, got, present)
		}
	}
}
