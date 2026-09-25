package main

import (
	"slices"
	"testing"

	"github.com/hobbyquaker/occulited/internal/acme"
	"github.com/hobbyquaker/occulited/internal/config"
)

// task 95, decided in D-67: the LED's no-internet check connects to the addon catalogue index's host
// and, while ACME is configured, the ACME directory's host - read at every check, since the
// certificate mode changes at runtime.
func TestLEDInternetTargets(t *testing.T) {
	certs, err := acme.New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cat := config.CatalogConfig{Enabled: true, URLs: []string{"https://catalogue.example.org/addons/index.json", config.BundledCatalog}}
	targets := ledInternetTargets(cat, certs)
	if got := targets(); !slices.Equal(got, []string{"catalogue.example.org:443"}) {
		t.Errorf("self-signed: %v", got)
	}
	if _, err := certs.SetSettings(acme.Update{Mode: acme.ModeACME, Directory: acme.DirLetsEncrypt, Names: []string{"box.example.org"}, Challenge: acme.ChallengeHTTP01}); err != nil {
		t.Fatalf("ACME settings: %v", err)
	}
	if got := targets(); !slices.Equal(got, []string{"catalogue.example.org:443", "acme-v02.api.letsencrypt.org:443"}) {
		t.Errorf("with ACME: %v", got)
	}
	cat.Enabled = false
	if got := ledInternetTargets(cat, certs)(); !slices.Equal(got, []string{"acme-v02.api.letsencrypt.org:443"}) {
		t.Errorf("the catalogue switched off: %v", got)
	}
	if got := ledInternetTargets(config.CatalogConfig{Enabled: true, URLs: []string{config.BundledCatalog}}, nil)(); len(got) != 0 {
		t.Errorf("only the bundled copy, no certificate service: %v", got)
	}
}
