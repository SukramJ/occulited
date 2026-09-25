package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// task 69: the storage route answers the report, and 501 on a daemon without the service
func TestStorageRoute(t *testing.T) {
	srv := systemServer(t)
	st, out, _ := do(t, srv, "GET", "/api/system/v1/storage", "", nil)
	if st != 501 || out["error"] != "unsupported" {
		t.Fatalf("without the service: %d %v", st, out)
	}

	r := fakeRoot(t)
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Storage: &system.Storage{Root: r}}).Register(mux)
	with := httptest.NewServer(mux)
	t.Cleanup(with.Close)
	st, out, _ = do(t, with, "GET", "/api/system/v1/storage", "", nil)
	if st != 200 || out["verdict"] != "good" || out["kernel_log"] != false {
		t.Fatalf("storage: %d %v", st, out)
	}
	if devs, ok := out["devices"].([]any); !ok || len(devs) != 0 {
		t.Fatalf("a root without /sys/block has no disks: %v", out["devices"])
	}
	if reasons, ok := out["reasons"].([]any); !ok || len(reasons) != 0 {
		t.Fatalf("reasons: %v", out["reasons"])
	}
}
