package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Task 36: the HTTP → HTTPS redirect and HSTS. Both are markers on the userfs that S50lighttpd
// reads at start and at every reload - the redirect one is upstream's (the CCU WebUI's
// "Systemsteuerung → Sicherheit → HTTPS-Weiterleitung"), the HSTS one is openccu-lite's and
// carries the max-age in seconds. The markers *are* the settings: nothing is kept a second
// time in the state directory, so what the page shows is what lighttpd was last told.
const (
	// HTTPSRedirectMarker makes S50lighttpd include conf.d/httpsredirect.conf on the :80 sockets.
	HTTPSRedirectMarker = "/etc/config/httpsRedirectEnabled"
	// HSTSMarker makes S50lighttpd include the lite overlay's conf.d/hsts.conf on the :443
	// sockets, with the max-age the file holds (seconds, digits only). A marker holding 0 is
	// the clearing state (task 96, D-64): HSTS is off, but the box sends max-age=0 so that a
	// browser that visits forgets the entry it has - removing the header clears nothing.
	HSTSMarker = "/etc/config/hstsEnabled"
	// HSTSClearUntilFile holds the Unix time the clearing ends, beside a marker holding 0. It is a
	// file of its own and not a second number in the marker: S50lighttpd has always read the marker
	// as digits only, so an older script would glue "0 <epoch>" into a max-age of years, while a
	// plain 0 is harmless to every version. S50lighttpd sends no header once the time has passed;
	// occulited removes both files at its start, on GET /https and at every renewal check.
	HSTSClearUntilFile = "/etc/config/hstsClearUntil"
	// DefaultHSTSMaxAgeDays is a week (D-64, from task 71): every visit renews the entry, so a box
	// opened at least weekly keeps its protection, and a lockout after a way back or a reset ends
	// a week after the last visit instead of a year.
	DefaultHSTSMaxAgeDays = 7
	// MaxHSTSClearingDays caps how long the box sends max-age=0: the previous max-age, at most this.
	MaxHSTSClearingDays = 30
	// MaxHSTSMaxAgeDays is two years: the longest the preload list asks for, and long enough.
	// A browser keeps refusing an untrusted certificate for exactly this long once it has
	// seen the header, which is the one way a box can lock its own user out (see the guard in
	// the API), so the ceiling is deliberately not higher.
	MaxHSTSMaxAgeDays = 730
)

// ErrHTTPSInvalid is a settings value the box does not accept; the message says which.
var ErrHTTPSInvalid = errors.New("invalid HTTPS settings")

// HTTPSSettings is GET/PUT /https.
type HTTPSSettings struct {
	// RedirectHTTPS: every request on :80 that is not the ACME challenge path and not from the
	// loopback is answered with a redirect to https://.
	RedirectHTTPS bool `json:"redirect_https"`
	// HSTS: the Strict-Transport-Security header on the TLS sockets, with HSTSMaxAgeDays.
	HSTS           bool `json:"hsts"`
	HSTSMaxAgeDays int  `json:"hsts_max_age_days"`
	// HSTSClearing: HSTS is off and the marker holds 0 - lighttpd sends max-age=0 until
	// HSTSClearUntil (task 96). Never together with HSTS.
	HSTSClearing bool `json:"hsts_clearing"`
	// HSTSClearUntil is the Unix time the clearing ends; 0 when the deadline cannot be read (a
	// marker holding 0 written by hand), which S50lighttpd treats as "until switched".
	HSTSClearUntil int64 `json:"-"`
	// RedirectFQDN: the marker FQDNRedirectMarker exists - GET and HEAD for the bare host name
	// are redirected to FQDNTarget while the live certificate covers it (fqdnredirect.go).
	RedirectFQDN bool `json:"redirect_fqdn"`
	// FQDNTarget is the name the marker holds, lower case; "" while off or when the marker
	// holds nothing S50lighttpd accepts. The API reports the target in a field of its own.
	FQDNTarget string `json:"-"`
}

