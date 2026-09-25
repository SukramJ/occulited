package shares

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
)

// The reads a share that hangs must not block: every stat, listing and open of a share's path goes
// through a Prober, so a server that does not answer becomes the state stale after a deadline
// instead of a handler that never returns. At most one probe per key is in flight; one that is
// stuck in an uninterruptible call is abandoned, and until it returns the key answers stale at once
// rather than stacking another goroutine on the same dead mount. (Lifted from task 86's backup
// targets, which use it for their directories as well.)

// ErrStale is a probe that did not answer in time: the share hangs.
var ErrStale = errors.New("the target does not answer (stale)")

// ProbeTimeout is how long a probe waits by default.
var ProbeTimeout = 5 * time.Second

// Prober runs the reads.
type Prober struct {
	Timeout time.Duration

	mu       sync.Mutex
	inflight map[string]bool
}

// Do runs fn for key within the deadline; ErrStale when it did not finish or one is still stuck.
func (p *Prober) Do(key string, fn func() error) error {
	return p.DoWithin(key, 0, fn)
}

// DoWithin is Do with its own deadline (0 = the Prober's): a first access that mounts a share may
// take the mount's own bound.
func (p *Prober) DoWithin(key string, timeout time.Duration, fn func() error) error {
	p.mu.Lock()
	if p.inflight == nil {
		p.inflight = map[string]bool{}
	}
	if p.inflight[key] {
		p.mu.Unlock()
		return ErrStale
	}
	p.inflight[key] = true
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		err := fn()
		p.mu.Lock()
		delete(p.inflight, key)
		p.mu.Unlock()
		done <- err
	}()
	if timeout <= 0 {
		timeout = p.Timeout
	}
	if timeout <= 0 {
		timeout = ProbeTimeout
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case err := <-done:
		return err
	case <-t.C:
		return ErrStale
	}
}

// ServerPort is the port a share's server answers on: NFS 2049, SMB 445.
func ServerPort(kind string) string {
	if kind == netmount.KindCIFS {
		return "445"
	}
	return "2049"
}

// DialServer opens the probe's connection (a test swaps it).
var DialServer = func(ctx context.Context, addr string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

// ServerAnswers says whether a share's server takes a TCP connection on its port within the probe's
// deadline - the watchdog's check of a mounted share (B-215). It never touches the mount point: a
// path walk through a direct autofs mount refreshes the automount's idle timer, so a statfs every
// minute kept every share mounted for good. A server that does not answer is ErrStale.
func ServerAnswers(ctx context.Context, kind, server string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = ProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := DialServer(ctx, net.JoinHostPort(server, ServerPort(kind)))
	if err != nil {
		return fmt.Errorf("%w: %s port %s: %v", ErrStale, server, ServerPort(kind), err)
	}
	return c.Close()
}

// Space is a directory's filesystem.
type Space struct {
	Free, Total int64
	FSType      int64
}

// StatSpace reads the filesystem of path (a statfs, which also triggers an automount).
func StatSpace(path string) (Space, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Space{}, err
	}
	return Space{Free: int64(st.Bavail) * int64(st.Bsize), Total: int64(st.Blocks) * int64(st.Bsize), FSType: int64(st.Type)}, nil
}

// The filesystem magics of a network share.
const (
	magicNFS  = 0x6969
	magicCIFS = 0xFF534D42
	magicSMB2 = 0xFE534D42
)

// IsNetworkFS says whether a statfs type is a mounted network share.
func IsNetworkFS(t int64) bool { return t == magicNFS || t == magicCIFS || t == magicSMB2 }

// Mountinfo is one entry of /proc/self/mountinfo (netmount.Mountinfo).
type Mountinfo = netmount.Mountinfo

// ReadMountinfo reads the network mount on where from the mountinfo file at path, when there is one.
func ReadMountinfo(path, where string) (Mountinfo, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Mountinfo{}, false
	}
	defer f.Close()
	return netmount.ParseMountinfo(f, where)
}

