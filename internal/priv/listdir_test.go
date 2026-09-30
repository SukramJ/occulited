package priv

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openccu-lite B-253: hmipserver's data directory is 0700 with 0600 files. The daemon may ask the
// helper for the names in exactly that directory (as /etc/config names it and as the userfs path
// resolves), and may read the module's identity files (.ap, .apkx, .bbkx) by their shape - never a
// device file, never another directory, never a path below.
func TestListDirAndIdentityReads(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "usr/local/etc/config/crRFD/data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	// the image's /etc/config is a link to the userfs
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/local/etc/config", filepath.Join(root, "etc/config")); err != nil {
		t.Fatal(err)
	}
	const sg = "3014F711A000040000000A01"
	for name, content := range map[string]string{sg + ".ap": "AP", sg + ".apkx": "APKX", sg + ".bbkx": "BBKX", sg + ".dev": "DEV", "linkData.conf": "LINKS", "metaData.conf": "META"} {
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(data, "old_20260101"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, sock := logListHelper(t, root, nil)

	// the listing: every name, through either spelling, sorted
	for _, dir := range []string{"etc/config/crRFD/data", "usr/local/etc/config/crRFD/data"} {
		names, err := c.ListDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if strings.Join(names, " ") != sg+".ap "+sg+".apkx "+sg+".bbkx "+sg+".dev linkData.conf metaData.conf old_20260101" {
			t.Errorf("%s: %v", dir, names)
		}
	}
	// refused: the directory above, a directory below, another daemon's, a traversal
	for _, bad := range []string{"etc/config/crRFD", "etc/config/crRFD/data/old_20260101", "etc/config/rfd", "usr/local/etc/config/crRFD/data/../../shadow", "usr/local/addons"} {
		if names, err := c.ListDir(filepath.Join(root, bad)); !errors.Is(err, ErrRefused) || names != nil {
			t.Errorf("%s: %v %v", bad, names, err)
		}
	}
	// a request with more than a directory is refused
	if res, _ := rawLogList(t, sock, request{Op: opListDir, Path: data, AllFiles: true}); res.OK || !strings.Contains(res.Error, "nothing else") {
		t.Errorf("with a flag: %+v", res)
	}
	// a data directory that is a link into somewhere else is not listed
	if err := os.Rename(data, data+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "usr/local/addons"), data); err != nil {
		t.Fatal(err)
	}
	if names, err := c.ListDir(data); !errors.Is(err, ErrRefused) || names != nil {
		t.Errorf("through a link out: %v %v", names, err)
	}
	if err := os.Remove(data); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(data+".real", data); err != nil {
		t.Fatal(err)
	}

	// the identity files by shape, through either spelling - and the device files, since the
	// module-move snapshot keeps them (openccu-lite B-285)
	for _, ext := range []string{".ap", ".apkx", ".bbkx", ".dev"} {
		for _, dir := range []string{"etc/config/crRFD/data", "usr/local/etc/config/crRFD/data"} {
			if b, err := c.ReadFile(filepath.Join(root, dir, sg+ext)); err != nil || string(b) != strings.ToUpper(ext[1:]) {
				t.Errorf("%s%s: %q %v", dir, ext, b, err)
			}
		}
	}
	// and nothing else in the directory: the link data, a name with the extension somewhere
	// else in it, a directory
	for _, bad := range []string{"linkData.conf", "metaData.conf", "x.ap.devx", "old_20260101", "old_20260101/" + sg + ".ap"} {
		if _, err := c.ReadFile(filepath.Join(data, bad)); !errors.Is(err, ErrRefused) {
			t.Errorf("%s was readable: %v", bad, err)
		}
	}
	p := DefaultPolicy("/", "")
	for _, bad := range []string{"/etc/config/crRFD/data/../hmip_user.conf.ap", "/etc/config/crRFD/data/a/b.ap", "/etc/config/crRFD/x.ap", "/etc/config/crRFD/data/*.ap"} {
		if p.readAllowed(bad) && !strings.Contains(bad, "*") {
			t.Errorf("readAllowed(%q)", bad)
		}
	}
}

// Local.ListDir answers names, not a link's target, and refuses a file.
func TestLocalListDir(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/one", "a/two", "outside/three"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(root, "a/link")); err != nil {
		t.Fatal(err)
	}
	names, err := Local{}.ListDir(filepath.Join(root, "a"))
	if err != nil || strings.Join(names, " ") != "link one two" {
		t.Errorf("%v %v", names, err)
	}
	if _, err := (Local{}).ListDir(filepath.Join(root, "a/link")); err == nil {
		t.Error("a symlinked directory was listed")
	}
	if _, err := (Local{}).ListDir(filepath.Join(root, "a/one")); err == nil {
		t.Error("a file was listed as a directory")
	}
	if _, err := (Local{}).ListDir(filepath.Join(root, "nothing")); err == nil {
		t.Error("a missing directory was listed")
	}
}
