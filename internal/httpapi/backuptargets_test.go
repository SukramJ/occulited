package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/backuptarget"
	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/system"
)

type targetsHelper struct{ calls []string }

func (h *targetsHelper) NetMount(_ context.Context, s netmount.Spec) error {
	h.calls = append(h.calls, "mount "+s.ID)
	return nil
}
func (h *targetsHelper) NetUnmount(_ context.Context, id string) error {
	h.calls = append(h.calls, "unmount "+id)
	return nil
}
func (h *targetsHelper) ShareList(ctx context.Context, dir string) (priv.ShareListResult, error) {
	h.calls = append(h.calls, "sharelist "+dir)
	return priv.Local{}.ShareList(ctx, dir)
}
func (h *targetsHelper) ShareOpen(path string) (*os.File, error) { return priv.Local{}.ShareOpen(path) }
func (h *targetsHelper) NetMountRemove(_ context.Context, id string) error {
	h.calls = append(h.calls, "remove "+id)
	return nil
}
func (h *targetsHelper) WriteTest(_ context.Context, dir string) (priv.WriteTestResult, error) {
	h.calls = append(h.calls, "test "+dir)
	return priv.WriteTestResult{OK: true, Step: "done", FSType: "vfat", FreeBytes: 1 << 30, TotalBytes: 2 << 30}, nil
}
func (h *targetsHelper) Run(_ context.Context, name string, args []string, _ []byte) (priv.Result, error) {
	h.calls = append(h.calls, name+" "+strings.Join(args, " "))
	return priv.Result{}, nil
}

