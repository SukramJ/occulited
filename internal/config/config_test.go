package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// task 19: the password-login switch defaults to on, holds only in mode oidc, and a file of an
// earlier version with the group mapping loads with those keys reported and dropped at the next save.
func TestPasswordLoginSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occulited.json")
	c, err := Load(path)
	if err != nil || !c.Auth.OIDC.PasswordLogin || !c.Auth.PasswordLoginOn() {
		t.Fatalf("no file: %v %+v", err, c.Auth)
	}
	old := `{"auth": {"mode": "oidc", "oidc": {"enabled": true, "issuer": "https://auth.example.org/o/lite/", "client_id": "x", "groups_claim": "groups", "admin_groups": ["ccu-admins"]}}}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Auth.OIDC.PasswordLogin || !c.Auth.PasswordLoginOn() {
		t.Errorf("a file without the key: password login must be on: %+v", c.Auth)
	}
	if strings.Join(c.Stale, ",") != "auth.oidc.groups_claim,auth.oidc.admin_groups" {
		t.Errorf("stale keys: %v", c.Stale)
	}
	c.Auth.OIDC.PasswordLogin = false
	if c.Auth.PasswordLoginOn() {
		t.Error("off in mode oidc must hold")
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "admin_groups") || strings.Contains(string(b), "groups_claim") {
		t.Errorf("the stale keys were written back:\n%s", b)
	}
	if !strings.Contains(string(b), `"password_login": false`) {
		t.Errorf("the switch was not written:\n%s", b)
	}
	c, err = Load(path)
	if err != nil || c.Auth.OIDC.PasswordLogin || c.Auth.PasswordLoginOn() || len(c.Stale) != 0 {
		t.Errorf("after save: %v %+v stale=%v", err, c.Auth, c.Stale)
	}
	// the switch holds in mode oidc only: local and off log in with a password (or not at all)
	for _, mode := range []string{"local", "off", ""} {
		c.Auth.Mode, c.Auth.OIDC.Enabled = mode, false
		if !c.Auth.PasswordLoginOn() {
			t.Errorf("mode %q with the switch off: password login must be on", mode)
		}
	}
	// an enabled provider without a mode is mode oidc (EffectiveMode), so the switch holds there too
	c.Auth.Mode, c.Auth.OIDC.Enabled = "", true
	if c.Auth.PasswordLoginOn() {
		t.Error("enabled provider without a mode: the switch must hold")
	}
	// the console command: on always writes, off only in mode oidc
	c.Auth.Mode, c.Auth.OIDC.Enabled, c.Auth.OIDC.PasswordLogin = "oidc", true, false
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}
	if c, err := SetPasswordLogin(path, true); err != nil || !c.Auth.OIDC.PasswordLogin {
		t.Errorf("console on: %v %+v", err, c.Auth.OIDC)
	}
	if c, _ := Load(path); !c.Auth.PasswordLoginOn() {
		t.Error("console on was not written")
	}
	if _, err := SetPasswordLogin(path, false); err != nil {
		t.Errorf("console off in mode oidc: %v", err)
	}
	if c, _ := Load(path); c.Auth.PasswordLoginOn() {
		t.Error("console off was not written")
	}
	c.Auth.Mode = "local"
	_ = Save(path, c)
	if _, err := SetPasswordLogin(path, false); err == nil {
		t.Error("console off in mode local must be refused")
	}
	if c, _ := Load(path); !c.Auth.PasswordLoginOn() {
		t.Error("the refused off was written")
	}
	// `"admin_groups": null`, as occulited wrote it for a while, is not stale
	if err := os.WriteFile(path, []byte(`{"auth": {"oidc": {"admin_groups": null, "groups_claim": ""}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(path); err != nil || len(c.Stale) != 0 {
		t.Errorf("null groups: %v %v", err, c.Stale)
	}
}

// D-119 and the publication on GitHub: a stored catalogue list that still names an old default -
// the openccu-lite-addons index or this repository's file on the private forge - follows the new
// default; anything else is the user's choice and stays, the bundled copy included.
func TestOldCatalogDefaultsFollow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occulited.json")
	file := `{"catalog": {"enabled": true, "urls": [
		"https://git.example.org/hobbyquaker/openccu-lite-addons/raw/branch/master/index.json",
		"https://git.example.org/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json",
		"https://git.example.org/hobbyquaker/occulited/raw/branch/main/catalog/catalog.json",
		"http://git.example.org/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json",
		"https://raw.githubusercontent.com/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json",
		"https://git.example.org/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json?x=1",
		"file:///etc/occulite/catalog.json"]}}`
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		DefaultCatalogURL,
		DefaultCatalogURL,
		"https://git.example.org/hobbyquaker/occulited/raw/branch/main/catalog/catalog.json",
		"http://git.example.org/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json",
		"https://raw.githubusercontent.com/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json",
		"https://git.example.org/hobbyquaker/occulited/raw/branch/master/catalog/catalog.json?x=1",
		BundledCatalog,
	}
	if strings.Join(c.Catalog.URLs, "\n") != strings.Join(want, "\n") {
		t.Errorf("urls:\n%s", strings.Join(c.Catalog.URLs, "\n"))
	}
	if !strings.HasPrefix(DefaultCatalogURL, "https://raw.githubusercontent.com/hobbyquaker/occulited/") {
		t.Errorf("the default is not the published file: %s", DefaultCatalogURL)
	}
	if d := Default(); len(d.Catalog.URLs) != 2 || d.Catalog.URLs[0] != DefaultCatalogURL || d.Catalog.URLs[1] != BundledCatalog {
		t.Errorf("defaults: %v", d.Catalog.URLs)
	}
}