// HTTPSSettings reads the two markers. A missing HSTS marker reads as off with the default
// max-age; a marker holding 0 as off and clearing, with the deadline beside it; a marker without
// a usable number (edited by hand) reads as on with the default, which is what S50lighttpd does
// with it too.
func (r Root) HTTPSSettings() HTTPSSettings {
	s := HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays}
	if _, err := os.Stat(r.join(HTTPSRedirectMarker)); err == nil {
		s.RedirectHTTPS = true
	}
	if b, err := os.ReadFile(r.join(FQDNRedirectMarker)); err == nil {
		s.RedirectFQDN = true
		s.FQDNTarget = markerName(b)
	}
	b, err := os.ReadFile(r.join(HSTSMarker))
	if err != nil {
		return s
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && secs == 0 {
		s.HSTSClearing = true
		if u, err := os.ReadFile(r.join(HSTSClearUntilFile)); err == nil {
			if until, err := strconv.ParseInt(strings.TrimSpace(string(u)), 10, 64); err == nil && until > 0 {
				s.HSTSClearUntil = until
			}
		}
		return s
	}
	s.HSTS = true
	if secs, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && secs > 0 {
		// whole days; a hand-written value between two days rounds down, never to zero
		if d := secs / 86400; d >= 1 {
			s.HSTSMaxAgeDays = d
		}
	}
	return s
}

// Validate is the rule for a PUT: a max-age between one day and MaxHSTSMaxAgeDays when HSTS
// is on; the value is not checked while HSTS is off, since it is not written then.
func (s HTTPSSettings) Validate() error {
	if s.HSTS && (s.HSTSMaxAgeDays < 1 || s.HSTSMaxAgeDays > MaxHSTSMaxAgeDays) {
		return fmt.Errorf("%w: hsts_max_age_days must be between 1 and %d", ErrHTTPSInvalid, MaxHSTSMaxAgeDays)
	}
	return nil
}

// HTTPSConfig applies the settings: the markers through the privilege helper (both under its
// /etc/config/ prefix, no operation of their own), then one lighttpd reload, which is where
// S50lighttpd turns the markers into the include files.
type HTTPSConfig struct {
	Root Root
	// Run executes systemctl or the init script; nil = the privilege boundary's exec.
	Run Runner
	// Systemd: reload through systemctl; otherwise the init script.
	Systemd bool
	// Now is the clock for the clearing deadline; nil = time.Now.
	Now func() time.Time

	// mu keeps a PUT and FollowFQDN (a page's poll, the renewal timer) from writing the markers
	// and reloading at the same time.
	mu sync.Mutex
}

