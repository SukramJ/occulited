package backuptarget

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/shares"
)

type fakeHelper struct {
	calls []string
	test  priv.WriteTestResult
	// asRoot, when set, answers ShareList and ShareOpen instead of the filesystem: what root sees
	// on a squashed export that occulited's own user cannot enter (B-217)
	asRoot map[string][]priv.ShareEntry
	files  map[string]string
}

func (f *fakeHelper) NetMount(_ context.Context, s netmount.Spec) error {
	f.calls = append(f.calls, "mount "+s.ID)
	return nil
}
func (f *fakeHelper) NetUnmount(_ context.Context, id string) error {
	f.calls = append(f.calls, "unmount "+id)
	return nil
}
func (f *fakeHelper) ShareList(ctx context.Context, dir string) (priv.ShareListResult, error) {
	f.calls = append(f.calls, "sharelist "+dir)
	if f.asRoot != nil {
		e, ok := f.asRoot[dir]
		if !ok {
			return priv.ShareListResult{Errno: "ENOENT", Error: "open " + dir + ": no such file or directory"}, nil
		}
		return priv.ShareListResult{Entries: e}, nil
	}
	return priv.Local{}.ShareList(ctx, dir)
}
func (f *fakeHelper) ShareOpen(path string) (*os.File, error) {
	f.calls = append(f.calls, "shareopen "+path)
	if src, ok := f.files[path]; ok {
		return os.Open(src)
	}
	return priv.Local{}.ShareOpen(path)
}
func (f *fakeHelper) NetMountRemove(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove "+id)
	return nil
}
func (f *fakeHelper) WriteTest(_ context.Context, dir string) (priv.WriteTestResult, error) {
	f.calls = append(f.calls, "test "+dir)
	return f.test, nil
}
func (f *fakeHelper) Run(_ context.Context, name string, args []string, _ []byte) (priv.Result, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return priv.Result{}, nil
}

