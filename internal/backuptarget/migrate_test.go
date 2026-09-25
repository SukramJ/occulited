package backuptarget

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/shares"
)

// task 228: task 86's NFS and SMB targets become shares of System → Storage and targets of kind
// share on them, once.
func TestMigrateMounts(t *testing.T) {
	root := t.TempDir()
	st := &Store{Dir: t.TempDir(), Root: root}
	sh := &shares.Store{Dir: st.Dir}
	if _, err := st.Put(Target{Kind: KindDirectory, Name: "USB", Enabled: true, Directory: &Directory{Path: "/media/usb1/backup"}}); err != nil {
		t.Fatal(err)
	}
	n, _ := st.Put(Target{Name: "TrueNAS", Kind: KindNFS, Enabled: true, MaxBackups: 7, Subdir: "lab-box", Encrypt: true, NFS: &NFS{Server: "192.0.2.10", Export: "/mnt/tank/b", Version: "4.2"}})
	pw := "s3cret"
	c, _ := st.Put(Target{Name: "NAS 2", Kind: KindCIFS, Enabled: true, CIFS: &CIFS{Server: "nas", Share: "backup", User: "ccu", Domain: "WORK", Password: &pw, Seal: true}})
	// a second one whose name comes out the same: it keeps its own id as the share's name
	o, _ := st.Put(Target{Name: "truenas", Kind: KindNFS, Enabled: false, NFS: &NFS{Server: "other", Export: "/x"}})
	_ = WriteResult(st.SecretDir(n.ID), Result{OK: true, State: StateWritable, Name: "x.sbk"})
	got, err := st.MigrateMounts(sh)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}
	want := []Migrated{
		{Target: n.ID, Name: "TrueNAS", Share: "truenas", Folder: "lab-box", OldMount: n.ID},
		{Target: c.ID, Name: "NAS 2", Share: "nas2", OldMount: c.ID},
		{Target: o.ID, Name: "truenas", Share: o.ID},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: %+v, want %+v", i, got[i], want[i])
		}
	}
	list, _ := sh.List()
	if len(list) != 3 || list[0].ID != "truenas" || list[0].Kind != "nfs" || list[0].Server != "192.0.2.10" || list[0].Path != "/mnt/tank/b" || list[0].Version != "4.2" {
		t.Fatalf("%+v", list)
	}
	if s := list[1]; s.ID != "nas2" || s.Kind != "cifs" || s.Path != "backup" || s.User != "ccu" || s.Domain != "WORK" || !s.Seal || !s.HasPassword {
		t.Fatalf("%+v", s)
	}
	if b, _ := os.ReadFile(filepath.Join(sh.SecretDir("nas2"), shares.CredFile)); string(b) != "username=ccu\npassword=s3cret\ndomain=WORK\n" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(filepath.Join(st.SecretDir(c.ID), CredFile)); !os.IsNotExist(err) {
		t.Fatal("the old credentials stayed")
	}
	tn, _, _ := st.Get(n.ID)
	if tn.Kind != KindShare || tn.Share == nil || tn.Share.ID != "truenas" || tn.Share.Folder != "lab-box" || tn.Subdir != "" || tn.NFS != nil || tn.MaxBackups != 7 || !tn.Enabled || !tn.Encrypt {
		t.Fatalf("%+v", tn)
	}
	if tn.Dir() != "/media/net/truenas/lab-box" || tn.MountID() != "truenas" {
		t.Fatal(tn.Dir(), tn.MountID())
	}
	if _, ok := ReadResult(st.SecretDir(n.ID)); !ok {
		t.Fatal("the result went")
	}
	// a second start finds nothing to do
	if again, err := st.MigrateMounts(sh); err != nil || again != nil {
		t.Fatalf("%+v %v", again, err)
	}
	if d, _, _ := st.Get(DirectoryID); d.Kind != KindDirectory {
		t.Fatal("the directory target changed")
	}
}

