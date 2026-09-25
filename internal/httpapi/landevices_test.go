package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/eq3disc"
	"github.com/hobbyquaker/occulited/internal/system"
)

type fakeLAN struct {
	mu        sync.Mutex
	scans     int
	devices   map[string]*eq3disc.Found
	set       []eq3disc.Config
	passwords []string
	to        []netip.Addr
	err       error
	keepDHCP  bool // like the lab's HAP-B1 and HMW-LGW: the addresses taken, DHCP left on
}

func (f *fakeLAN) Scan(context.Context) ([]eq3disc.Found, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans++
	out := []eq3disc.Found{}
	for _, s := range []string{"LEQ0636432", "30150377DC0003DB3393B323", "4118f6a32"} {
		out = append(out, *f.devices[s])
	}
	return out, nil
}

func (f *fakeLAN) Lookup(_ context.Context, serial string) (*eq3disc.Found, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devices[serial]
	if !ok {
		return nil, eq3disc.ErrNoAnswer
	}
	c := *d
	return &c, nil
}

func (f *fakeLAN) Configure(_ context.Context, to netip.Addr, _, serial string, cfg eq3disc.Config, password string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	f.set, f.passwords, f.to = append(f.set, cfg), append(f.passwords, password), append(f.to, to)
	// the device takes it: what it runs with afterwards
	d := f.devices[serial]
	c := cfg
	if f.keepDHCP {
		c.DHCP = true
	}
	d.Config = &c
	if !cfg.DHCP {
		r := cfg.Addresses
		d.Runtime = &r
	}
	return true, nil
}

type restarts struct {
	mu  sync.Mutex
	ids []string
}

func (r *restarts) List() ([]system.Service, error) { return nil, nil }
func (r *restarts) Control(_ context.Context, id, action string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, id+" "+action)
	return "", nil
}

func lanRig(t *testing.T) (*fakeLAN, *restarts, func(role auth.Role, method, path, body string) (int, map[string]any), system.Root) {
	t.Helper()
	root := fakeRoot(t)
	_ = os.MkdirAll(filepath.Join(string(root), "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(string(root), "etc/config/hs485d.conf"), []byte("[Interface 0]\nType = HMWLGW\nName = Wired\nSerial Number = LEQ0636432\nEncryption Key = gwkey123\nIP Address = 192.0.2.116\n\n"), 0o644)
	cfg := func(ip string, dhcp bool) *eq3disc.Config {
		return &eq3disc.Config{Addresses: eq3disc.Addresses{IP: ip, Gateway: "192.168.1.1", Netmask: "255.255.255.0"}, DHCP: dhcp, AutoIP: true, Crypt: 1, Name: "n"}
	}
	run := func(ip string) *eq3disc.Addresses {
		return &eq3disc.Addresses{IP: ip, Gateway: "192.0.2.1", Netmask: "255.255.255.0"}
	}
	f := &fakeLAN{devices: map[string]*eq3disc.Found{
		"LEQ0636432":               {Type: "eQ3-HMW-LGW-App", Serial: "LEQ0636432", IP: "192.0.2.116", ProtocolVersion: 2, Runtime: run("192.0.2.116"), Config: cfg("192.168.1.223", true)},
		"30150377DC0003DB3393B323": {Type: "eQ3-HMIP-HAP-App", Serial: "30150377DC0003DB3393B323", IP: "192.0.2.155", ProtocolVersion: 4, Runtime: run("192.0.2.155"), Config: cfg("192.168.1.224", true)},
		"4118f6a32":                {Type: "eQ3-HmIP-CCU3-App", Serial: "4118f6a32", IP: "192.0.2.230", ProtocolVersion: 2},
	}}
	svc := &restarts{}
	inUse := map[string]bool{"192.0.2.50": true}
	api := &SystemAPI{Root: root, LAN: f, Services: svc,
		LANNetworks:  func() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} },
		AddressInUse: func(_ context.Context, ip netip.Addr) bool { return inUse[ip.String()] }}
	oldAfter, oldFor := lanRefindAfter, lanRefindFor
	lanRefindAfter, lanRefindFor = 10*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { lanRefindAfter, lanRefindFor = oldAfter, oldFor })
	mux := http.NewServeMux()
	api.Register(mux)
	role := auth.RoleAdmin
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mux.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "admin", Role: role, Scopes: auth.RoleScopes(role)})))
	}))
	t.Cleanup(srv.Close)
	do := func(r auth.Role, method, path, body string) (int, map[string]any) {
		t.Helper()
		role = r
		req, _ := http.NewRequest(method, srv.URL+"/api/system/v1"+path, strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		return res.StatusCode, m
	}
	return f, svc, do, root
}

