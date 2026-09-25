package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/acme"
	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
	"github.com/hobbyquaker/occulited/internal/system"
)

// fqdnRig is a static box called ccu in the domain the case names (none for ""), with the
// certificate service on a stub installer that serves no certificate yet, the HTTPS settings
// recording their reloads, and the network transaction for a rename.
type fqdnRig struct {
	srv     *httptest.Server
	root    system.Root
	inst    *stubInstaller
	reloads *[]string
}

func newFQDNRig(t *testing.T, domain string) fqdnRig {
	t.Helper()
	r := fakeRoot(t)
	rig := fqdnRig{root: r, inst: &stubInstaller{}, reloads: &[]string{}}
	rig.write(t, "etc/config/netconfig", "HOSTNAME=ccu\nMODE=MANUAL\nIP=192.0.2.119\nNETMASK=255.255.255.0\nGATEWAY=192.0.2.1\nNAMESERVER1=192.0.2.1\n")
	rig.domain(t, domain)
	run := func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		*rig.reloads = append(*rig.reloads, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	cs, err := acme.New(filepath.Join(t.TempDir(), "acme"), nil)
	if err != nil {
		t.Fatal(err)
	}
	cs.Issuer, cs.Installer = &stubIssuer{}, rig.inst
	tx := &system.NetTx{Root: r, Applier: system.NetApplier{Root: r, Iface: "eth0", Run: run}, Window: time.Minute}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, NetTx: tx, Run: run, Cert: cs, HTTPS: &system.HTTPSConfig{Root: r, Run: rec, Systemd: true}}).Register(mux)
	rig.srv = httptest.NewServer(mux)
	t.Cleanup(rig.srv.Close)
	return rig
}

func (g fqdnRig) write(t *testing.T, p, c string) {
	t.Helper()
	full := filepath.Join(string(g.root), p)
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (g fqdnRig) domain(t *testing.T, domain string) {
	resolv := "nameserver 192.0.2.1\n"
	if domain != "" {
		resolv = "search " + domain + "\n" + resolv
	}
	g.write(t, "etc/resolv.conf", resolv)
}

// issued serves a CA certificate for names; selfSigned a self-signed one.
func (g fqdnRig) issued(names ...string) {
	leaf, key, ca := testcert.Issued(names, time.Now().Add(90*24*time.Hour))
	live, _ := certpem.AssemblePEM(append(leaf, ca...), key)
	g.inst.set(live, "acme")
}

func (g fqdnRig) selfSigned(names ...string) {
	cert, key := testcert.SelfSigned(names, nil, time.Now().Add(24*time.Hour))
	g.inst.set(append(cert, key...), "")
}

func (g fqdnRig) marker(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(string(g.root), system.FQDNRedirectMarker))
	if err != nil {
		return "-"
	}
	return string(b)
}

func (g fqdnRig) get(t *testing.T) map[string]any {
	t.Helper()
	st, out, _ := do(t, g.srv, "GET", "/api/system/v1/https", "", nil)
	if st != 200 {
		t.Fatalf("GET /https: %d %v", st, out)
	}
	return out
}

func strOrNil(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return "<nil>"
}

