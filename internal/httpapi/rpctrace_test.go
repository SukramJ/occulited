package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/rpctrace"
)

// task 79: the RPC trace's switch on the API - off, on for a while, on permanently; 501
// without a tracer; the SD-card warning only on an ARM product whose journal persists.
func TestRPCTraceRoutes(t *testing.T) {
	mux := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t)}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/rpc-trace", "", nil); st != 200 || out["available"] != false || out["mode"] != "off" {
		t.Fatalf("without a tracer: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/rpc-trace", `{"mode":"permanent"}`, nil); st != 501 {
		t.Fatalf("PUT without a tracer: %d", st)
	}
	tr := rpctrace.New(rpctrace.Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	mux2 := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t), RPCTrace: tr}).Register(mux2)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux2.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &auth.Session{ID: "s", User: "admin", Role: auth.RoleAdmin, Scopes: auth.Scopes{auth.ScopeAll}})))
	}))
	t.Cleanup(srv2.Close)
	st, out, _ := do(t, srv2, "GET", "/api/system/v1/rpc-trace", "", nil)
	if st != 200 || out["available"] != true || out["on"] != false || out["sd_warning"] != false {
		t.Fatalf("off: %d %v", st, out)
	}
	if st, out, _ := do(t, srv2, "PUT", "/api/system/v1/rpc-trace", `{"mode":"until","minutes":0}`, nil); st != 422 || out["error"] != "invalid" {
		t.Fatalf("no minutes: %d %v", st, out)
	}
	if st, _, _ := do(t, srv2, "PUT", "/api/system/v1/rpc-trace", `{"mode":"wizard"}`, nil); st != 422 {
		t.Fatalf("bogus mode: %d", st)
	}
	st, out, _ = do(t, srv2, "PUT", "/api/system/v1/rpc-trace", `{"mode":"until","minutes":5}`, nil)
	until, _ := time.Parse(time.RFC3339Nano, out["until"].(string))
	if st != 200 || out["on"] != true || out["mode"] != "until" || time.Until(until) < 4*time.Minute || time.Until(until) > 6*time.Minute {
		t.Fatalf("timed: %d %v", st, out)
	}
	if !tr.On() {
		t.Fatal("the tracer is off")
	}
	if st, out, _ := do(t, srv2, "PUT", "/api/system/v1/rpc-trace", `{"mode":"permanent"}`, nil); st != 200 || out["on"] != true || out["until"] != nil {
		t.Fatalf("permanent: %d %v", st, out)
	}
	if st, out, _ := do(t, srv2, "PUT", "/api/system/v1/rpc-trace", `{"mode":"off"}`, nil); st != 200 || out["on"] != false || tr.On() {
		t.Fatalf("off: %d %v", st, out)
	}
}
