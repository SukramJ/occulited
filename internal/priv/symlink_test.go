package priv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonunit"
)

// openccu-lite B-235: the symlink operation checked the link and not its target, and WriteFile
// followed any link - so a link from an allowed path to /etc/passwd made the next write there
// root's. Everything here goes through Server.do against a temporary root; the test's own user
// stands for root, so a link the test makes in a directory only it writes is "the image's" and a
// link in a world-writable directory is "an addon's".
func TestSymlinkBoundary(t *testing.T) {
	root := t.TempDir()
	at := func(rel string) string { return filepath.Join(root, rel) }
	mk := func(rel string, mode os.FileMode) { _ = os.MkdirAll(at(rel), mode) }
	mk("usr/local/tmp", 0o755)
	mk("usr/local/etc/config", 0o755)
	mk("usr/local/etc/config/addons/x", 0o755)
	// B-295: an addon's config directory is the daemon's to write only when the addon is confined
	mk("usr/local/etc/config/addon-policy", 0o755)
	_ = os.WriteFile(at("usr/local/etc/config/addon-policy/x.conf"), []byte(addonunit.DropIn{ID: "x", Mode: "confined", UID: 30001}.Render()), 0o644)
	mk("etc", 0o755)
	mk("var/etc", 0o755)
	_ = os.WriteFile(at("usr/local/etc/config/rfd.conf"), []byte("[rfd]\n"), 0o644)
	_ = os.WriteFile(at("var/etc/hostname"), []byte("old\n"), 0o644)
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite")}
	ctx := context.Background()
	do := func(req request) response { return srv.do(ctx, req) }
	refused := func(req request) string {
		r := do(req)
		if !strings.HasPrefix(r.Error, "refused") {
			t.Helper()
			t.Errorf("%s %s%s: not refused: %+v", req.Op, req.Path, req.Src+req.Dst, r)
		}
		return r.Error
	}
	ok := func(req request) {
		if r := do(req); r.Error != "" {
			t.Helper()
			t.Errorf("%s %s%s: %s", req.Op, req.Path, req.Src+req.Dst, r.Error)
		}
	}

	// 1. the finding: the link may not point outside the allowed prefixes ...
	refused(request{Op: "symlink", Path: at("usr/local/tmp/x"), Target: "/etc/passwd"})
	refused(request{Op: "symlink", Path: at("usr/local/tmp/x"), Target: "../../../etc/passwd"})
	refused(request{Op: "symlink", Path: at("usr/local/tmp/x"), Target: ""})
	refused(request{Op: "symlink", Path: at("usr/local/tmp/x"), Target: "/run/systemd/system/rfd.service"})
	refused(request{Op: "symlink", Path: at("etc/passwd"), Target: at("usr/local/tmp/x")}) // the link's own path, as before
	// ... and the update image's link, the one the daemon makes, still may
	ok(request{Op: "symlink", Path: at("usr/local/.firmwareUpdate"), Target: "/usr/local/tmp/img.tgz"})
	ok(request{Op: "symlink", Path: at("usr/local/.firmwareUpdate"), Target: at("usr/local/tmp/img.tgz")})
	if _, err := os.Lstat(at("usr/local/.firmwareUpdate")); err != nil {
		t.Fatal(err)
	}
	// 2. ... and even a link that is there already (planted before this fix, or by an addon) is
	// not written through when it leaves the allowed paths: /etc/passwd stays as it is
	if err := os.Symlink("/etc/passwd", at("usr/local/tmp/x")); err != nil {
		t.Fatal(err)
	}
	why := refused(request{Op: "write", Path: at("usr/local/tmp/x"), Data: []byte("root::0:0::/:/bin/sh\n"), Mode: 0o644})
	if !strings.Contains(why, "resolves to /etc/passwd") {
		t.Errorf("the reason: %s", why)
	}
	refused(request{Op: "touch", Path: at("usr/local/tmp/x"), Mode: 0o644})
	refused(request{Op: "chmod", Path: at("usr/local/tmp/x"), Mode: 0o777})
	// remove takes the link itself away, never its target
	ok(request{Op: "remove", Path: at("usr/local/tmp/x")})
	if _, err := os.Lstat(at("usr/local/tmp/x")); err == nil {
		t.Error("the link is still there")
	}
	if _, err := os.Stat("/etc/passwd"); err != nil {
		t.Error("/etc/passwd is gone")
	}

	// 3. an addon's link in its own tree (its config directory; since B-294 its directory under
	// /usr/local/addons is no write prefix at all): the tree is world-writable here, as an addon-owned
	// directory is to root's eyes - a directory component that is such a link, and a file that is
	if err := os.Chmod(at("usr/local/etc/config/addons/x"), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(at("usr/local/etc/config"), at("usr/local/etc/config/addons/x/data")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(at("usr/local/etc/config/rfd.conf"), at("usr/local/etc/config/addons/x/conf")); err != nil {
		t.Fatal(err)
	}
	why = refused(request{Op: "write", Path: at("usr/local/etc/config/addons/x/data/rfd.conf"), Data: []byte("evil"), Mode: 0o644})
	if !strings.Contains(why, "others may write") {
		t.Errorf("the reason: %s", why)
	}
	refused(request{Op: "write", Path: at("usr/local/etc/config/addons/x/conf"), Data: []byte("evil"), Mode: 0o644})
	refused(request{Op: "touch", Path: at("usr/local/etc/config/addons/x/data/marker"), Mode: 0o644})
	refused(request{Op: "mkdir", Path: at("usr/local/etc/config/addons/x/data/sub"), Mode: 0o755})
	refused(request{Op: "chmod", Path: at("usr/local/etc/config/addons/x/conf"), Mode: 0o666})
	refused(request{Op: "chown", Path: at("usr/local/etc/config/addons/x/data/rfd.conf"), UID: 30001, GID: 30001})
	refused(request{Op: "remove", Path: at("usr/local/etc/config/addons/x/data/rfd.conf")})
	refused(request{Op: "removeall", Path: at("usr/local/etc/config/addons/x/data/crRFD")})
	_ = os.WriteFile(at("usr/local/tmp/new.tgz"), []byte("tgz"), 0o644)
	refused(request{Op: "rename", Src: at("usr/local/tmp/new.tgz"), Dst: at("usr/local/etc/config/addons/x/data/new.tgz")})
	if b, _ := os.ReadFile(at("usr/local/etc/config/rfd.conf")); string(b) != "[rfd]\n" {
		t.Errorf("rfd.conf changed: %q", b)
	}
	// a link as rename's source is not moved into place either
	refused(request{Op: "rename", Src: at("usr/local/etc/config/addons/x/conf"), Dst: at("usr/local/tmp/conf")})
	// removing the addon's tree removes the links, not what they point to
	ok(request{Op: "removeall", Path: at("usr/local/etc/config/addons/x")})
	if _, err := os.Stat(at("usr/local/etc/config/rfd.conf")); err != nil {
		t.Error("removeall followed the addon's link")
	}
	if _, err := os.Lstat(at("usr/local/etc/config/addons/x")); err == nil {
		t.Error("the addon's tree is still there")
	}

	// 4. a link on removable media, whoever owns it
	mk("media/usb1", 0o755)
	if err := os.Symlink(at("usr/local/tmp"), at("media/usb1/backups")); err != nil {
		t.Fatal(err)
	}
	refused(request{Op: "rename", Src: at("usr/local/tmp/new.tgz"), Dst: at("media/usb1/backups/new.tgz")})
	refused(request{Op: "write", Path: at("media/usb1/backups/note"), Data: []byte("x"), Mode: 0o644})

	// 5. the image's own links keep working: /etc/hostname -> /var/etc/hostname on a read-only
	// /etc (B-53), and /etc/config -> ../usr/local/etc/config before the userfs has the
	// directory (B-188)
	if err := os.Symlink(at("var/etc/hostname"), at("etc/hostname")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../usr/local/etc/config", at("etc/config")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(at("etc"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(at("etc"), 0o755) })
	ok(request{Op: "write", Path: at("etc/hostname"), Data: []byte("new\n"), Mode: 0o644})
	if b, _ := os.ReadFile(at("var/etc/hostname")); string(b) != "new\n" {
		t.Errorf("hostname not written through the image's link: %q", b)
	}
	if fi, err := os.Lstat(at("etc/hostname")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the image's link was replaced")
	}
	_ = os.RemoveAll(at("usr/local/etc/config"))
	ok(request{Op: "write", Path: at("etc/config/firewall-rules.json"), Data: []byte("{}\n"), Mode: 0o644})
	if b, _ := os.ReadFile(at("usr/local/etc/config/firewall-rules.json")); string(b) != "{}\n" {
		t.Errorf("not written through the dangling link: %q", b)
	}
	ok(request{Op: "mkdir", Path: at("etc/config/crRFD"), Mode: 0o755})
	ok(request{Op: "touch", Path: at("etc/config/crRFD/.nobackup"), Mode: 0o644})
	ok(request{Op: "chmod", Path: at("etc/config/firewall-rules.json"), Mode: 0o600})
	if fi, _ := os.Stat(at("usr/local/etc/config/firewall-rules.json")); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Error("chmod through the image's link")
	}
	ok(request{Op: "rename", Src: at("usr/local/tmp/new.tgz"), Dst: at("etc/config/new.tgz")})
	ok(request{Op: "remove", Path: at("etc/config/new.tgz")})
	if _, err := os.Lstat(at("usr/local/etc/config/new.tgz")); err == nil {
		t.Error("remove through the image's link did not remove")
	}
	if fi, err := os.Lstat(at("etc/config")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the /etc/config link was replaced")
	}
}

// The helper's Local follows nothing: with a link swapped in after the Server's check, the
// operation fails instead of taking the detour - the race the check alone would leave open.
func TestLocalNoFollow(t *testing.T) {
	root := t.TempDir()
	at := func(rel string) string { return filepath.Join(root, rel) }
	_ = os.MkdirAll(at("real"), 0o755)
	_ = os.MkdirAll(at("tree"), 0o755)
	_ = os.WriteFile(at("real/secret"), []byte("keep"), 0o600)
	if err := os.Symlink(at("real"), at("tree/dir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(at("real/secret"), at("tree/file")); err != nil {
		t.Fatal(err)
	}
	l := Local{noFollow: true}
	bad := func(what string, err error) {
		if err == nil {
			t.Helper()
			t.Errorf("%s: followed a link", what)
		}
	}
	bad("write through a directory link", l.WriteFile(at("tree/dir/secret"), []byte("x"), 0o644))
	// a link at the last component is replaced by the file, never followed (rename semantics):
	// the write lands in the directory that was checked
	if err := l.WriteFile(at("tree/file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(at("tree/file")); err != nil || !fi.Mode().IsRegular() {
		t.Error("the link at the last component was not replaced by the file")
	}
	if err := os.Remove(at("tree/file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(at("real/secret"), at("tree/file")); err != nil {
		t.Fatal(err)
	}
	bad("touch of a link", l.Touch(at("tree/file"), 0o644))
	bad("touch through a directory link", l.Touch(at("tree/dir/new"), 0o644))
	bad("mkdir through a directory link", l.MkdirAll(at("tree/dir/sub/deep"), 0o755))
	bad("chmod of a link", l.Chmod(at("tree/file"), 0o666))
	bad("chmod through a directory link", l.Chmod(at("tree/dir/secret"), 0o666))
	bad("chown through a directory link", l.Chown(at("tree/dir/secret"), os.Getuid(), os.Getgid(), false))
	bad("remove through a directory link", l.Remove(at("tree/dir/secret")))
	bad("removeall through a directory link", l.RemoveAll(at("tree/dir/secret")))
	bad("rename of a link", l.Rename(at("tree/file"), at("tree/moved")))
	bad("rename into a directory link", l.Rename(at("real/secret"), at("tree/dir/moved")))
	bad("symlink into a directory link", l.Symlink("/x", at("tree/dir/lnk")))
	if b, _ := os.ReadFile(at("real/secret")); string(b) != "keep" {
		t.Errorf("the target changed: %q", b)
	}
	if fi, _ := os.Stat(at("real/secret")); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Error("the target's mode changed")
	}
	// the plain operations on a path without links work, missing directories included
	if err := l.WriteFile(at("tree/a/b/c.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(at("tree/a/b/c.txt")); string(b) != "hello" {
		t.Errorf("written: %q", b)
	}
	if fi, _ := os.Stat(at("tree/a/b/c.txt")); fi == nil || fi.Mode().Perm() != 0o640 {
		t.Error("mode")
	}
	if err := l.WriteFile(at("tree/a/b/c.txt"), []byte("again"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Touch(at("tree/a/marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Chmod(at("tree/a/marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.Chown(at("tree/a/marker"), os.Getuid(), os.Getgid(), false); err != nil {
		t.Fatal(err)
	}
	if err := l.Rename(at("tree/a/marker"), at("tree/a/b/marker")); err != nil {
		t.Fatal(err)
	}
	if err := l.Symlink("b/marker", at("tree/a/lnk")); err != nil {
		t.Fatal(err)
	}
	if err := l.Symlink("b/c.txt", at("tree/a/lnk")); err != nil { // replaces the existing link
		t.Fatal(err)
	}
	if got, _ := os.Readlink(at("tree/a/lnk")); got != "b/c.txt" {
		t.Errorf("link: %q", got)
	}
	if err := l.Remove(at("tree/a/lnk")); err != nil {
		t.Fatal(err)
	}
	if err := l.Remove(at("tree/a/none")); err != nil {
		t.Errorf("a missing file is not an error: %v", err)
	}
	if err := l.MkdirAll(at("tree/a/b"), 0o755); err != nil { // exists already
		t.Fatal(err)
	}
	if err := l.RemoveAll(at("tree/a")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(at("tree/a")); err == nil {
		t.Error("removeall left the tree")
	}
	if err := l.RemoveAll(at("tree/none")); err != nil {
		t.Errorf("a missing tree is not an error: %v", err)
	}
	// the helper Server runs Local this way
	if got := (&Server{}).ops(); got != (Local{noFollow: true}) {
		t.Errorf("the server's Local: %+v", got)
	}
}
