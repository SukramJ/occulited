package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The HTTPS routes (task 36): the HTTP → HTTPS redirect and HSTS, two markers on the userfs
// that S50lighttpd reads at every reload. The page is System → Certificate (task 132); the guard rail is
// here as well as there, because it is the API that would let a script lock the box's user out.

// httpsView is GET /https and the answer of PUT: the settings, and what the live certificate
// is - the page decides from it what to offer.
type httpsView struct {
	system.HTTPSSettings
	// ClearingUntil is when the clearing ends (task 96): until then lighttpd sends max-age=0.
	// Absent while HSTS is on or off without a header, and for a clearing without a deadline.
	ClearingUntil *time.Time `json:"hsts_clearing_until,omitempty"`
	// Certificate is nil when the certificate service is not on this box or the live file
	// cannot be read; then HSTS cannot be switched on.
	Certificate *httpsCert `json:"certificate"`
	fqdnView
}

type httpsCert struct {
	SelfSigned bool `json:"self_signed"`
	// Managed: occulited installed it (ACME or by hand through the Certificate page).
	Managed bool `json:"managed"`
	// Mode is the certificate settings' mode: self-signed, acme or manual.
	Mode string `json:"mode"`
}

// liveCertificate reads the live certificate once: its description for the page's HSTS rules,
// and its names for the FQDN redirect's guard. Both nil when there is none to read.
func (a *SystemAPI) liveCertificate() (*certpem.Info, *httpsCert) {
	if a.Cert == nil {
		return nil, nil
	}
	st := a.Cert.Status()
	if st.Current == nil {
		return nil, nil
	}
	return st.Current, &httpsCert{SelfSigned: st.Current.SelfSigned, Managed: st.Managed, Mode: st.Settings.Mode}
}

func (a *SystemAPI) httpsViewOf(s system.HTTPSSettings) httpsView {
	info, cert := a.liveCertificate()
	return httpsView{HTTPSSettings: s, ClearingUntil: clearingUntil(s), Certificate: cert, fqdnView: fqdnState(s, a.Root.Hostname(), a.Root.Domain(), info)}
}

// clearingUntil is the clearing's deadline as a time for the JSON answers; nil without one.
func clearingUntil(s system.HTTPSSettings) *time.Time {
	if !s.HSTSClearing || s.HSTSClearUntil == 0 {
		return nil
	}
	t := time.Unix(s.HSTSClearUntil, 0).UTC()
	return &t
}

// expireHSTSClearing ends a clearing whose deadline has passed before an answer reports it.
func (a *SystemAPI) expireHSTSClearing(ctx context.Context) {
	if a.HTTPS == nil {
		return
	}
	if _, err := a.HTTPS.ExpireHSTSClearing(ctx, func(l string) { slog.Info("https: " + l) }); err != nil {
		slog.Warn("https: the HSTS clearing could not be ended", "err", err)
	}
}

func (a *SystemAPI) httpsView() httpsView {
	return a.httpsViewOf(a.Root.HTTPSSettings())
}

func (a *SystemAPI) httpsUnavailable(w http.ResponseWriter) bool {
	if a.HTTPS == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "the HTTPS settings are not available on this system"})
		return true
	}
	return false
}

func (a *SystemAPI) https(w http.ResponseWriter, r *http.Request) {
	if a.httpsUnavailable(w) {
		return
	}
	// a rename or a new domain the redirect's target has not followed yet is applied first, and a
	// clearing whose deadline has passed is ended
	a.followFQDN(a.Root.Hostname(), a.Root.Domain())
	a.expireHSTSClearing(r.Context())
	writeJSON(w, 200, a.httpsView())
}