// Set writes the markers and reloads lighttpd; the answer is what the markers say afterwards.
// Nothing is reloaded when nothing changed. The FQDN marker is written only when the switch or
// its target changes, so a marker that holds a name the box cannot use now stays as it is while
// the other switches are saved.
func (h *HTTPSConfig) Set(ctx context.Context, s HTTPSSettings, log func(string)) (HTTPSSettings, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := s.Validate(); err != nil {
		return h.Root.HTTPSSettings(), err
	}
	l := certLog(log)
	before := h.Root.HTTPSSettings()
	s.HSTSClearing, s.HSTSClearUntil = false, 0
	if !s.HSTS {
		s.HSTSMaxAgeDays = DefaultHSTSMaxAgeDays // the field is not stored while off
		// off after on clears (task 96): max-age=0 for the previous max-age, at most 30 days; a
		// clearing that runs keeps its deadline
		switch {
		case before.HSTS:
			s.HSTSClearing, s.HSTSClearUntil = true, clearingDeadline(h.now(), before.HSTSMaxAgeDays)
		case before.HSTSClearing:
			s.HSTSClearing, s.HSTSClearUntil = true, before.HSTSClearUntil
		}
	}
	if !s.RedirectFQDN {
		s.FQDNTarget = ""
	}
	fqdnChanged := s.RedirectFQDN != before.RedirectFQDN || s.FQDNTarget != before.FQDNTarget
	if fqdnChanged && s.RedirectFQDN && !ValidFQDN(s.FQDNTarget) {
		return before, fmt.Errorf("%w: the redirect to the full name needs a DNS name of the form <host>.<domain>, not %q", ErrHTTPSInvalid, s.FQDNTarget)
	}
	if s == before {
		return before, nil
	}
	if fqdnChanged {
		if s.RedirectFQDN {
			if err := Priv.WriteFile(h.Root.join(FQDNRedirectMarker), []byte(s.FQDNTarget+"\n"), 0o644); err != nil {
				return before, fmt.Errorf("write %s: %w", FQDNRedirectMarker, err)
			}
		} else if err := Priv.Remove(h.Root.join(FQDNRedirectMarker)); err != nil {
			return before, fmt.Errorf("remove %s: %w", FQDNRedirectMarker, err)
		}
	}
	if s.RedirectHTTPS {
		if err := Priv.Touch(h.Root.join(HTTPSRedirectMarker), 0o644); err != nil {
			return before, fmt.Errorf("write %s: %w", HTTPSRedirectMarker, err)
		}
	} else if err := Priv.Remove(h.Root.join(HTTPSRedirectMarker)); err != nil {
		return before, fmt.Errorf("remove %s: %w", HTTPSRedirectMarker, err)
	}
	switch {
	case s.HSTS:
		secs := strconv.Itoa(s.HSTSMaxAgeDays * 86400)
		if err := Priv.WriteFile(h.Root.join(HSTSMarker), []byte(secs+"\n"), 0o644); err != nil {
			return before, fmt.Errorf("write %s: %w", HSTSMarker, err)
		}
		if err := Priv.Remove(h.Root.join(HSTSClearUntilFile)); err != nil {
			return before, fmt.Errorf("remove %s: %w", HSTSClearUntilFile, err)
		}
	case s.HSTSClearing && !before.HSTSClearing:
		if err := h.Root.writeHSTSClearing(s.HSTSClearUntil); err != nil {
			return before, err
		}
	case s.HSTSClearing:
		// the clearing runs on as it is
	default:
		if err := h.Root.removeHSTSMarkers(); err != nil {
			return before, err
		}
	}
	fqdn := "off"
	if s.RedirectFQDN {
		fqdn = "to " + s.FQDNTarget
	}
	hsts := fmt.Sprintf("on (max-age %d days)", s.HSTSMaxAgeDays)
	switch {
	case s.HSTSClearing && s.HSTSClearUntil > 0:
		hsts = "off, max-age=0 until " + time.Unix(s.HSTSClearUntil, 0).UTC().Format(time.RFC3339)
	case s.HSTSClearing:
		hsts = "off, max-age=0"
	case !s.HSTS:
		hsts = "off"
	}
	l.logf("https: redirect %s, hsts %s, bare host name redirect %s", onOff(s.RedirectHTTPS), hsts, fqdn)
	if err := reloadLighttpd(ctx, h.Root, h.Run, h.Systemd, l); err != nil {
		return h.Root.HTTPSSettings(), err
	}
	return h.Root.HTTPSSettings(), nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (h *HTTPSConfig) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// clearingDeadline: the previous max-age from now, at most MaxHSTSClearingDays - a browser that
// last saw the header just before the switch holds its entry exactly that long.
func clearingDeadline(now time.Time, maxAgeDays int) int64 {
	days := min(max(maxAgeDays, 1), MaxHSTSClearingDays)
	return now.Add(time.Duration(days) * 24 * time.Hour).Unix()
}

// writeHSTSClearing writes the deadline first and then the marker's 0, so a failure in between
// leaves HSTS as it was (the deadline file is read only beside a 0).
func (r Root) writeHSTSClearing(until int64) error {
	if err := Priv.WriteFile(r.join(HSTSClearUntilFile), []byte(strconv.FormatInt(until, 10)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", HSTSClearUntilFile, err)
	}
	if err := Priv.WriteFile(r.join(HSTSMarker), []byte("0\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", HSTSMarker, err)
	}
	return nil
}

// removeHSTSMarkers: HSTS off without a header - the marker first, then the deadline beside it.
func (r Root) removeHSTSMarkers() error {
	if err := Priv.Remove(r.join(HSTSMarker)); err != nil {
		return fmt.Errorf("remove %s: %w", HSTSMarker, err)
	}
	if err := Priv.Remove(r.join(HSTSClearUntilFile)); err != nil {
		return fmt.Errorf("remove %s: %w", HSTSClearUntilFile, err)
	}
	return nil
}

// startHSTSClearing turns HSTS that is on into the clearing state without a reload - for the switch
// back to a self-signed certificate, whose own reload picks it up, and for ClearHSTS. True when
// HSTS was on; a clearing that runs, or HSTS off, is left as it is.
func (r Root) startHSTSClearing(now time.Time) (bool, int64, error) {
	cur := r.HTTPSSettings()
	if !cur.HSTS {
		return false, cur.HSTSClearUntil, nil
	}
	until := clearingDeadline(now, cur.HSTSMaxAgeDays)
	if err := r.writeHSTSClearing(until); err != nil {
		return false, 0, err
	}
	return true, until, nil
}

// ClearHSTS switches HSTS that is on into the clearing state and reloads lighttpd - before the
// way back to OpenCCU is staged (task 96): OpenCCU sends no HSTS and later serves a self-signed
// certificate, so max-age=0 has to reach the browsers while this box still answers with its
// trusted one. It answers whether it switched and the deadline of the clearing now in force (0
// when none runs); HSTS off, or a clearing already running, is left alone without a reload.
func (h *HTTPSConfig) ClearHSTS(ctx context.Context, log func(string)) (bool, int64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := certLog(log)
	cleared, until, err := h.Root.startHSTSClearing(h.now())
	if err != nil || !cleared {
		return false, until, err
	}
	l.logf("HSTS switched off: max-age=0 until %s, so browsers that visit forget it", time.Unix(until, 0).UTC().Format(time.RFC3339))
	return true, until, reloadLighttpd(ctx, h.Root, h.Run, h.Systemd, l)
}

// ExpireHSTSClearing ends a clearing whose deadline has passed: both files go and lighttpd is
// reloaded, so the header stops. S50lighttpd already sends nothing after the deadline at a start
// or reload of its own; this makes the running lighttpd follow. True when it did something.
func (h *HTTPSConfig) ExpireHSTSClearing(ctx context.Context, log func(string)) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.Root.HTTPSSettings()
	if !cur.HSTSClearing || cur.HSTSClearUntil == 0 || h.now().Unix() < cur.HSTSClearUntil {
		return false, nil
	}
	l := certLog(log)
	if err := h.Root.removeHSTSMarkers(); err != nil {
		return false, err
	}
	l.logf("the HSTS clearing ended (max-age=0 was sent until %s): no header from now on", time.Unix(cur.HSTSClearUntil, 0).UTC().Format(time.RFC3339))
	return true, reloadLighttpd(ctx, h.Root, h.Run, h.Systemd, l)
}

// reloadLighttpd: `systemctl reload lighttpd` (the unit's ExecReload runs S50lighttpd.script
// reload, then lite-cert-perms, then USR1 to the angel) or the init script's reload.
func reloadLighttpd(ctx context.Context, root Root, runner Runner, systemd bool, l certLog) error {
	if runner == nil {
		runner = run
	}
	var out []byte
	var err error
	if systemd {
		out, err = runner(ctx, "systemctl", "reload", "--no-pager", "--", "lighttpd.service")
	} else {
		out, err = runner(ctx, root.join("/etc/init.d/S50lighttpd"), "reload")
	}
	if err != nil {
		return fmt.Errorf("lighttpd reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	l.logf("lighttpd reloaded")
	return nil
}
