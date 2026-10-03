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

// B-257: the hardening (once, B-264: its own marker) removes the empty world-writable CCU leftover addons/mh and takes the
// world-writable bit off any other 0777 directory under /usr/local/etc/config, and leaves the rest.
func TestHardenConfigDirs(t *testing.T) {
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

	run, ran, err := root.HardenConfigDirsOnce(state, time.Now())
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
	// once only: its marker keeps it from running again
	mkdir("usr/local/etc/config/addons/late", 0o777)
	if _, ran, _ := root.HardenConfigDirsOnce(state, time.Now()); ran {
		t.Error("the hardening ran twice")
	}
	if fi, _ := os.Stat(filepath.Join(cfg, "addons/late")); fi == nil || fi.Mode().Perm() != 0o777 {
		t.Error("a second start hardened again")
	}
	// the leftovers pass does not do it any more
	lrun, lran, err := root.RemoveLeftoversOnce(t.TempDir(), true, time.Now(), nil, nil)
	if err != nil || !lran || len(lrun.Hardened) != 0 {
		t.Errorf("the leftovers pass hardened: %+v %v %v", lrun, lran, err)
	}
	if fi, _ := os.Stat(filepath.Join(cfg, "addons/late")); fi == nil || fi.Mode().Perm() != 0o777 {
		t.Error("the leftovers pass changed a mode")
	}
}