// httpsPutBody is PUT /https. redirect_fqdn is optional: absent leaves the redirect from the bare
// host name as it is, so a script written for the two older switches does not turn it off.
// hsts_max_age_days is optional too: absent or null is the default of seven days (task 96).
type httpsPutBody struct {
	RedirectHTTPS  bool  `json:"redirect_https"`
	HSTS           bool  `json:"hsts"`
	HSTSMaxAgeDays *int  `json:"hsts_max_age_days"`
	RedirectFQDN   *bool `json:"redirect_fqdn"`
}

// httpsPut writes the markers and reloads lighttpd. HSTS is refused while the box serves a
// self-signed certificate, or none that can be read: a browser that has seen the header
// refuses an untrusted certificate for the whole max-age, and that is the one way a box can
// lock its own user out. Not merely a page rule - a PUT is what a script sends. The redirect to
// the full name is refused, when it is switched on, without a domain or a certificate that covers
// the name: it would lead straight into a certificate warning.
func (a *SystemAPI) httpsPut(w http.ResponseWriter, r *http.Request) {
	if a.httpsUnavailable(w) {
		return
	}
	var b httpsPutBody
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	s := system.HTTPSSettings{RedirectHTTPS: b.RedirectHTTPS, HSTS: b.HSTS, HSTSMaxAgeDays: system.DefaultHSTSMaxAgeDays}
	if b.HSTSMaxAgeDays != nil {
		s.HSTSMaxAgeDays = *b.HSTSMaxAgeDays
	}
	info, c := a.liveCertificate()
	if s.HSTS {
		switch {
		case c == nil:
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "HSTS needs a certificate the browser trusts, and no certificate could be read on this system"})
			return
		case c.SelfSigned:
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "HSTS on a self-signed certificate locks the browser out: once it has seen the header it refuses the certificate for the whole max-age. Install a certificate from a CA first"})
			return
		}
	}
	before := a.Root.HTTPSSettings()
	s.RedirectFQDN = before.RedirectFQDN
	if b.RedirectFQDN != nil {
		s.RedirectFQDN = *b.RedirectFQDN
	}
	if s.RedirectFQDN {
		hostname, domain := a.Root.Hostname(), a.Root.Domain()
		if !before.RedirectFQDN {
			if st := fqdnState(system.HTTPSSettings{}, hostname, domain, info); st.Reason != "" {
				writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: fqdnRefusal(st, hostname, domain), Detail: map[string]any{"reason": st.Reason}})
				return
			} else {
				s.FQDNTarget = *st.Target
			}
		} else {
			// on stays on: the box's current name, or the marker's while the box has none
			s.FQDNTarget = before.FQDNTarget
			if t := system.FQDN(hostname, domain); t != "" {
				s.FQDNTarget = t
			}
		}
	}
	got, err := a.HTTPS.Set(r.Context(), s, func(l string) { reqLog(r).Info("https: " + l) })
	if err != nil {
		if errors.Is(err, system.ErrHTTPSInvalid) {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, a.httpsViewOf(got))
}

// ---- the redirect from the bare host name to <host>.<domain> -----------------------------------

// The states of the redirect to the full name in GET /https.
const (
	FQDNActive      = "active"      // on, and the live certificate covers the target
	FQDNSuspended   = "suspended"   // on, but lighttpd does not redirect: the reason
	FQDNUnavailable = "unavailable" // off, and it cannot be switched on: the reason
	FQDNOff         = "off"         // off, and it could be switched on
)

// Why the redirect is suspended or unavailable.
const (
	FQDNNoDomain      = "no-domain"      // resolv.conf has neither a domain nor a search entry
	FQDNInvalidName   = "invalid-name"   // host name and domain do not make a usable DNS name
	FQDNNoCertificate = "no-certificate" // no live certificate could be read
	FQDNNotCovered    = "not-covered"    // the live certificate does not name the target
)

