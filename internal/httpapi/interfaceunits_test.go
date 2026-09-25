package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/health"
	"github.com/hobbyquaker/occulited/internal/system"
)

// unitsServices is a service manager that answers for the radio stack's units, as SystemdServices does.
type unitsServices struct {
	scriptBox
	calls int
	ifs   []system.Interface
	units []system.InterfaceUnit
}

func (u *unitsServices) InterfaceUnits(_ context.Context, ifs []system.Interface) []system.InterfaceUnit {
	u.calls++
	u.ifs = ifs
	return u.units
}

// task 94: GET /radio/health carries the units; hmipserver starting is there with its age, and the
// list of interfaces it was read against is InterfacesList.xml at the request
func TestRadioHealthUnits(t *testing.T) {
	r := fakeRoot(t)
	list := filepath.Join(string(r), "etc/config/InterfacesList.xml")
	if err := os.MkdirAll(filepath.Dir(list), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(list, []byte("<interfaces><ipc><name>HmIP-RF</name><url>xmlrpc://127.0.0.1:32010</url><info>HmIP-RF</info></ipc></interfaces>"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := &unitsServices{units: []system.InterfaceUnit{
		{Unit: "multimacd", Interfaces: []string{}, ActiveState: "active", SubState: "running"},
		{Unit: "hmipserver", Interfaces: []string{"HmIP-RF", "VirtualDevices"}, ActiveState: "activating", SubState: "start", Starting: true, StartingS: 23},
	}}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r, Services: svc, Health: &health.Sampler{}}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/radio/health", "", nil)
	if st != 200 {
		t.Fatalf("%d %v", st, out)
	}
	units, _ := out["units"].([]any)
	if len(units) != 2 {
		t.Fatalf("units %v", out["units"])
	}
	hmip := units[1].(map[string]any)
	if hmip["unit"] != "hmipserver" || hmip["starting"] != true || hmip["starting_s"] != 23.0 || hmip["active_state"] != "activating" {
		t.Errorf("hmipserver %v", hmip)
	}
	if len(svc.ifs) != 1 || svc.ifs[0].Name != "HmIP-RF" {
		t.Errorf("read against %+v", svc.ifs)
	}
	// a second request within the cache's time does not ask systemd again
	do(t, srv, "GET", "/api/system/v1/radio/health", "", nil)
	if svc.calls != 1 {
		t.Errorf("%d readings for two requests", svc.calls)
	}

	// a busybox box: no units at all
	mux2 := http.NewServeMux()
	(&SystemAPI{Root: r, Services: scriptBox{system.AddonScripts{Root: r}}, Health: &health.Sampler{}}).Register(mux2)
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	if _, out, _ := do(t, srv2, "GET", "/api/system/v1/radio/health", "", nil); out["units"] != nil {
		t.Errorf("busybox: %v", out["units"])
	}
}

// the cache hands out a reading with the starting ages moved on, so a counter never runs backwards
func TestRadioUnitsCacheAges(t *testing.T) {
	svc := &unitsServices{units: []system.InterfaceUnit{
		{Unit: "hmipserver", Starting: true, StartingS: 10},
		{Unit: "rfd", Starting: true, Queued: true},
		{Unit: "multimacd", ActiveState: "active", ActiveFor: time.Second},
	}}
	a := &SystemAPI{Root: fakeRoot(t), Services: svc}
	if u := a.radioUnits(context.Background()); u[0].StartingS != 10 {
		t.Fatalf("%+v", u)
	}
	a.units.mu.Lock()
	a.units.at = a.units.at.Add(-2 * time.Second)
	a.units.mu.Unlock()
	u := a.radioUnits(context.Background())
	if svc.calls != 1 {
		t.Errorf("%d readings", svc.calls)
	}
	if u[0].StartingS != 12 || u[1].StartingS != 0 || u[2].ActiveFor < 3*time.Second {
		t.Errorf("aged %+v", u)
	}
	// the cached copy itself is not changed
	if svc.units[0].StartingS != 10 || a.units.units[0].StartingS != 10 {
		t.Errorf("the reading was aged in place: %+v", a.units.units)
	}
	// past the cache's time: read again
	a.units.mu.Lock()
	a.units.at = a.units.at.Add(-unitsTTL)
	a.units.mu.Unlock()
	a.radioUnits(context.Background())
	if svc.calls != 2 {
		t.Errorf("%d readings after the cache ran out", svc.calls)
	}
}

// an error recorded before the interface's unit became active is the start, not a failure
func TestStaleErrors(t *testing.T) {
	st := &health.Status{Polled: time.Now().Add(-30 * time.Second), Errors: map[string]string{"HmIP-RF": "connection refused", "BidCos-RF": "connection refused", "BidCos-Wired": "no answer"}}
	units := []system.InterfaceUnit{
		// active 10 s, polled 30 s ago: the error is from its start
		{Unit: "hmipserver", Interfaces: []string{"HmIP-RF", "VirtualDevices"}, ActiveState: "active", ActiveFor: 10 * time.Second},
		// active for a minute, and still refused at the poll: a real error
		{Unit: "rfd", Interfaces: []string{"BidCos-RF"}, ActiveState: "active", ActiveFor: time.Minute},
		// starting: the page says so, the error stays for it to replace
		{Unit: "hs485d", Interfaces: []string{"BidCos-Wired"}, ActiveState: "activating", Starting: true},
	}
	if !staleErrors(st, units) {
		t.Error("nothing dropped")
	}
	if _, ok := st.Errors["HmIP-RF"]; ok || st.Errors["BidCos-RF"] == "" || st.Errors["BidCos-Wired"] == "" {
		t.Errorf("errors %v", st.Errors)
	}
	if staleErrors(st, units) {
		t.Error("dropped twice")
	}
	if staleErrors(&health.Status{Errors: map[string]string{"HmIP-RF": "x"}}, units) {
		t.Error("no poll yet, nothing is stale")
	}
}
