package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/system"
)

func (rig *powerRig) resetArmed() bool {
	_, err := os.Stat(string(rig.root) + "/usr/local/.doFactoryReset")
	return err == nil
}

// listDevicesStub answers listDevices with devices of the given types (one channel each), the
// way the factory reset's warning counts them.
func listDevicesStub(t *testing.T, types ...string) *httptest.Server {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><methodResponse><params><param><value><array><data>`)
	for i, typ := range types {
		addr := "DEV" + string(rune('A'+i))
		b.WriteString(`<value><struct><member><name>ADDRESS</name><value><string>` + addr + `</string></value></member><member><name>TYPE</name><value><string>` + typ + `</string></value></member><member><name>PARENT</name><value><string></string></value></member></struct></value>`)
		b.WriteString(`<value><struct><member><name>ADDRESS</name><value><string>` + addr + `:1</string></value></member><member><name>TYPE</name><value><string>CH</string></value></member><member><name>PARENT</name><value><string>` + addr + `</string></value></member></struct></value>`)
	}
	b.WriteString(`</data></array></value></param></params></methodResponse>`)
	body := b.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (rig *powerRig) get(t *testing.T, path string) (int, map[string]any) {
	t.Helper()
	res, err := http.Get(rig.srv.URL + "/api/system/v1" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, m
}

func (rig *powerRig) do(t *testing.T, method, path, body string) (int, apiError, map[string]any) {
	t.Helper()
	req, _ := http.NewRequest(method, rig.srv.URL+"/api/system/v1"+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var e apiError
	var m map[string]any
	_ = json.Unmarshal(raw, &e)
	_ = json.Unmarshal(raw, &m)
	return res.StatusCode, e, m
}

// The view: the host name to type, the paired devices per interface with rfd down shown as
// unknown, the key states (no crypttool under a fake root: not known), the local key.
func TestFactoryResetView(t *testing.T) {
	rig := newPowerRig(t, true)
	root := string(rig.root)
	_ = os.MkdirAll(root+"/etc/config/crRFD", 0o755)
	_ = os.WriteFile(root+"/etc/config/netconfig", []byte("HOSTNAME=lab-ccu\n"), 0o644)
	_ = os.WriteFile(root+"/etc/config/crRFD/hmip_user.conf", []byte("KeyServer.Mode=LOCAL\nNetwork.Key=00112233445566778899AABBCCDDEEFF\n"), 0o644)
	rfd := listDevicesStub(t, "HM-RCV-50 BidCoS-RF", "HM-CC-TC", "HM-Sec-SC")
	hmip := listDevicesStub(t, "HmIP-RFUSB", "HmIP-PDT")
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close()
	old := system.PairedDevicesTimeout
	system.PairedDevicesTimeout = 2e9
	t.Cleanup(func() { system.PairedDevicesTimeout = old })
	api := &SystemAPI{Root: rig.root, Manager: rig.m, Power: rig.power, RadioInterfaces: func() []interfaces.Interface {
		return interfaces.FromList([]struct{ Name, URL string }{
			{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(rfd.URL, "http://")},
			{"HmIP-RF", "xmlrpc://" + strings.TrimPrefix(hmip.URL, "http://")},
			{"BidCos-Wired", "xmlrpc://" + strings.TrimPrefix(dead.URL, "http://")},
		})
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	rig.srv.Close()
	rig.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mux.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "u", Role: rig.role, Scopes: auth.RoleScopes(rig.role)})))
	}))
	t.Cleanup(rig.srv.Close)

	st, out := rig.get(t, "/factory-reset")
	if st != 200 {
		t.Fatalf("view: %d %v", st, out)
	}
	if out["hostname"] != "lab-ccu" || out["armed"] != false || out["update_staged"] != false || out["container"] != "" {
		t.Errorf("view: %v", out)
	}
	if out["security_key_known"] != false || out["security_key_set"] != false {
		t.Errorf("no crypttool under a fake root, yet the key state is known: %v", out)
	}
	if out["hmip_local_key"] != true {
		t.Errorf("the local key is set and not seen: %v", out)
	}
	ifs, _ := out["interfaces"].(map[string]any)
	want := map[string][2]any{"BidCos-RF": {2.0, true}, "HmIP-RF": {1.0, true}, "BidCos-Wired": {0.0, false}}
	for name, w := range want {
		p, _ := ifs[name].(map[string]any)
		if p["devices"] != w[0] || p["known"] != w[1] {
			t.Errorf("%s: %v, want %v", name, p, w)
		}
	}
	if p, _ := ifs["BidCos-Wired"].(map[string]any); p["error"] == "" {
		t.Errorf("a dead interface without a reason: %v", p)
	}
	// a user session sees nothing of it
	rig.role = auth.RoleUser
	if st, _ := rig.get(t, "/factory-reset"); st != 403 {
		t.Errorf("a user session: %d", st)
	}
}

// Without the interface hook the view still answers - with no interfaces to warn about.
func TestFactoryResetViewNoInterfaces(t *testing.T) {
	rig := newPowerRig(t, true)
	st, out := rig.get(t, "/factory-reset")
	ifs, _ := out["interfaces"].(map[string]any)
	if st != 200 || len(ifs) != 0 {
		t.Fatalf("view: %d %v", st, out)
	}
}

// The reset: the host name typed has to be the system's, the marker is set and the reboot asked
// for; a staged update refuses; a failed reboot takes the marker away again; nothing happens on a
// refusal.
func TestFactoryResetRoute(t *testing.T) {
	cases := []struct {
		name    string
		role    auth.Role
		body    string
		setup   func(rig *powerRig)
		status  int
		code    string
		armed   bool
		reboots int
	}{
		{"the system's name", auth.RoleAdmin, `{"confirm":true,"hostname":"lab-ccu"}`, nil, 200, "", true, 1},
		{"case and space do not matter", auth.RoleAdmin, `{"confirm":true,"hostname":" LAB-CCU "}`, nil, 200, "", true, 1},
		{"the FQDN typed for the short name", auth.RoleAdmin, `{"confirm":true,"hostname":"lab-ccu.lan.example"}`, nil, 200, "", true, 1},
		{"another name", auth.RoleAdmin, `{"confirm":true,"hostname":"lab-ccu2"}`, nil, 400, "hostname", false, 0},
		{"no name", auth.RoleAdmin, `{"confirm":true}`, nil, 400, "hostname", false, 0},
		{"no confirm", auth.RoleAdmin, `{"hostname":"lab-ccu"}`, nil, 400, "confirm", false, 0},
		{"not JSON", auth.RoleAdmin, `yes`, nil, 422, "invalid-body", false, 0},
		{"a user session", auth.RoleUser, `{"confirm":true,"hostname":"lab-ccu"}`, nil, 403, "forbidden", false, 0},
		{"an update is staged", auth.RoleAdmin, `{"confirm":true,"hostname":"lab-ccu"}`, func(rig *powerRig) {
			root := string(rig.root)
			_ = os.MkdirAll(root+"/usr/local/tmp", 0o755)
			_ = os.WriteFile(root+"/usr/local/tmp/openccu-lite-x86_64-ova-1.0.0.zip", []byte("zip"), 0o644)
			_ = os.Symlink(root+"/usr/local/tmp/openccu-lite-x86_64-ova-1.0.0.zip", root+"/usr/local/.firmwareUpdate")
		}, 409, "update-staged", false, 0},
		{"the reboot fails: the marker goes again", auth.RoleAdmin, `{"confirm":true,"hostname":"lab-ccu"}`, func(rig *powerRig) { rig.m.setErr(errors.New("no reboot command")) }, 500, "", false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rig := newPowerRig(t, true)
			_ = os.MkdirAll(string(rig.root)+"/etc/config", 0o755)
			_ = os.WriteFile(string(rig.root)+"/etc/config/netconfig", []byte("HOSTNAME=lab-ccu\n"), 0o644)
			rig.role = c.role
			if c.setup != nil {
				c.setup(rig)
			}
			st, e, out := rig.post(t, "/factory-reset", c.body)
			if st != c.status || (c.code != "" && e.Error != c.code) {
				t.Fatalf("got %d %q %v, want %d %q", st, e.Error, out, c.status, c.code)
			}
			if rig.resetArmed() != c.armed || rig.m.Reboots() != c.reboots || rig.armed() {
				t.Fatalf("armed %v, reboots %d, recovery %v; want %v, %d, false", rig.resetArmed(), rig.m.Reboots(), rig.armed(), c.armed, c.reboots)
			}
			if c.status == 200 && (out["reset"] != true || out["rebooting"] != true) {
				t.Fatalf("answer %v", out)
			}
			if c.code == "update-staged" && e.Detail["file"] != "openccu-lite-x86_64-ova-1.0.0.zip" {
				t.Errorf("the staged file is not named: %v", e.Detail)
			}
		})
	}
}

// The cancel: a marker set earlier goes, an unset one is no error, and the view says so.
func TestFactoryResetCancel(t *testing.T) {
	rig := newPowerRig(t, true)
	_ = os.MkdirAll(string(rig.root)+"/usr/local", 0o755)
	_ = os.WriteFile(string(rig.root)+"/usr/local/.doFactoryReset", nil, 0o644)
	if _, out := rig.get(t, "/factory-reset"); out["armed"] != true {
		t.Fatalf("the view does not see the marker: %v", out)
	}
	st, _, out := rig.do(t, "DELETE", "/factory-reset", "")
	if st != 200 || out["armed"] != false || rig.resetArmed() {
		t.Fatalf("cancel: %d %v, armed %v", st, out, rig.resetArmed())
	}
	if st, _, _ := rig.do(t, "DELETE", "/factory-reset", ""); st != 200 {
		t.Fatalf("cancel of nothing: %d", st)
	}
	rig.role = auth.RoleUser
	_ = os.WriteFile(string(rig.root)+"/usr/local/.doFactoryReset", nil, 0o644)
	if st, _, _ := rig.do(t, "DELETE", "/factory-reset", ""); st != 403 || !rig.resetArmed() {
		t.Fatalf("a user session cancelled: %d, armed %v", st, rig.resetArmed())
	}
}

func TestSameHostname(t *testing.T) {
	for _, c := range []struct {
		typed, host string
		want        bool
	}{
		{"ccu", "ccu", true}, {" CCU ", "ccu", true}, {"ccu.lan", "ccu", true}, {"ccu.lan", "ccu.lan", true},
		{"ccu", "ccu.lan", false}, {"ccu2", "ccu", false}, {"", "ccu", false}, {"ccu", "", false}, {".ccu", "ccu", false},
	} {
		if got := sameHostname(c.typed, c.host); got != c.want {
			t.Errorf("sameHostname(%q, %q) = %v", c.typed, c.host, got)
		}
	}
}
