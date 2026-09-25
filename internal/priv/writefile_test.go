package priv

import (
	"os"
	"path/filepath"
	"testing"
)

// openccu-lite B-188: a write into a directory that is a symlink whose target does not exist yet
// (/etc/config -> ../usr/local/etc/config on a fresh userfs) creates the target and writes there,
// so `occulited firewall load` gets its rule file on the first boot instead of failing.
func TestLocalWriteFileThroughDanglingLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/local/etc/config", filepath.Join(root, "etc/config")); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).WriteFile(filepath.Join(root, "etc/config/firewall-rules.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "usr/local/etc/config/firewall-rules.json"))
	if err != nil || string(b) != "{}\n" {
		t.Fatalf("through the link: %q %v", b, err)
	}
	if fi, err := os.Lstat(filepath.Join(root, "etc/config")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v %v", fi, err)
	}
	// a second write, the target there now, goes the usual way
	if err := (Local{}).WriteFile(filepath.Join(root, "etc/config/firewall-rules.json"), []byte("[]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/local/etc/config/firewall-rules.json")); string(b) != "[]\n" {
		t.Fatalf("second write: %q", b)
	}
	// a link in the middle and one at the end, both dangling, an absolute one too
	if err := os.Symlink(filepath.Join(root, "store"), filepath.Join(root, "abs")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../deep/dir", filepath.Join(root, "store/cfg")); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).WriteFile(filepath.Join(root, "abs/cfg/f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "deep/dir/f")); err != nil {
		t.Fatal(err)
	}
	if got := throughLinks(filepath.Join(root, "plain/none/f")); got != filepath.Join(root, "plain/none/f") {
		t.Fatalf("no link: %q", got)
	}
}
