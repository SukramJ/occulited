package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/hmgroups"
	"github.com/hobbyquaker/occulited/internal/hmgroups/hmgroupstest"
	"github.com/hobbyquaker/occulited/internal/meta"
)

// groupsRig: a system API with task 180's groups on the fake hmipserver, and a metadata store
// that knows the two devices.
func groupsRig(t *testing.T) (*http.ServeMux, *hmgroupstest.Fake, *meta.Store, *hmgroups.Session) {
	t.Helper()
	s := hmgroups.NewSession()
	f := hmgroupstest.New(s.SID())
	t.Cleanup(f.Close)
	f.Devices = []hmgroupstest.Device{{ID: "KEQ9000003", Serial: "KEQ9000003", Type: "HM-Sec-SC"}, {ID: "00010000000A10:1", Serial: "00010000000A10:1", Type: "HmIP-eTRV"}}
	ms, _ := meta.New(nil, nil)
	for ref, name := range map[string]string{"BidCos-RF.KEQ9000003": "Fenster", "HmIP-RF.00010000000A10:1": "Ventil"} {
		if _, _, err := ms.PutObject(nil, ref, name, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	a := &SystemAPI{Groups: &GroupsAPI{Client: &hmgroups.Client{Base: f.URL(), Session: s}, Session: s, Meta: ms, Interface: "VirtualDevices"}}
	mux := http.NewServeMux()
	a.Register(mux)
	return mux, f, ms, s
}

func inGroup(t *testing.T, ms *meta.Store, ref string) any {
	t.Helper()
	o, err := ms.GetObject(ref)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(o.Meta["hmip"], &m)
	return m["inHeatingGroup"]
}

func TestGroupsRoutes(t *testing.T) {
	mux, f, ms, _ := groupsRig(t)
	const p = "/api/system/v1/groups"
	// the scopes are the middleware's: reads system:read, changes system:write, the CGI open
	scopes := RouteScopes()
	for pattern, want := range map[string]auth.Scope{"GET " + p: auth.ScopeSystemRead, "GET " + p + "/types": auth.ScopeSystemRead, "GET " + p + "/{id}": auth.ScopeSystemRead,
		"POST " + p: auth.ScopeSystemWrite, "PUT " + p + "/{id}": auth.ScopeSystemWrite, "DELETE " + p + "/{id}": auth.ScopeSystemWrite, "POST /api/homematic.cgi": scopeOpen} {
		if got := scopes[pattern]; len(got) != 1 || got[0] != want {
			t.Errorf("scopes of %s: %v, want %s", pattern, got, want)
		}
	}
	st, out := warnCall(t, mux, "GET", p, "", "monitor", user)
	if st != 200 || len(out["groups"].([]any)) != 0 || out["devices_to_configure"] == nil {
		t.Fatalf("list: %d %v", st, out)
	}
	st, out = warnCall(t, mux, "GET", p+"/types", "", "admin", auth.RoleAdmin)
	types := out["types"].([]any)
	if st != 200 || len(types) != 2 || len(types[0].(map[string]any)["assignable"].([]any)) != 2 {
		t.Fatalf("types: %d %v", st, out)
	}
	// the checks
	for body, want := range map[string]string{
		`{"name":"","type":"HomeMatic.heating"}`:                                "name",
		`{"name":"x"}`:                                                          "type",
		`{"name":"x","type":"nope"}`:                                            "not one hmipserver offers",
		`{"name":"x","type":"HomeMatic.heating","members":["a\"b"]}`:            "members",
		`{"name":"` + strings.Repeat("y", 65) + `","type":"HomeMatic.heating"}`: "name",
	} {
		if st, out := warnCall(t, mux, "POST", p, body, "admin", auth.RoleAdmin); st != 422 || !strings.Contains(out["message"].(string), want) {
			t.Errorf("POST %s: %d %v", body, st, out)
		}
	}
	if st, _ := warnCall(t, mux, "POST", p, `{`, "admin", auth.RoleAdmin); st != 422 {
		t.Errorf("bad body: %d", st)
	}
	// create: hmipserver's create then save, the members' flag and the group device's name
	st, out = warnCall(t, mux, "POST", p, `{"name":" Bad ","type":"HomeMatic.heating","members":["KEQ9000003"],"forbid_single_operation":true}`, "admin", auth.RoleAdmin)
	if st != 200 || out["id"] != float64(1) || out["name"] != "Bad" || out["device"] != "INT0000001" || out["ref"] != "VirtualDevices.INT0000001" || out["forbid_single_operation"] != true || len(out["members"].([]any)) != 1 || out["devices_to_configure"] == nil {
		t.Fatalf("create: %d %v", st, out)
	}
	if g := f.Groups[1]; g == nil || g.Name != "Bad" || g.DeviceName != "Bad INT0000001" || !g.ForbidSingle {
		t.Fatalf("stored: %+v", f.Groups[1])
	}
	if inGroup(t, ms, "BidCos-RF.KEQ9000003") != true || inGroup(t, ms, "HmIP-RF.00010000000A10:1") != nil {
		t.Fatalf("inHeatingGroup after create: %v %v", inGroup(t, ms, "BidCos-RF.KEQ9000003"), inGroup(t, ms, "HmIP-RF.00010000000A10:1"))
	}
	if o, err := ms.GetObject("VirtualDevices.INT0000001"); err != nil || o.Name != "Bad INT0000001" {
		t.Fatalf("group device object: %v %+v", err, o)
	}
	// the pending configuration comes with the answer
	f.Pending["INT0000001"] = []hmgroupstest.Device{{ID: "KEQ9000003", Serial: "KEQ9000003", Type: "HM-Sec-SC"}}
	st, out = warnCall(t, mux, "PUT", p+"/1", `{"name":"Schlafzimmer"}`, "admin", auth.RoleAdmin)
	if st != 200 || out["name"] != "Schlafzimmer" || len(out["members"].([]any)) != 1 || len(out["devices_to_configure"].([]any)) != 1 {
		t.Fatalf("rename: %d %v", st, out)
	}
	if o, _ := ms.GetObject("VirtualDevices.INT0000001"); o.Name != "Schlafzimmer INT0000001" {
		t.Fatalf("group device renamed: %+v", o)
	}
	// the members as a whole: the one that left loses the flag, the new one gets it
	st, out = warnCall(t, mux, "PUT", p+"/1", `{"members":["00010000000A10:1"]}`, "admin", auth.RoleAdmin)
	if st != 200 || out["name"] != "Schlafzimmer" || out["members"].([]any)[0].(map[string]any)["id"] != "00010000000A10:1" {
		t.Fatalf("members: %d %v", st, out)
	}
	if inGroup(t, ms, "BidCos-RF.KEQ9000003") != false || inGroup(t, ms, "HmIP-RF.00010000000A10:1") != true {
		t.Fatal("inHeatingGroup after the member change")
	}
	st, out = warnCall(t, mux, "GET", p+"/1", "", "monitor", user)
	if st != 200 || out["type"] != "HomeMatic.heating" || len(out["assignable"].([]any)) != 1 {
		t.Fatalf("get: %d %v", st, out)
	}
	if st, _ := warnCall(t, mux, "GET", p+"/x", "", "monitor", user); st != 400 {
		t.Errorf("bad id: %d", st)
	}
	// an unknown id is 404 from the list, never hmipserver's edit (which answers nothing for it)
	calls := len(f.Calls)
	if st, out := warnCall(t, mux, "GET", p+"/9", "", "monitor", user); st != 404 || out["error"] != "unknown-group" {
		t.Errorf("unknown group: %d %v", st, out)
	}
	if st, out := warnCall(t, mux, "PUT", p+"/9", `{"name":"x"}`, "admin", auth.RoleAdmin); st != 404 || out["error"] != "unknown-group" {
		t.Errorf("update unknown group: %d %v", st, out)
	}
	for _, c := range f.Calls[calls:] {
		if !strings.HasPrefix(c, "list ") {
			t.Errorf("an unknown id reached hmipserver: %s", c)
		}
	}
	if st, _ := warnCall(t, mux, "PUT", p+"/1", `{"name":""}`, "admin", auth.RoleAdmin); st != 200 {
		t.Errorf("an empty name keeps the old one: %d", st)
	}
	if st, _ := warnCall(t, mux, "PUT", p+"/1", `{"name":"\n"}`, "admin", auth.RoleAdmin); st != 422 {
		t.Errorf("a bad name: %d", st)
	}
	// delete: the former members' flag off, the object gone
	st, out = warnCall(t, mux, "DELETE", p+"/1", "", "admin", auth.RoleAdmin)
	if st != 200 || out["deleted"] != float64(1) || len(out["former_members"].([]any)) != 1 {
		t.Fatalf("delete: %d %v", st, out)
	}
	if inGroup(t, ms, "HmIP-RF.00010000000A10:1") != false {
		t.Fatal("inHeatingGroup after delete")
	}
	if _, err := ms.GetObject("VirtualDevices.INT0000001"); err == nil {
		t.Fatal("the group device's object must go")
	}
	if st, out := warnCall(t, mux, "DELETE", p+"/1", "", "admin", auth.RoleAdmin); st != 404 || out["error"] != "unknown-group" {
		t.Errorf("delete again: %d %v", st, out)
	}
	// hmipserver down: 502 with its word
	f.Close()
	if st, out := warnCall(t, mux, "GET", p, "", "monitor", user); st != 502 || out["error"] != "hmipserver" {
		t.Errorf("down: %d %v", st, out)
	}
}

func TestGroupsUnsupported(t *testing.T) {
	mux := http.NewServeMux()
	(&SystemAPI{}).Register(mux)
	for _, c := range [][2]string{{"GET", "/api/system/v1/groups"}, {"GET", "/api/system/v1/groups/types"}, {"GET", "/api/system/v1/groups/1"}, {"POST", "/api/system/v1/groups"}, {"PUT", "/api/system/v1/groups/1"}, {"DELETE", "/api/system/v1/groups/1"}} {
		if st, out := warnCall(t, mux, c[0], c[1], `{}`, "admin", auth.RoleAdmin); st != 501 || out["error"] != "unsupported" {
			t.Errorf("%s %s: %d %v", c[0], c[1], st, out)
		}
	}
	// the session check without groups: access denied, never a panic
	req := httptest.NewRequest("POST", "/api/homematic.cgi", strings.NewReader(`{"method":"Event.poll","params":{"_session_id_":"x"}}`))
	req.RemoteAddr = "127.0.0.1:5"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "access denied") {
		t.Fatalf("cgi without groups: %d %s", w.Code, w.Body.String())
	}
}

// the session check hmipserver makes before every group command
func TestHomematicCGI(t *testing.T) {
	mux, _, _, s := groupsRig(t)
	call := func(body, remote, forwarded string) (int, string) {
		req := httptest.NewRequest("POST", "/api/homematic.cgi", strings.NewReader(body))
		req.RemoteAddr = remote
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	live := `{"method":"Event.poll","params":{"_session_id_":"` + s.SID() + `"}}`
	if st, b := call(live, "127.0.0.1:5", ""); st != 200 || b != `{"error":null,"result":[],"version":"1.1"}` {
		t.Fatalf("live session: %d %s", st, b)
	}
	// through lighttpd: the forwarded address counts
	if st, b := call(live, "127.0.0.1:5", "127.0.0.1"); st != 200 || !strings.Contains(b, `"result":[]`) {
		t.Fatalf("forwarded loopback: %d %s", st, b)
	}
	if st, b := call(live, "127.0.0.1:5", "192.168.1.9"); st != 404 || !strings.Contains(b, "not-found") {
		t.Fatalf("forwarded from the LAN: %d %s", st, b)
	}
	if st, b := call(live, "192.0.2.1:1234", ""); st != 404 {
		t.Fatalf("from the LAN: %d %s", st, b)
	}
	// B-230: a client-sent X-Forwarded-For comes before lighttpd's element and is never the address
	if st, b := call(live, "127.0.0.1:5", "127.0.0.1, 192.168.1.9"); st != 404 {
		t.Fatalf("a forged loopback through lighttpd: %d %s", st, b)
	}
	if st, b := call(live, "192.0.2.1:1234", "127.0.0.1"); st != 404 {
		t.Fatalf("a forged loopback from the LAN directly: %d %s", st, b)
	}
	for _, body := range []string{
		`{"method":"Event.poll","params":{"_session_id_":"WRONGSID"}}`,
		`{"method":"Session.login","params":{"_session_id_":"` + s.SID() + `"}}`,
		`{"method":"Event.poll"}`,
		`not json`,
	} {
		st, b := call(body, "127.0.0.1:5", "")
		if st != 200 || !strings.Contains(b, `"result":null`) || !strings.Contains(b, `"code":400`) || !strings.Contains(b, "access denied") {
			t.Errorf("%s: %d %s", body, st, b)
		}
	}
}