// TestFQDNRedirectGuards: GET describes the switch before it is on; PUT switches it on only with a
// domain and a certificate that covers <host>.<domain> - a self-signed one that does is allowed.
func TestFQDNRedirectGuards(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		cert   func(fqdnRig)
		state  string
		reason string
		target string // "<nil>" = null
		put    int
	}{
		{"no domain", "", func(g fqdnRig) { g.issued("ccu.home.arpa", "ccu") }, FQDNUnavailable, FQDNNoDomain, "<nil>", 422},
		{"no certificate", "home.arpa", func(fqdnRig) {}, FQDNUnavailable, FQDNNoCertificate, "ccu.home.arpa", 422},
		{"a self-signed certificate without the name", "home.arpa", func(g fqdnRig) { g.selfSigned("openccu") }, FQDNUnavailable, FQDNNotCovered, "ccu.home.arpa", 422},
		{"a CA certificate for the bare name only", "home.arpa", func(g fqdnRig) { g.issued("ccu") }, FQDNUnavailable, FQDNNotCovered, "ccu.home.arpa", 422},
		{"a self-signed certificate that covers it", "home.arpa", func(g fqdnRig) { g.selfSigned("ccu.home.arpa", "ccu") }, FQDNOff, "", "ccu.home.arpa", 200},
		{"a wildcard certificate", "home.arpa", func(g fqdnRig) { g.issued("*.home.arpa") }, FQDNOff, "", "ccu.home.arpa", 200},
		{"a CA certificate for both names", "home.arpa", func(g fqdnRig) { g.issued("ccu.home.arpa", "ccu") }, FQDNOff, "", "ccu.home.arpa", 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := newFQDNRig(t, c.domain)
			c.cert(g)
			out := g.get(t)
			if out["redirect_fqdn"] != false || out["redirect_fqdn_state"] != c.state || strOrNil(out["redirect_fqdn_target"]) != c.target || out["redirect_fqdn_host"] != "ccu" {
				t.Fatalf("GET: %v", out)
			}
			if reason, _ := out["redirect_fqdn_reason"].(string); reason != c.reason {
				t.Fatalf("reason %q, want %q", reason, c.reason)
			}
			st, out, _ := do(t, g.srv, "PUT", "/api/system/v1/https", `{"redirect_https":false,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":true}`, nil)
			if st != c.put {
				t.Fatalf("PUT: %d %v", st, out)
			}
			if st == 422 {
				if d, _ := out["detail"].(map[string]any); d["reason"] != c.reason || out["error"] != "invalid" || g.marker(t) != "-" || len(*g.reloads) != 0 {
					t.Fatalf("refusal: %v, marker %q, reloads %v", out, g.marker(t), *g.reloads)
				}
				return
			}
			if out["redirect_fqdn"] != true || out["redirect_fqdn_state"] != FQDNActive || g.marker(t) != "ccu.home.arpa\n" || len(*g.reloads) != 1 {
				t.Fatalf("on: %v, marker %q, reloads %v", out, g.marker(t), *g.reloads)
			}
		})
	}
}

// TestFQDNRedirectPut: on, a PUT from a script that knows only the two older switches keeps it,
// the same again reloads nothing, off removes the marker.
func TestFQDNRedirectPut(t *testing.T) {
	g := newFQDNRig(t, "home.arpa")
	g.issued("ccu.home.arpa", "ccu")
	put := func(body string, wantOn bool, wantReloads int) {
		t.Helper()
		*g.reloads = nil
		st, out, _ := do(t, g.srv, "PUT", "/api/system/v1/https", body, nil)
		if st != 200 || out["redirect_fqdn"] != wantOn || len(*g.reloads) != wantReloads {
			t.Fatalf("%s: %d %v, reloads %v", body, st, out, *g.reloads)
		}
	}
	put(`{"redirect_https":false,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":true}`, true, 1)
	put(`{"redirect_https":true,"hsts":false,"hsts_max_age_days":365}`, true, 1)
	if g.marker(t) != "ccu.home.arpa\n" {
		t.Fatalf("marker after the older PUT: %q", g.marker(t))
	}
	put(`{"redirect_https":true,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":true}`, true, 0)
	put(`{"redirect_https":true,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":false}`, false, 1)
	if g.marker(t) != "-" {
		t.Fatalf("marker after off: %q", g.marker(t))
	}
	put(`{"redirect_https":true,"hsts":false,"hsts_max_age_days":365}`, false, 0)
	if out := g.get(t); out["redirect_fqdn_state"] != FQDNOff {
		t.Fatalf("off: %v", out)
	}
	// on stays on when the certificate no longer covers the name: saving other switches is not refused
	put(`{"redirect_https":true,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":true}`, true, 1)
	g.selfSigned("openccu")
	put(`{"redirect_https":false,"hsts":false,"hsts_max_age_days":365}`, true, 1)
	if out := g.get(t); out["redirect_fqdn_state"] != FQDNSuspended || out["redirect_fqdn_reason"] != FQDNNotCovered {
		t.Fatalf("suspended: %v", out)
	}
}

