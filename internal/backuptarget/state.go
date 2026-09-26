package backuptarget

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/shares"
	"github.com/pkg/sftp"
)

// The states a target is in (the analysis's table).
const (
	StateNotConfigured  = "not-configured"
	StateUnsupported    = "unsupported"
	StateIdle           = "idle"
	StateConnecting     = "connecting"
	StateWritable       = "writable"
	StateReadOnly       = "read-only"
	StateFull           = "full"
	StateUnreachable    = "unreachable"
	StateAuthFailed     = "auth-failed"
	StateHostKeyUnknown = "host-key-unknown"
	StateHostKeyChanged = "host-key-changed"
	StateNoSFTP         = "no-sftp"
	StateStale          = "stale"
	StateRunning        = "running"
	StateError          = "error"
	// StateNoMedium: a directory target on a USB stick whose stick is not there (openccu-lite
	// task 86's follow-up, task 161): the delivery is skipped, not failed - the other targets get
	// their copy and the nightly unit succeeds - and the Status warning names the target.
	StateNoMedium = "no-medium"
	// StateUpdateRoom: a directory target on the system's own user partition that would leave
	// less than UpdateRoom free even with its older backups removed (openccu-lite B-247): the
	// backup is skipped, and the Status page says why.
	StateUpdateRoom = "update-room"
)

// Failed says whether a state is a failure the Status page warns about.
func Failed(state string) bool {
	switch state {
	case StateReadOnly, StateFull, StateUnreachable, StateAuthFailed, StateHostKeyUnknown, StateHostKeyChanged, StateNoSFTP, StateStale, StateError, StateNoMedium, StateUpdateRoom:
		return true
	}
	return false
}

// Result is one delivery's outcome, backup-targets/<id>/last.json.
type Result struct {
	At         time.Time `json:"at"`
	Instance   string    `json:"instance,omitempty"`
	OK         bool      `json:"ok"`
	State      string    `json:"state"`
	Step       string    `json:"step,omitempty"`
	Error      string    `json:"error,omitempty"`
	Name       string    `json:"name,omitempty"`
	Size       int64     `json:"size,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	SHA256     string    `json:"sha256,omitempty"`
	Encrypted  bool      `json:"encrypted"`
	// Removed is what retention deleted after the verified delivery.
	Removed []string `json:"removed,omitempty"`
	// LastOK is the last successful delivery's time, carried from run to run.
	LastOK *time.Time `json:"last_ok,omitempty"`
}

// ReadResult reads a target's last result; ok false when there is none.
func ReadResult(dir string) (Result, bool) {
	b, err := os.ReadFile(filepath.Join(dir, LastFile))
	if err != nil {
		return Result{}, false
	}
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return Result{}, false
	}
	return r, true
}

// WriteResult writes r, carrying the previous success's time when r is a failure.
func WriteResult(dir string, r Result) error {
	if r.OK {
		at := r.At
		r.LastOK = &at
	} else if prev, ok := ReadResult(dir); ok {
		r.LastOK = prev.LastOK
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, LastFile), b, 0o600)
}

// Classify maps an error from a target onto a state.
func Classify(err error) string {
	if err == nil {
		return StateWritable
	}
	var hk *HostKeyError
	if errors.As(err, &hk) {
		if hk.Changed {
			return StateHostKeyChanged
		}
		return StateHostKeyUnknown
	}
	var st *sftp.StatusError
	if errors.As(err, &st) {
		switch {
		case st.FxCode() == sftp.ErrSSHFxPermissionDenied:
			return StateReadOnly
		case st.Code == 14 || st.Code == 15: // SSH_FX_NO_SPACE_ON_FILESYSTEM, SSH_FX_QUOTA_EXCEEDED
			return StateFull
		case strings.Contains(strings.ToLower(st.Error()), "no space"), strings.Contains(strings.ToLower(st.Error()), "quota"):
			// OpenSSH speaks version 3 and sends ENOSPC as a failure with the text
			return StateFull
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		return StateReadOnly
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
		// any other errno of a path (ENOTDIR, EISDIR, …) is what it says, not a network that does
		// not answer: syscall.Errno is a net.Error too, which read every such one as unreachable
		var op *net.OpError
		if !errors.As(err, &op) {
			return StateError
		}
	}
	if errors.Is(err, ErrStale) {
		return StateStale
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return StateUnreachable
	}
	var dns *net.DNSError
	var op *net.OpError
	if errors.As(err, &dns) || errors.As(err, &op) {
		return StateUnreachable
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"), strings.Contains(msg, "no supported methods remain"):
		return StateAuthFailed
	case strings.Contains(msg, "subsystem request failed"):
		return StateNoSFTP
	case strings.Contains(msg, "connection reset"), strings.Contains(msg, "EOF") && strings.Contains(msg, "handshake"):
		return StateUnreachable
	}
	return StateError
}

// ClassifyMountLog maps what a failed mount wrote to the journal (the mount unit's lines) onto a
// state: mount.nfs and mount.cifs say what happened only there.
func ClassifyMountLog(text string) string { return shares.ClassifyMountLog(text) }

// ErrStale is a probe that did not answer in time: the share hangs.
var ErrStale = shares.ErrStale