func newManager(t *testing.T) (*Manager, *fakeHelper, *time.Time) {
	t.Helper()
	root := t.TempDir()
	st := &Store{Dir: t.TempDir(), Root: root}
	h := &fakeHelper{}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	m := &Manager{Store: st, Root: root, Helper: func() Helper { return h }, Hostname: func() string { return "lab-box" },
		Container: func() string { return "" }, Now: func() time.Time { return now },
		Systemctl: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	return m, h, &now
}

func TestManagerStatesAndTest(t *testing.T) {
	m, h, now := newManager(t)
	ctx := context.Background()
	n, err := m.Store.Put(Target{Name: "NAS", Kind: KindNFS, Enabled: true, Encrypt: true, Subdir: "lab", NFS: &NFS{Server: "192.0.2.10", Export: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(ctx, n); err != nil {
		t.Fatal(err)
	}
	// no mount.cifs on this root: CIFS unsupported; NFS by address works without mount.nfs
	c, _ := m.Store.Put(Target{Name: "SMB", Kind: KindCIFS, Enabled: true, CIFS: &CIFS{Server: "nas", Share: "b", User: "u"}})
	views, err := m.Views(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]View{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if byID[n.ID].State.State != StateIdle || byID[c.ID].State.State != StateUnsupported || byID[c.ID].State.Unsupported != "tool" {
		t.Fatalf("%+v %+v", byID[n.ID].State, byID[c.ID].State)
	}
	// the write test: a root-squashed export answers EACCES at create
	h.test = priv.WriteTestResult{Step: "create", Errno: "EACCES", Error: "permission denied", FSType: "nfs"}
	r, err := m.Test(ctx, n.ID)
	if err != nil || r.OK || r.State != StateReadOnly {
		t.Fatalf("%+v %v", r, err)
	}
	v, _ := m.View(ctx, n.ID)
	if v.State.State != StateReadOnly || v.State.Failures != 1 || v.State.NextRetryAt == nil || !v.State.NextRetryAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("%+v", v.State)
	}
	// written, but onto the tmpfs under the mount point: not mounted
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "tmpfs"}
	if r, _ := m.Test(ctx, n.ID); r.OK || r.State != StateUnreachable {
		t.Fatalf("%+v", r)
	}
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "nfs", FreeBytes: 100 << 30}
	if r, _ := m.Test(ctx, n.ID); !r.OK || r.State != StateWritable {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(strings.Join(h.calls, "|"), "test "+m.join("/media/net/"+n.ID+"/lab")) {
		t.Fatalf("%q", h.calls)
	}
	// Back up now starts the unit; a bad instance never reaches systemctl
	if err := m.Start(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Start(ctx, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := m.Start(ctx, "tmissing"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(h.calls, "|"), "systemctl start --no-block occu-backup-create@"+n.ID+".service") {
		t.Fatalf("%q", h.calls)
	}
	// running while the unit is active
	m.Systemctl = func(context.Context, ...string) ([]byte, error) {
		return []byte("Id=occu-backup-create@all.service\nActiveState=activating\n\n"), nil
	}
	if v, _ := m.View(ctx, n.ID); v.State.State != StateRunning {
		t.Fatalf("%+v", v.State)
	}
	if err := m.Start(ctx, "all"); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	if err := m.Remove(ctx, n.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(h.calls, "|"), "remove "+n.ID) {
		t.Fatalf("%q", h.calls)
	}
}

func TestProblems(t *testing.T) {
	m, _, now := newManager(t)
	ctx := context.Background()
	s, _ := m.Store.Put(Target{Name: "SSH", Kind: KindSFTP, Enabled: true, SFTP: &SFTP{Host: "nas", User: "b"}})
	// created just now, never ran: nothing yet
	if p, _ := m.Problems(ctx); len(p) != 0 {
		t.Fatalf("%+v", p)
	}
	// a success 30 hours ago: too-old
	at := now.Add(-30 * time.Hour)
	_ = WriteResult(m.Store.SecretDir(s.ID), Result{At: at, OK: true, State: StateWritable})
	p, _ := m.Problems(ctx)
	if len(p) != 1 || p[0].Cause != "too-old" {
		t.Fatalf("%+v", p)
	}
	// the last run failed on the server's key: that cause, not too-old
	_ = WriteResult(m.Store.SecretDir(s.ID), Result{At: now.Add(-time.Hour), State: StateHostKeyChanged, Error: "changed"})
	p, _ = m.Problems(ctx)
	if len(p) != 1 || p[0].Cause != StateHostKeyChanged {
		t.Fatalf("%+v", p)
	}
	// nightly off: no too-old; a failure still warns
	_ = m.Store.SetNightly(false)
	_ = WriteResult(m.Store.SecretDir(s.ID), Result{At: at, OK: true, State: StateWritable})
	if p, _ := m.Problems(ctx); len(p) != 0 {
		t.Fatalf("%+v", p)
	}
}

func TestMountinfoProberRetry(t *testing.T) {
	info := `36 25 0:32 / /media rw,nosuid shared:14 - tmpfs tmpfs rw
90 36 0:50 / /media/net/nas1 rw,relatime shared:50 - autofs systemd-1 rw,fd=51
91 90 0:51 / /media/net/nas1 rw,nosuid,nodev,noexec shared:51 - nfs4 192.0.2.10:/volume1/b rw,vers=4.2,soft
`
	mi, ok := parseMountinfo(strings.NewReader(info), "/media/net/nas1")
	if !ok || mi.FSType != "nfs4" || mi.Source != "192.0.2.10:/volume1/b" {
		t.Fatalf("%+v %v", mi, ok)
	}
	if _, ok := parseMountinfo(strings.NewReader(info), "/media/net/other"); ok {
		t.Fatal("found another")
	}
	var p Prober
	p.Timeout = 20 * time.Millisecond
	block := make(chan struct{})
	if err := p.Do("x", func() error { <-block; return nil }); !errors.Is(err, ErrStale) {
		t.Fatal(err)
	}
	// still stuck: answered at once, no second goroutine
	start := time.Now()
	if err := p.Do("x", func() error { t.Fatal("ran"); return nil }); !errors.Is(err, ErrStale) || time.Since(start) > 10*time.Millisecond {
		t.Fatal(err)
	}
	close(block)
	time.Sleep(5 * time.Millisecond)
	if err := p.Do("x", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for n, want := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 5 * time.Minute, 4: 15 * time.Minute, 5: 30 * time.Minute, 9: 30 * time.Minute} {
		if RetryDelay(n) != want {
			t.Errorf("%d: %v", n, RetryDelay(n))
		}
	}
	for text, want := range map[string]string{
		"mount.nfs: access denied by server while mounting 192.0.2.10:/b":                      StateAuthFailed,
		"mount error(13): Permission denied":                                                   StateAuthFailed,
		"mount.nfs: No route to host":                                                          StateUnreachable,
		"mount error(112): Host is down":                                                       StateUnreachable,
		"mount.nfs: mounting nas:/x failed, reason given by server: No such file or directory": StateError,
	} {
		if got := ClassifyMountLog(text); got != want {
			t.Errorf("%q: %s", text, got)
		}
	}
}

// task 161: the write test of a USB target whose stick is not there answers no-medium without
// asking the helper (which would create the directory on /media's tmpfs); with the stick, the
// helper's test as before.
func TestManagerTestUSBWithoutStick(t *testing.T) {
	m, h, _ := newManager(t)
	m.Root = "/" // the check looks at the system's own /media (swapped below)
	ctx := context.Background()
	if _, err := m.Store.Put(Target{Kind: KindDirectory, Name: "USB", Enabled: true, MaxBackups: 2, Directory: &Directory{Path: "/media/usb1/backup"}}); err != nil {
		t.Fatal(err)
	}
	stick := false
	old := noMediumAt
	noMediumAt = func(Target) (bool, error) {
		if stick {
			return false, nil
		}
		return true, errors.New("no USB stick is mounted for /media/usb1/backup")
	}
	t.Cleanup(func() { noMediumAt = old })
	before := len(h.calls)
	r, err := m.Test(ctx, DirectoryID)
	if err != nil || r.OK || r.State != StateNoMedium || len(h.calls) != before {
		t.Fatalf("%+v %v %q", r, err, h.calls[before:])
	}
	stick = true
	h.test = priv.WriteTestResult{OK: true, Step: "done", FSType: "vfat", FreeBytes: 1 << 30}
	if r, _ := m.Test(ctx, DirectoryID); !r.OK || r.State != StateWritable {
		t.Fatalf("%+v", r)
	}
}

// B-212: a mounted share that refuses root (a root-squashed export) is read-only - the mount unit's
// journal, whose latest line is then systemd's "Mounted …", is read only when the share is not
// mounted.
func TestTestMountedShareNotUnreachable(t *testing.T) {
	m, h, _ := newManager(t)
	ctx := context.Background()
	n, err := m.Store.Put(Target{Name: "NAS", Kind: KindNFS, Enabled: true, Subdir: "lab", NFS: &NFS{Server: "192.0.2.10", Export: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	journal := "Mounting openccu-lite network share " + n.ID + "...\nMounted openccu-lite network share " + n.ID + "."
	m.Journal = func(string) string { return journal }
	writeMountinfo := func(lines string) {
		t.Helper()
		if err := os.MkdirAll(m.join("/proc/self"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(m.join("/proc/self/mountinfo"), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeMountinfo("91 90 0:51 / /media/net/" + n.ID + " rw - nfs4 192.0.2.10:/b rw\n")
	h.test = priv.WriteTestResult{Step: "mkdir", Errno: "EACCES", Error: "mkdir /media/net/x/lab: permission denied", FSType: "nfs"}
	r, _ := m.Test(ctx, n.ID)
	if r.OK || r.State != StateReadOnly || r.Step != "mkdir" || !strings.Contains(r.Error, "permission denied") {
		t.Fatalf("mounted: %+v", r)
	}
	// even unmounted, systemd's success line is never a reason
	writeMountinfo("")
	if r, _ := m.Test(ctx, n.ID); r.State != StateReadOnly || strings.HasPrefix(r.Error, "Mounted") {
		t.Fatalf("a Mounted line as the reason: %+v", r)
	}
	// not mounted and the mount failed: the unit's reason
	journal = "Mounting x...\nmount.nfs: Connection refused\nFailed to mount x."
	h.test = priv.WriteTestResult{Step: "mkdir", Errno: "ENODEV", Error: "no such device"}
	if r, _ := m.Test(ctx, n.ID); r.State != StateUnreachable || r.Error != "mount.nfs: Connection refused" {
		t.Fatalf("not mounted: %+v", r)
	}
}

// B-215: the watchdog never touches a mounted target's mount point (a statfs there reset the
// automount's idle timer): a share target is the shares' watchdog's, a task-86 NFS target's server
// is asked with a TCP connection, and one that does not answer is unmounted.
func TestWatchOnceAsksTheServer(t *testing.T) {
	m, h, _ := newManager(t)
	ctx := context.Background()
	n, err := m.Store.Put(Target{Name: "NAS", Kind: KindNFS, Enabled: true, NFS: &NFS{Server: "192.0.2.10", Export: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Store.Put(Target{Name: "Share", Kind: KindShare, Enabled: true, Share: &ShareRef{ID: "nas", Folder: "lab"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(m.join("/proc/self"), 0o755)
	_ = os.WriteFile(m.join("/proc/self/mountinfo"), []byte("91 90 0:51 / /media/net/"+n.ID+" rw - nfs4 192.0.2.10:/b rw\n92 90 0:52 / /media/net/nas rw - nfs4 192.0.2.11:/c rw\n"), 0o644)
	old := shares.DialServer
	t.Cleanup(func() { shares.DialServer = old })
	var dialed []string
	up := true
	shares.DialServer = func(_ context.Context, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		if !up {
			return nil, errors.New("i/o timeout")
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}
	h.calls = nil
	m.WatchOnce(ctx)
	if strings.Join(dialed, ",") != "192.0.2.10:2049" || len(h.calls) != 0 {
		t.Fatalf("%q %q", dialed, h.calls)
	}
	up = false
	m.WatchOnce(ctx)
	if strings.Join(h.calls, "|") != "unmount "+n.ID {
		t.Fatalf("%q", h.calls)
	}
	if v, _ := m.View(ctx, n.ID); v.State.State != StateStale {
		t.Fatalf("%+v", v.State)
	}
	if v, _ := m.View(ctx, s.ID); v.State.State == StateStale {
		t.Fatalf("the share target: %+v", v.State)
	}
}

// B-212 (lab): an errno of a path that is not in the list - ENOTDIR from a file where the folder
// should be - is an error, not unreachable (syscall.Errno is a net.Error too); a network errno
// inside a net.OpError stays unreachable.
func TestClassifyPathErrno(t *testing.T) {
	for err, want := range map[error]string{
		&os.PathError{Op: "mkdir", Path: "/media/net/x/f", Err: syscall.ENOTDIR}:                              StateError,
		&os.PathError{Op: "mkdir", Path: "/media/net/x/f", Err: syscall.EACCES}:                               StateReadOnly,
		&os.PathError{Op: "open", Path: "/media/net/x", Err: syscall.EHOSTDOWN}:                               StateUnreachable,
		&net.OpError{Op: "read", Net: "tcp", Err: &os.SyscallError{Syscall: "read", Err: syscall.ECONNRESET}}: StateUnreachable,
	} {
		if got := Classify(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}

// B-217: a share target's backups are listed and opened through the helper, as root - on a
// root-squashed export the folders root wrote are nobody's 0770 and occulited cannot enter them.
// The fake answers as root; the path itself does not exist for the daemon.
func TestShareTargetReadAsRoot(t *testing.T) {
	m, h, _ := newManager(t)
	ctx := context.Background()
	tg, err := m.Store.Put(Target{Name: "NAS", Kind: KindNFS, Enabled: true, Subdir: "lab", NFS: &NFS{Server: "192.0.2.10", Export: "/b"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := m.join("/media/net/" + tg.ID + "/lab")
	when := time.Date(2026, 9, 25, 0, 7, 0, 0, time.UTC)
	src := filepath.Join(t.TempDir(), "b.sbk")
	_ = os.WriteFile(src, []byte("backup"), 0o600)
	h.asRoot = map[string][]priv.ShareEntry{dir: {
		{Name: "lab-box-1.0.0-2026-09-24-0007.sbk", Size: 6, MTime: when.Add(-24 * time.Hour)},
		{Name: "lab-box-1.0.0-2026-09-25-0007.sbk", Size: 6, MTime: when},
		{Name: "notes.txt", Size: 1},
		{Name: "sub", Dir: true},
		{Name: "evil-1-2026-09-25-0007.sbk", Link: true},
	}}
	h.files = map[string]string{filepath.Join(dir, "lab-box-1.0.0-2026-09-25-0007.sbk"): src}
	files, err := m.Backups(ctx, tg.ID)
	if err != nil || len(files) != 2 || files[0].Name != "lab-box-1.0.0-2026-09-25-0007.sbk" || !files[0].Time.Equal(when) || files[0].Size != 6 {
		t.Fatalf("%+v %v", files, err)
	}
	rc, err := m.Open(ctx, tg.ID, "lab-box-1.0.0-2026-09-25-0007.sbk")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "backup" || !strings.Contains(strings.Join(h.calls, "|"), "shareopen "+filepath.Join(dir, "lab-box-1.0.0-2026-09-25-0007.sbk")) {
		t.Fatalf("%q %q", b, h.calls)
	}
	// a folder the first backup has not made yet: an empty list, as for a directory
	h.asRoot = map[string][]priv.ShareEntry{}
	if files, err := m.Backups(ctx, tg.ID); err != nil || len(files) != 0 {
		t.Fatalf("%+v %v", files, err)
	}
	// root may not read it either: the error, read-only
	h.asRoot = nil
	h.files = nil
	m.Helper = func() Helper { return &deniedHelper{fakeHelper: h} }
	if _, err := m.Backups(ctx, tg.ID); Classify(err) != StateReadOnly {
		t.Fatalf("%v (%s)", err, Classify(err))
	}
}

// deniedHelper answers every share read with EACCES.
type deniedHelper struct{ *fakeHelper }

func (d *deniedHelper) ShareList(context.Context, string) (priv.ShareListResult, error) {
	return priv.ShareListResult{Errno: "EACCES", Error: "permission denied"}, nil
}
