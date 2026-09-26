package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 227: the IPv6 routes - begin, confirm, a second change refused while one
// waits, revert, an invalid setting.
func TestIPv6Routes(t *testing.T) {
	r := fakeRoot(t)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	dir := t.TempDir()
	tx := &system.IPv6Tx{Applier: system.IPv6Applier{Root: r, Run: run}, Store: system.IPv6Store{Path: filepath.Join(dir, "ipv6.json")}, PendingPath: filepath.Join(dir, "p.json"), Window: time.Minute, Exists: func(n string) bool { return n == "eth0" }}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, IPv6: tx, Run: run}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/network/ipv6", "", nil)
	if st != 200 || out["window_seconds"] != float64(60) || out["pending"] != nil {
		t.Fatalf("get: %d %v", st, out)
	}
	if st, out, _ := do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"eth0","mode":"static","address":"fe80::1"}`, nil); st != 422 || !strings.Contains(out["message"].(string), "address") {
		t.Errorf("invalid: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"all","mode":"off"}`, nil); st != 422 {
		t.Errorf("interface all: %d", st)
	}
	st, out, _ = do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"eth0","mode":"static","address":"2001:db8::10","prefix":64,"gateway":"fe80::1","dns":["2001:db8::53"]}`, nil)
	p, _ := out["pending"].(map[string]any)
	if st != 200 || out["changed"] != true || p == nil || p["interface"] != "eth0" {
		t.Fatalf("begin: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"eth0","mode":"off"}`, nil); st != 409 {
		t.Errorf("a second change: %d", st)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/network/ipv6/confirm", `{"token":"nope"}`, nil); st != 404 {
		t.Errorf("a wrong token: %d", st)
	}
	st, out, _ = do(t, srv, "POST", "/api/system/v1/network/ipv6/confirm", `{"token":"`+p["token"].(string)+`"}`, nil)
	if st != 200 || out["confirmed"] != true || out["pending"] != nil || tx.Current("eth0").Mode != "static" {
		t.Fatalf("confirm: %d %v", st, out)
	}
	// another change, reverted by the user
	st, out, _ = do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"eth0","mode":"off"}`, nil)
	if st != 200 || out["changed"] != true || out["pending"] == nil {
		t.Fatalf("begin the second change: %d %v", st, out)
	}
	token := out["pending"].(map[string]any)["token"].(string)
	calls = nil
	st, out, _ = do(t, srv, "POST", "/api/system/v1/network/ipv6/revert", `{"token":"`+token+`"}`, nil)
	if st != 200 || out["confirmed"] != false || !strings.Contains(strings.Join(calls, "\n"), "/sbin/ip -6 addr replace 2001:db8::10/64 dev eth0") {
		t.Fatalf("revert: %d %v %q", st, out, calls)
	}
	// no change: nothing pending
	if _, out, _ := do(t, srv, "POST", "/api/system/v1/network/ipv6", `{"interface":"eth0","mode":"static","address":"2001:db8::10","gateway":"fe80::1","dns":["2001:db8::53"]}`, nil); out["changed"] != false {
		t.Errorf("unchanged: %v", out)
	}
}
