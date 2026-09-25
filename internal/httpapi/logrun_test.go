package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// task 102: GET /log?run= answers exactly that run's lines, refuses what is not a run id (it
// becomes a journalctl match), and says so when the log holds no line of the run.
func TestLogRunFilter(t *testing.T) {
	r := fakeRoot(t)
	log := memLog{
		{Tag: "occulited", Severity: "info", Message: "hmipserver stopped run=radio-firmware run_id=20260912T221500-0badcafe"},
		{Tag: "occulited", Severity: "info", Message: "http method=GET"},
		{Tag: "occulited", Severity: "err", Message: "failed: the flasher exited 1 run=radio-firmware run_id=20260912T221500-0badcafe"},
		{Tag: "occulited", Severity: "info", Message: "another run run=acme run_id=20260912T221600-00000001"},
	}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Log: log}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/log?run=20260912T221500-0badcafe", "", nil)
	lines, _ := out["lines"].([]any)
	if st != 200 || len(lines) != 2 || out["run_missing"] != nil {
		t.Fatalf("%d %v", st, out)
	}
	for _, l := range lines {
		if !strings.Contains(l.(map[string]any)["message"].(string), "run_id=20260912T221500-0badcafe") {
			t.Errorf("a line of another run: %v", l)
		}
	}
	// the filters combine with the run
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log?run=20260912T221500-0badcafe&severity=err", "", nil); len(out["lines"].([]any)) != 1 {
		t.Errorf("run and severity: %v", out)
	}
	// a run the log holds nothing of
	st, out, _ = do(t, srv, "GET", "/api/system/v1/log?run=20260101T000000-deadbeef", "", nil)
	if st != 200 || out["run_missing"] != true || len(out["lines"].([]any)) != 0 {
		t.Errorf("missing run: %d %v", st, out)
	}
	// without a run there is no such key
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); out["run_missing"] != nil {
		t.Errorf("no run: %v", out)
	}
	// not a run id: refused, on the download and the stream too
	for _, path := range []string{"/api/system/v1/log?run=OCCULITE_RUN=x", "/api/system/v1/log?run=%2B", "/api/system/v1/log/download?run=../x", "/api/system/v1/log?run=20260912T221500"} {
		if st, out, _ := do(t, srv, "GET", path, "", nil); st != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %v", path, st, out)
		}
	}
}
