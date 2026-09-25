package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/shares"
	"github.com/hobbyquaker/occulited/internal/system"
)

// journalTestHelper answers the write test with what the case wants and records where it ran.
type journalTestHelper struct {
	res  priv.WriteTestResult
	dirs []string
}

func (h *journalTestHelper) NetMount(context.Context, netmount.Spec) error { return nil }
func (h *journalTestHelper) NetUnmount(context.Context, string) error      { return nil }
func (h *journalTestHelper) ShareList(ctx context.Context, dir string) (priv.ShareListResult, error) {
	return priv.Local{}.ShareList(ctx, dir)
}
func (h *journalTestHelper) ShareOpen(path string) (*os.File, error) {
	return priv.Local{}.ShareOpen(path)
}
func (h *journalTestHelper) NetMountRemove(context.Context, string) error { return nil }
func (h *journalTestHelper) WriteTest(_ context.Context, dir string) (priv.WriteTestResult, error) {
	h.dirs = append(h.dirs, dir)
	return h.res, nil
}

// openccu-lite B-213: a share (or a plugged-in stick) the journal's copies go to is write-tested
// in the folder before PUT /journal takes it.
func TestJournalTargetWriteTest(t *testing.T) {
	r := fakeRoot(t)
	root := string(r)
	for _, d := range []string{"proc/self", "sbin", "media/usb1"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.nfs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(root, "proc/mounts"), []byte("/dev/sda1 /media/usb1 exfat rw 0 0\n"), 0o644)
	old := system.USBStickProps
	system.USBStickProps = func(_ system.Root, dev string) map[string]string {
		return map[string]map[string]string{"/dev/sda1": {"ID_FS_LABEL": "LOGSTICK"}}[dev]
	}
	t.Cleanup(func() { system.USBStickProps = old })
	h := &journalTestHelper{}
	sh := &shares.Manager{Store: &shares.Store{Dir: t.TempDir()}, Root: root, Helper: func() shares.Helper { return h }}
	if _, err := sh.Store.Create(shares.Share{ID: "nasnfs", Kind: shares.KindNFS, Server: "192.0.2.1", Path: "/mnt/lab"}); err != nil {
		t.Fatal(err)
	}
	svc := &fakeServices{}
	run := func(context.Context, string, ...string) ([]byte, error) { return []byte("1M\n"), nil }
	api := &SystemAPI{Root: r, Services: svc, Journal: &system.JournalLog{Run: run}, Shares: sh}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	put := func(body string) (int, map[string]any) {
		st, out, _ := do(t, srv, "PUT", "/api/system/v1/journal", body, nil)
		return st, out
	}

	// a root-squashed export: refused with the reason and the hint, nothing written
	h.res = priv.WriteTestResult{Step: "mkdir", Errno: "EACCES", Error: "mkdir /media/net/nasnfs/t/journal: permission denied", FSType: "nfs"}
	st, out := put(`{"storage":"ram-sync","target":"share:nasnfs/t/journal"}`)
	if st != 422 || out["error"] != "read-only" || !strings.Contains(out["message"].(string), "root_squash") || out["detail"].(map[string]any)["step"] != "mkdir" {
		t.Fatalf("%d %v", st, out)
	}
	if len(h.dirs) != 1 || h.dirs[0] != filepath.Join(root, "/media/net/nasnfs/t/journal") {
		t.Fatalf("%q", h.dirs)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/config/journal")); !os.IsNotExist(err) || len(svc.calls) != 0 {
		t.Fatalf("the setting was written: %v %v", err, svc.calls)
	}
	// the server down: unreachable
	h.res = priv.WriteTestResult{Step: "mkdir", Errno: "EHOSTDOWN", Error: "host is down"}
	if st, out := put(`{"storage":"ram-sync","target":"share:nasnfs/t/journal"}`); st != 422 || out["error"] != "unreachable" {
		t.Fatalf("%d %v", st, out)
	}
	// writable: taken, target_ok
	h.res = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs"}
	st, out = put(`{"storage":"ram-sync","target":"share:nasnfs/t/journal"}`)
	if st != 200 || out["target"] != "share:nasnfs/t/journal" || out["target_ok"] != true {
		t.Fatalf("%d %v", st, out)
	}
	// the same target saved again while it fails (the sizes changed): taken, target_ok false
	h.res = priv.WriteTestResult{Step: "create", Errno: "EACCES", Error: "permission denied", FSType: "nfs"}
	st, out = put(`{"storage":"ram-sync","target":"share:nasnfs/t/journal","system_max_use":"64M"}`)
	if st != 200 || out["target_ok"] != false {
		t.Fatalf("%d %v", st, out)
	}
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/journal", "", nil); out["target_ok"] != false {
		t.Fatalf("GET: %v", out)
	}
	// a plugged-in stick: tested in its folder; RAM only is not tested
	h.dirs = nil
	h.res = priv.WriteTestResult{Step: "create", Errno: "EROFS", Error: "read-only file system"}
	if st, out := put(`{"storage":"ram-sync","target":"usb:LOGSTICK/journal"}`); st != 422 || out["error"] != "read-only" {
		t.Fatalf("%d %v", st, out)
	}
	if len(h.dirs) != 1 || h.dirs[0] != filepath.Join(root, "/media/usb1/journal") {
		t.Fatalf("%q", h.dirs)
	}
	h.dirs = nil
	if st, _ := put(`{"storage":"ram","target":"usb:LOGSTICK/journal"}`); st != 200 || len(h.dirs) != 0 {
		t.Fatalf("%d %q", st, h.dirs)
	}
	// a stick that is not plugged in: nothing to test, taken (the copies wait for it)
	if st, _ := put(`{"storage":"ram-sync","target":"usb:OTHER/journal"}`); st != 200 || len(h.dirs) != 0 {
		t.Fatalf("%d %q", st, h.dirs)
	}
}

