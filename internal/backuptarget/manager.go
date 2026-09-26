package backuptarget

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/backupcrypt"
	"github.com/hobbyquaker/occulited/internal/location"
	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/shares"
	"github.com/hobbyquaker/occulited/internal/unitshow"
)

// The daemon's side: the targets' states, the test, mount and unmount through the helper, Back up
// now through the fork's units, the listings through the Prober, the watchdog and the retries.

// Helper is what the manager needs from the privilege boundary (system.Priv).
type Helper interface {
	NetMount(ctx context.Context, s netmount.Spec) error
	NetUnmount(ctx context.Context, id string) error
	NetMountRemove(ctx context.Context, id string) error
	WriteTest(ctx context.Context, dir string) (priv.WriteTestResult, error)
	Run(ctx context.Context, name string, args []string, stdin []byte) (priv.Result, error)
	// ShareList and ShareOpen read a share's folder and a backup file there as root (B-217).
	ShareList(ctx context.Context, dir string) (priv.ShareListResult, error)
	ShareOpen(path string) (*os.File, error)
}

// Manager is the daemon's view of the targets.
type Manager struct {
	Store *Store
	Crypt *backupcrypt.Store
	// Helper answers the privilege boundary at call time (the daemon switches it at start).
	Helper func() Helper
	// Hostname is the system's name now (the per-system subdirectory's default, retention's prefix).
	Hostname func() string
	// Container is the container kind, "" on hardware and VMs (system.Root.Container).
	Container func() string
	// Systemctl runs a read-only systemctl (show) as the daemon.
	Systemctl func(ctx context.Context, args ...string) ([]byte, error)
	// Journal answers a unit's last lines, for a mount's reason.
	Journal func(unit string) string
	// Root is the filesystem root the tools and /proc are under ("/" on a system).
	Root   string
	Now    func() time.Time
	Prober Prober

	mu     sync.Mutex
	checks map[string]Check
}

// Check is the last test or probe of a target.
type Check struct {
	At       time.Time `json:"at"`
	State    string    `json:"state"`
	Step     string    `json:"step,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Free     int64     `json:"free_bytes,omitempty"`
	Total    int64     `json:"total_bytes,omitempty"`
	MBps     float64   `json:"write_mbps,omitempty"`
	Failures int       `json:"failures,omitempty"`
	Next     time.Time `json:"next_retry_at,omitempty"`
}

// StateView is a target's state for the API.
type StateView struct {
	State       string     `json:"state"`
	Detail      string     `json:"detail,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	Failures    int        `json:"failures"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Mounted     bool       `json:"mounted"`
	Source      string     `json:"source,omitempty"`
	Options     string     `json:"options,omitempty"`
	FreeBytes   int64      `json:"free_bytes,omitempty"`
	TotalBytes  int64      `json:"total_bytes,omitempty"`
	Unsupported string     `json:"unsupported,omitempty"`
}

// View is one target for GET /backup/targets.
type View struct {
	Target
	State      StateView `json:"state"`
	LastBackup *Result   `json:"last_backup,omitempty"`
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) join(p string) string {
	if m.Root == "" || m.Root == "/" {
		return p
	}
	return filepath.Join(m.Root, p)
}

func (m *Manager) hostname() string {
	if m.Hostname != nil {
		if h := m.Hostname(); h != "" {
			return h
		}
	}
	return "openccu-lite"
}

func (m *Manager) setCheck(id string, c Check) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checks == nil {
		m.checks = map[string]Check{}
	}
	prev := m.checks[id]
	if Failed(c.State) {
		c.Failures = prev.Failures + 1
		c.Next = c.At.Add(RetryDelay(c.Failures))
	}
	m.checks[id] = c
}

func (m *Manager) check(id string) (Check, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.checks[id]
	return c, ok
}

// RetryDelay is the backoff after the n-th failure in a row: 1, 2, 5, 15, 30 minutes, then 30.
func RetryDelay(n int) time.Duration {
	steps := []time.Duration{time.Minute, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute}
	if n < 1 {
		n = 1
	}
	if n > len(steps) {
		return steps[len(steps)-1]
	}
	return steps[n-1]
}

