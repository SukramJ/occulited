package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// openccu-lite B-158: an addon that keeps a daemon - learned from a unit that held a process, or
// declared by runtime.daemon - shows its empty, finished unit as ended; a prepare-only addon's
// stays Completed, a failed one stays failed.
func TestMarkEnded(t *testing.T) {
	r := fakeRoot(t)
	state := filepath.Join(t.TempDir(), "addon-daemons.json")
	var asked []string
	a := &SystemdAddons{Scripts: AddonScripts{Root: r}, Daemons: &AddonDaemons{Path: state},
		Journalctl: func(_ context.Context, args ...string) ([]byte, error) {
			asked = append(asked, strings.Join(args, " "))
			return []byte(`{"MESSAGE":"1790000000: Opening ipv4 listen socket on port 8883.","__REALTIME_TIMESTAMP":"1790000000000000"}
{"MESSAGE":[69,114,114],"__REALTIME_TIMESTAMP":"1790000000500000"}
{"MESSAGE":"1790000001: Error: Address in use","__REALTIME_TIMESTAMP":"1790000001000000"}
`), nil
		}}
	// hmm declares it; mosquitto is learned below; prep never had a process
	if err := r.writeAddonPolicy(&AddonPolicy{ID: "hmm", Mode: "confined", Runtime: &AddonRuntime{Daemon: true}}); err != nil {
		t.Fatal(err)
	}
	running := []Service{{ID: "addon-mosquitto", Kind: "addon", Script: "addon-mosquitto.service", Running: true, PID: 42}}
	a.markEnded(t.Context(), running)
	if !a.Daemons.Learned("mosquitto") {
		t.Fatal("a unit with a process teaches that the addon keeps a daemon")
	}
	finished := func(id string) Service {
		return Service{ID: "addon-" + id, Kind: "addon", Script: "addon-" + id + ".service", Running: true, OneShot: true, Result: "success"}
	}
	failed := finished("broken")
	failed.Failed, failed.Result, failed.Running = true, "exit-code", false
	stray := Service{ID: "addon-stray", Kind: "addon", Running: true, Stray: true}
	list := a.markEnded(t.Context(), []Service{finished("mosquitto"), finished("hmm"), finished("prep"), failed, stray, {ID: "rfd", Running: true, OneShot: true}})
	byID := map[string]Service{}
	for _, s := range list {
		byID[s.ID] = s
	}
	if m := byID["addon-mosquitto"]; !m.Ended || m.Running || m.EndedAt != "2026-09-21T14:13:21Z" || len(m.EndedLog) != 2 || m.EndedLog[1] != "1790000001: Error: Address in use" {
		t.Fatalf("mosquitto: %+v", m)
	}
	if h := byID["addon-hmm"]; !h.Ended {
		t.Fatalf("hmm declares runtime.daemon: %+v", h)
	}
	if p := byID["addon-prep"]; p.Ended || !p.Running || !p.OneShot {
		t.Fatalf("a prepare-only addon stays Completed: %+v", p)
	}
	if b := byID["addon-broken"]; b.Ended || !b.Failed {
		t.Fatalf("a failed unit stays failed: %+v", b)
	}
	if byID["addon-stray"].Ended || byID["rfd"].Ended {
		t.Fatal("a stray addon and a system unit are not ended")
	}
	if a.Daemons.Learned("stray") {
		t.Fatal("a process outside the unit teaches nothing")
	}
	// the journal once per ended unit within the TTL
	n := len(asked)
	a.markEnded(t.Context(), []Service{finished("mosquitto")})
	if len(asked) != n {
		t.Fatalf("the last lines are cached: %v", asked)
	}
	if !strings.Contains(asked[0], "-u addon-mosquitto.service") || !strings.Contains(asked[0], "-n 3") {
		t.Fatalf("journalctl args: %q", asked[0])
	}
	// learned survives a restart, and an uninstall forgets it
	again := &AddonDaemons{Path: state}
	if !again.Learned("mosquitto") || again.Learned("hmm") {
		t.Fatal("the learned daemons are kept in the state file")
	}
	again.Forget("mosquitto")
	if (&AddonDaemons{Path: state}).Learned("mosquitto") {
		t.Fatal("forgotten on disk")
	}
	if _, err := os.Stat(state + ".tmp"); err == nil {
		t.Fatal("no temporary file left")
	}
	var none *AddonDaemons
	none.Learn("x")
	none.Forget("x")
	if none.Learned("x") {
		t.Fatal("nil learns nothing")
	}
}

// the manifest's runtime.daemon reaches the stored block, and a merge keeps it
func TestRuntimeDaemonFromManifest(t *testing.T) {
	m, err := manifest.Parse([]byte(`{"format":1,"id":"mosquitto","name":"Mosquitto","runtime":{"daemon":true,"needs":[]}}`))
	if err != nil {
		t.Fatal(err)
	}
	rt := RuntimeFromManifest(m.Runtime)
	if rt == nil || !rt.Daemon {
		t.Fatalf("%+v", rt)
	}
	if !MergeRuntime(&AddonRuntime{}, rt).Daemon || !MergeRuntime(rt, &AddonRuntime{}).Daemon {
		t.Fatal("either side's daemon counts")
	}
}

// B-158: Start on an addon whose unit is active (exited) and empty runs it again (restart); a unit
// that is stopped is started.
func TestStartOfAnEndedAddonIsARestart(t *testing.T) {
	var calls []string
	state := map[string]string{"addon-mosquitto.service": "Type=oneshot\nActiveState=active\nSubState=exited\nTasksCurrent=0\n", "addon-hmm.service": "Type=oneshot\nActiveState=inactive\nSubState=dead\nTasksCurrent=[not set]\n"}
	s := SystemdServices{Root: fakeRoot(t), Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if args[0] == "show" {
			unit := args[len(args)-1]
			return []byte("Id=" + unit + "\n" + state[unit]), nil
		}
		return nil, nil
	}}
	if _, err := s.Control(t.Context(), "addon-mosquitto", "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Control(t.Context(), "addon-hmm", "start"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "systemctl restart --no-pager -- addon-mosquitto.service") || !strings.Contains(joined, "systemctl start --no-pager -- addon-hmm.service") {
		t.Fatalf("calls:\n%s", joined)
	}
}
