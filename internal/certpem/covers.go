package certpem

import "strings"

// Covers reports whether a certificate for names (its DNS subject alternative names) is valid
// for host the way a browser checks it: the same name in any case, or a wildcard "*.<rest>"
// standing for exactly one leftmost label. A trailing dot on host is ignored; an IP address or
// an empty host is never covered by a DNS name, and the subject's common name does not count.
func Covers(names []string, host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" || strings.HasPrefix(h, "[") || strings.Trim(h, "0123456789.") == "" || strings.Contains(h, ":") {
		return false
	}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n), "."))
		if n == "" {
			continue
		}
		if n == h {
			return true
		}
		if rest, ok := strings.CutPrefix(n, "*."); ok && rest != "" {
			if label, tail, found := strings.Cut(h, "."); found && label != "" && tail == rest {
				return true
			}
		}
	}
	return false
}
