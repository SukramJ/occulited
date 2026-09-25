package httpapi

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hobbyquaker/occulited/internal/backuptarget"
	"github.com/hobbyquaker/occulited/internal/shares"
	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 228, phases 3-4: the location picker's list per use and the folder completion.
func TestLocationsRoutes(t *testing.T) {
	g := newCryptRig(t)
	root := string(g.root)
	mk := func(p string) { _ = os.MkdirAll(filepath.Join(root, p), 0o755) }
	for _, d := range []string{"proc/self", "sbin", "media/usb1", "media/usb2", "usr/local/backup/old", "usr/local/backup/ccu", "usr/local/etc/occulite/data", "etc/config"} {
		mk(d)
	}
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.cifs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.nfs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(root, "proc/mounts"), []byte("/dev/sda1 /media/usb1 exfat rw 0 0\n/dev/sdb1 /media/usb2 vfat ro 0 0\n"), 0o644)
	old := system.USBStickProps
	system.USBStickProps = func(_ system.Root, dev string) map[string]string {
		return map[string]map[string]string{"/dev/sda1": {"ID_FS_LABEL": "LOGSTICK", "ID_MODEL": "Ultra_Fit"}, "/dev/sdb1": {"ID_FS_LABEL": "ROSTICK"}}[dev]
	}
	t.Cleanup(func() { system.USBStickProps = old })
	mk("media/usb1/journal/ccu")
	mk("media/usb1/jobs")
	_ = os.WriteFile(filepath.Join(root, "media/usb1/journey.txt"), nil, 0o644)
	mk("media/usb1/.hidden")
	_ = os.Symlink("/etc", filepath.Join(root, "media/usb1/jlink"))
	// the journal copies to the stick; a share and a read-only one
	_ = os.WriteFile(filepath.Join(root, "etc/config/journal"), []byte("STORAGE=ram-sync\nTARGET=usb:LOGSTICK/journal\n"), 0o644)
	h := &targetsHelper{}
	sh := &shares.Manager{Store: &shares.Store{Dir: g.state}, Root: root, Helper: func() shares.Helper { return h }}
	g.api.Shares = sh
	sh.Uses = g.api.ShareUses
	_, _ = sh.Store.Create(shares.Share{ID: "nas", Kind: "cifs", Server: "nas", Path: "b", User: "u"})
	_, _ = sh.Store.Create(shares.Share{ID: "media", Kind: "nfs", Server: "nas", Path: "/m", ReadOnly: true})
	bt := &backuptarget.Manager{Store: &backuptarget.Store{Dir: g.state, Root: root, ShareInfo: func(id string) (bool, bool) { return id == "nas", false }}, Root: root,
		Helper: func() backuptarget.Helper { return h }, Container: func() string { return "" }}
	g.api.BackupTargets = bt
	if _, err := bt.Store.Put(backuptarget.Target{Kind: backuptarget.KindShare, Name: "NAS", Enabled: true, Share: &backuptarget.ShareRef{ID: "nas", Folder: "ccu"}}); err != nil {
		t.Fatal(err)
	}

	byID := func(out map[string]any) map[string]map[string]any {
		m := map[string]map[string]any{}
		for _, l := range out["locations"].([]any) {
			v := l.(map[string]any)
			m[v["id"].(string)] = v
		}
		return m
	}
	if st, _ := g.do(t, "GET", "/storage/locations?use=x", ""); st != 400 {
		t.Fatalf("a bad use: %d", st)
	}
	// the journal: all three kinds, a read-only stick and share not
	st, out := g.do(t, "GET", "/storage/locations?use=journal", "")
	if st != 200 {
		t.Fatalf("%d %v", st, out)
	}
	l := byID(out)
	if len(l) != 5 || l["userfs"]["allowed"] != true || l["userfs"]["fixed"] != true || l["usb:LOGSTICK"]["allowed"] != true || l["usb:ROSTICK"]["allowed"] != false ||
		l["share:nas"]["allowed"] != true || l["share:media"]["allowed"] != false || l["share:media"]["code"] != "read-only" {
		t.Fatalf("%v", l)
	}
	if u := l["usb:LOGSTICK"]["uses"].([]any); len(u) != 1 || u[0].(map[string]any)["kind"] != "journal" {
		t.Fatalf("stick uses: %v", u)
	}
	if u := l["share:nas"]["uses"].([]any); len(u) != 1 || u[0].(map[string]any)["name"] != "NAS" || u[0].(map[string]any)["folder"] != "ccu" {
		t.Fatalf("share uses: %v", u)
	}
	// the database: the userfs only - a share never, with the reason
	_, out = g.do(t, "GET", "/storage/locations?use=store", "")
	l = byID(out)
	if l["userfs"]["allowed"] != true || l["userfs"]["default"] != "etc/occulite/data" || l["usb:LOGSTICK"]["code"] != "copy-only" || l["usb:LOGSTICK"]["allowed"] != true || l["share:nas"]["code"] != "no-share-for-store" || !strings.Contains(l["share:nas"]["reason"].(string), "fsync") {
		t.Fatalf("%v", l)
	}
	// the share's own list names its use, and it cannot be removed while used
	_, out = g.do(t, "GET", "/storage/shares", "")
	if u := out["shares"].([]any)[0].(map[string]any)["uses"].([]any); len(u) != 1 {
		t.Fatalf("%v", out["shares"])
	}
	if st, out := g.do(t, "DELETE", "/storage/shares/nas", ""); st != 409 || !strings.Contains(out["message"].(string), "the backup target NAS") {
		t.Fatalf("%d %v", st, out)
	}
	// folders: directories only, no hidden ones, no links, by prefix
	st, out = g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=j", "")
	if st != 200 || strings.Join(dirList(out["dirs"]), ",") != "jobs,journal" || out["exists"] != false {
		t.Fatalf("%d %v", st, out)
	}
	_, out = g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=journal/", "")
	if strings.Join(dirList(out["dirs"]), ",") != "journal/ccu" {
		t.Fatalf("%v", out)
	}
	_, out = g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=journal", "")
	if out["exists"] != true {
		t.Fatalf("%v", out)
	}
	if st, _ := g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=jlink/", ""); st != 200 {
		t.Fatalf("%d", st)
	} else if _, out = g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=jlink/", ""); len(dirList(out["dirs"])) != 0 {
		t.Fatalf("followed a link: %v", out)
	}
	if st, _ := g.do(t, "GET", "/storage/dirs?use=journal&location=usb:LOGSTICK&prefix=../", ""); st != 400 {
		t.Fatalf("..: %d", st)
	}
	if st, _ := g.do(t, "GET", "/storage/dirs?use=journal&location=usb:NOSTICK&prefix=", ""); st != 404 {
		t.Fatalf("no stick: %d", st)
	}
	// the userfs: only below the use's part of it
	_, out = g.do(t, "GET", "/storage/dirs?use=backup&location=userfs&prefix=", "")
	if strings.Join(dirList(out["dirs"]), ",") != "backup" {
		t.Fatalf("%v", out)
	}
	_, out = g.do(t, "GET", "/storage/dirs?use=backup&location=userfs&prefix=backup/", "")
	if strings.Join(dirList(out["dirs"]), ",") != "backup/ccu,backup/old" {
		t.Fatalf("%v", out)
	}
	_, out = g.do(t, "GET", "/storage/dirs?use=store&location=userfs&prefix=etc/occulite/", "")
	if strings.Join(dirList(out["dirs"]), ",") != "etc/occulite/data" {
		t.Fatalf("%v", out)
	}
	// a share: its mount point (the fake root's directory)
	mk("media/net/nas/ccu")
	_, out = g.do(t, "GET", "/storage/dirs?use=backup&location=share:nas&prefix=", "")
	if strings.Join(dirList(out["dirs"]), ",") != "ccu" {
		t.Fatalf("%v", out)
	}
	// the journal on a share that does not exist, or is read-only: refused before the file
	for target, ok := range map[string]bool{"share:none/journal": false, "share:media/journal": false, "share:nas/journal": true, "usb:LOGSTICK/journal": true, "userfs": true} {
		if err := g.api.journalShareOK(target); (err == nil) != ok {
			t.Errorf("%s: %v", target, err)
		}
	}
}

