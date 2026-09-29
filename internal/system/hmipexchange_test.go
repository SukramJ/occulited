package system

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

const exPrevSGTIN = "3014F711A0001F0000000A04" // the previous module, whose network is on the system

// lkFatal leaves the marker of a rejected exchange under the rig's root, as the ready step does.
func lkFatal(t *testing.T, root, cause string) string {
	t.Helper()
	p := filepath.Join(root, "run/occulite/radio/hmipserver.fatal")
	b, _ := json.Marshal(radio.HmIPFatal{Code: "adapter-exchange-rejected", Line: "Adapter exchange was rejected by key server.", Adapter: lkSGTIN, Cause: cause, At: time.Now()})
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// task 155 (D-106): the view tells the previous module from the one in use; the retry clears the
// marker and restarts; the fresh start moves the previous identity into a snapshot of its own,
// generates the keys when asked, clears the marker and restarts - and refuses when nothing is
// stopped or nothing is there to move
func TestExchangeRetryAndFreshStart(t *testing.T) {
	fake := &fakeHmIPServer{state: "ok"}
	k, svc, root := lkRig(t, fake.serve(t))
	data := filepath.Join(root, "etc/config/crRFD/data")
	// the rig's identity belongs to the module in use: without a previous one, nothing to move
	if err := k.FreshStart(false); err == nil || !strings.Contains(err.Error(), "not stopped") {
		t.Fatalf("no marker: %v", err)
	}
	if err := k.RetryExchange(); err == nil {
		t.Fatal("retry without a marker")
	}
	marker := lkFatal(t, root, radio.CauseRefused)
	if err := k.FreshStart(false); err == nil || !strings.Contains(err.Error(), "nothing to move aside") {
		t.Fatalf("no previous identity: %v", err)
	}
	// the previous module's network: its three files beside the rig's, in the case hmipserver uses
	for _, ext := range []string{".ap", ".apkx", ".bbkx"} {
		if err := os.WriteFile(filepath.Join(data, exPrevSGTIN+ext), []byte("OLD-"+ext), 0o664); err != nil {
			t.Fatal(err)
		}
	}
	v := k.Exchange()
	if v.Fatal == nil || v.Fatal.Cause != radio.CauseRefused || v.Module != lkSGTIN || len(v.Previous) != 1 || v.Previous[0] != exPrevSGTIN || v.LocalKey || len(v.ReplacesSnapshots) != 0 {
		t.Fatalf("view: %+v", v)
	}

	// the retry: the marker goes, hmipserver restarts, nothing else changes
	if err := k.RetryExchange(); err != nil {
		t.Fatal(err)
	}
	if st := lkWait(t, k); st.Error != "" {
		t.Fatalf("retry: %+v", st)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the marker survived the retry")
	}
	if !strings.Contains(strings.Join(svc.calls, " "), "hmipserver restart") {
		t.Errorf("no restart: %v", svc.calls)
	}
	if _, err := os.Stat(filepath.Join(data, exPrevSGTIN+".ap")); err != nil {
		t.Error("the retry touched the previous identity")
	}

	// a snapshot of the previous module is kept already: the view says a fresh start replaces it
	_ = os.MkdirAll(k.snapshotDir(exPrevSGTIN), 0o700)
	_ = os.WriteFile(filepath.Join(k.snapshotDir(exPrevSGTIN), "snapshot.json"), []byte(`{"sgtin":"`+exPrevSGTIN+`","at":"2026-09-01T00:00:00Z","files":["x"]}`), 0o600)
	_ = os.WriteFile(filepath.Join(k.snapshotDir(exPrevSGTIN), "x"), []byte("stale"), 0o600)
	lkFatal(t, root, radio.CauseRefused)
	if v = k.Exchange(); len(v.ReplacesSnapshots) != 1 || v.ReplacesSnapshots[0] != exPrevSGTIN {
		t.Fatalf("replaces: %+v", v)
	}

	// the fresh start with local key mode: the identity moved, the keys written, the marker gone,
	// HmIP-RF restarted
	svc.calls = nil
	if err := k.FreshStart(true); err != nil {
		t.Fatal(err)
	}
	st := lkWait(t, k)
	if st.Error != "" || !st.Enabled || st.Source != "generated" {
		t.Fatalf("after the fresh start: %+v", st)
	}
	for _, ext := range []string{".ap", ".apkx", ".bbkx"} {
		if _, err := os.Stat(filepath.Join(data, exPrevSGTIN+ext)); !os.IsNotExist(err) {
			t.Errorf("%s is still in the data directory", exPrevSGTIN+ext)
		}
		if b := readFile(filepath.Join(k.snapshotDir(exPrevSGTIN), exPrevSGTIN+ext)); b != "OLD-"+ext {
			t.Errorf("snapshot %s: %q", ext, b)
		}
		if b := readFile(filepath.Join(data, lkSGTIN+ext)); b != "EQ3-"+ext {
			t.Errorf("the module in use lost %s: %q", ext, b)
		}
	}
	if _, err := os.Stat(filepath.Join(k.snapshotDir(exPrevSGTIN), "x")); !os.IsNotExist(err) {
		t.Error("the stale snapshot was not replaced")
	}
	snaps := k.Snapshots()
	if len(snaps) != 1 || snaps[0].SGTIN != exPrevSGTIN || snaps[0].Kind != SnapshotFreshStart || len(snaps[0].Files) != 3 {
		t.Fatalf("snapshots: %+v", snaps)
	}
	if b := readFile(filepath.Join(k.snapshotDir(exPrevSGTIN), "hmip_user.conf")); !strings.Contains(b, "occulite.hmip.path=direct") || strings.Contains(b, "Network.Key") {
		t.Errorf("the snapshot's hmip_user.conf is not the one from before: %q", b)
	}
	conf := readFile(filepath.Join(root, "etc/config/crRFD/hmip_user.conf"))
	key := radio.ReadLocalKey(conf)
	if len(key.NetworkKey) != 32 || len(key.BackboneKey) != 32 || key.KeyServerMode != radio.KeyServerLocal {
		t.Fatalf("keys:\n%s", conf)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("the marker survived the fresh start")
	}
	if !strings.Contains(strings.Join(svc.calls, " "), "hmipserver restart") {
		t.Errorf("no restart: %v", svc.calls)
	}
	// the revert is blocked: the snapshot is another module's, as the status says
	if st.RevertBlocked == "" || !strings.Contains(st.RevertBlocked, exPrevSGTIN) {
		t.Errorf("revert: %q", st.RevertBlocked)
	}

	// a second fresh start with local key mode on already generates nothing new
	for _, ext := range []string{".ap"} {
		_ = os.WriteFile(filepath.Join(data, "3014F711A00000000000AAAA"+ext), []byte("OTHER"), 0o664)
	}
	lkFatal(t, root, radio.CauseRefused)
	if err := k.FreshStart(true); err != nil {
		t.Fatal(err)
	}
	if st = lkWait(t, k); st.Error != "" {
		t.Fatalf("second: %+v", st)
	}
	if after := radio.ReadLocalKey(readFile(filepath.Join(root, "etc/config/crRFD/hmip_user.conf"))); after.NetworkKey != key.NetworkKey {
		t.Error("the key was generated anew")
	}
	if len(k.Snapshots()) != 2 {
		t.Errorf("snapshots: %+v", k.Snapshots())
	}
}

// openccu-lite task 212: the way back without local key mode. The previous module is in use again
// and its fresh-start snapshot goes back: the files return to the data directory, the refused
// module's fresh identity moves into a snapshot of its own, the marker goes, HmIP-RF restarts, and
// the snapshot is consumed. Refused for a switch snapshot, a module not in use, local key mode on.
func TestRestoreFreshStartSnapshot(t *testing.T) {
	fake := &fakeHmIPServer{state: "ok"}
	k, svc, root := lkRig(t, fake.serve(t))
	data := filepath.Join(root, "etc/config/crRFD/data")
	for _, ext := range []string{".ap", ".apkx", ".bbkx"} {
		if err := os.WriteFile(filepath.Join(data, exPrevSGTIN+ext), []byte("OLD-"+ext), 0o664); err != nil {
			t.Fatal(err)
		}
	}
	// the fresh start with the refused module in use, local key mode off: the previous identity is a snapshot now
	lkFatal(t, root, radio.CauseRefused)
	if err := k.FreshStart(false); err != nil {
		t.Fatal(err)
	}
	if st := lkWait(t, k); st.Error != "" || st.Enabled {
		t.Fatalf("fresh start: %+v", st)
	}
	// the refusals: not an SGTIN, no snapshot, the module not in use
	for _, tc := range []struct{ sgtin, want string }{
		{"nope", "not an SGTIN"},
		{"3014F711A00000000000AAAA", "no snapshot of"},
		{exPrevSGTIN, "is not in use (" + lkSGTIN + " is)"},
	} {
		if err := k.RestoreSnapshot(tc.sgtin); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", tc.sgtin, err)
		}
	}
	// a switch snapshot of the module in use is Disable's, not this
	_ = os.MkdirAll(k.snapshotDir(lkSGTIN), 0o700)
	_ = os.WriteFile(filepath.Join(k.snapshotDir(lkSGTIN), "snapshot.json"), []byte(`{"sgtin":"`+lkSGTIN+`","at":"2026-09-01T00:00:00Z","files":["`+lkSGTIN+`.ap"]}`), 0o600)
	if err := k.RestoreSnapshot(lkSGTIN); err == nil || !strings.Contains(err.Error(), "from a switch to local key mode") {
		t.Errorf("switch snapshot: %v", err)
	}
	_ = os.RemoveAll(k.snapshotDir(lkSGTIN))

	// the old module is back: the plan names it
	plan := filepath.Join(root, "run/occulite/radio/plan.json")
	b, _ := os.ReadFile(plan)
	_ = os.WriteFile(plan, []byte(strings.ReplaceAll(string(b), lkSGTIN, exPrevSGTIN)), 0o644)
	// with local key mode on the way back is Disable's
	confPath := filepath.Join(root, "etc/config/crRFD/hmip_user.conf")
	conf, _ := os.ReadFile(confPath)
	_ = os.WriteFile(confPath, append(append([]byte{}, conf...), []byte("Network.Key=00112233445566778899AABBCCDDEEFF\n")...), 0o664)
	if err := k.RestoreSnapshot(exPrevSGTIN); err == nil || !strings.Contains(err.Error(), "local key mode is on") {
		t.Errorf("local key on: %v", err)
	}
	_ = os.WriteFile(confPath, conf, 0o664)
	// the status carries the system's name, which the page has typed as the confirmation
	_ = os.WriteFile(filepath.Join(root, "etc/config/netconfig"), []byte("HOSTNAME=lite-test\n"), 0o644)
	if st := k.Status(); st.Hostname != "lite-test" {
		t.Errorf("the status carries no host name: %+v", st)
	}

	// hmipserver made the refused module's fresh identity meanwhile, and a marker stands again
	// (the old module back, the identity of the other one foreign to it)
	_ = os.WriteFile(filepath.Join(data, lkSGTIN+".ap"), []byte("FRESH-.ap"), 0o664)
	marker := lkFatal(t, root, radio.CauseRefused)
	svc.calls = nil
	if err := k.RestoreSnapshot(exPrevSGTIN); err != nil {
		t.Fatal(err)
	}
	if k.Status().Switching != "restore" && k.Status().Switching != "" {
		t.Errorf("switching: %q", k.Status().Switching)
	}
	st := lkWait(t, k)
	if st.Error != "" || st.Enabled {
		t.Fatalf("after the restore: %+v", st)
	}
	for _, ext := range []string{".ap", ".apkx", ".bbkx"} {
		if b := readFile(filepath.Join(data, exPrevSGTIN+ext)); b != "OLD-"+ext {
			t.Errorf("restored %s: %q", ext, b)
		}
	}
	// the refused module's fresh identity is a fresh-start snapshot of its own now, and gone from the directory
	if _, err := os.Stat(filepath.Join(data, lkSGTIN+".ap")); !os.IsNotExist(err) {
		t.Error("the other module's identity is still in the data directory")
	}
	snaps := k.Snapshots()
	if len(snaps) != 1 || snaps[0].SGTIN != lkSGTIN || snaps[0].Kind != SnapshotFreshStart || readFile(filepath.Join(k.snapshotDir(lkSGTIN), lkSGTIN+".ap")) != "FRESH-.ap" {
		t.Fatalf("snapshots after the restore: %+v", snaps)
	}
	if _, err := os.Stat(k.snapshotDir(exPrevSGTIN)); !os.IsNotExist(err) {
		t.Error("the restored snapshot was not consumed")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Error("the marker survived the restore")
	}
	if !strings.Contains(strings.Join(svc.calls, " "), "hmipserver restart") {
		t.Errorf("no restart: %v", svc.calls)
	}
	// a second time: nothing to restore
	if err := k.RestoreSnapshot(exPrevSGTIN); err == nil || !strings.Contains(err.Error(), "no snapshot of") {
		t.Errorf("again: %v", err)
	}
}
