package system

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// LiveCertPath is the box's TLS certificate and key, one PEM: what lighttpd's ssl.pemfile
// names, what S50lighttpd regenerates when it is missing or about to expire, and what the certs
// group reads (D-46). On every product /etc/config is the userfs.
const LiveCertPath = "/etc/config/server.pem"

// CertInstaller is the box side of the ACME service (task 35, D-48): the live file through the
// privilege helper's enumerated operation, lighttpd's reload, and the restart of every confined
// addon that has the certs group in its drop-in - a broker re-reads the certificate only at a
// start, and a short interruption every sixty days beats serving an expired one.
type CertInstaller struct {
	Root Root
	// Run executes systemctl and the init script; nil = the privilege boundary's exec, main
	// substitutes the dry runner off a real box.
	Run Runner
	// Systemd: reload through systemctl, restart the addon units; otherwise the init scripts.
	Systemd bool
}

// certLog is one install's line sink.
type certLog func(string)

func (l certLog) logf(format string, a ...any) {
	if l != nil {
		l(fmt.Sprintf(format, a...))
	}
}

func (c CertInstaller) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if c.Run != nil {
		return c.Run(ctx, name, args...)
	}
	return run(ctx, name, args...)
}

// Install writes the file (root:certs 0640 through the helper), reloads lighttpd and restarts
// the certs-group addons. Every step after the write is best effort: the file is what matters,
// and a failed restart is in the attempt's lines.
func (c CertInstaller) Install(ctx context.Context, livePEM []byte, mode string, log func(string)) ([]string, error) {
	l := certLog(log)
	if err := Priv.WriteCertificate(c.Root.join(LiveCertPath), livePEM, mode); err != nil {
		return nil, err
	}
	l.logf("wrote %s", LiveCertPath)
	if err := c.reloadLighttpd(ctx, l); err != nil {
		return nil, err
	}
	return c.restartCertsAddons(ctx, l), nil
}

// Remove deletes the markers first - with one in place S50lighttpd's check_certificate would
// leave the file alone - then the file, and reloads lighttpd, whose check_certificate generates
// a self-signed certificate when the file is missing; then the addons are restarted so they
// pick that one up.
func (c CertInstaller) Remove(ctx context.Context, log func(string)) ([]string, error) {
	l := certLog(log)
	for _, suffix := range []string{priv.ManagedSuffix, priv.MarkerSuffix} {
		if err := Priv.Remove(c.Root.join(LiveCertPath + suffix)); err != nil {
			return nil, err
		}
	}
	if err := Priv.Remove(c.Root.join(LiveCertPath)); err != nil {
		return nil, err
	}
	l.logf("removed %s and its markers", LiveCertPath)
	// task 36: HSTS on a self-signed certificate is the one way to lock a browser out - it keeps
	// refusing the certificate for max-age - so the switch back turns it off, in the same reload.
	// Task 96: into the clearing state, not without a header; the page asks the user to switch HSTS
	// off first, since max-age=0 only reaches a browser over the certificate it trusts.
	if off, until, err := c.Root.startHSTSClearing(time.Now()); err != nil {
		return nil, err
	} else if off {
		l.logf("HSTS switched off (max-age=0 until %s): a browser that saw the header would refuse the self-signed certificate", time.Unix(until, 0).UTC().Format(time.RFC3339))
	}
	if err := c.reloadLighttpd(ctx, l); err != nil {
		return nil, err
	}
	return c.restartCertsAddons(ctx, l), nil
}

// ReadLive is the certificate half of the file and the marker's line, through the helper.
func (c CertInstaller) ReadLive() ([]byte, string, error) {
	return Priv.ReadCertificate(c.Root.join(LiveCertPath))
}

// reloadLighttpd is the shared one in https.go: systemctl or the init script.
func (c CertInstaller) reloadLighttpd(ctx context.Context, l certLog) error {
	return reloadLighttpd(ctx, c.Root, c.Run, c.Systemd, l)
}

// CertsGroupAddons lists the addons whose drop-in carries the certs group: every confined
// policy, when the box has the group (renderDropIn adds it to exactly those).
func (r Root) CertsGroupAddons() []string {
	if !r.HasGroup(CertsGroup) {
		return nil
	}
	var ids []string
	for id, p := range r.AddonPolicies() {
		if p.Mode == "confined" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// restartCertsAddons restarts them one by one and returns the ones that were; a failure is
// logged and does not stop the rest.
func (c CertInstaller) restartCertsAddons(ctx context.Context, l certLog) []string {
	if !c.Systemd {
		return nil // the busybox products run addons as root; they read the file either way and are not restarted
	}
	var done []string
	for _, id := range c.Root.CertsGroupAddons() {
		unit := "addon-" + id + ".service"
		if out, err := c.run(ctx, "systemctl", "restart", "--no-pager", "--", unit); err != nil {
			l.logf("restart %s failed: %v %s", unit, err, strings.TrimSpace(string(out)))
			continue
		}
		l.logf("restarted %s (certs group)", unit)
		done = append(done, id)
	}
	return done
}