func newTargetsRig(t *testing.T) (*cryptRig, *backuptarget.Manager, *targetsHelper) {
	t.Helper()
	g := newCryptRig(t)
	h := &targetsHelper{}
	m := &backuptarget.Manager{Store: &backuptarget.Store{Dir: g.state, Root: string(g.root)}, Crypt: g.store, Root: string(g.root),
		Helper: func() backuptarget.Helper { return h }, Hostname: func() string { return "lab-box" }, Container: func() string { return "" },
		Systemctl: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	g.api.BackupTargets = m
	return g, m, h
}

func TestBackupTargetsRoutes(t *testing.T) {
	g, m, h := newTargetsRig(t)
	// the directory target (upstream's markers) and an SFTP target, which gets its key at once
	st, out := g.do(t, "POST", "/backup/targets", `{"kind":"directory","name":"USB","enabled":true,"max_backups":10,"encrypt":true,"directory":{"path":"/media/usb1/backup"}}`)
	if st != 201 {
		t.Fatalf("%d %v", st, out)
	}
	if st, out := g.do(t, "POST", "/backup/targets", `{"kind":"directory","name":"USB2","directory":{"path":"/media/usb2"}}`); st != 409 {
		t.Fatalf("a second directory target: %d %v", st, out)
	}
	st, out = g.do(t, "POST", "/backup/targets", `{"id":"evil","kind":"sftp","name":"NAS","enabled":true,"max_backups":30,"subdir":"lab-box","encrypt":true,"sftp":{"host":"nas.lan","user":"backup","path":"/srv/backup","public_key":"x"}}`)
	if st != 201 {
		t.Fatalf("%d %v", st, out)
	}
	tg := out["target"].(map[string]any)
	id := tg["id"].(string)
	sf := tg["sftp"].(map[string]any)
	if id == "evil" || !strings.HasPrefix(sf["public_key"].(string), "ssh-ed25519 ") || sf["port"].(float64) != 22 {
		t.Fatalf("%v", tg)
	}
	// task 228: NFS and SMB are shares of System → Storage now - a new task-86 kind is refused,
	// a share target names a share (it must exist and be writable) and a folder
	if st, _ := g.do(t, "POST", "/backup/targets", `{"kind":"nfs","name":"TrueNAS","enabled":true,"nfs":{"server":"192.0.2.10","export":"/mnt/tank/b"}}`); st != 400 {
		t.Fatalf("a new NFS target: %d", st)
	}
	m.Store.ShareInfo = func(id string) (bool, bool) { return id == "nas" || id == "media", id == "media" }
	for _, bad := range []string{`{"id":"none","folder":"x"}`, `{"id":"media","folder":"x"}`, `{"id":"nas","folder":"../x"}`} {
		if st, out := g.do(t, "POST", "/backup/targets", `{"kind":"share","name":"NAS","enabled":true,"share":`+bad+`}`); st != 400 {
			t.Fatalf("%s: %d %v", bad, st, out)
		}
	}
	st, out = g.do(t, "POST", "/backup/targets", `{"kind":"share","name":"TrueNAS","enabled":true,"share":{"id":"nas","folder":"openccu/lab-box"}}`)
	if st != 201 {
		t.Fatalf("%d %v", st, out)
	}
	nfsID := out["target"].(map[string]any)["id"].(string)
	if strings.Contains(strings.Join(h.calls, "|"), "mount ") {
		t.Fatalf("a share target renders no units: %q", h.calls)
	}
	// the list: three targets, the kinds and the nightly switch
	st, out = g.do(t, "GET", "/backup/targets", "")
	if st != 200 || len(out["targets"].([]any)) != 3 || out["nightly"].(map[string]any)["enabled"] != true || out["hostname"] != "lab-box" {
		t.Fatalf("%d %v", st, out)
	}
	if k := out["kinds"].(map[string]any); k["share"] != "" || k["sftp"] != "" {
		t.Fatalf("%v", k)
	}
	// edits: a kind change refused, an unknown id 404, a bad field 400
	if st, _ := g.do(t, "PUT", "/backup/targets/"+id, `{"kind":"nfs","name":"x","nfs":{"server":"a","export":"/b"}}`); st != 400 {
		t.Fatalf("kind change: %d", st)
	}
	if st, _ := g.do(t, "PUT", "/backup/targets/tnothere", `{"kind":"sftp","name":"x","sftp":{"host":"a","user":"b"}}`); st != 404 {
		t.Fatalf("unknown: %d", st)
	}
	if st, _ := g.do(t, "PUT", "/backup/targets/"+id, `{"kind":"sftp","name":"x","subdir":"../etc","sftp":{"host":"a","user":"b"}}`); st != 400 {
		t.Fatalf("bad subdir: %d", st)
	}
	// the test of the directory target runs through the helper, as root
	st, out = g.do(t, "POST", "/backup/targets/directory/test", "")
	if st != 200 || out["ok"] != true || out["state"] != "writable" {
		t.Fatalf("%d %v", st, out)
	}
	// Back up now: the unit, and an unknown target never reaches systemctl
	if st, out := g.do(t, "POST", "/backup/targets/"+id+"/run", ""); st != 202 {
		t.Fatalf("%d %v", st, out)
	}
	if st, _ := g.do(t, "POST", "/backup/run", `{"target":"../x"}`); st != 400 {
		t.Fatalf("bad instance: %d", st)
	}
	if st, _ := g.do(t, "POST", "/backup/schedule/run", ""); st != 202 {
		t.Fatalf("schedule run: %d", st)
	}
	calls := strings.Join(h.calls, "|")
	if !strings.Contains(calls, "systemctl start --no-block occu-backup-create@"+id+".service") || !strings.Contains(calls, "occu-backup-create@directory.service") {
		t.Fatalf("%q", h.calls)
	}
	// the nightly switch is upstream's NoCronBackup
	if st, out := g.do(t, "PUT", "/backup/nightly", `{"enabled":false}`); st != 200 || out["enabled"] != false {
		t.Fatalf("%d %v", st, out)
	}
	if _, err := os.Stat(string(g.root) + backuptarget.MarkerNoNightly); err != nil {
		t.Fatal("NoCronBackup not written")
	}
	// mount and unmount are for shares only
	if st, _ := g.do(t, "POST", "/backup/targets/"+id+"/unmount", ""); st != 409 {
		t.Fatalf("unmount sftp: %d", st)
	}
	h.calls = nil
	if st, _ := g.do(t, "POST", "/backup/targets/"+nfsID+"/unmount", ""); st != 200 {
		t.Fatalf("unmount share: %d", st)
	}
	if strings.Join(h.calls, "|") != "unmount nas" {
		t.Fatalf("the share's mount: %q", h.calls)
	}
	// removal: the units go, the files on the target are never touched
	if st, _ := g.do(t, "DELETE", "/backup/targets/"+nfsID, ""); st != 204 {
		t.Fatalf("delete: %d", st)
	}
	if strings.Contains(strings.Join(h.calls, "|"), "remove ") {
		t.Fatalf("the share's units stay: %q", h.calls)
	}
	_ = m
}

func TestBackupTargetsRestoreAndWarnings(t *testing.T) {
	g, m, _ := newTargetsRig(t)
	dir := string(g.root) + "/media/usb1/backup"
	_ = os.MkdirAll(dir, 0o755)
	if _, err := m.Store.Put(backuptarget.Target{Kind: backuptarget.KindDirectory, Name: "USB", Enabled: true, Encrypt: true, Directory: &backuptarget.Directory{Path: "/media/usb1/backup"}}); err != nil {
		t.Fatal(err)
	}
	name := "lab-box-1.0.0-2026-09-24-0007.sbk"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(g.sbk), 0o644); err != nil {
		t.Fatal(err)
	}
	st, out := g.do(t, "GET", "/backup/targets/directory/backups", "")
	if st != 200 || len(out["backups"].([]any)) != 1 {
		t.Fatalf("%d %v", st, out)
	}
	// the restore check straight from the target, as an upload would
	st, out = g.do(t, "POST", "/restore/check", `{"target":"directory","name":"`+name+`"}`)
	if st != 200 || out["file"] != "restore-"+name || out["check"] == nil {
		t.Fatalf("%d %v", st, out)
	}
	if _, err := os.Stat(filepath.Join(string(g.root), system.BackupDir, "restore-"+name)); err != nil {
		t.Fatal("not staged")
	}
	for _, bad := range []string{`{"target":"directory","name":"../../etc/passwd"}`, `{"target":"directory","name":"x.txt"}`, `{"target":"tnothere","name":"a.sbk"}`} {
		if st, _ := g.do(t, "POST", "/restore/check", bad); st != 400 && st != 404 {
			t.Errorf("%s: %d", bad, st)
		}
	}
	// a failed delivery is a Status warning with its cause; a success a day and a half ago too-old
	sshT, _ := m.Store.Put(backuptarget.Target{Name: "NAS", Kind: backuptarget.KindSFTP, Enabled: true, SFTP: &backuptarget.SFTP{Host: "nas", User: "b"}})
	_ = backuptarget.WriteResult(m.Store.SecretDir(sshT.ID), backuptarget.Result{At: time.Now(), State: backuptarget.StateAuthFailed, Error: "unable to authenticate"})
	_ = backuptarget.WriteResult(m.Store.SecretDir(backuptarget.DirectoryID), backuptarget.Result{At: time.Now().Add(-36 * time.Hour), OK: true, State: backuptarget.StateWritable})
	ws, ok := g.api.backupDeliveryWarnings(context.Background())
	if !ok || len(ws) != 2 {
		t.Fatalf("%v %+v", ok, ws)
	}
	got := map[string]string{}
	for _, w := range ws {
		got[w.Variant] = w.Severity
	}
	if got[sshT.ID+":auth-failed"] != "error" || got["directory:too-old"] != "warning" {
		t.Fatalf("%v", got)
	}
	// backups that leave the system unencrypted while a network target exists
	if ws, _ := g.api.backupUnencryptedWarning(context.Background()); len(ws) != 1 {
		t.Fatalf("%+v", ws)
	}
}