// openccu-lite B-223: root can write the share's folder and the system's own user cannot read what
// root wrote (a root-squashed NFS export): PUT /journal takes the target - the copies are written -
// and the view says target_unreadable; the Log page's answer names the copies it could not read.
func TestJournalTargetUnreadable(t *testing.T) {
	r := fakeRoot(t)
	root := string(r)
	for _, d := range []string{"proc/self", "sbin"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.nfs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), nil, 0o644)
	h := &journalTestHelper{res: priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs"}}
	sh := &shares.Manager{Store: &shares.Store{Dir: t.TempDir()}, Root: root, Helper: func() shares.Helper { return h },
		ReadBack: func(dir string) error { return fmt.Errorf("%w: %s: permission denied", shares.ErrUnreadable, dir) }}
	if _, err := sh.Store.Create(shares.Share{ID: "nasnfs", Kind: shares.KindNFS, Server: "192.0.2.1", Path: "/mnt/lab"}); err != nil {
		t.Fatal(err)
	}
	run := func(context.Context, string, ...string) ([]byte, error) { return []byte("1M\n"), nil }
	api := &SystemAPI{Root: r, Services: &fakeServices{}, Journal: &system.JournalLog{Run: run}, Shares: sh, Log: memLog{{Tag: "kernel", Severity: "info", Message: "in RAM"}}}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "PUT", "/api/system/v1/journal", `{"storage":"ram-sync","target":"share:nasnfs/t/journal"}`, nil)
	if st != 200 || out["target_ok"] != true || out["target_unreadable"] != true {
		t.Fatalf("%d %v", st, out)
	}
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/journal", "", nil); out["target_unreadable"] != true {
		t.Fatalf("GET: %v", out)
	}
	// readable again: the next test clears it
	sh.ReadBack = func(string) error { return nil }
	if _, out, _ := do(t, srv, "PUT", "/api/system/v1/journal", `{"storage":"ram-sync","target":"share:nasnfs/t/journal"}`, nil); out["target_unreadable"] != nil {
		t.Fatalf("readable: %v", out)
	}

	// the Log page: nothing to say while the share is not mounted
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); out["copies_unreadable"] != nil {
		t.Fatalf("idle: %v", out)
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a folder whatever its mode")
	}
	// mounted, the copies' folder one root made on a squashed export: not enterable for the user
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte("40 22 0:30 / /media/net/nasnfs rw - autofs systemd-1 rw\n41 40 0:31 / /media/net/nasnfs rw - nfs4 192.0.2.1:/mnt/lab rw\n"), 0o644)
	dir := filepath.Join(root, "media/net/nasnfs/t/journal")
	_ = os.MkdirAll(filepath.Join(dir, "0123abcd"), 0o755)
	_ = os.Chmod(dir, 0o300)
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	st, out, _ = do(t, srv, "GET", "/api/system/v1/log", "", nil)
	cu, _ := out["copies_unreadable"].(map[string]any)
	if st != 200 || len(out["lines"].([]any)) != 1 || cu["path"] != "/media/net/nasnfs/t/journal" || !strings.Contains(cu["hint"].(string), "Maproot") {
		t.Fatalf("%d %v", st, out)
	}
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/journal", "", nil); out["target_unreadable"] != true {
		t.Fatalf("mounted: %v", out)
	}
	_ = os.Chmod(dir, 0o755)
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); out["copies_unreadable"] != nil {
		t.Fatalf("readable: %v", out)
	}
}
