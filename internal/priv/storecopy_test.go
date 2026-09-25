package priv

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openccu-lite task 229: the database's snapshot goes from the helper's fixed source into a
// folder on a stick at /media/usb1…8 - no other place, no link on the way, not onto /media's own
// filesystem when no stick is mounted.
func TestStoreCopyBoundary(t *testing.T) {
	root := t.TempDir()
	c, _ := mountHelper(t, root)
	for _, d := range []string{"media/usb1", "media/usb2", "run/occulite", "etc"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	snap := filepath.Join(t.TempDir(), "snap.db")
	_ = os.WriteFile(snap, []byte("bolt"), 0o600)
	f, err := os.Open(snap)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_ = os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "media/usb1/evil"))
	// the temp root's /media/usb1 is no mount: refused while the check is on
	if err := c.StoreCopy(f, filepath.Join(root, "media/usb1/occulited")); !errors.Is(err, ErrRefused) {
		t.Fatalf("no stick mounted: %v", err)
	}
	storeCopyMountCheck = false
	t.Cleanup(func() { storeCopyMountCheck = true })
	if err := c.StoreCopy(f, filepath.Join(root, "media/usb1/occulited/lab")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "media/usb1/occulited/lab", StoreSnapshotName))
	if err != nil || string(b) != "bolt" {
		t.Fatalf("%q %v", b, err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "media/usb1/occulited/lab/.*partial")); len(left) != 0 {
		t.Fatalf("%v", left)
	}
	for _, bad := range []string{"/media/usb1", "/media/usb0/x", "/media/usb9/x", "/media/net/nas/x", "/etc/x", "/media/usb1/evil/x", "/media/usb1/../../etc", "/media/usb1/.hidden", "/media/usb1/a/b/c/d/e"} {
		if err := c.StoreCopy(f, root+bad); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "etc", StoreSnapshotName)); err == nil {
		t.Fatal("written through the link")
	}
}

// without the descriptor the helper refuses: it never opens a path the daemon names
func TestStoreCopyNeedsTheFile(t *testing.T) {
	root := t.TempDir()
	c, _ := mountHelper(t, root)
	_ = os.MkdirAll(filepath.Join(root, "media/usb1"), 0o755)
	storeCopyMountCheck = false
	t.Cleanup(func() { storeCopyMountCheck = true })
	// a caller that sends no descriptor gets the helper's "send" and nothing more: no file appears
	_, _ = c.call(context.Background(), request{Op: opStoreCopy, Path: filepath.Join(root, "media/usb1/x")})
	var srv Server
	if r := srv.storeCopy(request{Op: opStoreCopy, Path: filepath.Join(root, "media/usb1/x")}, nil); !errors.Is(r.err(), ErrRefused) {
		t.Fatal(r)
	}
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "media/usb1/x")); err == nil {
		t.Fatal("a folder was made without the snapshot")
	}
}
