package shares

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// The daemon's side: the shares' states, the test, mount and unmount through the helper, the
// watchdog and the retries - task 86's machinery for its mount targets, for any share.

// Helper is what the manager needs from the privilege boundary (system.Priv).
type Helper interface {
	NetMount(ctx context.Context, s netmount.Spec) error
	NetUnmount(ctx context.Context, id string) error
	NetMountRemove(ctx context.Context, id string) error
	WriteTest(ctx context.Context, dir string) (priv.WriteTestResult, error)
	// ShareList lists a folder of a share as root (B-217: the folder picker on a root-squashed
	// export, whose folders occulited's own user cannot enter).
	ShareList(ctx context.Context, dir string) (priv.ShareListResult, error)
}

// MountWait bounds a first access that mounts a share: the mount unit's own 30 s and a margin.
const MountWait = 40 * time.Second

// Manager is the daemon's view of the shares.
type Manager struct {
	Store *Store
	// Helper answers the privilege boundary at call time (the daemon switches it at start).
	Helper func() Helper
	// Container is the container kind, "" on hardware and VMs (system.Root.Container).
	Container func() string
	// Journal answers a unit's last lines, for a mount's reason.
	Journal func(unit string) string
	// Root is the filesystem root the tools and /proc are under ("/" on a system).
	Root   string
	Now    func() time.Time
	Prober Prober
	// Uses answers what uses a share (the journal's copies, backup targets); nil = nothing.
	Uses func(id string) []Use
	// ReadBack checks that the daemon's own user can read a folder root wrote (B-223); nil = the
	// package's ReadBack.
	ReadBack func(dir string) error

	mu     sync.Mutex
	checks map[string]Check
	spaces map[string]spaceReading
}

// Check is the last test, mount or probe of a share.
type Check struct {
	At       time.Time
	State    string
	Step     string
	Detail   string
	Free     int64
	Total    int64
	Failures int
	Next     time.Time
}

// StateView is a share's state for the API.
type StateView struct {
	// State is mounted, idle (the automount armed, nothing mounted), unreachable, auth-failed,
	// stale, read-only, error - or unsupported (Unsupported says why: container, tool).
	State       string     `json:"state"`
	Detail      string     `json:"detail,omitempty"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`
	Mounted     bool       `json:"mounted"`
	Source      string     `json:"source,omitempty"`
	Options     string     `json:"options,omitempty"`
	FreeBytes   int64      `json:"free_bytes,omitempty"`
	TotalBytes  int64      `json:"total_bytes,omitempty"`
	Unsupported string     `json:"unsupported,omitempty"`
}

// View is one share for GET /storage/shares.
type View struct {
	Share
	Where string    `json:"where"`
	State StateView `json:"state"`
	Uses  []Use     `json:"uses"`
}

// Use is one thing that uses a share: the journal's copies, a backup target (its name and id).
type Use struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

// ErrInUse: a share that is used is not removed.
var ErrInUse = errors.New("the share is in use")

func (m *Manager) uses(id string) []Use {
	if m.Uses == nil {
		return []Use{}
	}
	if u := m.Uses(id); u != nil {
		return u
	}
	return []Use{}
}

