package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
	"github.com/hobbyquaker/occulited/internal/system"
)

// task 143: System -> Remote access - the switches, the pair (a generated password answered once),
// the refusals, and classic RPC's firewall rules following the switches
func TestRemoteAccessRoutes(t *testing.T) {
	r := fakeRoot(t)
	w := func(p, c string) {
		full := filepath.Join(string(r), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(c), 0o644)
	}
	// rfd listens on 127.0.0.1:32001 (0x7D01), hmipserver does not
	w("proc/net/tcp", "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
		"   0: 0100007F:7D01 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0\n")
	fp := &fwPriv{}
	old := system.Priv
	system.Priv = fp
	t.Cleanup(func() { system.Priv = old })
	fr := &system.FirewallRules{Root: r, StateDir: t.TempDir(), Window: time.Minute, Owners: func() map[string][]firewall.PortSpec {
		o := map[string][]firewall.PortSpec{}
		if p := r.ClassicRPCOwner(); p != nil {
			o[firewall.OwnerRPC] = p
		}
		return o
	}}
	if err := fr.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	oldFC := system.FirewallChanged
	system.FirewallChanged = func(ctx context.Context) { _ = fr.Sync(ctx, false) }
	t.Cleanup(func() { system.FirewallChanged = oldFC })
	var mu sync.Mutex
	var reloads, restarts int
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(strings.Join(args, " "), "reload"):
			reloads++
		case strings.Contains(strings.Join(args, " "), "restart"):
			restarts++
		}
		return nil, nil
	}
	svc := scriptBox{system.AddonScripts{Root: r}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc, Addons: svc, Manager: svc, Nav: svc, FirewallRules: fr,
		ClassicRPC: &system.ClassicRPCConfig{Root: r, Run: run, Systemd: true, RestartDelay: time.Millisecond}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/remote-access", "", nil)
	classic := out["classic"].(map[string]any)
	ports := out["ports"].([]any)
	if st != 200 || classic["auth"] != "none" || classic["plain"] != false || len(ports) != 6 || out["hs485d"] != false || len(out["rules"].([]any)) != 0 {
		t.Fatalf("GET: %d %v", st, out)
	}
	// task 223: nothing switched on, nothing for the firewall to let in
	if fw := out["firewall"].(map[string]any); fw["hint"] != "closed" || len(fw["ports"].([]any)) != 0 {
		t.Fatalf("firewall, all off: %v", fw)
	}
	if p := ports[0].(map[string]any); p["port"] != float64(2001) || p["interface"] != "BidCos-RF" || p["running"] != true || p["open"] != false {
		t.Fatalf("2001: %v", p)
	}
	if p := ports[1].(map[string]any); p["port"] != float64(2010) || p["running"] != false {
		t.Fatalf("2010: %v", p)
	}
	// password auth needs the pair first
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/remote-access", `{"classic":{"plain":true,"tls":false,"auth":"password"}}`, nil); st != 422 {
		t.Fatalf("password without a pair: %d", st)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/remote-access/classic-password", `{"user":"ccu","password":"short"}`, nil); st != 422 {
		t.Fatalf("a short password: %d", st)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/remote-access/classic-password", `{"user":"ccu","password":"long enough pw","generate":true}`, nil); st != 422 {
		t.Fatalf("both: %d", st)
	}
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/remote-access/classic-password", `{"user":"ccu","generate":true}`, nil)
	if st != 200 || len(out["password"].(string)) != 32 || out["classic"].(map[string]any)["user"] != "ccu" || out["classic"].(map[string]any)["auth"] != "password" {
		t.Fatalf("generate: %d %v", st, out)
	}
	// never again: GET has no password
	if _, out, _ = do(t, srv, "GET", "/api/system/v1/remote-access", "", nil); out["password"] != nil {
		t.Fatal("GET answers a password")
	}
	st, out, _ = do(t, srv, "PUT", "/api/system/v1/remote-access", `{"classic":{"plain":true,"tls":true,"auth":"password"}}`, nil)
	if st != 200 || out["classic"].(map[string]any)["tls"] != true || out["ports"].([]any)[3].(map[string]any)["open"] != true {
		t.Fatalf("both on: %d %v", st, out)
	}
	// the firewall follows: six owned rules from the local networks, with their comments
	rules := out["rules"].([]any)
	if len(rules) != 6 || rules[0].(map[string]any)["source"] != firewall.LocalNetworks || rules[0].(map[string]any)["comment"] != "XMLRPC rfd (BidCos-RF)" {
		t.Fatalf("rules: %v", rules)
	}
	// task 223: each open port's verdict - the owned rule accepts it
	fw := out["firewall"].(map[string]any)
	if vs := fw["ports"].([]any); fw["hint"] != "open" || len(vs) != 6 || vs[0].(map[string]any)["port"] != float64(2001) || vs[0].(map[string]any)["target"] != "ACCEPT" || vs[0].(map[string]any)["owner"] != firewall.OwnerRPC {
		t.Fatalf("firewall, all on: %v", fw)
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if reloads != 1 || restarts != 1 {
		t.Errorf("lighttpd reloaded %d times (want 1, the pair), restarted %d (want 1, the switches)", reloads, restarts)
	}
	mu.Unlock()
	// off again: the rules go
	if _, out, _ = do(t, srv, "PUT", "/api/system/v1/remote-access", `{"classic":{"plain":false,"tls":false,"auth":"none"}}`, nil); len(out["rules"].([]any)) != 0 || out["classic"].(map[string]any)["password_set"] != false {
		t.Fatalf("off: %v", out)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/system/v1/remote-access", `{}`, nil); st != 422 {
		t.Fatalf("no classic: %d", st)
	}
}

// task 223: the verdicts of the open ports and the hint - an open port a rule rejects is "blocked",
// a closed port is not listed
func TestClassicVerdicts(t *testing.T) {
	c := firewall.Config{Policy: firewall.Policy{V4: "DROP", V6: "DROP"}, Rules: []firewall.Rule{
		{ID: "a", Port: 2001, Proto: "tcp", Source: firewall.LocalNetworks, Family: "both", Target: "ACCEPT", Owner: firewall.OwnerRPC},
		{ID: "b", Port: 2010, Proto: "tcp", Source: "0/0", Family: "ipv4", Target: "REJECT"},
	}}
	ports := []remotePort{{ClassicRPCPort: system.ClassicRPCPort{Port: 2001}, Open: true}, {ClassicRPCPort: system.ClassicRPCPort{Port: 2010}, Open: true}, {ClassicRPCPort: system.ClassicRPCPort{Port: 9292}}}
	for _, tc := range []struct {
		name  string
		ports []remotePort
		hint  string
		n     int
	}{
		{"none open", ports[2:], "closed", 0},
		{"accepted", ports[:1], "open", 1},
		{"one rejected", ports, "blocked", 2},
	} {
		f := classicVerdicts(c, tc.ports)
		if f.Hint != tc.hint || len(f.Ports) != tc.n {
			t.Errorf("%s: %+v", tc.name, f)
		}
	}
	if f := classicVerdicts(c, ports); f.Ports[1].Target != "REJECT" || f.Ports[1].Rule != "b" || f.Ports[0].Source != firewall.LocalNetworks {
		t.Errorf("verdicts: %+v", f.Ports)
	}
}
