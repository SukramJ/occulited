package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openccu-lite task 146: an addon whose .nobackup directories all hold nothing but the tag has
// lost its program files to a restore (tar --exclude-tag keeps the directory and the tag).
func TestAddonPayloadCheck(t *testing.T) {
	root := Root(t.TempDir())
	addons := string(root) + "/usr/local/addons"
	rcd := string(root) + "/usr/local/etc/config/rc.d"
	mk := func(parts ...string) {
		p := filepath.Join(parts...)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// hmm: app, bin, www and var/npm-cache tagged, with content; its script under rc.d, untagged
	for _, d := range []string{"app", "bin", "www", "var/npm-cache"} {
		mk(addons, "hmm", d, ".nobackup")
		mk(addons, "hmm", d, "payload")
	}
	mk(addons, "hmm", "rc.d", "hmm")
	mk(addons, "hmm", "etc", "config.json")
	mk(rcd, "hmm")
	_ = os.Symlink("/usr/local/addons/hmm/rc.d/hmm", filepath.Join(rcd, "hmm.script"))
	// plain: no tag anywhere, nothing to lose in a backup
	mk(addons, "plain", "etc", "x")
	mk(rcd, "plain")
	p := &PayloadRecord{Root: root, Path: filepath.Join(t.TempDir(), PayloadFile)}
	versions := map[string]string{"hmm": "3.0.0", "plain": "1"}

	st := p.Check([]string{"hmm", "plain"}, versions, nil)
	if st["hmm"].Missing || st["plain"].Missing {
		t.Fatalf("missing before any restore: %+v", st)
	}
	// one tagged directory emptied (a cache cleared): not the restore's shape
	_ = os.Remove(filepath.Join(addons, "hmm/var/npm-cache/payload"))
	if st = p.Check([]string{"hmm", "plain"}, versions, nil); st["hmm"].Missing {
		t.Errorf("one empty tagged directory counted as missing: %+v", st["hmm"])
	}
	// the restore: every tagged directory holds only its tag, the rest of the tree is back
	for _, d := range []string{"app", "bin", "www"} {
		_ = os.Remove(filepath.Join(addons, "hmm", d, "payload"))
	}
	st = p.Check([]string{"hmm", "plain"}, versions, nil)
	// var/npm-cache is a cache, not the program (B-267): not named, not counted
	if !st["hmm"].Missing || len(st["hmm"].Dirs) != 3 || st["hmm"].Dirs[0] != "app" || st["hmm"].Dirs[2] != "www" || st["hmm"].Dismissed {
		t.Fatalf("after the restore: %+v", st["hmm"])
	}
	if st["plain"].Missing {
		t.Error("plain is missing")
	}
	// dismissed at this version: hidden; a new version shows it again
	if err := p.Dismiss("hmm", "3.0.0"); err != nil {
		t.Fatal(err)
	}
	if st = p.Check([]string{"hmm", "plain"}, versions, nil); !st["hmm"].Missing || !st["hmm"].Dismissed {
		t.Errorf("dismissed: %+v", st["hmm"])
	}
	if st = p.Check([]string{"hmm", "plain"}, map[string]string{"hmm": "3.1.0"}, nil); !st["hmm"].Missing || st["hmm"].Dismissed {
		t.Errorf("another version: %+v", st["hmm"])
	}
	// the reinstall brings the payload back: not missing, the dismissal gone, the file with it
	_ = p.Dismiss("hmm", "3.0.0")
	for _, d := range []string{"app", "bin", "www", "lib"} {
		mk(addons, "hmm", d, ".nobackup")
		mk(addons, "hmm", d, "payload")
	}
	st = p.Check([]string{"hmm", "plain"}, versions, nil)
	if st["hmm"].Missing || st["hmm"].Dismissed {
		t.Errorf("after the reinstall: %+v", st["hmm"])
	}
	if _, err := os.Stat(p.Path); err == nil {
		t.Error("the dismissals file stayed although none is left")
	}
	// a dismissal of an addon no longer installed goes too
	_ = p.Dismiss("gone", "1")
	p.Check([]string{"plain"}, versions, nil)
	if _, err := os.Stat(p.Path); err == nil {
		t.Error("a dismissal of an uninstalled addon stayed")
	}
}

// Without any tag the wrapper's <id>.script link pointing into the void is the signal.
func TestAddonPayloadFallbackDanglingScript(t *testing.T) {
	root := Root(t.TempDir())
	rcd := string(root) + "/usr/local/etc/config/rc.d"
	_ = os.MkdirAll(rcd, 0o755)
	_ = os.MkdirAll(string(root)+"/usr/local/addons/redmatic/etc", 0o755)
	_ = os.WriteFile(filepath.Join(rcd, "redmatic"), nil, 0o755)
	_ = os.Symlink("/usr/local/addons/redmatic/bin/redmatic", filepath.Join(rcd, "redmatic.script"))
	p := &PayloadRecord{Root: root, Path: filepath.Join(t.TempDir(), PayloadFile)}
	st := p.Check([]string{"redmatic"}, nil, nil)
	if !st["redmatic"].Missing || len(st["redmatic"].Dirs) != 1 || filepath.Base(st["redmatic"].Dirs[0]) != "redmatic" {
		t.Fatalf("%+v", st["redmatic"])
	}
	// the script's target back: fine
	_ = os.MkdirAll(string(root)+"/usr/local/addons/redmatic/bin", 0o755)
	_ = os.WriteFile(string(root)+"/usr/local/addons/redmatic/bin/redmatic", nil, 0o755)
	if st = p.Check([]string{"redmatic"}, nil, nil); st["redmatic"].Missing {
		t.Errorf("%+v", st["redmatic"])
	}
	if _, err := os.Stat(p.Path); err == nil {
		t.Error("a file was written with nothing to keep")
	}
}

// openccu-lite B-267: the shapes of the lab restore. As occulite the daemon cannot list a confined
// addon's bin/ or lib/ (0751), only www/; the unit skipped by the fork's program check is a signal
// of its own; RedMatic's caches (tmp/, var/npm-cache/) are not its program.
func TestAddonPayloadRestoreShapes(t *testing.T) {
	root := Root(t.TempDir())
	addons := string(root) + "/usr/local/addons"
	rcd := string(root) + "/usr/local/etc/config/rc.d"
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Mosquitto restored: bin, lib, www hold only the tag; bin and lib closed to others; the
	// wrapper's script link points into bin
	for _, d := range []string{"bin", "lib", "www"} {
		mk(filepath.Join(addons, "mosquitto", d, ".nobackup"))
	}
	mk(filepath.Join(addons, "mosquitto", "etc", "mosquitto.conf"))
	mk(filepath.Join(rcd, "mosquitto"))
	_ = os.Symlink("/usr/local/addons/mosquitto/bin/mosquitto-service", filepath.Join(rcd, "mosquitto.script"))
	// RedMatic with its program in the backup ("full"): bin, lib there and untagged, the caches
	// tagged and empty
	mk(filepath.Join(addons, "redmatic", "bin", "redmatic"))
	mk(filepath.Join(addons, "redmatic", "lib", "node_modules", "x"))
	mk(filepath.Join(addons, "redmatic", "tmp", ".nobackup"))
	mk(filepath.Join(addons, "redmatic", "var", "npm-cache", ".nobackup"))
	mk(filepath.Join(rcd, "redmatic"))
	_ = os.Symlink("/usr/local/addons/redmatic/bin/redmatic", filepath.Join(rcd, "redmatic.script"))
	// hmm installed, its www with content; bin and app closed to others
	for _, d := range []string{"app", "bin", "www"} {
		mk(filepath.Join(addons, "hmm", d, ".nobackup"))
		mk(filepath.Join(addons, "hmm", d, "payload"))
	}
	closed := []string{"mosquitto/bin", "mosquitto/lib", "hmm/app", "hmm/bin"}
	if os.Geteuid() != 0 { // root lists a 0111 directory all the same
		for _, d := range closed {
			if err := os.Chmod(filepath.Join(addons, d), 0o111); err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() {
			for _, d := range closed {
				_ = os.Chmod(filepath.Join(addons, d), 0o755)
			}
		})
	}
	p := &PayloadRecord{Root: root, Path: filepath.Join(t.TempDir(), PayloadFile)}
	ids := []string{"hmm", "mosquitto", "redmatic"}

	// the ownership warning's question: the file system alone, the dismissals untouched
	_ = p.Dismiss("hmm", "3.0.0")
	if !p.ProgramMissing("mosquitto") || p.ProgramMissing("hmm") || p.ProgramMissing("redmatic") {
		t.Errorf("ProgramMissing: mosquitto %v hmm %v redmatic %v", p.ProgramMissing("mosquitto"), p.ProgramMissing("hmm"), p.ProgramMissing("redmatic"))
	}
	if _, err := os.Stat(p.Path); err != nil {
		t.Errorf("ProgramMissing touched the dismissals: %v", err)
	}
	st := p.Check(ids, nil, nil)
	m := st["mosquitto"]
	if !m.Missing || strings.Join(m.Dirs, ",") != "bin,lib,www" {
		t.Errorf("mosquitto restored: %+v", m)
	}
	if st["redmatic"].Missing {
		t.Errorf("redmatic full: its empty caches counted as a missing program: %+v", st["redmatic"])
	}
	if st["hmm"].Missing {
		t.Errorf("hmm installed: %+v", st["hmm"])
	}
	// the unit skipped by the program check: missing, whatever this daemon can see
	st = p.Check(ids, nil, map[string]bool{"hmm": true})
	if h := st["hmm"]; !h.Missing || (os.Geteuid() != 0 && strings.Join(h.Dirs, ",") != "app,bin") {
		t.Errorf("hmm skipped by its unit's check: %+v", h)
	}
	// the dangling link alone, its tagged directory with content: the link's target is named
	if err := os.WriteFile(filepath.Join(addons, "mosquitto", "www", "index.html"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		st = p.Check(ids, nil, nil)
		if m := st["mosquitto"]; !m.Missing || strings.Join(m.Dirs, ",") != "bin,lib" {
			t.Errorf("mosquitto, www back, bin closed: %+v", m)
		}
	}
	for _, d := range []string{"bin", "lib"} {
		_ = os.Chmod(filepath.Join(addons, "mosquitto", d), 0o755)
		if err := os.RemoveAll(filepath.Join(addons, "mosquitto", d)); err != nil {
			t.Fatal(err)
		}
	}
	st = p.Check(ids, nil, nil)
	if m := st["mosquitto"]; !m.Missing || len(m.Dirs) != 1 || filepath.Base(m.Dirs[0]) != "mosquitto-service" {
		t.Errorf("mosquitto, only the link: %+v", m)
	}
}

func TestProgramDir(t *testing.T) {
	for rel, want := range map[string]bool{".": true, "bin": true, "www": true, "share/node": true, "tmp": false, "cache": false, "var": false, "var/npm-cache": false, "lib/cache": false, "x/tmp": false} {
		if got := programDir(rel); got != want {
			t.Errorf("programDir(%q) = %v", rel, got)
		}
	}
}