// fqdnView is the redirect to the full name in GET /https, beside redirect_fqdn.
type fqdnView struct {
	// Host is the bare name that is redirected: the target's first label, else the box's host name.
	Host string `json:"redirect_fqdn_host"`
	// Target is <host>.<domain>: the marker's name while the switch is on, the box's current name
	// otherwise; nil when there is none.
	Target *string `json:"redirect_fqdn_target"`
	State  string  `json:"redirect_fqdn_state"`
	Reason string  `json:"redirect_fqdn_reason,omitempty"`
}

// fqdnState is the rule S50lighttpd applies, seen from here: the target must be a usable name and
// the live certificate must cover it (certpem.Covers; the script asks openssl's -checkhost).
func fqdnState(s system.HTTPSSettings, hostname, domain string, current *certpem.Info) fqdnView {
	v := fqdnView{Host: strings.ToLower(strings.TrimSpace(hostname))}
	target := system.FQDN(hostname, domain)
	if s.RedirectFQDN && s.FQDNTarget != "" {
		target = s.FQDNTarget // what lighttpd was told
	}
	switch {
	case target == "" && strings.Trim(strings.TrimSpace(domain), ".") == "":
		v.Reason = FQDNNoDomain
	case target == "":
		v.Reason = FQDNInvalidName
	case current == nil:
		v.Reason = FQDNNoCertificate
	case !certpem.Covers(current.Names, target):
		v.Reason = FQDNNotCovered
	}
	if target != "" {
		v.Target = &target
		v.Host, _, _ = strings.Cut(target, ".")
	}
	switch {
	case s.RedirectFQDN && v.Reason == "":
		v.State = FQDNActive
	case s.RedirectFQDN:
		v.State = FQDNSuspended
	case v.Reason != "":
		v.State = FQDNUnavailable
	default:
		v.State = FQDNOff
	}
	return v
}

// fqdnRefusal is the 422's message for a reason.
func fqdnRefusal(v fqdnView, hostname, domain string) string {
	target := ""
	if v.Target != nil {
		target = *v.Target
	}
	switch v.Reason {
	case FQDNNoDomain:
		return "the redirect to <host>.<domain> needs the system's DNS domain, and none is known: /etc/resolv.conf has neither a domain nor a search entry"
	case FQDNInvalidName:
		return "the host name " + hostname + " and the domain " + domain + " do not make a DNS name the redirect can use"
	case FQDNNoCertificate:
		return "the redirect needs a certificate that covers " + target + ", and no certificate could be read on this system"
	default:
		return "the certificate does not cover " + target + ": the redirect would lead straight into a certificate warning. Install a certificate for that name first"
	}
}

// followFQDN keeps the redirect's target the box's <host>.<domain> (system.HTTPSConfig.FollowFQDN)
// and answers the name the marker held and whether it was rewritten.
func (a *SystemAPI) followFQDN(hostname, domain string) (string, bool) {
	if a.HTTPS == nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	prev, rewritten, err := a.HTTPS.FollowFQDN(ctx, hostname, domain, func(l string) { slog.Info("https: " + l) })
	if err != nil {
		slog.Warn("https: the bare host name redirect could not follow the box's name", "err", err)
	}
	return prev, rewritten
}

// fqdnRename is the rename answer's fqdn_redirect: where the redirect to the full name stands after
// the rename, for the notice after it.
type fqdnRename struct {
	On     bool    `json:"on"`
	State  string  `json:"state"`
	Target *string `json:"target"`
	// Previous is the name the redirect led to before, when the rename rewrote it.
	Previous string `json:"previous,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// followFQDNRename applies a rename to the redirect's target; nil when the HTTPS settings are not
// on the box. Called after the ACME names followed, with the new name already saved.
func (a *SystemAPI) followFQDNRename(newHost string) *fqdnRename {
	if a.HTTPS == nil {
		return nil
	}
	prev, rewritten := a.followFQDN(newHost, a.Root.Domain())
	v := a.httpsView()
	out := &fqdnRename{On: v.RedirectFQDN, State: v.State, Target: v.Target, Reason: v.Reason}
	if rewritten {
		out.Previous = prev
	}
	return out
}
