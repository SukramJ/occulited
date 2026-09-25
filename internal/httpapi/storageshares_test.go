package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/shares"
)

// openccu-lite task 228, phase 2: the shares' routes - add, edit, the password write-only, test,
// mount and unmount through the helper, remove.
func TestSharesRoutes(t *testing.T) {
	g := newCryptRig(t)
	h := &targetsHelper{}
	root := string(g.root)
	for _, d := range []string{"proc/self", "sbin"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.cifs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.nfs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), nil, 0o644)
	if st, _ := g.do(t, "GET", "/storage/shares", ""); st != 501 {
		t.Fatalf("no manager: %d", st)
	}
	g.api.Shares = &shares.Manager{Store: &shares.Store{Dir: g.state}, Root: root, Helper: func() shares.Helper { return h }}

	st, out := g.do(t, "POST", "/storage/shares", `{"id":"nas","kind":"cifs","server":"nas.lan","path":"data","user":"ccu","password":"s3cret","version":"3.1.1"}`)
	if st != 201 {
		t.Fatalf("%d %v", st, out)
	}
	sh := out["share"].(map[string]any)
	if sh["has_password"] != true || sh["password"] != nil || sh["where"] != "/media/net/nas" || sh["state"].(map[string]any)["state"] != "idle" {
		t.Fatalf("%v", sh)
	}
	if strings.Join(h.calls, "|") != "mount nas" {
		t.Fatalf("%q", h.calls)
	}
	if st, _ := g.do(t, "POST", "/storage/shares", `{"id":"nas","kind":"nfs","server":"nas.lan","path":"/x"}`); st != 409 {
		t.Fatalf("the same name: %d", st)
	}
	if st, _ := g.do(t, "POST", "/storage/shares", `{"id":"Bad Name","kind":"nfs","server":"nas.lan","path":"/x"}`); st != 400 {
		t.Fatalf("a bad name: %d", st)
	}
	if st, _ := g.do(t, "POST", "/storage/shares", `{"id":"x","kind":"nfs","server":"nas.lan","path":"/x","where":"/etc"}`); st != 422 {
		t.Fatalf("a field that is not the share's: %d", st)
	}
	// the list never carries the password
	st, out = g.do(t, "GET", "/storage/shares", "")
	if st != 200 || out["kinds"].(map[string]any)["cifs"] != "" {
		t.Fatalf("%d %v", st, out)
	}
	if b, _ := os.ReadFile(filepath.Join(g.state, "shares.json")); strings.Contains(string(b), "s3cret") {
		t.Fatal("the password in shares.json")
	}
	// an edit: read-only now, no rename
	if st, _ := g.do(t, "PUT", "/storage/shares/nas", `{"id":"other","kind":"cifs","server":"nas.lan","path":"data","user":"ccu"}`); st != 400 {
		t.Fatalf("a rename: %d", st)
	}
	st, out = g.do(t, "PUT", "/storage/shares/nas", `{"kind":"cifs","server":"nas.lan","path":"data","user":"ccu","read_only":true}`)
	if st != 200 || out["share"].(map[string]any)["read_only"] != true || out["share"].(map[string]any)["has_password"] != true {
		t.Fatalf("%d %v", st, out)
	}
	if st, _ := g.do(t, "PUT", "/storage/shares/none", `{"kind":"nfs","server":"nas.lan","path":"/x"}`); st != 404 {
		t.Fatalf("%d", st)
	}
	// the test of a read-only share lists it: nothing mounted in the fake root, so unreachable
	h.calls = nil
	st, out = g.do(t, "POST", "/storage/shares/nas/test", "")
	if st != 200 || out["ok"] != false || out["state"] != "unreachable" || out["read_only"] != true {
		t.Fatalf("%d %v", st, out)
	}
	// a writable NFS share: the helper's write test (vfat in the fake: not the share)
	g.do(t, "POST", "/storage/shares", `{"id":"nfs1","kind":"nfs","server":"192.0.2.9","path":"/export"}`)
	h.calls = nil
	st, out = g.do(t, "POST", "/storage/shares/nfs1/test", "")
	if st != 200 || out["state"] != "unreachable" || out["step"] != "mount" {
		t.Fatalf("%d %v", st, out)
	}
	if strings.Join(h.calls, "|") != "test "+filepath.Join(root, "media/net/nfs1")+"|unmount nfs1" {
		t.Fatalf("%q", h.calls)
	}
	if st, _ := g.do(t, "POST", "/storage/shares/nfs1/unmount", ""); st != 200 {
		t.Fatalf("%d", st)
	}
	if st, _ := g.do(t, "POST", "/storage/shares/none/mount", ""); st != 404 {
		t.Fatalf("%d", st)
	}
	if st, _ := g.do(t, "DELETE", "/storage/shares/nfs1", ""); st != 204 {
		t.Fatalf("%d", st)
	}
	if st, _ := g.do(t, "DELETE", "/storage/shares/nfs1", ""); st != 404 {
		t.Fatalf("%d", st)
	}
}
