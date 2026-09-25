package acme

import (
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

// task 102: no secret of the settings reaches a run's lines - for every field every provider marks
// secret, in the shapes an error text or a URL carries it; a non-secret field stays readable.
func TestRedactorEveryProviderField(t *testing.T) {
	checked := 0
	for _, p := range Providers {
		for _, f := range p.Fields {
			value := "S3cr3t-" + p.ID + "-" + f.Key + "-9f8e7d6c"
			r := Redactor(Settings{Challenge: ChallengeDNS01, DNSProvider: p.ID, DNSCredentials: map[string]string{f.Key: value}})
			shapes := []string{
				"plain " + value + " end",
				"order: GET https://api.example.net/update?domains=box&token=" + value + "&txt=x: 401",
				`request body {"` + f.Key + `":"` + value + `","other":1}`,
			}
			for _, msg := range shapes {
				got := r(msg)
				if f.Secret && strings.Contains(got, value) {
					t.Errorf("%s %s: the secret is in %q", p.ID, f.Key, got)
				}
			}
			if !f.Secret && !strings.Contains(r(shapes[0]), value) {
				t.Errorf("%s %s is not secret and was redacted: %q", p.ID, f.Key, r(shapes[0]))
			}
			if f.Secret {
				checked++
			}
		}
	}
	// cloudflare, hetzner, netcup (2), duckdns: a provider added with a secret field is checked too
	if checked < 5 {
		t.Errorf("only %d secret fields checked", checked)
	}
}

func TestRedactorEABKeysAndShapes(t *testing.T) {
	cert, key := testcert.SelfSigned([]string{"box"}, nil, time.Now().Add(365*24*time.Hour))
	set := Settings{EABKID: "kid-visible", EABHMAC: "cGxlYXNlLWRvLW5vdC1sb2ctbWU", Challenge: ChallengeHTTP01,
		// a stored value under a key that is secret in another provider (left from a switch)
		DNSCredentials: map[string]string{"api_token": "left-over-token-1234", "customer": "12345"}}
	r := Redactor(set)
	for _, c := range []struct{ in, gone, stays string }{
		{"registering with external account binding (kid kid-visible) hmac cGxlYXNlLWRvLW5vdC1sb2ctbWU", "cGxlYXNlLWRvLW5vdC1sb2ctbWU", "kid-visible"},
		{"stale left-over-token-1234 and customer 12345", "left-over-token-1234", "12345"},
		{"the key:\n" + string(key) + "\nthe cert:\n" + string(cert), "PRIVATE KEY-----\nM", "BEGIN CERTIFICATE"},
		{"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEI (a cut line)", "MHcCAQEEI", "[private key redacted]"},
		{"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.e30.abcdef", "eyJhbGciOiJIUzI1NiJ9", "Authorization"},
		{`netcup: {"apikey":"nk-123456","apipassword":"np-abcdef","apisessionid":"sess-7890"}`, "np-abcdef", "netcup"},
		{"url https://dyn.example/update?user=me&password=hunter2hunter2&x=1", "hunter2hunter2", "user=me"},
	} {
		got := r(c.in)
		if strings.Contains(got, c.gone) || !strings.Contains(got, c.stays) {
			t.Errorf("%q\n -> %q", c.in, got)
		}
	}
	for _, gone := range []string{"nk-123456", "sess-7890"} {
		if got := r(`netcup: {"apikey":"nk-123456","apisessionid":"sess-7890"}`); strings.Contains(got, gone) {
			t.Errorf("%s in %q", gone, got)
		}
	}
	// ordinary lines of a run stay as they are
	for _, ok := range []string{
		"http-01: no token store",
		"[INFO] [box.example.org] acme: Obtaining bundled SAN certificate",
		"[INFO] [box.example.org] acme: authorization already valid; skipping challenge",
		"issuer CN=Test CA, valid until 2026-12-11 (90 days)",
		"installed as /etc/config/server.pem, lighttpd reloaded",
	} {
		if got := r(ok); got != ok {
			t.Errorf("%q changed to %q", ok, got)
		}
	}
	// a secret shorter than four characters is not replaced literally (it would blank words)
	if got := Redactor(Settings{EABHMAC: "ab"})("a tab"); got != "a tab" {
		t.Errorf("short secret: %q", got)
	}
}