// TestResult is POST /storage/shares/{id}/test.
type TestResult struct {
	OK         bool      `json:"ok"`
	State      string    `json:"state"`
	Step       string    `json:"step"`
	Error      string    `json:"error,omitempty"`
	FreeBytes  int64     `json:"free_bytes"`
	TotalBytes int64     `json:"total_bytes"`
	WriteMBps  float64   `json:"write_mbps,omitempty"`
	FSType     string    `json:"fs_type,omitempty"`
	ReadOnly   bool      `json:"read_only,omitempty"`
	At         time.Time `json:"at"`
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

// Failed says whether a state is a failure.
func Failed(state string) bool {
	switch state {
	case StateUnreachable, StateAuthFailed, StateStale, StateError, StateReadOnly, StateFull:
		return true
	}
	return false
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

func (m *Manager) setCheck(id string, c Check) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.checks == nil {
		m.checks = map[string]Check{}
	}
	if Failed(c.State) {
		c.Failures = m.checks[id].Failures + 1
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

func (m *Manager) forget(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.checks, id)
}

// Kinds answers which kinds this system supports, and why not: "container" (no mount in a
// container), "tool" (mount.cifs is not in this image).
func (m *Manager) Kinds() map[string]string {
	out := map[string]string{KindNFS: "", KindCIFS: ""}
	if m.Container != nil && m.Container() != "" {
		out[KindNFS], out[KindCIFS] = "container", "container"
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

// Unsupported answers why a share cannot be mounted here, "" when it can.
func (m *Manager) Unsupported(s Share) string {
	if why := m.Kinds()[s.Kind]; why != "" {
		return why
	}
	// NFS without mount.nfs mounts by an address only (the kernel wants addr=)
	if s.Kind == KindNFS && !m.exists("/sbin/mount.nfs") && !m.exists("/usr/sbin/mount.nfs") && !isIP(s.Server) {
		return "tool"
	}
	return ""
}

func isIP(s string) bool {
	_, err := netip.ParseAddr(s)
	return err == nil
}

// Mounted reads the mount on a share's mount point, when there is one. It never touches the path
// itself, so it neither mounts an idle share nor hangs on a dead one.
func (m *Manager) Mounted(id string) (Mountinfo, bool) {
	return ReadMountinfo(m.join("/proc/self/mountinfo"), netmount.Base+"/"+id)
}

// SpaceTTL is how long a mounted share's measured space is shown before it is measured again:
// longer than the automount's idle time, so that looking at the list does not keep a share mounted.
var SpaceTTL = 10 * time.Minute

type spaceReading struct {
	at time.Time
	sp Space
}

// space measures a mounted share's space through the probe, or answers the last measurement while
// it is younger than SpaceTTL.
func (m *Manager) space(s Share) (Space, error) {
	m.mu.Lock()
	r, ok := m.spaces[s.ID]
	m.mu.Unlock()
	if ok && m.now().Sub(r.at) < SpaceTTL {
		return r.sp, nil
	}
	var sp Space
	err := m.Prober.Do("space:"+s.ID, func() error {
		var err error
		sp, err = StatSpace(m.join(s.Where()))
		return err
	})
	if err == nil {
		m.mu.Lock()
		if m.spaces == nil {
			m.spaces = map[string]spaceReading{}
		}
		m.spaces[s.ID] = spaceReading{at: m.now(), sp: sp}
		m.mu.Unlock()
	}
	return sp, err
}

// Views answers every share with its state.
func (m *Manager) Views() ([]View, error) {
	list, err := m.Store.List()
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(list))
	for _, s := range list {
		out = append(out, m.view(s))
	}
	return out, nil
}

// View answers one share.
func (m *Manager) View(id string) (View, error) {
	s, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	return m.view(s), nil
}

func (m *Manager) view(s Share) View {
	v := View{Share: s, Where: s.Where(), Uses: m.uses(s.ID)}
	if why := m.Unsupported(s); why != "" {
		v.State = StateView{State: StateUnsupported, Unsupported: why}
		return v
	}
	c, hasCheck := m.check(s.ID)
	if hasCheck {
		at := c.At
		v.State.CheckedAt = &at
		if Failed(c.State) && !c.Next.IsZero() {
			n := c.Next
			v.State.NextRetryAt = &n
		}
	}
	if mi, ok := m.Mounted(s.ID); ok {
		v.State.Mounted, v.State.Source, v.State.Options = true, mi.Source, mi.Options
		v.State.State = StateMounted
		// the space of a mounted share: through the probe, so a dead server answers stale - and at
		// most once per SpaceTTL: a statfs walks the mount point and resets the automount's idle
		// timer, so a page polling the list once a minute kept every share mounted (B-215)
		sp, err := m.space(s)
		if err != nil {
			v.State.State, v.State.Detail = ClassifyErrno(err), err.Error()
			if v.State.State == StateMounted {
				v.State.State = StateError
			}
		} else {
			v.State.FreeBytes, v.State.TotalBytes = sp.Free, sp.Total
		}
		return v
	}
	v.State.State = StateIdle
	if hasCheck && Failed(c.State) {
		v.State.State, v.State.Detail = c.State, c.Detail
	}
	return v
}

// Apply renders a share's units through the helper (after a create or an edit, and at start: /run
// is empty after a boot).
func (m *Manager) Apply(ctx context.Context, s Share) error {
	if m.Unsupported(s) != "" {
		return nil
	}
	m.forget(s.ID)
	return m.Helper().NetMount(ctx, s.Spec())
}

// ApplyAll renders every share's units; the errors are returned per share.
func (m *Manager) ApplyAll(ctx context.Context) map[string]error {
	out := map[string]error{}
	list, err := m.Store.List()
	if err != nil {
		out[""] = err
		return out
	}
	for _, s := range list {
		if err := m.Apply(ctx, s); err != nil {
			out[s.ID] = err
		}
	}
	return out
}

// Remove takes a share's units down and deletes its configuration and secrets; the files on the
// share stay.
func (m *Manager) Remove(ctx context.Context, id string) error {
	if _, ok, err := m.Store.Get(id); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	if u := m.uses(id); len(u) > 0 {
		var names []string
		for _, x := range u {
			if x.Kind == "journal" {
				names = append(names, "the journal's copies")
			} else {
				names = append(names, "the backup target "+x.Name)
			}
		}
		return fmt.Errorf("%w by %s: choose another location there first", ErrInUse, strings.Join(names, ", "))
	}
	if err := m.Helper().NetMountRemove(ctx, id); err != nil {
		return fmt.Errorf("units: %w", err)
	}
	m.forget(id)
	return m.Store.Delete(id)
}

// mountReason reads the mount unit's journal for why a mount failed; state "" when there is none,
// and when the share is mounted: then the error is its own reason (EACCES on a root-squashed
// export), not the unit's last line (B-212).
func (m *Manager) mountReason(id string) (state, detail string) {
	if m.Journal == nil {
		return "", ""
	}
	if _, mounted := m.Mounted(id); mounted {
		return "", ""
	}
	text := m.Journal(netmount.UnitName(id) + ".mount")
	if text == "" {
		return "", ""
	}
	return MountReason(text)
}

// MountFailure is why the share's last mount failed, from its unit's journal: state "" when the
// journal says nothing (or the share is mounted).
func (m *Manager) MountFailure(id string) (state, detail string) { return m.mountReason(id) }

// access makes the first access - the automount mounts the share - and answers the mount.
func (m *Manager) access(s Share) (Mountinfo, error) {
	where := m.join(s.Where())
	err := m.Prober.DoWithin("access:"+s.ID, MountWait, func() error {
		_, err := os.ReadDir(where)
		return err
	})
	mi, mounted := m.Mounted(s.ID)
	if mounted {
		// mounted; a directory this daemon may not list (an export only root reads) is no failure
		// of the mount
		if err != nil && !errors.Is(err, ErrStale) && ClassifyErrno(err) == StateReadOnly {
			err = nil
		}
		return mi, err
	}
	if err == nil {
		err = errors.New("the share did not mount")
	}
	return Mountinfo{}, err
}

// Mount mounts a share now (the first access through the automount).
func (m *Manager) Mount(ctx context.Context, id string) (View, error) {
	s, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	if why := m.Unsupported(s); why != "" {
		return m.view(s), nil
	}
	c := Check{At: m.now(), State: StateMounted, Step: "mount"}
	if _, err := m.access(s); err != nil {
		c.State, c.Detail = ClassifyErrno(err), err.Error()
		if !errors.Is(err, ErrStale) {
			if st, d := m.mountReason(id); st != "" {
				c.State, c.Detail = st, d
			} else if c.State == StateError || c.State == StateMounted {
				c.State = StateUnreachable
			}
		}
	}
	m.setCheck(id, c)
	return m.view(s), nil
}

// Unmount detaches the share now; the automount stays armed.
func (m *Manager) Unmount(ctx context.Context, id string) (View, error) {
	s, ok, err := m.Store.Get(id)
	if err != nil {
		return View{}, err
	}
	if !ok {
		return View{}, ErrNotFound
	}
	if err := m.Helper().NetUnmount(ctx, id); err != nil {
		return View{}, err
	}
	return m.view(s), nil
}

// Test mounts the share, writes, reads back and deletes a 1 MiB probe file as root (the identity
// the backups and the journal's copies write with) - for a read-only share it lists it instead -
// and unmounts it again when it was not mounted before.
func (m *Manager) Test(ctx context.Context, id string) (TestResult, error) {
	s, ok, err := m.Store.Get(id)
	if err != nil {
		return TestResult{}, err
	}
	if !ok {
		return TestResult{}, ErrNotFound
	}
	r := TestResult{At: m.now(), ReadOnly: s.ReadOnly}
	if why := m.Unsupported(s); why != "" {
		r.State, r.Step, r.Error = StateUnsupported, "kind", why
		return r, nil
	}
	_, wasMounted := m.Mounted(id)
	r = m.test(ctx, s, "", r)
	if !wasMounted && r.State != StateStale {
		// the test leaves the share as it found it; a share in use stays mounted
		_ = m.Helper().NetUnmount(ctx, id)
	}
	c := Check{At: r.At, State: r.State, Step: r.Step, Detail: r.Error, Free: r.FreeBytes, Total: r.TotalBytes}
	if r.OK {
		c.State = StateIdle
	}
	m.setCheck(id, c)
	return r, nil
}

// TestFolder is the write test in a folder on the share (made when missing) - for a use that
// writes there, the journal's copies (B-213): root may be able to write the share's root and not
// the folder, or the other way round. A read-only share is read-only. The share is left mounted
// or not as it was; its own state is not changed.
func (m *Manager) TestFolder(ctx context.Context, id, folder string) (TestResult, error) {
	s, ok, err := m.Store.Get(id)
	if err != nil {
		return TestResult{}, err
	}
	if !ok {
		return TestResult{}, ErrNotFound
	}
	r := TestResult{At: m.now(), ReadOnly: s.ReadOnly}
	if why := m.Unsupported(s); why != "" {
		r.State, r.Step, r.Error = StateUnsupported, "kind", why
		return r, nil
	}
	if s.ReadOnly {
		r.State, r.Step, r.Error = StateReadOnly, "config", "the share is mounted read-only"
		return r, nil
	}
	_, wasMounted := m.Mounted(id)
	r = m.test(ctx, s, folder, r)
	if !wasMounted && r.State != StateStale {
		_ = m.Helper().NetUnmount(ctx, id)
	}
	return r, nil
}

// test is Test's and TestFolder's work: a read-only share is listed, any other written (in folder
// when there is one).
func (m *Manager) test(ctx context.Context, s Share, folder string, r TestResult) TestResult {
	id := s.ID
	if s.ReadOnly {
		mi, err := m.access(s)
		switch {
		case err != nil:
			r.Step, r.State, r.Error = "mount", ClassifyErrno(err), err.Error()
			if !errors.Is(err, ErrStale) {
				if st, d := m.mountReason(id); st != "" {
					r.State, r.Error = st, d
				} else if r.State == StateError || r.State == StateMounted {
					r.State = StateUnreachable
				}
			}
		default:
			r.OK, r.Step, r.State, r.FSType = true, "done", StateReadable, mi.FSType
			if sp, err := StatSpace(m.join(s.Where())); err == nil {
				r.FreeBytes, r.TotalBytes = sp.Free, sp.Total
			}
		}
	} else {
		dir := s.Where()
		if folder != "" {
			dir += "/" + folder
		}
		w, err := m.Helper().WriteTest(ctx, m.join(dir))
		if err != nil {
			r.Step, r.State, r.Error = "helper", StateError, err.Error()
			return r
		}
		r.OK, r.Step, r.Error, r.FreeBytes, r.TotalBytes, r.WriteMBps, r.FSType = w.OK, w.Step, w.Error, w.FreeBytes, w.TotalBytes, w.WriteMBps, w.FSType
		r.State = StateFromTest(w)
		if w.OK && w.FSType != "nfs" && w.FSType != "cifs" && w.FSType != "smb2" {
			// written, but not onto the share: the mount did not come up
			r.OK, r.Step, r.State = false, "mount", StateUnreachable
			r.Error = "the share is not mounted (" + w.FSType + ")"
		}
		if r.OK {
			// B-223: root wrote there; can the system's own user read it back? A root-squashed
			// export gives root's folders to nobody, 0770 - written, and never shown.
			if err := m.readBack(id, m.join(dir)); err != nil {
				r.State, r.Step, r.Error = StateUnreadable, "read-back", err.Error()
			}
		}
		if !r.OK && (r.Step == "mkdir" || r.Step == "statfs" || r.Step == "mount") {
			// the automount answers ENODEV; why the mount failed is in its unit's journal
			if st, d := m.mountReason(id); st != "" {
				r.State, r.Error = st, d
			}
		}
	}
	return r
}

// readBack is ReadBack through the prober: a share that hangs answers stale, which is not a
// permission error, so nil.
func (m *Manager) readBack(id, dir string) error {
	fn := m.ReadBack
	if fn == nil {
		fn = ReadBack
	}
	var rerr error
	if err := m.Prober.Do("readback:"+id, func() error { rerr = fn(dir); return nil }); err != nil {
		return nil
	}
	return rerr
}

// StateFromTest maps the helper's write test onto a state.
func StateFromTest(w priv.WriteTestResult) string {
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

// Watch is the watchdog and the retries, until ctx ends: every round the server of a mounted share
// is probed with a TCP connection - never the mount point, which would keep the share from idling
// out (B-215) - and a share whose server does not answer is unmounted, so the automount mounts it
// fresh next time; a share whose last check failed is tried again on the backoff schedule.
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
	for _, s := range list {
		if m.Unsupported(s) != "" {
			continue
		}
		if _, ok := m.Mounted(s.ID); ok {
			// B-215: the server, not the mount point - a statfs there every round reset the
			// automount's idle timer, and no share ever unmounted while occulited ran
			err := ServerAnswers(ctx, s.Kind, s.Server, m.Prober.Timeout)
			if err != nil {
				m.setCheck(s.ID, Check{At: m.now(), State: StateStale, Step: "watchdog", Detail: err.Error()})
				if uerr := m.Helper().NetUnmount(ctx, s.ID); uerr != nil {
					slog.Warn("shares: a stale share was not unmounted", "share", s.ID, "err", uerr)
				} else {
					slog.Warn("shares: the share does not answer; unmounted, the next use mounts it again", "share", s.ID)
				}
			}
			continue
		}
		c, ok := m.check(s.ID)
		if !ok || !Failed(c.State) || m.now().Before(c.Next) {
			continue
		}
		// the retry: a mount through the first access, no write; an idle share unmounts again after
		// the automount's idle time
		_, _ = m.Mount(ctx, s.ID)
	}
}

// LastCheck answers the last check of a share (for the uses that report a share's state).
func (m *Manager) LastCheck(id string) (Check, bool) { return m.check(id) }
