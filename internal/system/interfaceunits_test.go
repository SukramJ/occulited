package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUnitOfInterface(t *testing.T) {
	for _, c := range []struct {
		name, url, unit string
	}{
		{"BidCos-RF", "xmlrpc_bin://127.0.0.1:32001", "rfd"},
		{"BidCos-Wired", "xmlrpc_bin://127.0.0.1:32000", "hs485d"},
		{"HmIP-RF", "xmlrpc://127.0.0.1:32010/", "hmipserver"},
		{"VirtualDevices", "xmlrpc://127.0.0.1:39292/groups", "hmipserver"},
		// a name the firmware does not write, served by a known port
		{"HmIP-Wired", "xmlrpc://127.0.0.1:2010", "hmipserver"},
		{"rf", "xmlrpc_bin://127.0.0.1:2001", "rfd"},
		{"wired-tls", "xmlrpc_bin://127.0.0.1:42000", "hs485d"},
		// an addon's own interface (CUxD) is no unit of the radio stack
		{"CUxD", "xmlrpc_bin://127.0.0.1:8701", ""},
		{"broken", "not a url", ""},
	} {
		if got := UnitOfInterface(Interface{Name: c.name, URL: c.url}); got != c.unit {
			t.Errorf("%s %s: %q, want %q", c.name, c.url, got, c.unit)
		}
	}
}

func TestUnitStarting(t *testing.T) {
	for _, c := range []struct {
		active, sub, job string
		starting, queued bool
	}{
		{"activating", "start", "", true, false},
		{"activating", "start-pre", "12", true, false},
		{"activating", "start-post", "", true, false},
		// a crash loop waiting for its next try is not a start in progress
		{"activating", "auto-restart", "", false, false},
		{"activating", "auto-restart-queued", "", false, false},
		// rfd waiting for multimacd: a start job, nothing begun yet
		{"inactive", "dead", "31", true, true},
		{"inactive", "dead", "", false, false},
		{"inactive", "dead", "0", false, false},
		{"active", "running", "", false, false},
		{"active", "running", "40", false, false}, // a stop or restart job queued on a running unit
		{"deactivating", "stop-sigterm", "", false, false},
		{"failed", "failed", "", false, false},
	} {
		starting, queued := unitStarting(c.active, c.sub, c.job)
		if starting != c.starting || queued != c.queued {
			t.Errorf("%s/%s job %q: %v/%v, want %v/%v", c.active, c.sub, c.job, starting, queued, c.starting, c.queued)
		}
	}
}

// one `systemctl show` for the whole stack; the ages from the monotonic stamps against the uptime
func TestInterfaceUnits(t *testing.T) {
	r := rootWith(t, map[string]string{"proc/uptime": "100.50 90.00\n"})
	blocks := map[string]string{
		"occu-init-rf-hardware.service": "LoadState=loaded\nActiveState=active\nSubState=exited\nJob=\nInactiveExitTimestampMonotonic=60000000\nActiveEnterTimestampMonotonic=64000000",
		"multimacd.service":             "LoadState=loaded\nActiveState=active\nSubState=running\nJob=\nInactiveExitTimestampMonotonic=66000000\nActiveEnterTimestampMonotonic=70500000",
		// hmipserver's JVM: activating since 77.5 s, 23 s ago
		"hmipserver.service": "LoadState=loaded\nActiveState=activating\nSubState=start\nJob=58\nInactiveExitTimestampMonotonic=77500000\nActiveEnterTimestampMonotonic=0",
		// rfd waits for what it is ordered after
		"rfd.service":     "LoadState=loaded\nActiveState=inactive\nSubState=dead\nJob=57\nInactiveExitTimestampMonotonic=0\nActiveEnterTimestampMonotonic=0",
		"hs485d.service":  "LoadState=loaded\nActiveState=inactive\nSubState=dead\nJob=\nInactiveExitTimestampMonotonic=0\nActiveEnterTimestampMonotonic=0",
		"hmlangw.service": "LoadState=not-found\nActiveState=inactive\nSubState=dead\nJob=",
	}
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name != "systemctl" || args[0] != "show" {
			return nil, errors.New("unexpected")
		}
		var out []string
		for i, a := range args {
			if a == "--" {
				for _, u := range args[i+1:] {
					out = append(out, "Id="+u+"\n"+blocks[u]+"\n")
				}
			}
		}
		return []byte(strings.Join(out, "\n")), nil
	}
	ifs := []Interface{
		{Name: "BidCos-RF", URL: "xmlrpc_bin://127.0.0.1:32001"},
		{Name: "HmIP-RF", URL: "xmlrpc://127.0.0.1:32010"},
		{Name: "VirtualDevices", URL: "xmlrpc://127.0.0.1:39292/groups"},
		{Name: "CUxD", URL: "xmlrpc_bin://127.0.0.1:8701"},
	}
	units := SystemdServices{Root: r, Run: run}.InterfaceUnits(context.Background(), ifs)
	if len(calls) != 1 || !strings.Contains(calls[0], "-- occu-init-rf-hardware.service multimacd.service rfd.service hmipserver.service hs485d.service hmlangw.service") || !strings.Contains(calls[0], "Job,InactiveExitTimestampMonotonic") {
		t.Fatalf("calls: %v", calls)
	}
	by := map[string]InterfaceUnit{}
	var order []string
	for _, u := range units {
		by[u.Unit] = u
		order = append(order, u.Unit)
	}
	// boot order; hmlangw is not on this box
	if strings.Join(order, " ") != "occu-init-rf-hardware multimacd rfd hmipserver hs485d" {
		t.Errorf("units %v", order)
	}
	if u := by["hmipserver"]; !u.Starting || u.Queued || u.StartingS != 23 || fmt.Sprint(u.Interfaces) != "[HmIP-RF VirtualDevices]" {
		t.Errorf("hmipserver %+v", u)
	}
	if u := by["rfd"]; !u.Starting || !u.Queued || u.StartingS != 0 || fmt.Sprint(u.Interfaces) != "[BidCos-RF]" {
		t.Errorf("rfd %+v", u)
	}
	if u := by["multimacd"]; u.Starting || u.ActiveFor != 30*time.Second || len(u.Interfaces) != 0 {
		t.Errorf("multimacd %+v", u)
	}
	if u := by["hs485d"]; u.Starting || u.ActiveFor != 0 || len(u.Interfaces) != 0 {
		t.Errorf("hs485d %+v", u)
	}
	// the wire: interfaces always a list, starting_s only while it counts, the active age not at all
	b, _ := json.Marshal(by["rfd"])
	if string(b) != `{"unit":"rfd","interfaces":["BidCos-RF"],"active_state":"inactive","sub_state":"dead","starting":true,"queued":true}` {
		t.Errorf("json %s", b)
	}
	b, _ = json.Marshal(by["multimacd"])
	if string(b) != `{"unit":"multimacd","interfaces":[],"active_state":"active","sub_state":"running"}` {
		t.Errorf("json %s", b)
	}

	// systemd does not answer: nothing, not a list of stopped units
	failing := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no bus") }
	if units := (SystemdServices{Root: r, Run: failing}).InterfaceUnits(context.Background(), ifs); units != nil {
		t.Errorf("without systemd: %+v", units)
	}
}