// Kinds answers which kinds this system supports, and why not. NFS and SMB are task 86's kinds,
// migrated to shares (task 228): answered for an older client, never offered for a new target.
func (m *Manager) Kinds() map[string]string {
	out := map[string]string{KindDirectory: "", KindSFTP: "", KindShare: "", KindNFS: "", KindCIFS: ""}
	if m.Container != nil && m.Container() != "" {
		out[KindNFS], out[KindCIFS], out[KindShare] = "container", "container", "container"
		return out
	}
	if !m.exists("/sbin/mount.cifs") && !m.exists("/usr/sbin/mount.cifs") {
		out[KindCIFS] = "tool"
	}
	return out
}

func (m *Manager) exists(p string) bool {
	_, err := os.Stat(m.join(p))
	return err == nil
}

// unsupported answers why a target cannot work here, "" when it can.
func (m *Manager) unsupported(t Target, kinds map[string]string) string {
	why := kinds[t.Kind]
	if why != "" {
		return why
	}
	// NFS without mount.nfs mounts by an address only (the kernel wants addr=)
	if t.Kind == KindNFS && t.NFS != nil && !m.exists("/sbin/mount.nfs") && !m.exists("/usr/sbin/mount.nfs") {
		if !isIP(t.NFS.Server) {
			return "tool"
		}
	}
	return ""
}

// Mountinfo is one entry of /proc/self/mountinfo.
type Mountinfo = shares.Mountinfo

// mounted reads the mount on a target's mount point, when there is one of a network type.
func (m *Manager) mounted(id string) (Mountinfo, bool) {
	return shares.ReadMountinfo(m.join("/proc/self/mountinfo"), netmount.Base+"/"+id)
}

// mountLog reads why a share's mount failed from its unit's journal; state "" when there is nothing
// to say. Only for a share that is not mounted: on a mounted one the error is its own reason (EACCES
// on a root-squashed export is read-only) and the journal's latest line is systemd's "Mounted …",
// which read as unreachable (B-212).
func (m *Manager) mountLog(id string) (state, detail string) {
	if m.Journal == nil {
		return "", ""
	}
	if _, mounted := m.mounted(id); mounted {
		return "", ""
	}
	text := m.Journal("media-net-" + id + ".mount")
	if text == "" {
		return "", ""
	}
	return shares.MountReason(text)
}

func parseMountinfo(r io.Reader, where string) (Mountinfo, bool) {
	return shares.ParseMountinfo(r, where)
}

// running answers which targets have a run in progress: the units for their id, all and nightly.
func (m *Manager) running(ctx context.Context, ids []string) map[string]bool {
	out := map[string]bool{}
	if m.Systemctl == nil {
		return out
	}
	units := []string{fmt.Sprintf(CreateUnit, "all"), fmt.Sprintf(DeliverUnit, "all"), fmt.Sprintf(CreateUnit, "nightly"), fmt.Sprintf(DeliverUnit, "nightly")}
	for _, id := range ids {
		units = append(units, fmt.Sprintf(CreateUnit, id), fmt.Sprintf(DeliverUnit, id))
	}
	b, err := m.Systemctl(ctx, append([]string{"show", "-p", "Id,ActiveState"}, units...)...)
	if err != nil {
		return out
	}
	active := func(unit string) bool {
		s := unitshow.ByID(b)[unit]["ActiveState"]
		return s == "activating" || s == "active" || s == "reloading"
	}
	all := active(units[0]) || active(units[1]) || active(units[2]) || active(units[3])
	for _, id := range ids {
		out[id] = all || active(fmt.Sprintf(CreateUnit, id)) || active(fmt.Sprintf(DeliverUnit, id))
	}
	return out
}

// Views answers every target with its state.
func (m *Manager) Views(ctx context.Context) ([]View, error) {
	list, err := m.Store.List()
	if err != nil {
		return nil, err
	}
	kinds := m.Kinds()
	var ids []string
	for _, t := range list {
		ids = append(ids, t.ID)
	}
	run := m.running(ctx, ids)
	out := make([]View, 0, len(list))
	for _, t := range list {
		out = append(out, m.view(t, kinds, run[t.ID]))
	}
	return out, nil
}

