package main

import (
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/config"
)

// B-102: which stored sessions a start restores follows the mode the start runs in.
func TestSessionMethods(t *testing.T) {
	provider := config.OIDCConfig{Issuer: "https://auth.example.org/", ClientID: "occulite"}
	enabled := provider
	enabled.Enabled = true
	for _, tc := range []struct {
		name string
		auth config.AuthConfig
		want string
	}{
		{"no auth block", config.AuthConfig{}, "password"},
		{"local", config.AuthConfig{Mode: "local"}, "password"},
		{"local with a provider not enabled", config.AuthConfig{Mode: "local", OIDC: provider}, "password"},
		{"oidc", config.AuthConfig{Mode: "oidc", OIDC: provider}, "password,oidc"},
		{"oidc without an issuer", config.AuthConfig{Mode: "oidc", OIDC: config.OIDCConfig{ClientID: "x"}}, "password"},
		{"the old oidc.enabled", config.AuthConfig{OIDC: enabled}, "password,oidc"},
		{"local with oidc.enabled (the start enables it)", config.AuthConfig{Mode: "local", OIDC: enabled}, "password,oidc"},
		{"off", config.AuthConfig{Mode: "off"}, ""},
		{"off with a provider", config.AuthConfig{Mode: "off", OIDC: enabled}, ""},
	} {
		if got := strings.Join(sessionMethods(tc.auth), ","); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