func dirList(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

// openccu-lite B-214: a folder listing that fails says why - only a folder that does not exist yet
// is "nothing there".
func TestLocationDirsFailures(t *testing.T) {
	g := newCryptRig(t)
	root := string(g.root)
	for _, d := range []string{"proc/self", "sbin", "media/net/nas/journal/ccu"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.nfs"), nil, 0o755)
	_ = os.WriteFile(filepath.Join(root, "sbin/mount.cifs"), nil, 0o755)
	mounted := "41 40 0:31 / /media/net/nas rw - nfs4 192.0.2.1:/data rw\n"
	_ = os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte(mounted), 0o644)
	h := &targetsHelper{}
	sh := &shares.Manager{Store: &shares.Store{Dir: g.state}, Root: root, Helper: func() shares.Helper { return h }}
	sh.Journal = func(unit string) string {
		if unit == "media-net-smb.mount" {
			return "Mounting x...\nmount error(13): Permission denied\nFailed to mount x."
		}
		return ""
	}
	g.api.Shares = sh
	_, _ = sh.Store.Create(shares.Share{ID: "nas", Kind: "nfs", Server: "192.0.2.1", Path: "/data"})
	_, _ = sh.Store.Create(shares.Share{ID: "smb", Kind: "cifs", Server: "nas", Path: "b", User: "u"})
	_, _ = sh.Store.Create(shares.Share{ID: "gone", Kind: "nfs", Server: "192.0.2.2", Path: "/x"})

	// mounted: a folder that is not there yet is nothing; a trailing slash names an existing folder
	st, out := g.do(t, "GET", "/storage/dirs?use=journal&location=share:nas&prefix=new/", "")
	if st != 200 || len(dirList(out["dirs"])) != 0 || out["exists"] != false {
		t.Fatalf("%d %v", st, out)
	}
	st, out = g.do(t, "GET", "/storage/dirs?use=journal&location=share:nas&prefix=journal/", "")
	if st != 200 || strings.Join(dirList(out["dirs"]), ",") != "journal/ccu" || out["exists"] != true {
		t.Fatalf("%d %v", st, out)
	}
	// B-217: a share's folders are read through the helper, as root
	if !strings.Contains(strings.Join(h.calls, "|"), "sharelist "+filepath.Join(root, "media/net/nas/journal")) {
		t.Fatalf("%q", h.calls)
	}
	// the mount failed: its reason, not "no folders"
	st, out = g.do(t, "GET", "/storage/dirs?use=journal&location=share:smb&prefix=", "")
	if st != 502 || out["error"] != "auth-failed" || !strings.Contains(out["message"].(string), "mount error(13)") {
		t.Fatalf("%d %v", st, out)
	}
	st, out = g.do(t, "GET", "/storage/dirs?use=journal&location=share:gone&prefix=", "")
	if st != 502 || out["error"] != "unreachable" {
		t.Fatalf("%d %v", st, out)
	}
	// a mounted folder the system may not read: read-only
	perm := &os.PathError{Op: "open", Path: "/media/net/nas/x", Err: syscall.EACCES}
	if st, _ := g.api.dirsFailure("share", "nas", perm); st != shares.StateReadOnly {
		t.Fatal(st)
	}
	if st, _ := g.api.dirsFailure("usb", "LOGSTICK", perm); st != shares.StateReadOnly {
		t.Fatal(st)
	}
	if st, _ := g.api.dirsFailure("share", "nas", &os.PathError{Op: "open", Path: "x", Err: syscall.EIO}); st != shares.StateStale {
		t.Fatal(st)
	}
	if st, _ := g.api.dirsFailure("usb", "LOGSTICK", &os.PathError{Op: "open", Path: "x", Err: syscall.ENOENT}); st != "" {
		t.Fatal(st)
	}
}