// the Services page: an activating unit is starting, with its age; a crash loop is not
func TestServicesStarting(t *testing.T) {
	r := rootWith(t, map[string]string{"proc/uptime": "100.00 90.00\n"})
	rows := `[{"unit":"hmipserver.service","load":"loaded","active":"activating","sub":"start","description":"HmIP"},
{"unit":"rfd.service","load":"loaded","active":"activating","sub":"auto-restart","description":"rfd"},
{"unit":"addon-mosquitto.service","load":"loaded","active":"activating","sub":"start","description":"Addon mosquitto"},
{"unit":"multimacd.service","load":"loaded","active":"active","sub":"running","description":"multimacd"}]`
	props := map[string]string{
		"hmipserver.service":      "Type=forking\nInactiveExitTimestampMonotonic=58000000",
		"rfd.service":             "Type=simple\nInactiveExitTimestampMonotonic=20000000\nResult=exit-code",
		"addon-mosquitto.service": "Type=oneshot\nTasksCurrent=1\nInactiveExitTimestampMonotonic=",
		"multimacd.service":       "Type=simple\nMainPID=1\nInactiveExitTimestampMonotonic=30000000\nActiveEnterTimestampMonotonic=32000000",
	}
	var showArgs string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch args[0] {
		case "list-units":
			return []byte(rows), nil
		case "show":
			showArgs = strings.Join(args, " ")
			var out []string
			for i, a := range args {
				if a == "--" {
					for _, u := range args[i+1:] {
						out = append(out, "Id="+u+"\n"+props[u]+"\n")
					}
				}
			}
			return []byte(strings.Join(out, "\n")), nil
		}
		return nil, errors.New("unexpected " + args[0])
	}
	list, err := SystemdServices{Root: r, Run: run}.List()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(showArgs, "InactiveExitTimestampMonotonic") {
		t.Errorf("show does not ask for the start stamp: %s", showArgs)
	}
	by := map[string]Service{}
	for _, sv := range list {
		by[sv.ID] = sv
	}
	if sv := by["hmipserver"]; !sv.Starting || sv.StartingS != 42 || sv.Running || sv.Failed {
		t.Errorf("hmipserver %+v", sv)
	}
	if sv := by["rfd"]; sv.Starting || sv.StartingS != 0 {
		t.Errorf("a unit waiting for its automatic restart is not starting: %+v", sv)
	}
	// an addon's start script still running: starting, without an age systemd did not give
	if sv := by["addon-mosquitto"]; !sv.Starting || sv.StartingS != 0 || sv.Running {
		t.Errorf("addon-mosquitto %+v", sv)
	}
	if sv := by["multimacd"]; sv.Starting || !sv.Running {
		t.Errorf("multimacd %+v", sv)
	}
	b, _ := json.Marshal(by["hmipserver"])
	if !strings.Contains(string(b), `"starting":true,"starting_s":42`) {
		t.Errorf("json %s", b)
	}
	if b, _ := json.Marshal(by["multimacd"]); strings.Contains(string(b), "starting") {
		t.Errorf("json %s", b)
	}
}
