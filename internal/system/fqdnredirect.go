package system

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The redirect from the bare host name to the box's full name: https://<host>/ answers 302 to
// https://<host>.<domain>/, so a browser keeps one name - one session cookie, one HSTS entry, one
// saved password. A third marker beside the two of the HTTP → HTTPS redirect and HSTS, in the
// same shape: the file holds the target name, and S50lighttpd writes the include at every start
// and reload. S50lighttpd uses the name only while the live certificate covers it, so the
// redirect never leads into a certificate warning: after a rename it waits for a certificate for
// the new name, and the install's own reload brings it back without occulited.

// FQDNRedirectMarker makes S50lighttpd include the lite overlay's conf.d/fqdnredirect.conf on
// the TLS sockets (and on port 80 together with the HTTP → HTTPS redirect), redirecting the
// bare host name to the name the file holds.
const FQDNRedirectMarker = "/etc/config/fqdnRedirect"

// fqdnRe is S50lighttpd's rule for the marker, word for word.
var fqdnRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidFQDN is the rule S50lighttpd applies to the marker: a lower-case DNS name of two labels or
// more, at most 253 characters. The script ignores anything else, so occulited never writes it.
func ValidFQDN(name string) bool { return len(name) <= 253 && fqdnRe.MatchString(name) }

// FQDN is the redirect's target for a host name and a domain: <host>.<domain> in lower case; ""
// without either, for a host name that already carries a dot, and for a result S50lighttpd would
// not accept.
func FQDN(hostname, domain string) string {
	host := strings.ToLower(strings.TrimSpace(hostname))
	dom := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	if host == "" || dom == "" || strings.Contains(host, ".") {
		return ""
	}
	if n := host + "." + dom; ValidFQDN(n) {
		return n
	}
	return ""
}

// markerName is the name the marker holds as S50lighttpd reads it: the first line in lower case
// without blanks, "" when that is not a valid name.
func markerName(b []byte) string {
	line, _, _ := strings.Cut(string(b), "\n")
	n := strings.ToLower(strings.NewReplacer(" ", "", "\t", "", "\r", "").Replace(line))
	if !ValidFQDN(n) {
		return ""
	}
	return n
}

// FollowFQDN keeps the redirect's target the box's current <host>.<domain>: while the marker
// exists and holds another name, it is rewritten and lighttpd reloaded once. Called where the
// ACME names follow a rename or a new domain - at start, on GET /network, /certificate and /https,
// after a rename and at every renewal check. Nothing happens while the switch is off, or without
// a usable name for the box: at boot resolv.conf may not carry the lease yet, and the target must
// not flip away and back. It answers the name the marker held and whether it was rewritten; a
// failed reload is the error, with the marker already written.
func (h *HTTPSConfig) FollowFQDN(ctx context.Context, hostname, domain string, log func(string)) (previous string, rewritten bool, err error) {
	target := FQDN(hostname, domain)
	if target == "" {
		return "", false, nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.Root.HTTPSSettings()
	if !cur.RedirectFQDN || cur.FQDNTarget == target {
		return cur.FQDNTarget, false, nil
	}
	if err := Priv.WriteFile(h.Root.join(FQDNRedirectMarker), []byte(target+"\n"), 0o644); err != nil {
		return cur.FQDNTarget, false, fmt.Errorf("write %s: %w", FQDNRedirectMarker, err)
	}
	l := certLog(log)
	was := cur.FQDNTarget
	if was == "" {
		was = "no usable name"
	}
	l.logf("the bare host name redirect follows the box's name: %s (was %s)", target, was)
	if err := reloadLighttpd(ctx, h.Root, h.Run, h.Systemd, l); err != nil {
		return cur.FQDNTarget, true, err
	}
	return cur.FQDNTarget, true, nil
}
