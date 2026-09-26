package system

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openccu-lite task 250: the CCU's leftovers go once after the first start - exactly the list,
// nothing else; kept while the name import has still to read the ReGa database; the marker keeps
// it from running twice, and a system without leftovers records an empty run.
func TestRemoveLeftoversOnce(t *testing.T) {
	root := Root(t.TempDir())
	state := t.TempDir()
	put := func(p, c string) {
		full := filepath.Join(string(root), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("etc/config/homematic.regadom", "regadom")
	put("etc/config/homematic.regadom.bak", "bak")
	put("etc/config/measurement/a.dat", "0123456789")
	put("etc/config/userprofiles/admin", "p")
	// what must stay: the radio's identity, the keys, the interface configuration, an addon's data
	for _, p := range []string{"etc/config/ids", "etc/config/keys", "etc/config/rfd.conf", "etc/config/InterfacesList.xml", "usr/local/addons/mosquitto/etc/mosquitto.conf"} {
		put(p, "keep")
	}
	now := time.Date(2026, 9, 25, 18, 0, 0, 0, time.UTC)
	// the import has not read the database yet: nothing goes, no marker
	if _, ran, err := root.RemoveLeftoversOnce(state, false, now, nil, nil); !ran && !errors.Is(err, ErrMigrationIncomplete) {
		t.Fatalf("%v %v", ran, err)
	}
	if _, err := os.Stat(filepath.Join(string(root), "etc/config/homematic.regadom")); err != nil {
		t.Fatal("removed before the import")
	}
	if _, err := os.Stat(filepath.Join(state, LeftoversMarker)); err == nil {
		t.Fatal("marker written on a skip")
	}
	// settled: the list goes, the rest stays, the marker says what
	run, ran, err := root.RemoveLeftoversOnce(state, true, now, nil, nil)
	if err != nil || !ran {
		t.Fatalf("%v %v", ran, err)
	}
	if len(run.Removed) != 4 || run.Freed != int64(len("regadom")+len("bak")+10+1) {
		t.Fatalf("%+v", run)
	}
	for _, p := range []string{"etc/config/homematic.regadom", "etc/config/homematic.regadom.bak", "etc/config/measurement", "etc/config/userprofiles"} {
		if _, err := os.Lstat(filepath.Join(string(root), p)); !os.IsNotExist(err) {
			t.Fatalf("%s still there", p)
		}
	}
	for _, p := range []string{"etc/config/ids", "etc/config/keys", "etc/config/rfd.conf", "etc/config/InterfacesList.xml", "usr/local/addons/mosquitto/etc/mosquitto.conf"} {
		if _, err := os.Stat(filepath.Join(string(root), p)); err != nil {
			t.Fatalf("%s removed", p)
		}
	}
	var m LeftoverRun
	b, _ := os.ReadFile(filepath.Join(state, LeftoversMarker))
	if json.Unmarshal(b, &m) != nil || len(m.Removed) != 4 || m.At != "2026-09-25T18:00:00Z" || len(m.Paths) != 4 {
		t.Fatalf("%s", b)
	}
	// once only: a leftover that comes back later (a restored CCU backup) stays
	put("etc/config/homematic.regadom", "restored")
	if _, ran, err := root.RemoveLeftoversOnce(state, true, now, nil, nil); ran || err != nil {
		t.Fatalf("second run: %v %v", ran, err)
	}
	if _, err := os.Stat(filepath.Join(string(root), "etc/config/homematic.regadom")); err != nil {
		t.Fatal("removed twice")
	}
	// a system with nothing left over: an empty run, recorded
	state2 := t.TempDir()
	clean := Root(t.TempDir())
	run, ran, err = clean.RemoveLeftoversOnce(state2, true, now, nil, nil)
	if err != nil || !ran || len(run.Removed) != 0 {
		t.Fatalf("%+v %v %v", run, ran, err)
	}
	if _, err := os.Stat(filepath.Join(state2, LeftoversMarker)); err != nil {
		t.Fatal("no marker for an empty run")
	}
}

// B-257: the first-boot pass removes the empty world-writable CCU leftover addons/mh and takes the
// world-writable bit off any other 0777 directory under /usr/local/etc/config, and leaves the rest.
func TestRemoveLeftoversHardensConfig(t *testing.T) {
	root := Root(t.TempDir())
	state := t.TempDir()
	cfg := filepath.Join(string(root), "usr/local/etc/config")
	mkdir := func(p string, mode os.FileMode) {
		full := filepath.Join(string(root), p)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(full, mode); err != nil { // MkdirAll applies the umask; set it exactly
			t.Fatal(err)
		}
	}
	mkdir("usr/local/etc/config/addons/mh", 0o777)    // the empty leftover: removed
	mkdir("usr/local/etc/config/addons/other", 0o777) // world-writable but not empty: chmod
	_ = os.WriteFile(filepath.Join(cfg, "addons/other/keep"), []byte("x"), 0o644)
	mkdir("usr/local/etc/config/addons/normal", 0o755) // fine: untouched
	// addons itself must stay traversable and not world-writable so the walk can reach the leaves
	_ = os.Chmod(filepath.Join(cfg, "addons"), 0o755)

	run, ran, err := root.RemoveLeftoversOnce(state, true, time.Now(), nil, nil)
	if err != nil || !ran {
		t.Fatalf("%v %v", ran, err)
	}
	if _, err := os.Lstat(filepath.Join(cfg, "addons/mh")); !os.IsNotExist(err) {
		t.Error("the empty addons/mh was not removed")
	}
	if fi, err := os.Stat(filepath.Join(cfg, "addons/other")); err != nil {
		t.Error("addons/other was removed, not just hardened")
	} else if fi.Mode().Perm()&0o002 != 0 {
		t.Errorf("addons/other is still world-writable: %04o", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Join(cfg, "addons/normal")); fi == nil || fi.Mode().Perm() != 0o755 {
		t.Error("addons/normal was changed")
	}
	if len(run.Hardened) < 2 {
		t.Errorf("the run did not record both changes: %v", run.Hardened)
	}
	// once only: the marker keeps it from running again
	if _, ran, _ := root.RemoveLeftoversOnce(state, true, time.Now(), nil, nil); ran {
		t.Error("the hardening ran twice")
	}
}