// ParseMountinfo finds the network mount on where (netmount.ParseMountinfo).
func ParseMountinfo(r io.Reader, where string) (Mountinfo, bool) {
	return netmount.ParseMountinfo(r, where)
}

// The states a share (and a backup target on one) is in.
const (
	StateUnsupported = "unsupported"
	StateIdle        = "idle"
	StateMounted     = "mounted"
	StateWritable    = "writable"
	StateReadable    = "readable"
	StateReadOnly    = "read-only"
	StateFull        = "full"
	StateUnreachable = "unreachable"
	StateAuthFailed  = "auth-failed"
	StateStale       = "stale"
	StateError       = "error"
)

// ClassifyMountLog maps what a failed mount wrote to the journal (the mount unit's lines) onto a
// state: mount.nfs and mount.cifs say what happened only there.
func ClassifyMountLog(text string) string {
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "access denied"), strings.Contains(t, "mount error(13)"), strings.Contains(t, "permission denied"),
		strings.Contains(t, "logon failure"), strings.Contains(t, "status_logon_failure"):
		return StateAuthFailed
	case strings.Contains(t, "no route to host"), strings.Contains(t, "connection refused"), strings.Contains(t, "timed out"),
		strings.Contains(t, "host is down"), strings.Contains(t, "could not resolve"), strings.Contains(t, "failed to resolve"),
		strings.Contains(t, "name or service not known"), strings.Contains(t, "mount error(112)"), strings.Contains(t, "network is unreachable"),
		strings.Contains(t, "mount error(115)"), strings.Contains(t, "mount error(111)"):
		return StateUnreachable
	case strings.Contains(t, "no such file or directory"), strings.Contains(t, "mount error(2)"):
		return StateError
	}
	return StateUnreachable
}

// ClassifyErrno maps an error from a share's path onto a state.
func ClassifyErrno(err error) string {
	if err == nil {
		return StateMounted
	}
	if errors.Is(err, ErrStale) {
		return StateStale
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.EROFS, syscall.EACCES, syscall.EPERM:
			return StateReadOnly
		case syscall.ENOSPC, syscall.EDQUOT:
			return StateFull
		case syscall.ESTALE, syscall.EIO, syscall.ENOTCONN:
			return StateStale
		case syscall.EHOSTDOWN, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ECONNREFUSED, syscall.ETIMEDOUT, syscall.ENODEV:
			return StateUnreachable
		}
	}
	return StateError
}

// MountReason reads a mount unit's journal lines (oldest first) for the latest attempt - the lines
// after the last "Mounting …" - and answers its state and the line that says why: mount.nfs's or
// mount.cifs's own words when there are some, not systemd's closing "Failed to mount …". An attempt
// that ended in "Mounted …" did not fail: state "" (B-212 - systemd's success line is no reason).
func MountReason(text string) (state, detail string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	start := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "Mounting ") {
			start = i
		}
	}
	lines = lines[start:]
	if strings.HasPrefix(lines[len(lines)-1], "Mounted ") {
		return "", ""
	}
	attempt := strings.Join(lines, "\n")
	detail = lines[len(lines)-1]
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "refer to the mount.cifs(8)") {
			continue // mount.cifs's pointer to its manual, after the line that says why
		}
		if strings.Contains(l, "mount.nfs") || strings.Contains(l, "mount error") || strings.Contains(l, "mount.cifs") || strings.Contains(l, "cifs:") {
			detail = lines[i]
			break
		}
		if strings.Contains(l, "result 'timeout'") || strings.Contains(l, "timed out") {
			detail = "the server did not answer within " + netmountTimeout
		}
	}
	return ClassifyMountLog(attempt), detail
}

// netmountTimeout is the mount unit's bound, as the reason says it.
const netmountTimeout = "30 s"

// LastLine is a text's last line (a mount unit's journal: its reason).
func LastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}
