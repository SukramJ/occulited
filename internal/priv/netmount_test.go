package priv

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/netmount"
)

// openccu-lite task 86: the mount operations take checked fields and nothing else, and the write
// test runs only under /media - a request of any other shape is refused before it reaches the
// operation.

type recordMount struct {
	Local
	calls []string
}

func (r *recordMount) NetMount(_ context.Context, s netmount.Spec) error {
	r.calls = append(r.calls, "mount "+s.ID+" "+s.What())
	return nil
}
func (r *recordMount) NetUnmount(_ context.Context, id string) error {
	r.calls = append(r.calls, "unmount "+id)
	return nil
}
func (r *recordMount) NetMountRemove(_ context.Context, id string) error {
	r.calls = append(r.calls, "remove "+id)
	return nil
}
func (r *recordMount) WriteTest(_ context.Context, dir string) (WriteTestResult, error) {
	r.calls = append(r.calls, "test "+dir)
	return WriteTestResult{OK: true, Step: "done"}, nil
}

func mountHelper(t *testing.T, root string) (Client, *recordMount) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ops := &recordMount{}
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite"), Ops: ops}
	go func() { _ = srv.Serve(ctx, l) }()
	return Client{Socket: sock}, ops
}

func TestNetMountBoundary(t *testing.T) {
	root := t.TempDir()
	c, ops := mountHelper(t, root)
	ctx := context.Background()
	good := netmount.Spec{ID: "nas1", Kind: "nfs", Server: "nas.lan", Path: "/volume1/b"}
	if err := c.NetMount(ctx, good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []netmount.Spec{
		{ID: "nas-1", Kind: "nfs", Server: "nas", Path: "/b"},
		{ID: "nas1", Kind: "nfs", Server: "nas", Path: "/b,uid=0"},
		{ID: "nas1", Kind: "nfs", Server: "nas", Path: "/b\nWhere=/etc"},
		{ID: "nas1", Kind: "ext4", Server: "nas", Path: "/b"},
		{ID: "nas1", Kind: "nfs", Server: "nas;reboot", Path: "/b"},
	} {
		if err := c.NetMount(ctx, bad); !errors.Is(err, ErrRefused) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// a field the spec does not have, and a request carrying more than the spec
	for _, req := range []request{
		{Op: opNetMount, Data: []byte(`{"id":"nas1","kind":"nfs","server":"nas","path":"/b","options":"exec"}`)},
		{Op: opNetMount, Data: []byte(`{"id":"nas1","kind":"nfs","server":"nas","path":"/b"}`), Path: "/etc"},
		{Op: opNetUnmount, Name: "nas1", Args: []string{"-f"}},
		{Op: opNetUnmount, Name: "../x"},
		{Op: opNetMountRemove, Name: "nas1", Path: "/"},
		{Op: opWriteTest, Path: "/media/usb1/backup", Name: "x"},
	} {
		if _, err := c.call(ctx, req); !errors.Is(err, ErrRefused) {
			t.Errorf("%+v: %v", req, err)
		}
	}
	if err := c.NetUnmount(ctx, "nas1"); err != nil {
		t.Fatal(err)
	}
	if err := c.NetMountRemove(ctx, "nas1"); err != nil {
		t.Fatal(err)
	}
	// the mount point replaced by a link: refused
	if err := os.MkdirAll(filepath.Join(root, "media/net"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "media/net/nas1")); err != nil {
		t.Fatal(err)
	}
	if err := c.NetMount(ctx, good); !errors.Is(err, ErrRefused) {
		t.Errorf("a linked mount point: %v", err)
	}
	// unmount acts on the unit by name: a link there changes nothing and is not refused (task 228:
	// the mount point of a hung share cannot even be looked at)
	if err := c.NetUnmount(ctx, "nas1"); err != nil {
		t.Errorf("unmount over a link: %v", err)
	}
	want := []string{"mount nas1 nas.lan:/volume1/b", "unmount nas1", "remove nas1", "unmount nas1"}
	if strings.Join(ops.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %q, want %q", ops.calls, want)
	}
}

func TestWriteTestBoundary(t *testing.T) {
	root := t.TempDir()
	c, ops := mountHelper(t, root)
	ctx := context.Background()
	for _, d := range []string{"media/usb1", "usr/local/sdcard/backup", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// upstream's /media/usb0 -> /usr/local/sdcard, and a link planted to /etc
	if err := os.Symlink(filepath.Join(root, "usr/local/sdcard"), filepath.Join(root, "media/usb0")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "media/evil")); err != nil {
		t.Fatal(err)
	}
	for _, ok := range []string{"/media/usb1/backup", "/media/usb0/backup", "/media/net/nas1/lab", "/usr/local/backup", "/usr/local/backup/ccu"} {
		if _, err := c.WriteTest(ctx, filepath.Join(root, ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"/etc", "/usr/local/etc", "/usr/local/backupx", "/usr/local", "/media/evil/x", "/media/../etc", "/media/usb1/", "media/usb1"} {
		p := bad
		if strings.HasPrefix(bad, "/") {
			p = root + bad
		}
		if _, err := c.WriteTest(ctx, p); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	if len(ops.calls) != 5 {
		t.Fatalf("calls %q", ops.calls)
	}
}

// The test itself, on a plain directory: done, the file gone, the space measured; a directory that
// cannot be written answers the step and the errno.
func TestWriteTestLocal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sub", "box")
	r, err := Local{}.WriteTest(context.Background(), dir)
	if err != nil || !r.OK || r.Step != "done" || r.TotalBytes <= 0 {
		t.Fatalf("%+v %v", r, err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Fatalf("left behind: %v", left)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"step":"done"`) {
		t.Fatal(string(b))
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes a read-only directory")
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	r, _ = Local{}.WriteTest(context.Background(), ro)
	if r.OK || r.Step != "create" || r.Errno != "EACCES" {
		t.Fatalf("%+v", r)
	}
}

// task 228: a share's credentials are the share store's file, a backup target's its own - the
// daemon says which store with a flag, never with a path
func TestNetMountCredFile(t *testing.T) {
	for _, tc := range []struct {
		s    netmount.Spec
		want string
	}{
		{netmount.Spec{ID: "t1234567", Kind: "cifs"}, "/usr/local/etc/occulite/backup-targets/t1234567/cifs.cred"},
		{netmount.Spec{ID: "nas", Kind: "cifs", Share: true}, "/usr/local/etc/occulite/shares/nas/cifs.cred"},
	} {
		if got := credFile(tc.s); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.s, got, tc.want)
		}
	}
}

// A share whose server is gone: an lstat of its mount point answers EHOSTDOWN after seconds, so a
// new mount over it is accepted when the mount table says a network share is mounted there - and
// only then.
func TestNetMountPointOfAHungShare(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "proc/self"), 0o755)
	p := DefaultPolicy(root, "/usr/local/etc/occulite")
	if p.networkMountAt("/media/net/nas1") {
		t.Fatal("no mountinfo, yet mounted")
	}
	info := "40 22 0:30 / /media/net/nas1 rw - autofs systemd-1 rw\n41 40 0:31 / /media/net/nas1 rw - cifs //nas/b rw\n"
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte(info), 0o644)
	if !p.networkMountAt("/media/net/nas1") || p.networkMountAt("/media/net/nas2") {
		t.Fatal("mountinfo not read")
	}
}
