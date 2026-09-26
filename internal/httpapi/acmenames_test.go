package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/acme"
	"github.com/hobbyquaker/occulited/internal/system"
)

// renameRig is a static box called ccu in the domain home.arpa, with an optional certificate
// service whose settings the case chooses.
func renameRig(t *testing.T, settings *acme.Update) (*httptest.Server, system.Root, *acme.Service) {
	t.Helper()
	r := fakeRoot(t)
	w := func(p, c string) {
		full := filepath.Join(string(r), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("etc/config/netconfig", "HOSTNAME=ccu\nMODE=MANUAL\nIP=192.0.2.119\nNETMASK=255.255.255.0\nGATEWAY=192.0.2.1\nNAMESERVER1=192.0.2.1\n")
	w("etc/resolv.conf", "search home.arpa\nnameserver 192.0.2.1\n")
	run := func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	tx := &system.NetTx{Root: r, Applier: system.NetApplier{Root: r, Iface: "eth0", Run: run}, Window: time.Minute}
	api := &SystemAPI{Root: r, NetTx: tx, Run: run}
	var cs *acme.Service
	if settings != nil {
		var err error
		cs, err = acme.New(filepath.Join(t.TempDir(), "acme"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cs.SetSettings(*settings); err != nil {
			t.Fatal(err)
		}
		api.Cert = cs
	}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, r, cs
}

func acmeUpdate(mode string, names ...string) *acme.Update {
	return &acme.Update{Mode: mode, Directory: acme.DirLetsEncrypt, Challenge: acme.ChallengeHTTP01, Names: names}
}

const renameBody = `{"hostname":"lite","mode":"static","address":"192.0.2.119","netmask":"255.255.255.0","gateway":"192.0.2.1","dns":["192.0.2.1"]}`

// A hostname-only change answers what became of the ACME names.
func TestRenameAnswersTheACMENames(t *testing.T) {
	cases := []struct {
		name     string
		settings *acme.Update
		state    string
		names    []string
	}{
		{"ACME with the default names: adapted", acmeUpdate(acme.ModeACME, "ccu.home.arpa", "ccu"), acme.NamesAdapted, []string{"lite.home.arpa", "lite"}},
		{"ACME with names set by hand: left alone", acmeUpdate(acme.ModeACME, "homematic.example.org"), acme.NamesSetByHand, []string{"homematic.example.org"}},
		{"self-signed: not in use", acmeUpdate(acme.ModeSelfSigned), acme.NamesNotInUse, []string{}},
		{"no certificate service: not in use", nil, acme.NamesNotInUse, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, r, cs := renameRig(t, c.settings)
			st, out, _ := do(t, srv, "GET", "/api/system/v1/network", "", nil)
			if st != 200 || out["network"].(map[string]any)["domain"] != "home.arpa" {
				t.Fatalf("domain: %d %v", st, out["network"])
			}
			st, out, _ = do(t, srv, "POST", "/api/system/v1/network", renameBody, nil)
			if st != 200 || out["applied"] != true {
				t.Fatalf("rename: %d %v", st, out)
			}
			ch, ok := out["acme_names"].(map[string]any)
			if !ok || ch["state"] != c.state {
				t.Fatalf("acme_names %v, want state %s", out["acme_names"], c.state)
			}
			if got := toStrings(ch["names"]); !reflect.DeepEqual(got, c.names) {
				t.Fatalf("names %v, want %v", got, c.names)
			}
			if cs != nil && !reflect.DeepEqual(cs.Settings().Names, c.names) {
				t.Fatalf("stored %v", cs.Settings().Names)
			}
			if r.Hostname() != "lite" {
				t.Fatalf("not renamed: %q", r.Hostname())
			}
			// openccu-lite task 62: the rename says what became of the lease (a static rig: nothing
			// to tell) and whether the live certificate names the new host (no certificate here)
			ren, ok := out["rename"].(map[string]any)
			if !ok || ren["hostname"] != "lite" || ren["previous"] != "ccu" || ren["lease"].(map[string]any)["static"] != true || ren["lease"].(map[string]any)["renewed"] != false {
				t.Fatalf("rename: %v", out["rename"])
			}
			cert, ok := out["certificate"].(map[string]any)
			if !ok || cert["fits"] != false || cert["known"] != false {
				t.Fatalf("certificate: %v", out["certificate"])
			}
			// the same name again: nothing renamed, nothing to say
			_, out, _ = do(t, srv, "POST", "/api/system/v1/network", renameBody, nil)
			if _, has := out["acme_names"]; has {
				t.Fatalf("no rename, yet acme_names: %v", out)
			}
			if _, has := out["rename"]; has {
				t.Fatalf("no rename, yet rename: %v", out)
			}
		})
	}
}

// A rename that comes with an address change is applied when it is confirmed; the confirmation
// answers the ACME names.
func TestConfirmedRenameAnswersTheACMENames(t *testing.T) {
	srv, _, cs := renameRig(t, acmeUpdate(acme.ModeACME, "ccu.home.arpa", "ccu"))
	st, out, _ := do(t, srv, "POST", "/api/system/v1/network", `{"hostname":"lite","mode":"static","address":"192.0.2.120","netmask":"255.255.255.0","gateway":"192.0.2.1","dns":["192.0.2.1"]}`, nil)
	if st != 200 || out["applied"] != false {
		t.Fatalf("begin: %d %v", st, out)
	}
	if _, has := out["acme_names"]; has || !reflect.DeepEqual(cs.Settings().Names, []string{"ccu.home.arpa", "ccu"}) {
		t.Fatalf("adapted before the confirmation: %v %v", out, cs.Settings().Names)
	}
	token := out["pending"].(map[string]any)["token"].(string)
	st, out, _ = do(t, srv, "POST", "/api/system/v1/network/confirm", `{"token":"`+token+`"}`, nil)
	ch, _ := out["acme_names"].(map[string]any)
	if st != 200 || ch["state"] != acme.NamesAdapted || !reflect.DeepEqual(cs.Settings().Names, []string{"lite.home.arpa", "lite"}) {
		t.Fatalf("confirm: %d %v, stored %v", st, out, cs.Settings().Names)
	}
}

// A domain change another DHCP lease brought is seen when the page polls GET /network or opens
// the Certificate page: names that are the old domain's default follow the new domain; names set
// by hand stay. A rename after it uses the new domain.
func TestDomainChangeFollowsTheACMENames(t *testing.T) {
	cases := []struct {
		name     string
		route    string
		settings *acme.Update
		want     []string
	}{
		{"the Network page, default names: adapted", "/api/system/v1/network", acmeUpdate(acme.ModeACME, "ccu.home.arpa", "ccu"), []string{"ccu.lan", "ccu"}},
		{"the Certificate page, default names: adapted", "/api/system/v1/certificate", acmeUpdate(acme.ModeACME, "ccu.home.arpa", "ccu"), []string{"ccu.lan", "ccu"}},
		{"the Network page, names set by hand: left alone", "/api/system/v1/network", acmeUpdate(acme.ModeACME, "homematic.example.org"), []string{"homematic.example.org"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, r, cs := renameRig(t, c.settings)
			// the first look remembers home.arpa and changes nothing
			if st, _, _ := do(t, srv, "GET", c.route, "", nil); st != 200 {
				t.Fatalf("first look: %d", st)
			}
			if got := cs.Settings().Names; !reflect.DeepEqual(got, c.settings.Names) {
				t.Fatalf("changed at the first look: %v", got)
			}
			if err := os.WriteFile(filepath.Join(string(r), "etc/resolv.conf"), []byte("domain lan\nnameserver 192.0.2.1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if st, _, _ := do(t, srv, "GET", c.route, "", nil); st != 200 {
				t.Fatalf("after the lease: %d", st)
			}
			if got := cs.Settings().Names; !reflect.DeepEqual(got, c.want) {
				t.Fatalf("names %v, want %v", got, c.want)
			}
		})
	}
}

// A domain that changed unseen is applied before a rename, so the rename compares with the new
// domain's default and adapts.
func TestRenameAfterAnUnseenDomainChange(t *testing.T) {
	srv, r, cs := renameRig(t, acmeUpdate(acme.ModeACME, "ccu.home.arpa", "ccu"))
	if st, _, _ := do(t, srv, "GET", "/api/system/v1/network", "", nil); st != 200 {
		t.Fatalf("first look: %d", st)
	}
	if err := os.WriteFile(filepath.Join(string(r), "etc/resolv.conf"), []byte("search lan\nnameserver 192.0.2.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out, _ := do(t, srv, "POST", "/api/system/v1/network", renameBody, nil)
	ch, _ := out["acme_names"].(map[string]any)
	if st != 200 || ch["state"] != acme.NamesAdapted || !reflect.DeepEqual(cs.Settings().Names, []string{"lite.lan", "lite"}) {
		t.Fatalf("rename: %d %v, stored %v", st, out, cs.Settings().Names)
	}
}

func toStrings(v any) []string {
	out := []string{}
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// TestNamesFit (openccu-lite task 62): a certificate names the host bare, in a domain, or not.
func TestNamesFit(t *testing.T) {
	for _, c := range []struct {
		names []string
		host  string
		want  bool
	}{
		{[]string{"lite.home.arpa", "lite"}, "lite", true},
		{[]string{"ccu.home.arpa", "ccu", "192.0.2.119"}, "lite", false},
		{[]string{"lite.home.arpa"}, "LITE", true},
		{[]string{"*.home.arpa"}, "lite", false},
		{[]string{"literal.home.arpa"}, "lite", false},
		{nil, "lite", false},
	} {
		if got := NamesFit(c.names, c.host); got != c.want {
			t.Errorf("%v %q: %v", c.names, c.host, got)
		}
	}
}