// openccu-lite B-264: a system upgraded from dev.24-dev.28 has the leftovers marker from before
// B-257 (no "hardened" key) and no hardening marker: the hardening runs once there, writes its own
// marker, and leaves the leftovers marker as it was.
func TestHardenConfigDirsOnUpgradedSystem(t *testing.T) {
	root := Root(t.TempDir())
	state := t.TempDir()
	cfg := filepath.Join(string(root), "usr/local/etc/config")
	mh := filepath.Join(cfg, "addons/mh")
	if err := os.MkdirAll(filepath.Join(mh, "html"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(mh, "html", "index.html"), []byte("x"), 0o777)
	for _, d := range []string{mh, filepath.Join(mh, "html")} {
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	old := []byte(`{"at":"2026-09-25T16:49:47Z","removed":["measurement"],"freed_bytes":12}` + "\n")
	leftovers := filepath.Join(state, LeftoversMarker)
	if err := os.WriteFile(leftovers, old, 0o600); err != nil {
		t.Fatal(err)
	}
	// the leftovers pass: done long ago, nothing happens
	if _, ran, err := root.RemoveLeftoversOnce(state, true, time.Now(), nil, nil); ran || err != nil {
		t.Fatalf("the leftovers pass ran again: %v %v", ran, err)
	}
	// the hardening: runs, both directories lose the world-writable bit, mh stays (not empty)
	at := time.Date(2026, 9, 28, 8, 0, 0, 0, time.UTC)
	run, ran, err := root.HardenConfigDirsOnce(state, at)
	if err != nil || !ran || len(run.Hardened) != 2 || run.At != "2026-09-28T08:00:00Z" {
		t.Fatalf("%+v %v %v", run, ran, err)
	}
	for _, d := range []string{mh, filepath.Join(mh, "html")} {
		if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o775 {
			t.Errorf("%s: %v %v", d, fi.Mode().Perm(), err)
		}
	}
	var m HardenRun
	if b, err := os.ReadFile(filepath.Join(state, HardenMarker)); err != nil || json.Unmarshal(b, &m) != nil || len(m.Hardened) != 2 {
		t.Fatalf("marker: %+v %v", m, err)
	}
	if b, _ := os.ReadFile(leftovers); string(b) != string(old) {
		t.Errorf("the leftovers marker changed: %s", b)
	}
	if _, ran, _ := root.HardenConfigDirsOnce(state, at); ran {
		t.Error("ran twice")
	}
	// a state directory it cannot write: the run is reported, no marker, the next start tries again
	ro := filepath.Join(t.TempDir(), "missing", "state")
	if _, ran, err := root.HardenConfigDirsOnce(ro, at); !ran || err == nil {
		t.Errorf("no marker written: %v %v", ran, err)
	}
}

// openccu-lite task 312: the CCU's addons/mh with content keeps every file, and its files lose the
// world-writable bit like its directories; a link in it is not followed, files elsewhere under
// config keep their modes. A system whose marker is from the first pass (no version) runs the
// hardening once more; one with the current pass's marker does not.
func TestHardenConfigDirsMhFiles(t *testing.T) {
	root := Root(t.TempDir())
	state := t.TempDir()
	cfg := filepath.Join(string(root), "usr/local/etc/config")
	mh := filepath.Join(cfg, "addons/mh")
	outside := filepath.Join(string(root), "outside.sh")
	put := func(p string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil { // WriteFile applies the umask; set it exactly
			t.Fatal(err)
		}
	}
	put(filepath.Join(mh, "addcron.sh"), 0o777)
	put(filepath.Join(mh, "html/index.html"), 0o666)
	put(filepath.Join(mh, "readonly.conf"), 0o644)
	put(filepath.Join(cfg, "daemon.conf"), 0o666) // not mh's: untouched
	put(outside, 0o777)
	if err := os.Symlink(outside, filepath.Join(mh, "link.sh")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{mh, filepath.Join(mh, "html")} {
		if err := os.Chmod(d, 0o777); err != nil {
			t.Fatal(err)
		}
	}
	// the first pass's marker, as dev.29-dev.37 wrote it
	old := []byte(`{"at":"2026-09-28T08:00:00Z","hardened":["addons/mh (o-w, now 0775)"]}` + "\n")
	if err := os.WriteFile(filepath.Join(state, HardenMarker), old, 0o600); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 3, 9, 0, 0, 0, time.UTC)
	run, ran, err := root.HardenConfigDirsOnce(state, at)
	if err != nil || !ran || run.Version != HardenVersion {
		t.Fatalf("%+v %v %v", run, ran, err)
	}
	want := map[string]os.FileMode{
		mh:                                   0o775,
		filepath.Join(mh, "html"):            0o775,
		filepath.Join(mh, "addcron.sh"):      0o775,
		filepath.Join(mh, "html/index.html"): 0o664,
		filepath.Join(mh, "readonly.conf"):   0o644,
		filepath.Join(cfg, "daemon.conf"):    0o666,
		outside:                              0o777,
	}
	for p, m := range want {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != m {
			t.Errorf("%s: %v, want %04o (%v)", p, fi.Mode().Perm(), m, err)
		}
	}
	if fi, err := os.Lstat(filepath.Join(mh, "link.sh")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link in mh: %v %v", fi, err)
	}
	if len(run.Hardened) != 4 {
		t.Errorf("the run recorded %v", run.Hardened)
	}
	var m HardenRun
	if b, err := os.ReadFile(filepath.Join(state, HardenMarker)); err != nil || json.Unmarshal(b, &m) != nil || m.Version != HardenVersion || len(m.Hardened) != 4 {
		t.Fatalf("marker: %+v %v", m, err)
	}
	// the current pass's marker: no further run, a file made 0777 later stays as it is
	if err := os.Chmod(filepath.Join(mh, "addcron.sh"), 0o777); err != nil {
		t.Fatal(err)
	}
	if _, ran, _ := root.HardenConfigDirsOnce(state, at); ran {
		t.Error("ran twice")
	}
	// an unreadable marker counts as an old one
	if err := os.WriteFile(filepath.Join(state, HardenMarker), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ran, err := root.HardenConfigDirsOnce(state, at); !ran || err != nil {
		t.Errorf("an unreadable marker: %v %v", ran, err)
	}
	if fi, _ := os.Stat(filepath.Join(mh, "addcron.sh")); fi == nil || fi.Mode().Perm() != 0o775 {
		t.Error("addcron.sh not hardened on the rerun")
	}
}