// View answers one target.
func (m *Manager) View(ctx context.Context, id string) (View, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	return m.view(t, m.Kinds(), m.running(ctx, []string{id})[id]), nil
}

func (m *Manager) view(t Target, kinds map[string]string, running bool) View {
	v := View{Target: t}
	if r, ok := ReadResult(m.Store.SecretDir(t.ID)); ok {
		v.LastBackup = &r
	}
	if why := m.unsupported(t, kinds); why != "" {
		v.State = StateView{State: StateUnsupported, Unsupported: why}
		return v
	}
	if t.Kind == KindShare && t.Share != nil && m.Store.ShareInfo != nil {
		if exists, _ := m.Store.ShareInfo(t.Share.ID); !exists {
			v.State = StateView{State: StateError, Detail: "the share " + t.Share.ID + " is not on System → Storage any more"}
			return v
		}
	}
	if t.IsMount() {
		if mi, ok := m.mounted(t.MountID()); ok {
			v.State.Mounted, v.State.Source, v.State.Options = true, mi.Source, mi.Options
		}
	}
	c, hasCheck := m.check(t.ID)
	state, detail := StateIdle, ""
	var at time.Time
	if v.LastBackup != nil {
		state, at = v.LastBackup.State, v.LastBackup.At
		if !v.LastBackup.OK {
			detail = v.LastBackup.Error
		}
	}
	if hasCheck && !c.At.Before(at) {
		state, detail, at = c.State, c.Detail, c.At
		v.State.FreeBytes, v.State.TotalBytes = c.Free, c.Total
	}
	if hasCheck {
		v.State.Failures = c.Failures
		if !c.Next.IsZero() && Failed(c.State) {
			n := c.Next
			v.State.NextRetryAt = &n
		}
	}
	if !at.IsZero() {
		v.State.CheckedAt = &at
	}
	if state == "" {
		state = StateIdle
	}
	// an SSH target whose server key nobody confirmed yet says so before any check does
	if t.Kind == KindSFTP && t.SFTP != nil && t.SFTP.HostKey == nil && !hasCheck && v.LastBackup == nil {
		state = StateHostKeyUnknown
	}
	if running {
		state = StateRunning
	}
	v.State.State, v.State.Detail = state, detail
	return v
}

func isIP(s string) bool {
	return strings.Count(s, ":") >= 2 || (strings.Count(s, ".") == 3 && strings.Trim(s, "0123456789.") == "")
}

// NeededBytes is what the next backup needs on a target: the last backup's size and a tenth.
func (m *Manager) NeededBytes() int64 {
	list, _ := m.Store.List()
	var size int64
	var at time.Time
	for _, t := range list {
		if r, ok := ReadResult(m.Store.SecretDir(t.ID)); ok && r.Size > 0 && r.At.After(at) {
			size, at = r.Size, r.At
		}
	}
	return size + size/10
}

// Apply renders a mount target's units through the helper (after a create or an edit, and at
// start: /run is empty after a boot). Other kinds need nothing.
func (m *Manager) Apply(ctx context.Context, t Target) error {
	spec, ok := t.Mount()
	if !ok {
		return nil
	}
	if why := m.unsupported(t, m.Kinds()); why != "" {
		return nil
	}
	return m.Helper().NetMount(ctx, spec)
}

// ApplyAll renders every mount target's units; the errors are returned per target.
func (m *Manager) ApplyAll(ctx context.Context) map[string]error {
	list, err := m.Store.List()
	out := map[string]error{}
	if err != nil {
		out[""] = err
		return out
	}
	for _, t := range list {
		if t.IsMount() {
			if err := m.Apply(ctx, t); err != nil {
				out[t.ID] = err
			}
		}
	}
	return out
}

