package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// task 186: GET /log?area= answers that area's lines, and refuses an area logctl does not know
// (it becomes a journalctl match, so nothing else may pass) - on the download too.
func TestLogAreaFilter(t *testing.T) {
	log := memLog{
		{Tag: "occulited", Severity: "debug", Area: "acme", Message: "order finalized"},
		{Tag: "occulited", Severity: "info", Message: "sessions: restored"},
		{Tag: "occulited", Severity: "warning", Area: "addons", Message: "addon updates: check failed"},
	}
	mux := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t), Log: log}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/log?area=acme", "", nil)
	lines, _ := out["lines"].([]any)
	if st != 200 || len(lines) != 1 || lines[0].(map[string]any)["area"] != "acme" {
		t.Fatalf("%d %v", st, out)
	}
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); len(out["lines"].([]any)) != 3 {
		t.Errorf("no area: %v", out)
	}
	for _, path := range []string{"/api/system/v1/log?area=nothing", "/api/system/v1/log?area=acme%20OR", "/api/system/v1/log/download?area=_PID=1"} {
		if st, out, _ := do(t, srv, "GET", path, "", nil); st != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v", path, st, out)
		}
	}
	var _ system.LogReader = log
}