func TestMigrateResumes(t *testing.T) {
	st := &Store{Dir: t.TempDir(), Root: t.TempDir()}
	sh := &shares.Store{Dir: st.Dir}
	n, _ := st.Put(Target{Name: "NAS", Kind: KindNFS, NFS: &NFS{Server: "nas", Export: "/b"}})
	// an earlier run made the share and stopped before the target was saved
	if _, err := sh.Create(shares.Share{ID: "nas", Kind: "nfs", Server: "nas", Path: "/b"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MigrateMounts(sh)
	if err != nil || len(got) != 1 || got[0].Share != "nas" {
		t.Fatalf("%+v %v", got, err)
	}
	if l, _ := sh.List(); len(l) != 1 {
		t.Fatalf("%+v", l)
	}
	if tn, _, _ := st.Get(n.ID); tn.Share == nil || tn.Share.ID != "nas" {
		t.Fatalf("%+v", tn)
	}
}

// a share the user added for the same server and path, with its own account and name, is never
// taken over: the target gets a share of its own
func TestMigrateKeepsOtherShares(t *testing.T) {
	st := &Store{Dir: t.TempDir(), Root: t.TempDir()}
	sh := &shares.Store{Dir: st.Dir}
	pw := "right"
	c, _ := st.Put(Target{Name: "Samba Lab", Kind: KindCIFS, CIFS: &CIFS{Server: "nas", Share: "b", User: "ccu", Password: &pw}})
	other := "wrong"
	if _, err := sh.Create(shares.Share{ID: "badpw", Kind: "cifs", Server: "nas", Path: "b", User: "ccu", Password: &other}); err != nil {
		t.Fatal(err)
	}
	// and one named as the target would be, but for another server
	if _, err := sh.Create(shares.Share{ID: "sambalab", Kind: "cifs", Server: "elsewhere", Path: "b", User: "x"}); err != nil {
		t.Fatal(err)
	}
	got, err := st.MigrateMounts(sh)
	if err != nil || len(got) != 1 || got[0].Share != c.ID {
		t.Fatalf("%+v %v", got, err)
	}
	if b, _ := os.ReadFile(filepath.Join(sh.SecretDir(c.ID), shares.CredFile)); string(b) != "username=ccu\npassword=right\n" {
		t.Fatalf("%q", b)
	}
}

func TestShareName(t *testing.T) {
	for in, want := range map[string]string{"NAS": "nas", "True NAS 2": "truenas2", "2nd NAS": "ndnas", "---": "", "Überspeicher": "berspeicher", "a very long name for a share": "averylongnamefor"} {
		if got := shareName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// task 228: a directory target chosen by its location follows the stick wherever it is mounted
func TestDirectoryLocation(t *testing.T) {
	root := t.TempDir()
	mounts := map[string]string{"BACKUPS": "/media/usb2"}
	st := &Store{Dir: t.TempDir(), Root: root, USBMount: func(l string) (string, bool) { m, ok := mounts[l]; return m, ok }}
	d, err := st.Put(Target{Kind: KindDirectory, Name: "USB", Enabled: true, Directory: &Directory{Location: "usb:BACKUPS/ccu/backup"}})
	if err != nil || d.Directory.Path != "/media/usb2/ccu/backup" || d.Directory.Location != "usb:BACKUPS/ccu/backup" {
		t.Fatalf("%+v %v", d.Directory, err)
	}
	if readTrim(filepath.Join(root, MarkerPath)) != "/media/usb2/ccu/backup" {
		t.Fatal(readTrim(filepath.Join(root, MarkerPath)))
	}
	mounts["BACKUPS"] = "/media/usb1"
	if d, _, _ = st.Get(DirectoryID); d.Dir() != "/media/usb1/ccu/backup" || !d.OnUSB() {
		t.Fatal(d.Dir())
	}
	delete(mounts, "BACKUPS")
	if d, _, _ = st.Get(DirectoryID); d.Dir() != MissingStickDir+"/ccu/backup" || !d.OnUSB() {
		t.Fatal(d.Dir())
	}
	u, err := st.Put(Target{Kind: KindDirectory, Name: "USB", Enabled: true, Directory: &Directory{Location: "userfs:backup"}})
	if err != nil || u.Dir() != "/usr/local/backup" || u.OnUSB() {
		t.Fatalf("%+v %v", u.Directory, err)
	}
	for _, bad := range []string{"userfs:etc", "share:nas/x", "usb:A B/x"} {
		if _, err := st.Put(Target{Kind: KindDirectory, Name: "USB", Directory: &Directory{Location: bad}}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
