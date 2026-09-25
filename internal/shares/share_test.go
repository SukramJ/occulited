package shares

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
)

func pw(s string) *string { return &s }

func TestValidate(t *testing.T) {
	nfs := Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/volume1/data"}
	smb := Share{ID: "smb", Kind: KindCIFS, Server: "192.0.2.5", Path: "data", User: "ccu"}
	for _, tc := range []struct {
		name string
		s    Share
		ok   bool
	}{
		{"nfs", nfs, true},
		{"nfs read-only 4.1", func() Share { s := nfs; s.ReadOnly, s.Version = true, "4.1"; return s }(), true},
		{"smb", smb, true},
		{"smb with a password and a domain", func() Share { s := smb; s.Password, s.Domain = pw("secret"), "WORK"; return s }(), true},
		{"name upper case", func() Share { s := nfs; s.ID = "NAS"; return s }(), false},
		{"name with a dash", func() Share { s := nfs; s.ID = "my-nas"; return s }(), false},
		{"name empty", func() Share { s := nfs; s.ID = ""; return s }(), false},
		{"kind", func() Share { s := nfs; s.Kind = "sshfs"; return s }(), false},
		{"nfs with a user", func() Share { s := nfs; s.User = "x"; return s }(), false},
		{"nfs export relative", func() Share { s := nfs; s.Path = "data"; return s }(), false},
		{"smb without a user", func() Share { s := smb; s.User = ""; return s }(), false},
		{"smb password with a newline", func() Share { s := smb; s.Password = pw("a\nb"); return s }(), false},
		{"smb domain with a space", func() Share { s := smb; s.Domain = "a b"; return s }(), false},
		{"smb version 2", func() Share { s := smb; s.Version = "2.1"; return s }(), false},
	} {
		err := tc.s.Validate()
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v is not ErrInvalid", tc.name, err)
		}
	}
}

func TestSpecAndSource(t *testing.T) {
	s := Share{ID: "nas", Kind: KindCIFS, Server: "nas.lan", Path: "data/ccu", ReadOnly: true, Seal: true, User: "u"}
	sp := s.Spec()
	if !sp.Share || !sp.ReadOnly || !sp.Seal || sp.ID != "nas" || sp.Path != "data/ccu" {
		t.Fatalf("%+v", sp)
	}
	if s.Source() != "//nas.lan/data/ccu" || s.Where() != netmount.Base+"/nas" {
		t.Fatal(s.Source(), s.Where())
	}
}

