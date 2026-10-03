package system

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddonsFingerprint(t *testing.T) {
	root := t.TempDir()
	r := Root(root)
	cfg := filepath.Join(root, "usr/local/etc/config")
	for _, d := range []string{"rc.d", "nav.d", "lighttpd"} {
		if err := os.MkdirAll(filepath.Join(cfg, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(cfg, rel), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	empty := r.AddonsFingerprint()
	if empty != r.AddonsFingerprint() {
		t.Fatal("not stable")
	}
	seen := map[string]string{"empty": empty}
	step := func(name string, f func()) {
		t.Helper()
		f()
		fp := r.AddonsFingerprint()
		for was, v := range seen {
			if v == fp {
				t.Fatalf("%s: the same fingerprint as %s", name, was)
			}
		}
		seen[name] = fp
	}
	step("install", func() { write("rc.d/hm2mqtt", "#!/bin/sh\n", 0o755) })
	step("disable", func() {
		if err := os.Chmod(filepath.Join(cfg, "rc.d/hm2mqtt"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	step("frontend", func() { write("lighttpd/hm2mqtt.conf", `$HTTP["url"] =~ "^/addons/hm2mqtt/" {}`, 0o644) })
	step("nav.d", func() { write("nav.d/x.json", `{}`, 0o644) })
	step("hm_addons.cfg", func() { write("hm_addons.cfg", "x", 0o644) })
	// a link's target changes
	target := filepath.Join(root, "target.conf")
	if err := os.WriteFile(target, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	step("link", func() {
		if err := os.Symlink(target, filepath.Join(cfg, "lighttpd/red.conf")); err != nil {
			t.Fatal(err)
		}
	})
	step("link target", func() {
		if err := os.WriteFile(target, []byte("abc"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	step("uninstall", func() {
		if err := os.Remove(filepath.Join(cfg, "rc.d/hm2mqtt")); err != nil {
			t.Fatal(err)
		}
	})
	// an update rewrites the script with the same size: the time says it
	write("rc.d/red", "#!/bin/sh\n", 0o755)
	before := r.AddonsFingerprint()
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(cfg, "rc.d/red"), later, later); err != nil {
		t.Fatal(err)
	}
	if r.AddonsFingerprint() == before {
		t.Fatal("a rewritten script left the fingerprint as it was")
	}
}