// Remove takes a target's units down and deletes its configuration and secrets; the files on the
// target stay.
func (m *Manager) Remove(ctx context.Context, id string) error {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if _, own := t.Mount(); own {
		// a task-86 target's own units; a share target's share stays (System → Storage)
		if err := m.Helper().NetMountRemove(ctx, id); err != nil {
			return fmt.Errorf("units: %w", err)
		}
	}
	m.mu.Lock()
	delete(m.checks, id)
	m.mu.Unlock()
	return m.Store.Delete(id)
}

// Test runs the write test: as root through the helper for a directory or a mount (the identity
// the backup writes with), as occulited over SFTP.
func (m *Manager) Test(ctx context.Context, id string) (TestResult, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return TestResult{}, err
	}
	if !ok {
		return TestResult{}, ErrNotFound
	}
	if why := m.unsupported(t, m.Kinds()); why != "" {
		return TestResult{State: StateUnsupported, Step: "kind", Error: why, At: m.now()}, nil
	}
	var r TestResult
	switch {
	case t.Kind == KindSFTP:
		r = m.testSFTP(ctx, t)
	default:
		r = m.testLocal(ctx, t)
	}
	r.At = m.now()
	r.NeededBytes = m.NeededBytes()
	if r.OK && r.NeededBytes > 0 && r.FreeBytes > 0 && r.FreeBytes < r.NeededBytes {
		r.State = StateFull
	}
	m.setCheck(id, Check{At: r.At, State: r.State, Step: r.Step, Detail: r.Error, Free: r.FreeBytes, Total: r.TotalBytes, MBps: r.WriteMBps})
	return r, nil
}

func (m *Manager) testSFTP(ctx context.Context, t Target) TestResult {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	c, err := DialSFTP(ctx, t.SFTP, m.Store.SecretDir(t.ID))
	if err != nil {
		return TestResult{Step: "connect", State: Classify(err), Error: err.Error()}
	}
	defer c.Close()
	return c.Test(t.RemoteDir())
}

func (m *Manager) testLocal(ctx context.Context, t Target) TestResult {
	dir := t.Dir()
	if dir == "" {
		return TestResult{Step: "config", State: StateError, Error: "no directory"}
	}
	if t.Kind == KindDirectory && !strings.HasPrefix(dir+"/", "/media/") && !strings.HasPrefix(dir+"/", location.UserfsPath("backup")+"/") {
		return TestResult{Step: "config", State: StateError, Error: "the write test runs for a directory under /media or /usr/local/backup only"}
	}
	// task 161: a USB target whose stick is not there is answered before the write test, which
	// would create the directory on /media's tmpfs - hidden once the stick is mounted over it.
	// Only on the system itself: a development root has no /media of its own to look at.
	if missing, err := noMediumAt(t); missing && (m.Root == "" || m.Root == "/") {
		return TestResult{Step: "medium", State: StateNoMedium, Error: err.Error()}
	}
	w, err := m.Helper().WriteTest(ctx, m.join(dir))
	if err != nil {
		return TestResult{Step: "helper", State: StateError, Error: err.Error()}
	}
	r := TestResult{OK: w.OK, Step: w.Step, Error: w.Error, FreeBytes: w.FreeBytes, TotalBytes: w.TotalBytes, WriteMBps: w.WriteMBps, FSType: w.FSType}
	r.State = stateFromTest(w)
	if t.IsMount() && w.OK && w.FSType != "nfs" && w.FSType != "cifs" && w.FSType != "smb2" {
		// written, but not onto the share: the mount did not come up
		r.OK, r.Step, r.State = false, "mount", StateUnreachable
		r.Error = "the share is not mounted (" + w.FSType + ")"
	}
	if t.IsMount() && !r.OK && (r.Step == "mkdir" || r.Step == "statfs" || r.Step == "mount") {
		// the automount answers ENODEV; why the mount failed is in its unit's journal
		if st, d := m.mountLog(t.MountID()); st != "" {
			r.State, r.Error = st, d
		}
	}
	if t.Kind == KindDirectory && w.OK && (w.FSType == "tmpfs") {
		r.OK, r.State, r.Step = false, StateError, "statfs"
		r.Error = "the directory is in RAM: nothing there survives a reboot"
		if t.OnUSB() {
			// /media without a stick (task 161)
			r.State, r.Error = StateNoMedium, "no USB stick is mounted for "+dir
		}
	}
	return r
}

