package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// SSH is the state of the shell access, as the WebUI's security page shows it: the
// /etc/config/sshEnabled marker (S50sshd starts only with it, libfirewall opens port 22 only
// with it) and whether sshd runs.
type SSH struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	// KeyOnly (task 245): sshd refuses root's password, a key is the only login; absent when the
	// helper could not say
	KeyOnly *bool `json:"key_only,omitempty"`
}

// ReadSSH reads the marker and asks the service manager whether sshd runs.
//
// The pid file is only the fallback (svc == nil, development against a fake root): sshd writes
// /run/sshd.pid with mode 0600, and occulited is unprivileged since task 17 - reading it fails
// and the page then claimed sshd was down while it was serving the very session looking at the
// page (B-10). The service manager answers from systemctl.
func (r Root) ReadSSH(svc ServiceManager) SSH {
	s := SSH{}
	if _, err := os.Stat(r.join("/etc/config/sshEnabled")); err == nil {
		s.Enabled = true
	}
	if svc != nil {
		if list, err := svc.List(); err == nil {
			for _, v := range list {
				if v.ID == "sshd" {
					s.Running = v.Running
					return s
				}
			}
			return s
		}
	}
	if pid := pidFromFile(r.join("/var/run/sshd.pid")); pid > 0 {
		if _, err := os.Stat(r.join(fmt.Sprintf("/proc/%d", pid))); err == nil || string(r) != "/" {
			s.Running = true
		}
	}
	return s
}

// SetSSH writes or removes the marker, lets the firewall follow (port 22's rule follows it) and
// starts or stops sshd - what CCU.setSSH and CCU.restartSSHDaemon do together.
//
// The daemon is switched through the service manager, not by calling the init script: on the
// systemd product a script-level stop leaves systemd believing sshd.service is active, and with
// the unit's Restart=always it brings the daemon straight back (the same class of mistake as
// B-3). svc == nil keeps the old direct call for development and the busybox tests.
func (r Root) SetSSH(ctx context.Context, run Runner, svc ServiceManager, enabled bool) error {
	if run == nil {
		run = ExecRunner
	}
	marker := r.join("/etc/config/sshEnabled")
	if enabled {
		if err := touch(marker, 0o664); err != nil {
			return err
		}
	} else if err := remove(marker); err != nil {
		return err
	}
	// task 157: port 22's owned rule follows the marker
	notifyFirewall(ctx)
	action := "stop"
	if enabled {
		action = "restart"
	}
	if svc != nil {
		if out, err := svc.Control(ctx, "sshd", action); err != nil {
			return fmt.Errorf("sshd %s: %w: %s", action, err, strings.TrimSpace(out))
		}
		return nil
	}
	if out, err := run(ctx, r.join("/etc/init.d/S50sshd"), action); err != nil {
		return fmt.Errorf("sshd %s: %w: %s", action, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PasswordHasher turns a password into its crypt hash - the one step of SetRootPassword that
// sees the plaintext. nil means MkpasswdHasher; tests substitute a fake that records what it was
// handed.
//
// It is its own type and not a Runner on purpose (openccu-lite task 302, GitHub #2): a Runner
// carries no stdin, so the password could never have reached `mkpasswd -s` through one, and the
// production Runner is the privilege helper, whose program list rightly has no mkpasswd. The
// page answered "refused by the privilege helper: program mkpasswd" on every system; had the
// helper allowed the program, the hash of an empty input would have been set and the page would
// have reported success.
type PasswordHasher func(ctx context.Context, password string) (string, error)

// MkpasswdHasher runs `mkpasswd -m sha512 -s` in the daemon's own process, as its own
// unprivileged user, with the password on stdin and never in an argument. Hashing needs no
// privilege, so it never goes through the helper; the unit's sandbox lets the busybox applet
// run (task 208 measured it).
func MkpasswdHasher(ctx context.Context, password string) (string, error) {
	res, err := priv.Local{}.Run(ctx, "mkpasswd", []string{"-m", "sha512", "-s"}, []byte(password+"\n"))
	if err != nil {
		return "", fmt.Errorf("mkpasswd: %w", err)
	}
	if res.Exit != 0 {
		return "", fmt.Errorf("mkpasswd: exit %d: %s", res.Exit, strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// SetRootPassword sets root's password for SSH the way CCU.setSSHPassword does: a sha512 crypt
// from the hasher (mkpasswd, by default), written into /etc/config/shadow's root line.
//
// The write is the privilege helper's own operation (B-15). occulited runs as occulite and the
// file is 0640 root:root, so reading it to replace the root line failed and the page could not
// work at all; the alternative was putting shadow on the helper's read list, which would have
// carried every hash on the box through the unprivileged process. Instead the helper is handed
// one hash and rewrites one field of one line as root - the file never crosses the boundary in
// either direction. A hasher that fails or answers nothing usable sets nothing: the old password
// stays, rather than one nobody knows.
func (r Root) SetRootPassword(ctx context.Context, hash PasswordHasher, password string) error {
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	if strings.ContainsAny(password, "\n\r") {
		return errors.New("password must not contain line breaks")
	}
	if hash == nil {
		hash = MkpasswdHasher
	}
	digest, err := hash(ctx, password)
	if err != nil {
		return err
	}
	digest = strings.TrimSpace(digest)
	if digest == "" || strings.ContainsAny(digest, ":\n") {
		return errors.New("mkpasswd produced no usable hash")
	}
	return Priv.SetRootPasswordHash(r.join("/etc/config/shadow"), digest)
}