func TestLANDevicesFind(t *testing.T) {
	f, _, do, _ := lanRig(t)
	// task 237: opening the page sends nothing - the GET before any search answers an empty list
	// without a time, and no scan ran
	st, out := do(auth.RoleUser, "GET", "/radio/lan-devices", "")
	if st != 200 || out["scanned"] != nil || len(out["devices"].([]any)) != 0 || f.scans != 0 {
		t.Fatalf("before a search: %d %v (scans %d)", st, out, f.scans)
	}
	st, out = do(auth.RoleUser, "POST", "/radio/lan-devices/search", "")
	if st != 200 || out["scanned"] == nil {
		t.Fatalf("%d %v", st, out)
	}
	devs, _ := out["devices"].([]any)
	if len(devs) != 3 {
		t.Fatalf("%v", out)
	}
	byKind := map[string]map[string]any{}
	for _, d := range devs {
		m := d.(map[string]any)
		byKind[m["kind"].(string)] = m
	}
	gw, ap, ccu := byKind["gateway"], byKind["access-point"], byKind["ccu"]
	if gw["configured"] != "wired" || gw["name"] != "Wired" || gw["password"] != "configured" || gw["writable"] != true || gw["same_subnet"] != true {
		t.Errorf("gateway %v", gw)
	}
	if ap["password"] != "sticker" || ap["writable"] != true || ap["protocol_version"] != 4.0 || ap["paired"] != nil {
		t.Errorf("access point %v", ap)
	}
	if ccu["writable"] != false || ccu["link"] != "http://192.0.2.230/" || ccu["runtime"] != nil {
		t.Errorf("ccu %v", ccu)
	}
	if strings.Contains(strings.ToLower(jsonOf(out)), "gwkey123") {
		t.Error("the gateway key left the system")
	}
	// the GET serves the last search, however old, and never scans - not with ?refresh=1 either;
	// only the POST does
	if _, again := do(auth.RoleUser, "GET", "/radio/lan-devices?refresh=1", ""); len(again["devices"].([]any)) != 3 || again["scanned"] != out["scanned"] {
		t.Errorf("the cached answer %v", again)
	}
	do(auth.RoleUser, "POST", "/radio/lan-devices/search", "")
	if f.scans != 2 {
		t.Errorf("scans %d", f.scans)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestLANDevicesNetwork(t *testing.T) {
	f, svc, do, root := lanRig(t)
	hap := "/radio/lan-devices/30150377DC0003DB3393B323/network"
	static := `{"dhcp":false,"ip":"192.0.2.160","netmask":"255.255.255.0","gateway":"192.0.2.1","dns1":"192.0.2.1","password":"stickerpw"}`

	// a user's session lacks system:write: the middleware refuses it (TestRouteTable)
	if st, out := do(auth.RoleAdmin, "POST", "/radio/lan-devices/4118f6a32/network", `{"dhcp":true}`); st != 403 || out["error"] != "not-writable" {
		t.Errorf("another CCU: %d %v", st, out)
	}
	if st, _ := do(auth.RoleAdmin, "POST", "/radio/lan-devices/NOPE/network", `{"dhcp":true}`); st != 404 {
		t.Errorf("unknown: %d", st)
	}
	for _, bad := range []string{
		`{"ip":"192.0.2.160","netmask":"255.0.255.0"}`,
		`{"ip":"192.0.2.0","netmask":"255.255.255.0"}`,
		`{"ip":"192.0.2.255","netmask":"255.255.255.0"}`,
		`{"ip":"192.0.2.160","netmask":"255.255.255.0","gateway":"10.0.0.1"}`,
		`{"ip":"::1","netmask":"255.255.255.0"}`,
		`{"ip":"192.0.2.160","netmask":"255.255.255.0","dns1":"x"}`,
		`{"ip":"192.0.2.160","netmask":"x"}`,
	} {
		if st, out := do(auth.RoleAdmin, "POST", hap, bad); st != 422 {
			t.Errorf("%s: %d %v", bad, st, out)
		}
	}
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"ip":"10.1.2.3","netmask":"255.255.255.0"}`); st != 409 || out["error"] != "other-subnet" {
		t.Errorf("other subnet: %d %v", st, out)
	}
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"ip":"192.0.2.50","netmask":"255.255.255.0"}`); st != 409 || out["error"] != "address-in-use" {
		t.Errorf("in use: %d %v", st, out)
	}
	// the gateway's address is another device's (from the last scan)
	do(auth.RoleUser, "POST", "/radio/lan-devices/search", "")
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"ip":"192.0.2.116","netmask":"255.255.255.0"}`); st != 409 || out["error"] != "address-in-use" {
		t.Errorf("another device's: %d %v", st, out)
	}
	if len(f.set) != 0 {
		t.Fatalf("written on a refusal: %v", f.set)
	}

	// the access point: static, with its sticker password, found again with the new address
	st, out := do(auth.RoleAdmin, "POST", hap, static)
	if st != 200 || out["written"] != true {
		t.Fatalf("%d %v", st, out)
	}
	if dev, _ := out["device"].(map[string]any); dev == nil || dev["runtime"].(map[string]any)["ip"] != "192.0.2.160" {
		t.Errorf("not found again: %v", out)
	}
	if f.passwords[0] != "stickerpw" || f.set[0].DHCP || f.set[0].IP != "192.0.2.160" || !f.set[0].AutoIP || f.set[0].Name != "n" || f.to[0].String() != "192.0.2.155" {
		t.Errorf("%+v %v %v", f.set[0], f.passwords, f.to)
	}
	// the other subnet, confirmed: sent by broadcast, since it may not be reachable any more
	f.devices["30150377DC0003DB3393B323"].IP = "10.1.2.3"
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"password":"stickerpw"}`); st != 200 || f.to[1].String() != "255.255.255.255" || !f.set[1].DHCP {
		t.Errorf("back to DHCP from another subnet: %d %v %v", st, out, f.to)
	}

	// the configured gateway: its key from hs485d.conf, the file follows the new address, hs485d restarts
	st, out = do(auth.RoleAdmin, "POST", "/radio/lan-devices/LEQ0636432/network", `{"ip":"192.0.2.117","netmask":"255.255.255.0","gateway":"192.0.2.1"}`)
	if st != 200 || out["gateway_file"] != true || out["restarted"] != "hs485d" {
		t.Fatalf("%d %v", st, out)
	}
	if f.passwords[2] != "gwkey123" {
		t.Errorf("the configured key was not used: %v", f.passwords)
	}
	gws := root.ReadLANGateways(system.GatewayWired)
	if len(gws) != 1 || gws[0].Address != "192.0.2.117" || gws[0].Key() != "gwkey123" || gws[0].Name != "Wired" {
		t.Errorf("%+v", gws)
	}
	if len(svc.ids) != 1 || svc.ids[0] != "hs485d restart" {
		t.Errorf("%v", svc.ids)
	}

	// DHCP with the fallback addresses the device keeps beside it
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"ip":"192.168.1.224","netmask":"255.255.255.0","gateway":"192.168.1.1","password":"stickerpw"}`); st != 200 || !f.set[3].DHCP || f.set[3].IP != "192.168.1.224" || out["differs"] != nil {
		t.Errorf("DHCP with fallback: %d %v %+v", st, out, f.set[3])
	}
	if st, _ := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"ip":"192.168.1.224","netmask":"x","password":"stickerpw"}`); st != 422 {
		t.Errorf("a bad fallback: %d", st)
	}
	// a device that takes the addresses but keeps DHCP on: said, not claimed as done
	f.keepDHCP = true
	f.devices["30150377DC0003DB3393B323"].IP = "192.0.2.155"
	if st, out := do(auth.RoleAdmin, "POST", hap, static); st != 200 || jsonOf(out["differs"]) != `["dhcp"]` {
		t.Errorf("kept DHCP: %d %v", st, out)
	}
	f.keepDHCP = false
	// a wrong password is said as such
	f.err = eq3disc.ErrWrongPassword
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"password":"x"}`); st != 422 || out["error"] != "wrong-password" {
		t.Errorf("%d %v", st, out)
	}
	f.err = eq3disc.ErrNoAnswer
	if st, _ := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"password":"x"}`); st != 502 {
		t.Errorf("%d", st)
	}
	f.err = eq3disc.ErrRefused
	if st, out := do(auth.RoleAdmin, "POST", hap, `{"dhcp":true,"password":"x"}`); st != 502 || out["error"] != "refused" {
		t.Errorf("%d %v", st, out)
	}
	if st, _ := do(auth.RoleAdmin, "POST", hap, `{`); st != 400 {
		t.Errorf("%d", st)
	}
}

func TestInNetworksAndOwn(t *testing.T) {
	if inNetworks([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "x") || !inNetworks([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "10.1.1.1") {
		t.Error("inNetworks")
	}
	if !ownAddress(netip.MustParseAddr("127.0.0.1")) || ownAddress(netip.MustParseAddr("192.0.2.77")) {
		t.Error("ownAddress")
	}
	if lanKind("eQ3-HM-LGW-App") != "gateway" || lanKind("eQ3-HmIPW-DRAP-App") != "access-point" || lanKind("eQ3-Other") != "other" {
		t.Error("lanKind")
	}
	if len(localNetworks()) == 0 {
		t.Log("no IPv4 network here")
	}
	_ = addressInUse(context.Background(), netip.MustParseAddr("127.0.0.1"))
}
