package backuptarget

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreTargets(t *testing.T) {
	root := t.TempDir()
	st := &Store{Dir: t.TempDir(), Root: root}
	list, err := st.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("%v %v", list, err)
	}
	// the directory target is upstream's markers
	d, err := st.Put(Target{Kind: KindDirectory, Name: "USB", Enabled: true, MaxBackups: 5, Encrypt: true, Directory: &Directory{Path: "/media/usb1/backup"}})
	if err != nil {
		t.Fatal(err)
	}
	if d.ID != DirectoryID || readTrim(filepath.Join(root, MarkerPath)) != "/media/usb1/backup" || readTrim(filepath.Join(root, MarkerMax)) != "5" {
		t.Fatalf("%+v", d)
	}
	// an NFS and a CIFS target; the password lands in the credentials file only
	n, err := st.Put(Target{Name: "NAS", Kind: KindNFS, Enabled: true, MaxBackups: 30, Subdir: "lab", Encrypt: true, NFS: &NFS{Server: "nas.lan", Export: "/volume1/b"}})
	if err != nil || !strings.HasPrefix(n.ID, "t") || len(n.ID) != 8 || n.Created.IsZero() {
		t.Fatalf("%+v %v", n, err)
	}
	pw := "s3cret"
	c, err := st.Put(Target{Name: "SMB", Kind: KindCIFS, Enabled: true, Encrypt: true, CIFS: &CIFS{Server: "nas", Share: "backup", User: "ccu", Password: &pw}})
	if err != nil || !c.CIFS.HasPassword || c.CIFS.Password != nil {
		t.Fatalf("%+v %v", c.CIFS, err)
	}
	b, _ := os.ReadFile(filepath.Join(st.Dir, ConfigFile))
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), `"password"`) {
		t.Fatalf("a secret in the config: %s", b)
	}
	cred, _ := os.ReadFile(filepath.Join(st.SecretDir(c.ID), CredFile))
	if string(cred) != "username=ccu\npassword=s3cret\n" {
		t.Fatalf("%q", cred)
	}
	fi, _ := os.Stat(filepath.Join(st.SecretDir(c.ID), CredFile))
	if fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
	// an edit without a password keeps it; the kind cannot change
	c.CIFS.User, c.CIFS.Password = "ccu2", nil
	if _, err := st.Put(c); err != nil {
		t.Fatal(err)
	}
	cred, _ = os.ReadFile(filepath.Join(st.SecretDir(c.ID), CredFile))
	if string(cred) != "username=ccu2\npassword=s3cret\n" {
		t.Fatalf("%q", cred)
	}
	n.Kind, n.NFS, n.SFTP = KindSFTP, nil, &SFTP{Host: "h", User: "u"}
	if _, err := st.Put(n); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kind change: %v", err)
	}
	list, _ = st.List()
	if len(list) != 3 || list[0].ID != DirectoryID || list[1].Kind != KindNFS || list[2].Kind != KindCIFS {
		t.Fatalf("%+v", list)
	}
	if list[1].Dir() != "/media/net/"+list[1].ID+"/lab" || list[0].Dir() != "/media/usb1/backup" {
		t.Fatal(list[1].Dir(), list[0].Dir())
	}
	// the nightly switch is upstream's marker
	if !st.Nightly() {
		t.Fatal("nightly off")
	}
	_ = st.SetNightly(false)
	if st.Nightly() {
		t.Fatal("nightly on")
	}
	// deleting removes the secrets, never anything else; the directory target loses its path
	if err := st.Delete(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.SecretDir(c.ID)); !os.IsNotExist(err) {
		t.Fatal("secrets left")
	}
	if err := st.Delete(DirectoryID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.Get(DirectoryID); ok {
		t.Fatal("directory target still there")
	}
	if err := st.Delete("tnothere"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestValidate(t *testing.T) {
	good := Target{ID: "tabc1234", Name: "x", Kind: KindSFTP, SFTP: &SFTP{Host: "nas", Port: 22, User: "backup", Path: "/srv/b"}}
	for _, tc := range []struct {
		name string
		mod  func(*Target)
		ok   bool
	}{
		{"sftp", func(*Target) {}, true},
		{"no name", func(t *Target) { t.Name = " " }, false},
		{"name with a newline", func(t *Target) { t.Name = "a\nb" }, false},
		{"subdir with a slash", func(t *Target) { t.Subdir = "a/b" }, false},
		{"subdir ..", func(t *Target) { t.Subdir = ".." }, false},
		{"subdir", func(t *Target) { t.Subdir = "lab-ccu.1" }, true},
		{"path with ..", func(t *Target) { t.SFTP.Path = "/srv/../etc" }, false},
		{"port 0", func(t *Target) { t.SFTP.Port = 0 }, false},
		{"user with a space", func(t *Target) { t.SFTP.User = "a b" }, false},
		{"host with a space", func(t *Target) { t.SFTP.Host = "a b" }, false},
		{"two kinds", func(t *Target) { t.NFS = &NFS{Server: "a", Export: "/b"} }, false},
		{"directory id taken", func(t *Target) { t.ID = DirectoryID }, false},
		{"max", func(t *Target) { t.MaxBackups = 1001 }, false},
		{"kind", func(t *Target) { t.Kind = "s3" }, false},
	} {
		tg := good
		s := *good.SFTP
		tg.SFTP = &s
		tc.mod(&tg)
		if err := tg.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	dir := Target{ID: DirectoryID, Name: "USB", Kind: KindDirectory, Directory: &Directory{Path: "/usr/local"}}
	if err := dir.Validate(); err == nil {
		t.Error("/usr/local accepted")
	}
	pw := "a\nb"
	c := Target{ID: "tabc1234", Name: "x", Kind: KindCIFS, CIFS: &CIFS{Server: "nas", Share: "b", User: "u", Password: &pw}}
	if err := c.Validate(); err == nil {
		t.Error("a password with a newline accepted")
	}
}

func TestExpired(t *testing.T) {
	files := []BackupFile{{Name: "box-1.sbk"}, {Name: "box-2.sbk.age"}, {Name: "other-1.sbk"}, {Name: "box-3.sbk"}, {Name: "box.txt"}}
	for i := range files {
		files[i].Time = files[i].Time.AddDate(0, 0, i)
	}
	if got := Expired(files, "box", 2); len(got) != 1 || got[0] != "box-1.sbk" {
		t.Fatal(got)
	}
	if got := Expired(files, "box", 0); got != nil {
		t.Fatal(got)
	}
	if got := Expired(files, "", 1); got != nil {
		t.Fatal(got)
	}
}