func TestStore(t *testing.T) {
	dir := t.TempDir()
	taken := map[string]bool{"t1234567": true}
	st := &Store{Dir: dir, Taken: func(id string) bool { return taken[id] }}
	if l, err := st.List(); err != nil || len(l) != 0 {
		t.Fatal(l, err)
	}
	smb := Share{ID: "nas", Kind: KindCIFS, Server: "nas.lan", Path: "data", User: "ccu", Password: pw("s3cret")}
	got, err := st.Create(smb)
	if err != nil {
		t.Fatal(err)
	}
	if !got.HasPassword || got.Password != nil || got.Created.IsZero() {
		t.Fatalf("%+v", got)
	}
	cred, _ := os.ReadFile(filepath.Join(dir, "shares/nas/cifs.cred"))
	if string(cred) != "username=ccu\npassword=s3cret\n" {
		t.Fatalf("%q", cred)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "shares/nas/cifs.cred")); fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
	b, _ := os.ReadFile(filepath.Join(dir, ConfigFile))
	if strings.Contains(string(b), "s3cret") || strings.Contains(string(b), "has_password\": true") {
		t.Fatalf("a secret in shares.json: %s", b)
	}
	// the same name again, a backup target's id, a kind change
	if _, err := st.Create(smb); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if _, err := st.Create(Share{ID: "t1234567", Kind: KindNFS, Server: "a", Path: "/b"}); !errors.Is(err, ErrExists) {
		t.Fatal(err)
	}
	if _, err := st.Update(Share{ID: "nas", Kind: KindNFS, Server: "a", Path: "/b"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := st.Update(Share{ID: "none", Kind: KindNFS, Server: "a", Path: "/b"}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	// an edit without a password keeps it; the domain is written beside it
	up := smb
	up.Password, up.Domain, up.ReadOnly = nil, "WORK", true
	got, err = st.Update(up)
	if err != nil || !got.HasPassword || !got.ReadOnly || !got.Created.Equal(got.Created) {
		t.Fatal(got, err)
	}
	cred, _ = os.ReadFile(filepath.Join(dir, "shares/nas/cifs.cred"))
	if string(cred) != "username=ccu\npassword=s3cret\ndomain=WORK\n" {
		t.Fatalf("%q", cred)
	}
	// a guest share: the file exists, with no password
	if g, err := st.Create(Share{ID: "guest", Kind: KindCIFS, Server: "nas", Path: "pub", User: "guest"}); err != nil || g.HasPassword {
		t.Fatal(g, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shares/guest/cifs.cred")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Create(Share{ID: "nfs", Kind: KindNFS, Server: "nas", Path: "/export"}); err != nil {
		t.Fatal(err)
	}
	l, _ := st.List()
	if len(l) != 3 || l[0].ID != "nas" || l[2].ID != "nfs" {
		t.Fatalf("%+v", l)
	}
	if err := st.Delete("nas"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shares/nas")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the secrets stayed")
	}
	if err := st.Delete("nas"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	for i := 0; i < MaxShares; i++ {
		_, _ = st.Create(Share{ID: "s" + string(rune('a'+i)), Kind: KindNFS, Server: "nas", Path: "/e"})
	}
	if _, err := st.Create(Share{ID: "toomany", Kind: KindNFS, Server: "nas", Path: "/e"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

// fakeHelper records the helper's calls; mounted says what the fake mountinfo holds.
type fakeHelper struct {
	root  string
	calls []string
	test  priv.WriteTestResult
}

func (f *fakeHelper) NetMount(_ context.Context, s netmount.Spec) error {
	f.calls = append(f.calls, "mount "+s.ID+" "+s.What())
	return nil
}
func (f *fakeHelper) NetUnmount(_ context.Context, id string) error {
	f.calls = append(f.calls, "unmount "+id)
	return nil
}
func (f *fakeHelper) ShareList(ctx context.Context, dir string) (priv.ShareListResult, error) {
	return priv.Local{}.ShareList(ctx, dir)
}
func (f *fakeHelper) ShareOpen(path string) (*os.File, error) { return priv.Local{}.ShareOpen(path) }
func (f *fakeHelper) NetMountRemove(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return nil
}
func (f *fakeHelper) WriteTest(_ context.Context, dir string) (priv.WriteTestResult, error) {
	f.calls = append(f.calls, "test "+strings.TrimPrefix(dir, f.root))
	return f.test, nil
}

func newManager(t *testing.T) (*Manager, *fakeHelper, string) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"proc/self", "sbin", "media/net"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"sbin/mount.nfs", "sbin/mount.cifs"} {
		_ = os.WriteFile(filepath.Join(root, tool), nil, 0o755)
	}
	mountinfo(t, root, "")
	h := &fakeHelper{root: root}
	m := &Manager{Store: &Store{Dir: filepath.Join(root, "state")}, Helper: func() Helper { return h }, Root: root}
	return m, h, root
}

func mountinfo(t *testing.T, root, lines string) {
	t.Helper()
	base := "22 1 0:21 / / rw - ext4 /dev/sda2 rw\n40 22 0:30 / /media/net/nas rw,relatime - autofs systemd-1 rw\n"
	if err := os.WriteFile(filepath.Join(root, "proc/self/mountinfo"), []byte(base+lines), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManagerKindsAndViews(t *testing.T) {
	m, h, root := newManager(t)
	ctx := context.Background()
	if _, err := m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/v1/data"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/v1/data"}); err != nil {
		t.Fatal(err)
	}
	v, err := m.Views()
	if err != nil || len(v) != 1 || v[0].State.State != StateIdle || v[0].Where != "/media/net/nas" {
		t.Fatalf("%+v %v", v, err)
	}
	// mounted: the state and the source from mountinfo (a statfs on the fake root's directory)
	_ = os.MkdirAll(filepath.Join(root, "media/net/nas"), 0o755)
	mountinfo(t, root, "41 40 0:31 / /media/net/nas rw,nosuid - nfs4 nas.lan:/v1/data rw,vers=4.2\n")
	v, _ = m.Views()
	if v[0].State.State != StateMounted || !v[0].State.Mounted || v[0].State.Source != "nas.lan:/v1/data" || v[0].State.TotalBytes == 0 {
		t.Fatalf("%+v", v[0].State)
	}
	// mount.cifs missing: SMB unsupported; a container: both
	_ = os.Remove(filepath.Join(root, "sbin/mount.cifs"))
	if k := m.Kinds(); k[KindCIFS] != "tool" || k[KindNFS] != "" {
		t.Fatal(k)
	}
	// mount.nfs missing: NFS by a name is unsupported, by an address works (the kernel's addr=)
	_ = os.Remove(filepath.Join(root, "sbin/mount.nfs"))
	if m.Unsupported(Share{Kind: KindNFS, Server: "nas.lan"}) != "tool" || m.Unsupported(Share{Kind: KindNFS, Server: "192.0.2.1"}) != "" {
		t.Fatal("nfs without mount.nfs")
	}
	m.Container = func() string { return "lxc" }
	v, _ = m.Views()
	if v[0].State.State != StateUnsupported || v[0].State.Unsupported != "container" {
		t.Fatalf("%+v", v[0].State)
	}
	if err := m.Apply(ctx, Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/v1/data"}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(h.calls, "|") != "mount nas nas.lan:/v1/data" {
		t.Fatalf("a container got a mount: %q", h.calls)
	}
	// remove: the units, then the configuration
	m.Container = nil
	if err := m.Remove(ctx, "nas"); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, "nas"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if h.calls[len(h.calls)-1] != "remove nas" {
		t.Fatal(h.calls)
	}
}

func TestManagerTest(t *testing.T) {
	m, h, root := newManager(t)
	ctx := context.Background()
	_, _ = m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/data"})
	// written onto the share: writable, and the test unmounts what it mounted
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs", FreeBytes: 5e9, TotalBytes: 9e9, WriteMBps: 11}
	r, err := m.Test(ctx, "nas")
	if err != nil || !r.OK || r.State != StateWritable || r.FreeBytes != 5e9 {
		t.Fatalf("%+v %v", r, err)
	}
	if strings.Join(h.calls, "|") != "test /media/net/nas|unmount nas" {
		t.Fatalf("%q", h.calls)
	}
	// written onto the tmpfs under the mount point: the share did not mount; the journal's reason
	m.Journal = func(unit string) string {
		if unit != "media-net-nas.mount" {
			t.Errorf("unit %s", unit)
		}
		return "mount.nfs: access denied by server while mounting nas.lan:/data"
	}
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "tmpfs"}
	r, _ = m.Test(ctx, "nas")
	if r.OK || r.State != StateAuthFailed || r.Step != "mount" || !strings.Contains(r.Error, "access denied") {
		t.Fatalf("%+v", r)
	}
	v, _ := m.View("nas")
	if v.State.State != StateAuthFailed || v.State.NextRetryAt == nil {
		t.Fatalf("%+v", v.State)
	}
	// a root-squashed export: read-only
	h.test = priv.WriteTestResult{Step: "create", Errno: "EACCES", Error: "permission denied", FSType: "nfs"}
	if r, _ = m.Test(ctx, "nas"); r.State != StateReadOnly {
		t.Fatalf("%+v", r)
	}
	// mounted before the test (in use): it stays mounted
	h.calls = nil
	_ = os.MkdirAll(filepath.Join(root, "media/net/nas"), 0o755)
	mountinfo(t, root, "41 40 0:31 / /media/net/nas rw - nfs4 nas.lan:/data rw\n")
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs"}
	if r, _ = m.Test(ctx, "nas"); !r.OK {
		t.Fatalf("%+v", r)
	}
	if strings.Join(h.calls, "|") != "test /media/net/nas" {
		t.Fatalf("%q", h.calls)
	}
	// a read-only share is listed, not written
	_, _ = m.Store.Create(Share{ID: "ro", Kind: KindNFS, Server: "nas.lan", Path: "/media", ReadOnly: true})
	_ = os.MkdirAll(filepath.Join(root, "media/net/ro"), 0o755)
	mountinfo(t, root, "42 40 0:32 / /media/net/ro ro - nfs4 nas.lan:/media ro\n")
	h.calls = nil
	r, _ = m.Test(ctx, "ro")
	if !r.OK || r.State != StateReadable || !r.ReadOnly || len(h.calls) != 0 {
		t.Fatalf("%+v %q", r, h.calls)
	}
	if _, err := m.Test(ctx, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestManagerMountAndWatch(t *testing.T) {
	m, h, root := newManager(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	_, _ = m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/data"})
	// the access finds no mount: unreachable, with a retry scheduled
	v, err := m.Mount(ctx, "nas")
	if err != nil || v.State.State != StateUnreachable || v.State.NextRetryAt == nil || !v.State.NextRetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("%+v %v", v.State, err)
	}
	// not yet due: nothing; due and mounted by then: the check clears
	m.WatchOnce(ctx)
	now = now.Add(2 * time.Minute)
	_ = os.MkdirAll(filepath.Join(root, "media/net/nas"), 0o755)
	mountinfo(t, root, "41 40 0:31 / /media/net/nas rw - nfs4 nas.lan:/data rw\n")
	// B-215: the watchdog asks the server, never the mount point (a statfs there would keep the
	// automount from idling out)
	oldDial := DialServer
	t.Cleanup(func() { DialServer = oldDial })
	var dialed []string
	serverUp := true
	DialServer = func(_ context.Context, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if !serverUp {
			return nil, errors.New("i/o timeout")
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	h.calls = nil
	m.WatchOnce(ctx) // mounted now: the watchdog probes its server, no retry needed
	if v, _ = m.View("nas"); v.State.State != StateMounted || strings.Join(dialed, ",") != "nas.lan:2049" || len(h.calls) != 0 {
		t.Fatalf("%+v %q %q", v.State, dialed, h.calls)
	}
	// the server does not answer: the watchdog unmounts the share
	serverUp = false
	m.WatchOnce(ctx)
	if strings.Join(h.calls, "|") != "unmount nas" {
		t.Fatalf("%q", h.calls)
	}
	if c, _ := m.LastCheck("nas"); c.State != StateStale {
		t.Fatalf("%+v", c)
	}
	if _, err := m.Unmount(ctx, "nas"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Mount(ctx, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, StateMounted},
		{ErrStale, StateStale},
		{syscall.EACCES, StateReadOnly},
		{syscall.ENOSPC, StateFull},
		{syscall.ESTALE, StateStale},
		{syscall.EHOSTUNREACH, StateUnreachable},
		{errors.New("x"), StateError},
	} {
		if got := ClassifyErrno(tc.err); got != tc.want {
			t.Errorf("%v: %s, want %s", tc.err, got, tc.want)
		}
	}
	for text, want := range map[string]string{
		"mount error(13): Permission denied":  StateAuthFailed,
		"mount.nfs: Connection refused":       StateUnreachable,
		"mount error(2): No such file or dir": StateError,
		"something else":                      StateUnreachable,
	} {
		if got := ClassifyMountLog(text); got != want {
			t.Errorf("%q: %s, want %s", text, got, want)
		}
	}
	if LastLine("a\nb\n") != "b" {
		t.Fatal(LastLine("a\nb\n"))
	}
}

func TestProberWithin(t *testing.T) {
	var p Prober
	start := time.Now()
	block := make(chan struct{})
	defer close(block)
	if err := p.DoWithin("k", 30*time.Millisecond, func() error { <-block; return nil }); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited the default")
	}
	// the stuck one is still in flight: the next answers at once
	if err := p.Do("k", func() error { t.Error("ran"); return nil }); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
}

func TestMountReason(t *testing.T) {
	text := `Mounting openccu-lite network share nas...
media-net-nas.mount: Mount process exited, code=killed, status=15/TERM
media-net-nas.mount: Failed with result 'timeout'.
Failed to mount openccu-lite network share nas.
Mounting openccu-lite network share nas...
mount.nfs: mounting 192.0.2.1:/export/rw failed, reason given by server: No such file or directory
media-net-nas.mount: Failed with result 'exit-code'.
Failed to mount openccu-lite network share nas.`
	st, d := MountReason(text)
	if st != StateError || !strings.HasPrefix(d, "mount.nfs: mounting") {
		t.Fatalf("%s %q", st, d)
	}
	// only the latest attempt counts: a timeout
	st, d = MountReason(strings.SplitN(text, "\nMounting", 2)[0])
	if st != StateUnreachable || d != "the server did not answer within 30 s" {
		t.Fatalf("%s %q", st, d)
	}
	if st, d = MountReason("Mounting x...\nmount error(13): Permission denied\nRefer to the mount.cifs(8) manual page (e.g. man mount.cifs) and kernel log messages (dmesg)\nFailed to mount x."); st != StateAuthFailed || d != "mount error(13): Permission denied" {
		t.Fatalf("%s %q", st, d)
	}
	// B-212: an attempt that mounted is no reason, whatever failed before it
	if st, d = MountReason(text + "\nMounting openccu-lite network share nas...\nMounted openccu-lite network share nas."); st != "" || d != "" {
		t.Fatalf("%s %q", st, d)
	}
}

// B-212: a mounted share's error is its own; the unit's journal is read only when it is not mounted.
func TestMountReasonOnlyWhenNotMounted(t *testing.T) {
	m, h, root := newManager(t)
	ctx := context.Background()
	_, _ = m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/data"})
	m.Journal = func(string) string { return "Mounting x...\nmount.nfs: Connection refused\nFailed to mount x." }
	_ = os.MkdirAll(filepath.Join(root, "media/net/nas"), 0o755)
	mountinfo(t, root, "41 40 0:31 / /media/net/nas rw - nfs4 nas.lan:/data rw\n")
	h.test = priv.WriteTestResult{Step: "mkdir", Errno: "EACCES", Error: "permission denied", FSType: "nfs"}
	if r, _ := m.Test(ctx, "nas"); r.State != StateReadOnly || r.Error != "permission denied" {
		t.Fatalf("mounted: %+v", r)
	}
	mountinfo(t, root, "")
	h.test = priv.WriteTestResult{Step: "mkdir", Errno: "ENODEV", Error: "no such device"}
	if r, _ := m.Test(ctx, "nas"); r.State != StateUnreachable || r.Error != "mount.nfs: Connection refused" {
		t.Fatalf("not mounted: %+v", r)
	}
}

// B-215: a mounted share's space is measured at most once per SpaceTTL - a page that polls the list
// once a minute must not keep the share mounted by walking its mount point.
func TestViewSpaceCached(t *testing.T) {
	m, _, root := newManager(t)
	now := time.Date(2026, 9, 25, 4, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	_, _ = m.Store.Create(Share{ID: "nas", Kind: KindNFS, Server: "nas.lan", Path: "/data"})
	_ = os.MkdirAll(filepath.Join(root, "media/net/nas"), 0o755)
	mountinfo(t, root, "41 40 0:31 / /media/net/nas rw - nfs4 nas.lan:/data rw\n")
	v, _ := m.View("nas")
	if v.State.State != StateMounted || v.State.TotalBytes == 0 {
		t.Fatalf("%+v", v.State)
	}
	// the mount point gone: within the TTL the last reading is answered, nothing walks it
	_ = os.RemoveAll(filepath.Join(root, "media/net/nas"))
	if v, _ = m.View("nas"); v.State.State != StateMounted || v.State.TotalBytes == 0 {
		t.Fatalf("measured again: %+v", v.State)
	}
	now = now.Add(SpaceTTL)
	if v, _ = m.View("nas"); v.State.State == StateMounted {
		t.Fatalf("not measured after the TTL: %+v", v.State)
	}
}