func stateFromTest(w priv.WriteTestResult) string {
	if w.OK {
		return StateWritable
	}
	switch w.Errno {
	case "EACCES", "EPERM", "EROFS":
		return StateReadOnly
	case "ENOSPC", "EDQUOT":
		return StateFull
	case "ESTALE", "EIO", "ENOTCONN":
		return StateStale
	case "EHOSTDOWN", "EHOSTUNREACH", "ECONNREFUSED", "ETIMEDOUT", "ENODEV", "ENOENT":
		return StateUnreachable
	}
	if w.Step == "timeout" {
		return StateStale
	}
	return StateError
}

// Mount mounts a share now (the first access through the automount); Unmount detaches it.
func (m *Manager) Mount(ctx context.Context, id string) (View, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	if !t.IsMount() {
		return View{}, ErrNotMount
	}
	where := m.join(netmount.Base + "/" + t.MountID())
	perr := m.Prober.Do("mount:"+id, func() error {
		_, err := os.ReadDir(where)
		return err
	})
	c := Check{At: m.now(), State: StateWritable}
	if perr != nil {
		c.State, c.Detail = Classify(perr), perr.Error()
		if !errors.Is(perr, ErrStale) {
			if st, d := m.mountLog(id); st != "" {
				c.State, c.Detail = st, d
			}
		}
	} else if mi, ok := m.mounted(id); !ok {
		c.State, c.Detail = StateUnreachable, "the share did not mount"
	} else if sp, err := StatSpace(where); err == nil {
		c.Free, c.Total = sp.Free, sp.Total
		_ = mi
	}
	if c.State == StateWritable {
		// mounted and answering; writability is the test's to say
		c.State = StateIdle
	}
	m.setCheck(id, c)
	return m.View(ctx, id)
}

// ErrNotMount: mount and unmount are for NFS and CIFS targets.
var ErrNotMount = errors.New("only NFS and SMB targets are mounted")

// Unmount detaches the share now; the automount stays armed.
func (m *Manager) Unmount(ctx context.Context, id string) (View, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	if !t.IsMount() {
		return View{}, ErrNotMount
	}
	if err := m.Helper().NetUnmount(ctx, t.MountID()); err != nil {
		return View{}, err
	}
	return m.View(ctx, id)
}

// ErrBusy: a run is in progress already.
var ErrBusy = errors.New("a backup is running")

