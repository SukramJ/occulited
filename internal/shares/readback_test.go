package shares

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// B-223: ReadBack as the daemon's user - a folder a root-squashed export gave to nobody (0770 there,
// modelled by a mode a non-root test user cannot enter), a file it cannot open; a folder not there
// yet, an empty one and the write test's own file are no answer.
func TestReadBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder whatever its mode; the manager's tests fake ReadBack instead")
	}
	dir := t.TempDir()
	if err := ReadBack(filepath.Join(dir, "missing")); err != nil {
		t.Fatalf("missing: %v", err)
	}
	if err := ReadBack(dir); err != nil {
		t.Fatalf("empty: %v", err)
	}
	// the journal's copies: <dir>/<machine-id>/system@….journal, readable
	mid := filepath.Join(dir, "0123456789abcdef")
	_ = os.MkdirAll(mid, 0o750)
	_ = os.WriteFile(filepath.Join(mid, "system.journal"), []byte("LPKSHHRH"), 0o640)
	_ = os.WriteFile(filepath.Join(dir, ".occulite-write-test-00"), nil, 0o000)
	if err := ReadBack(dir); err != nil {
		t.Fatalf("readable: %v", err)
	}
	// a file the user cannot open
	f := filepath.Join(mid, "user-1000.journal")
	_ = os.WriteFile(f, []byte("x"), 0o000)
	err := ReadBack(dir)
	if !errors.Is(err, ErrUnreadable) || !errors.Is(err, syscall.EACCES) || !strings.Contains(err.Error(), "user-1000.journal") {
		t.Fatalf("file: %v", err)
	}
	_ = os.Remove(f)
	// the machine's folder root made on a squashed export: not enterable
	_ = os.Chmod(mid, 0o000)
	t.Cleanup(func() { _ = os.Chmod(mid, 0o755) })
	if err := ReadBack(dir); !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), mid) {
		t.Fatalf("folder: %v", err)
	}
	// the target folder itself
	_ = os.Chmod(mid, 0o755)
	_ = os.Chmod(dir, 0o300)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := ReadBack(dir); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("target: %v", err)
	}
}

func TestManagerTestUnreadable(t *testing.T) {
	m, h, root := newManager(t)
	ctx := context.Background()
	_, _ = m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/data"})
	var asked []string
	m.ReadBack = func(dir string) error {
		asked = append(asked, strings.TrimPrefix(dir, root))
		return fmt.Errorf("%w: %s: permission denied", ErrUnreadable, dir)
	}
	// root wrote, the system's user cannot read it back: unreadable, the write itself worked
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs"}
	r, err := m.Test(ctx, "nas")
	if err != nil || !r.OK || r.State != StateUnreadable || r.Step != "read-back" || !strings.Contains(r.Error, "cannot read what root wrote") {
		t.Fatalf("%+v %v", r, err)
	}
	if strings.Join(asked, "|") != "/media/net/nas" {
		t.Fatalf("%q", asked)
	}
	// the journal's folder
	asked = nil
	if r, _ = m.TestFolder(ctx, "nas", "lab/journal"); !r.OK || r.State != StateUnreadable || strings.Join(asked, "|") != "/media/net/nas/lab/journal" {
		t.Fatalf("%+v %q", r, asked)
	}
	// a failed write is not read back
	asked = nil
	h.test = priv.WriteTestResult{Step: "create", Errno: "EACCES", Error: "permission denied", FSType: "nfs"}
	if r, _ = m.Test(ctx, "nas"); r.State != StateReadOnly || len(asked) != 0 {
		t.Fatalf("%+v %q", r, asked)
	}
	// readable: writable as before
	m.ReadBack = func(string) error { return nil }
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs"}
	if r, _ = m.Test(ctx, "nas"); !r.OK || r.State != StateWritable {
		t.Fatalf("%+v", r)
	}
}
