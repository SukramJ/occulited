package system

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"time"
)

// Power is what the top bar's power menu does beyond the plain reboot - which stays the addon
// manager's (AddonScripts.Reboot, shared with the addon installer): halting the box, and the
// preparation of a boot into the recovery system.
type Power struct {
	Root    Root
	Systemd bool
	// Run executes the halt command; nil = through the privilege boundary.
	Run Runner
	// Later runs the action once the route has answered; nil = after PowerDelay.
	Later func(func())
}

// PowerDelay is how long a halt or a reboot waits after its route answered, so the answer reaches
// the browser instead of a dropped connection.
var PowerDelay = time.Second

var (
	// ErrUpdateStaged refuses a boot into the recovery system while a system update is staged: the
	// recovery would install it, and nobody asked for that.
	ErrUpdateStaged = errors.New("a system update is staged and the recovery system would install it: install or discard it first")
	// ErrNoRecovery: a container has no recovery system.
	ErrNoRecovery = errors.New("this system has no recovery system")
)

// HaltCommand is what a halt runs: `systemctl poweroff` on systemd, busybox's poweroff otherwise.
func (p Power) HaltCommand() (string, []string, error) {
	if p.Systemd {
		return "systemctl", []string{"poweroff"}, nil
	}
	for _, c := range []string{"/sbin/poweroff", "/bin/poweroff"} {
		if _, err := os.Stat(p.Root.join(c)); err == nil {
			return p.Root.join(c), nil, nil
		}
	}
	return "", nil, errors.New("no poweroff command on this system")
}

// Halt checks that the box can be powered off and powers it off once the caller has answered.
func (p Power) Halt() error {
	name, args, err := p.HaltCommand()
	if err != nil {
		return err
	}
	run := p.Run
	if run == nil {
		run = ExecRunner
	}
	p.later(func() {
		slog.Warn("power: halting the box")
		if out, err := run(context.Background(), name, args...); err != nil {
			slog.Error("power: halt failed", "cmd", name, "err", err, "output", string(out))
		}
	})
	return nil
}

func (p Power) later(f func()) {
	if p.Later != nil {
		p.Later(f)
		return
	}
	time.AfterFunc(PowerDelay, f)
}

// UpdateStaged reports whether /usr/local/.firmwareUpdate exists - as a link, dangling or not: the
// recovery system reads it and installs what it names.
func (r Root) UpdateStaged() bool {
	_, err := os.Lstat(r.join(updateLink))
	return err == nil
}

// ArmRecovery sets /usr/local/.recoveryMode without an update, so the next boot is the recovery
// system's - for repairs and for flashing an image from its own page. Refused on a container,
// which has none, and while an update is staged, which the recovery would install.
func (r Root) ArmRecovery() error {
	if r.Container() != "" {
		return ErrNoRecovery
	}
	if r.UpdateStaged() {
		return ErrUpdateStaged
	}
	return touch(r.join(recoveryFlag), 0o644)
}

// DisarmRecovery takes the marker away again: the reboot it was set for did not start.
func (r Root) DisarmRecovery() error { return remove(r.join(recoveryFlag)) }
