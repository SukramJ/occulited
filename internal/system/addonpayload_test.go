package system

import (
	"os"
	"path/filepath"
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

	st := p.Check([]string{"hmm", "plain"}, versions)
	if st["hmm"].Missing || st["plain"].Missing {
		t.Fatalf("missing before any restore: %+v", st)
	}
	// one tagged directory emptied (a cache cleared): not the restore's shape
	_ = os.Remove(filepath.Join(addons, "hmm/var/npm-cache/payload"))
	if st = p.Check([]string{"hmm", "plain"}, versions); st["hmm"].Missing {
		t.Errorf("one empty tagged directory counted as missing: %+v", st["hmm"])
	}
	// the restore: every tagged directory holds only its tag, the rest of the tree is back
	for _, d := range []string{"app", "bin", "www"} {
		_ = os.Remove(filepath.Join(addons, "hmm", d, "payload"))
	}
	st = p.Check([]string{"hmm", "plain"}, versions)
	if !st["hmm"].Missing || len(st["hmm"].Dirs) != 4 || st["hmm"].Dirs[0] != "app" || st["hmm"].Dirs[3] != "www" || st["hmm"].Dismissed {
		t.Fatalf("after the restore: %+v", st["hmm"])
	}
	if st["plain"].Missing {
		t.Error("plain is missing")
	}
	// dismissed at this version: hidden; a new version shows it again
	if err := p.Dismiss("hmm", "3.0.0"); err != nil {
		t.Fatal(err)
	}
	if st = p.Check([]string{"hmm", "plain"}, versions); !st["hmm"].Missing || !st["hmm"].Dismissed {
		t.Errorf("dismissed: %+v", st["hmm"])
	}
	if st = p.Check([]string{"hmm", "plain"}, map[string]string{"hmm": "3.1.0"}); !st["hmm"].Missing || st["hmm"].Dismissed {
		t.Errorf("another version: %+v", st["hmm"])
	}
	// the reinstall brings the payload back: not missing, the dismissal gone, the file with it
	_ = p.Dismiss("hmm", "3.0.0")
	for _, d := range []string{"app", "bin", "www", "lib"} {
		mk(addons, "hmm", d, ".nobackup")
		mk(addons, "hmm", d, "payload")
	}
	st = p.Check([]string{"hmm", "plain"}, versions)
	if st["hmm"].Missing || st["hmm"].Dismissed {
		t.Errorf("after the reinstall: %+v", st["hmm"])
	}
	if _, err := os.Stat(p.Path); err == nil {
		t.Error("the dismissals file stayed although none is left")
	}
	// a dismissal of an addon no longer installed goes too
	_ = p.Dismiss("gone", "1")
	p.Check([]string{"plain"}, versions)
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
	_ = os.Symlink(string(root)+"/usr/local/addons/redmatic/bin/redmatic", filepath.Join(rcd, "redmatic.script"))
	p := &PayloadRecord{Root: root, Path: filepath.Join(t.TempDir(), PayloadFile)}
	st := p.Check([]string{"redmatic"}, nil)
	if !st["redmatic"].Missing || len(st["redmatic"].Dirs) != 1 || filepath.Base(st["redmatic"].Dirs[0]) != "redmatic" {
		t.Fatalf("%+v", st["redmatic"])
	}
	// the script's target back: fine
	_ = os.MkdirAll(string(root)+"/usr/local/addons/redmatic/bin", 0o755)
	_ = os.WriteFile(string(root)+"/usr/local/addons/redmatic/bin/redmatic", nil, 0o755)
	if st = p.Check([]string{"redmatic"}, nil); st["redmatic"].Missing {
		t.Errorf("%+v", st["redmatic"])
	}
	if _, err := os.Stat(p.Path); err == nil {
		t.Error("a file was written with nothing to keep")
	}
}