// Start starts a run: a target's id, or all. It returns at once; the state is running until the
// units are done.
func (m *Manager) Start(ctx context.Context, instance string) error {
	if !ValidInstance(instance) || instance == "nightly" {
		return fmt.Errorf("%w: instance %q", ErrInvalid, instance)
	}
	if instance != "all" {
		if _, ok, err := m.Store.Get(instance); err != nil {
			return err
		} else if !ok {
			return ErrNotFound
		}
	}
	list, _ := m.Store.List()
	var ids []string
	for _, t := range list {
		ids = append(ids, t.ID)
	}
	for _, busy := range m.running(ctx, ids) {
		if busy {
			return ErrBusy
		}
	}
	res, err := m.Helper().Run(ctx, "systemctl", []string{"start", "--no-block", fmt.Sprintf(CreateUnit, instance)}, nil)
	if err != nil {
		return err
	}
	if res.Exit != 0 {
		return fmt.Errorf("systemctl start: %s", strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// Backups lists a target's backups (newest first), with the recovery key an encrypted one needs
// for the first few.
func (m *Manager) Backups(ctx context.Context, id string) ([]BackupFile, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	if t.Kind == KindSFTP {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		c, err := DialSFTP(ctx, t.SFTP, m.Store.SecretDir(t.ID))
		if err != nil {
			m.setCheck(id, Check{At: m.now(), State: Classify(err), Step: "connect", Detail: err.Error()})
			return nil, err
		}
		defer c.Close()
		files, err := c.List(t.RemoteDir())
		if err != nil {
			return nil, err
		}
		m.annotate(files, func(name string) (io.ReadCloser, error) { return c.Open(t.RemoteDir(), name) })
		return files, nil
	}
	dir := m.join(t.Dir())
	var files []BackupFile
	err = m.Prober.DoWithin("list:"+id, shares.MountWait, func() error {
		var err error
		if t.IsMount() {
			// B-217: read as root, the identity that wrote them - on a root-squashed export
			// occulited's own user can neither list nor open the folders root made there
			files, err = m.listShare(ctx, dir)
			if err == nil {
				m.annotate(files, func(name string) (io.ReadCloser, error) { return m.Helper().ShareOpen(filepath.Join(dir, name)) })
			}
			return err
		}
		files, err = ListDir(dir)
		if err == nil {
			m.annotate(files, func(name string) (io.ReadCloser, error) { return os.Open(filepath.Join(dir, name)) })
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrStale) {
			m.setCheck(id, Check{At: m.now(), State: StateStale, Step: "list", Detail: err.Error()})
		}
		return nil, err
	}
	return files, nil
}

// annotate reads the age header of the newest encrypted files: which recovery key they need.
// listShare lists a share target's folder through the helper, as root (B-217): the backups, newest
// first; a folder that does not exist yet is an empty list, as ListDir answers.
func (m *Manager) listShare(ctx context.Context, dir string) ([]BackupFile, error) {
	entries, err := priv.ListShare(ctx, m.Helper(), dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []BackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupFile{}
	for _, e := range entries {
		if e.Dir || e.Link || !IsBackupName(e.Name) {
			continue
		}
		out = append(out, BackupFile{Name: e.Name, Size: e.Size, Time: e.MTime, Encrypted: filepath.Ext(e.Name) == ".age"})
	}
	SortBackups(out)
	return out, nil
}

func (m *Manager) annotate(files []BackupFile, open func(string) (io.ReadCloser, error)) {
	n := 0
	for i := range files {
		if !files[i].Encrypted || n >= 10 {
			continue
		}
		n++
		rc, err := open(files[i].Name)
		if err != nil {
			continue
		}
		h, _, err := backupcrypt.Sniff(io.LimitReader(rc, 64*1024))
		rc.Close()
		if err != nil || !h.Encrypted {
			continue
		}
		files[i].RecoveryFingerprint = h.RecoveryFingerprint
		if m.Crypt != nil && h.RecoveryFingerprint != "" {
			files[i].Known, _ = m.Crypt.MatchFingerprint(h.RecoveryFingerprint)
		}
	}
}

// Open opens a backup on a target for a restore. The name is a backup name and nothing else.
func (m *Manager) Open(ctx context.Context, id, name string) (io.ReadCloser, error) {
	if !IsBackupName(name) || filepath.Base(name) != name || strings.Contains(name, "/") {
		return nil, fmt.Errorf("%w: name %q", ErrInvalid, name)
	}
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	if t.Kind == KindSFTP {
		c, err := DialSFTP(ctx, t.SFTP, m.Store.SecretDir(t.ID))
		if err != nil {
			return nil, err
		}
		f, err := c.Open(t.RemoteDir(), name)
		if err != nil {
			c.Close()
			return nil, err
		}
		return &closeBoth{ReadCloser: f, conn: c}, nil
	}
	var f *os.File
	err = m.Prober.DoWithin("open:"+id, shares.MountWait, func() error {
		var err error
		if t.IsMount() {
			f, err = m.Helper().ShareOpen(filepath.Join(m.join(t.Dir()), name)) // as root (B-217)
			return err
		}
		f, err = os.Open(filepath.Join(m.join(t.Dir()), name))
		return err
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

type closeBoth struct {
	io.ReadCloser
	conn *SFTPConn
}

func (c *closeBoth) Close() error {
	err := c.ReadCloser.Close()
	c.conn.Close()
	return err
}

// Keypair makes a new SSH key for an SFTP target.
func (m *Manager) Keypair(id string) (string, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	if t.Kind != KindSFTP {
		return "", fmt.Errorf("%w: keys are for SSH targets", ErrInvalid)
	}
	return GenerateKey(m.Store.SecretDir(id), m.hostname())
}

// HostKeyView is GET /backup/targets/{id}/hostkey.
type HostKeyView struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Trusted     bool   `json:"trusted"`
	Changed     bool   `json:"changed"`
	// TrustedFingerprint is the pinned key's when the server now shows another one.
	TrustedFingerprint string `json:"trusted_fingerprint,omitempty"`
}

// ScanHostKey connects to an SFTP target's server and answers its key against the pin.
func (m *Manager) ScanHostKey(ctx context.Context, id string) (HostKeyView, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return HostKeyView{}, err
	}
	if !ok {
		return HostKeyView{}, ErrNotFound
	}
	if t.Kind != KindSFTP {
		return HostKeyView{}, fmt.Errorf("%w: host keys are for SSH targets", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	k, err := ScanHostKey(ctx, t.SFTP)
	if err != nil {
		m.setCheck(id, Check{At: m.now(), State: Classify(err), Step: "connect", Detail: err.Error()})
		return HostKeyView{}, err
	}
	v := HostKeyView{Type: k.Type(), Fingerprint: fingerprint(k)}
	if pin := TrustedHostKey(m.Store.SecretDir(id)); pin != nil {
		v.Trusted = pin.Fingerprint == v.Fingerprint
		v.Changed = !v.Trusted
		if v.Changed {
			v.TrustedFingerprint = pin.Fingerprint
		}
	}
	return v, nil
}

// ErrFingerprint: the server's key is not the one the user confirmed.
var ErrFingerprint = errors.New("the server's key is not the one confirmed: scan it again and compare")

// TrustHostKey pins the server's key when it is the one the user confirmed.
func (m *Manager) TrustHostKey(ctx context.Context, id, fp string) (HostKeyView, error) {
	t, ok, err := m.Store.Get(id)
	if err != nil {
		return HostKeyView{}, err
	}
	if !ok {
		return HostKeyView{}, ErrNotFound
	}
	if t.Kind != KindSFTP {
		return HostKeyView{}, fmt.Errorf("%w: host keys are for SSH targets", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	k, err := ScanHostKey(ctx, t.SFTP)
	if err != nil {
		return HostKeyView{}, err
	}
	if fingerprint(k) != fp {
		return HostKeyView{}, ErrFingerprint
	}
	if err := TrustHostKey(m.Store.SecretDir(id), k); err != nil {
		return HostKeyView{}, err
	}
	m.mu.Lock()
	delete(m.checks, id)
	m.mu.Unlock()
	return HostKeyView{Type: k.Type(), Fingerprint: fp, Trusted: true}, nil
}

// Watch is the watchdog and the retries, until ctx ends: every minute a mounted share is probed
// (a stale one is unmounted, so the automount mounts it fresh next time), and a target whose last
// check failed is checked again on the backoff schedule.
func (m *Manager) Watch(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.WatchOnce(ctx)
	}
}

// WatchOnce is one round of Watch.
func (m *Manager) WatchOnce(ctx context.Context) {
	list, err := m.Store.List()
	if err != nil {
		return
	}
	kinds := m.Kinds()
	for _, t := range list {
		if m.unsupported(t, kinds) != "" {
			continue
		}
		if t.IsMount() && t.Kind != KindShare {
			// a share target's mount is the share's, and the shares' watchdog looks after it; a
			// task-86 NFS/SMB target (before its migration) gets the same check here: its server,
			// never the mount point - a statfs there every round kept the automount from ever
			// idling out (B-215)
			if _, ok := m.mounted(t.MountID()); ok {
				var err error
				if spec, ok := t.Mount(); ok {
					err = shares.ServerAnswers(ctx, spec.Kind, spec.Server, m.Prober.Timeout)
				}
				if err != nil {
					m.setCheck(t.ID, Check{At: m.now(), State: StateStale, Step: "watchdog", Detail: err.Error()})
					_ = m.Helper().NetUnmount(ctx, t.MountID())
					continue
				}
			}
		}
		c, ok := m.check(t.ID)
		failed := ok && Failed(c.State)
		if !ok {
			if r, rok := ReadResult(m.Store.SecretDir(t.ID)); rok && !r.OK && t.Enabled {
				failed = true
				c = Check{At: r.At, State: r.State, Next: r.At.Add(RetryDelay(1))}
			}
		}
		if !failed || m.now().Before(c.Next) {
			continue
		}
		m.recheck(ctx, t)
	}
}

// recheck is the light check a retry runs: reachable, and the directory there - no write.
func (m *Manager) recheck(ctx context.Context, t Target) {
	c := Check{At: m.now(), State: StateIdle, Step: "retry"}
	switch {
	case t.Kind == KindSFTP:
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		conn, err := DialSFTP(cctx, t.SFTP, m.Store.SecretDir(t.ID))
		if err == nil {
			if free, total, ok := conn.Space(t.RemoteDir()); ok {
				c.Free, c.Total = free, total
			}
			conn.Close()
		} else {
			c.State, c.Detail = Classify(err), err.Error()
		}
		cancel()
	default:
		dir := m.join(t.Dir())
		if t.IsMount() {
			dir = m.join(netmount.Base + "/" + t.MountID())
		}
		err := m.Prober.Do("retry:"+t.ID, func() error {
			if _, err := os.ReadDir(dir); err != nil {
				return err
			}
			sp, err := StatSpace(dir)
			if err == nil {
				c.Free, c.Total = sp.Free, sp.Total
				if t.IsMount() && !IsNetworkFS(sp.FSType) {
					return errors.New("the share did not mount")
				}
			}
			return err
		})
		if err == nil {
			if missing, merr := noMedium(t); missing {
				err = merr
			}
		}
		if err != nil {
			c.State, c.Detail = Classify(err), err.Error()
			if c.State == StateError {
				c.State = StateUnreachable
			}
			if t.OnUSB() && !errors.Is(err, ErrStale) {
				c.State = StateNoMedium // a stick that is not there, not a server that does not answer
			}
			if t.IsMount() && !errors.Is(err, ErrStale) {
				if st, d := m.mountLog(t.MountID()); st != "" {
					c.State, c.Detail = st, d
				}
			}
		}
	}
	m.setCheck(t.ID, c)
}

// TooOld is how long an enabled target may go without a successful backup while the nightly run
// is on: a night plus the random delay.
const TooOld = 26 * time.Hour

// Problem is one target's warning for the Status page.
type Problem struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Cause  string `json:"cause"`
	Detail string `json:"detail,omitempty"`
}

// Problems answers the warnings: a failed state, and too-old.
func (m *Manager) Problems(ctx context.Context) ([]Problem, error) {
	views, err := m.Views(ctx)
	if err != nil {
		return nil, err
	}
	nightly := m.Store.Nightly()
	var out []Problem
	for _, v := range views {
		if !v.Enabled || v.State.State == StateUnsupported {
			continue
		}
		cause := ""
		if v.State.State == StateHostKeyUnknown && v.LastBackup == nil {
			// set up, never contacted: the card asks for the server's key, the Status page does not yet
			continue
		}
		switch s := v.State.State; {
		case Failed(s) && s == StateError && v.LastBackup != nil && !v.LastBackup.OK:
			cause = "last-failed"
		case Failed(s):
			cause = s
		}
		if cause == "" && nightly && v.State.State != StateRunning {
			var last time.Time
			if v.LastBackup != nil && v.LastBackup.LastOK != nil {
				last = *v.LastBackup.LastOK
			}
			switch {
			case !last.IsZero() && m.now().Sub(last) > TooOld:
				cause = "too-old"
			case last.IsZero() && v.LastBackup != nil && m.now().Sub(v.LastBackup.At) > TooOld:
				cause = "too-old"
			case last.IsZero() && v.LastBackup == nil && !v.Created.IsZero() && m.now().Sub(v.Created) > TooOld:
				cause = "too-old"
			}
		}
		if cause != "" {
			out = append(out, Problem{ID: v.ID, Name: v.Name, Cause: cause, Detail: v.State.Detail})
		}
	}
	return out, nil
}
