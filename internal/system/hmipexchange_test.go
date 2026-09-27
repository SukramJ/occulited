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
