package acme

import (
	"regexp"
	"sort"
)

// Task 102: an attempt's lines go to the journal, and the journal can be forwarded to a remote
// syslog server (B-96). No secret may reach it: the EAB HMAC, a DNS provider's secret fields, a
// private key. Redactor is applied to every line - the service's own, lego's, the installer's -
// before it is written, and to the error the service logs at the end of a run.

const redacted = "[redacted]"

// minSecret is the shortest stored value replaced literally: a one- or two-character "secret"
// would blank out ordinary words of every line.
const minSecret = 4

// privateKeyRe is a PEM private key block, or its unterminated start (a cut line).
var privateKeyRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(-----END [A-Z0-9 ]*PRIVATE KEY-----|$)`)

// assignRe is a secret-looking assignment in a URL, a header or an error text: token=...,
// "api_password": "...", Authorization: ... - the name kept, the value replaced.
var assignRe = regexp.MustCompile(`(?i)\b(token|auth_token|api_token|apitoken|api_key|apikey|api-key|api_password|apipassword|password|passwd|secret|client_secret|hmac|hmac_key|eab_hmac|apisessionid|authorization)("?\s*[:=]\s*"?)(bearer\s+)?([^\s"'&,;]{2,})`)

// bearerRe is a bearer credential anywhere.
var bearerRe = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`)

// secretKeys are the DNS provider fields marked secret, of every provider: a value stored under
// such a key is never written, whichever provider is chosen now.
func secretKeys() map[string]bool {
	keys := map[string]bool{}
	for _, p := range Providers {
		for _, f := range p.Fields {
			if f.Secret {
				keys[f.Key] = true
			}
		}
	}
	return keys
}

// Redactor is the line filter for a run with these settings.
func Redactor(set Settings) func(string) string {
	var secrets []string
	add := func(v string) {
		if len(v) >= minSecret {
			secrets = append(secrets, v)
		}
	}
	add(set.EABHMAC)
	keys := secretKeys()
	for k, v := range set.DNSCredentials {
		if keys[k] {
			add(v)
		}
	}
	// the longest first, so a secret that contains another is replaced whole
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return func(s string) string { return redact(s, secrets) }
}

func redact(s string, secrets []string) string {
	for _, v := range secrets {
		s = replaceAll(s, v, redacted)
	}
	s = privateKeyRe.ReplaceAllString(s, "[private key redacted]")
	s = bearerRe.ReplaceAllString(s, "Bearer "+redacted)
	s = assignRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := assignRe.FindStringSubmatch(m)
		if sub[4] == redacted || sub[4] == "[redacted" {
			return m
		}
		return sub[1] + sub[2] + sub[3] + redacted
	})
	return s
}

func replaceAll(s, old, repl string) string {
	if old == "" {
		return s
	}
	out := make([]byte, 0, len(s))
	for {
		i := indexOf(s, old)
		if i < 0 {
			return string(append(out, s...))
		}
		out = append(out, s[:i]...)
		out = append(out, repl...)
		s = s[i+len(old):]
	}
}

func indexOf(s, sub string) int {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}