// TestFQDNRedirectFollows: a rename and a domain change rewrite the target with one reload; the
// redirect is suspended while the certificate does not cover the new name and active again once
// one does; switched off, a rename touches nothing.
func TestFQDNRedirectFollows(t *testing.T) {
	g := newFQDNRig(t, "home.arpa")
	g.issued("ccu.home.arpa", "ccu")
	if st, out, _ := do(t, g.srv, "PUT", "/api/system/v1/https", `{"redirect_https":false,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":true}`, nil); st != 200 {
		t.Fatalf("on: %d %v", st, out)
	}

	// a rename: the answer says where the redirect stands
	*g.reloads = nil
	st, out, _ := do(t, g.srv, "POST", "/api/system/v1/network", renameBody, nil)
	fr, _ := out["fqdn_redirect"].(map[string]any)
	if st != 200 || fr["on"] != true || fr["state"] != FQDNSuspended || fr["reason"] != FQDNNotCovered || fr["target"] != "lite.home.arpa" || fr["previous"] != "ccu.home.arpa" {
		t.Fatalf("rename: %d %v", st, out)
	}
	if g.marker(t) != "lite.home.arpa\n" || len(*g.reloads) != 1 {
		t.Fatalf("marker %q, reloads %v", g.marker(t), *g.reloads)
	}
	if out := g.get(t); out["redirect_fqdn_state"] != FQDNSuspended || out["redirect_fqdn_host"] != "lite" || len(*g.reloads) != 1 {
		t.Fatalf("GET after the rename: %v %v", out, *g.reloads)
	}
	// the certificate for the new names brings it back (lighttpd's own reload at the install)
	g.issued("lite.home.arpa", "lite")
	if out := g.get(t); out["redirect_fqdn_state"] != FQDNActive {
		t.Fatalf("after the new certificate: %v", out)
	}

	// a domain change, seen by the Network page's poll
	*g.reloads = nil
	g.domain(t, "lan")
	if st, _, _ := do(t, g.srv, "GET", "/api/system/v1/network", "", nil); st != 200 || g.marker(t) != "lite.lan\n" || len(*g.reloads) != 1 {
		t.Fatalf("GET /network: %d, marker %q, reloads %v", st, g.marker(t), *g.reloads)
	}
	if out := g.get(t); out["redirect_fqdn_state"] != FQDNSuspended || out["redirect_fqdn_target"] != "lite.lan" {
		t.Fatalf("after the domain change: %v", out)
	}
	// and by the Certificate page
	g.domain(t, "example.net")
	if st, _, _ := do(t, g.srv, "GET", "/api/system/v1/certificate", "", nil); st != 200 || g.marker(t) != "lite.example.net\n" || len(*g.reloads) != 2 {
		t.Fatalf("GET /certificate: %d, marker %q, reloads %v", st, g.marker(t), *g.reloads)
	}
	// the domain goes away (a lease without one): the target stays
	g.domain(t, "")
	if out := g.get(t); g.marker(t) != "lite.example.net\n" || out["redirect_fqdn_target"] != "lite.example.net" || len(*g.reloads) != 2 {
		t.Fatalf("without a domain: %v, marker %q", out, g.marker(t))
	}

	// switched off, a rename leaves everything alone
	g.domain(t, "home.arpa")
	if st, _, _ := do(t, g.srv, "PUT", "/api/system/v1/https", `{"redirect_https":false,"hsts":false,"hsts_max_age_days":365,"redirect_fqdn":false}`, nil); st != 200 {
		t.Fatal("off")
	}
	*g.reloads = nil
	st, out, _ = do(t, g.srv, "POST", "/api/system/v1/network", strings.Replace(renameBody, `"lite"`, `"ccu"`, 1), nil)
	fr, _ = out["fqdn_redirect"].(map[string]any)
	if st != 200 || fr["on"] != false || fr["previous"] != nil || g.marker(t) != "-" || len(*g.reloads) != 0 {
		t.Fatalf("rename while off: %d %v, marker %q, reloads %v", st, out, g.marker(t), *g.reloads)
	}
}
