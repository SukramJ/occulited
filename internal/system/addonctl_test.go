package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

type chownPriv struct {
	priv.Local
	chowns []string
}

func (p *chownPriv) Chown(path string, uid, gid int, _ bool) error {
	p.chowns = append(p.chowns, filepath.Base(path)+":"+strings.Repeat("", 0)+itoa(uid))
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// Tokens: one per addon in rc.d, the file owned by the policy's user, the lookup exact, an
// unknown token refused, a forgotten addon gone. A restart of occulited would mint new ones.
func TestAddonTokens(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "usr/local/etc/config/rc.d"), 0o755)
	os.MkdirAll(filepath.Join(root, "usr/local/etc/config/addon-policy"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/local/etc/config/rc.d/mosquitto"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/local/etc/config/rc.d/redmatic"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/local/etc/config/addon-policy/redmatic.json"), []byte(`{"id":"redmatic","mode":"confined","uid":30001,"user":"addon-redmatic"}`), 0o644)
	fp := &chownPriv{}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	a := NewSystemdAddons(Root(root), SystemdServices{Run: run})
	if p := a.RefreshAddonTokens(context.Background()); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
	b, err := os.ReadFile(filepath.Join(root, AddonTokenDir, "redmatic"))
	if err != nil || len(strings.TrimSpace(string(b))) != 48 {
		t.Fatalf("token file: %q %v", b, err)
	}
	tok := strings.TrimSpace(string(b))
	if id, ok := a.Tokens.Lookup(tok); !ok || id != "redmatic" {
		t.Errorf("lookup: %q %v", id, ok)
	}
	if _, ok := a.Tokens.Lookup("deadbeef"); ok {
		t.Error("an unknown token must not resolve")
	}
	if strings.Join(fp.chowns, " ") != "mosquitto:0 redmatic:30001" && strings.Join(fp.chowns, " ") != "redmatic:30001 mosquitto:0" {
		t.Errorf("ownership: %v", fp.chowns)
	}
	// a second refresh keeps the token
	a.RefreshAddonTokens(context.Background())
	if b2, _ := os.ReadFile(filepath.Join(root, AddonTokenDir, "redmatic")); string(b2) != string(b) {
		t.Error("the token changed on a refresh")
	}
	a.Tokens.forget(Root(root), "redmatic")
	if _, ok := a.Tokens.Lookup(tok); ok {
		t.Error("forgotten token still resolves")
	}
	if _, err := os.Stat(filepath.Join(root, AddonTokenDir, "redmatic")); !os.IsNotExist(err) {
		t.Error("forgotten token file still there")
	}
	// adoptRC goes through the runner as the fork's tool
	a.adoptRC(context.Background(), "mosquitto")
	if len(calls) == 0 || calls[len(calls)-1] != AddonRCTool+" adopt mosquitto" {
		t.Errorf("adopt call: %v", calls)
	}
}

// fakeMinter stands in for the auth store: it grants every declared scope but "*" and "power".
type fakeMinter struct {
	minted  []string
	dropped []string
}

func (m *fakeMinter) MintAddonToken(id string, declared []string) (string, []string, []string, error) {
	var granted, refused []string
	for _, s := range declared {
		if s == "*" || s == "power" {
			refused = append(refused, s)
		} else {
			granted = append(granted, s)
		}
	}
	m.minted = append(m.minted, id+":"+strings.Join(declared, ","))
	if len(granted) == 0 {
		return "", nil, refused, nil
	}
	return "olt_" + id + "_" + itoa(len(m.minted)), granted, refused, nil
}

func (m *fakeMinter) DropAddonToken(id string) { m.dropped = append(m.dropped, id) }

// The addons' API tokens (task 66): minted from the policy's api_scopes with the refused ones
// left out, the file beside the control token, 0600 and the addon's user; minted again only when
// the declaration changes, rewritten for a new user; nothing for an addon without a declaration
// or with nothing left; removed with a changed declaration, and gone on uninstall.
func TestAddonAPITokens(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "usr/local/etc/config/rc.d"), 0o755)
	os.MkdirAll(filepath.Join(root, "usr/local/etc/config/addon-policy"), 0o755)
	for _, id := range []string{"hmm", "redmatic", "mosquitto"} {
		os.WriteFile(filepath.Join(root, "usr/local/etc/config/rc.d/"+id), []byte("#!/bin/sh\n"), 0o755)
	}
	writePolicy := func(id, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "usr/local/etc/config/addon-policy/"+id+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writePolicy("hmm", `{"id":"hmm","mode":"confined","uid":30001,"user":"addon-hmm","runtime":{"api_scopes":["meta:write","power"]}}`)
	writePolicy("mosquitto", `{"id":"mosquitto","mode":"confined","uid":30002,"user":"addon-mosquitto","runtime":{"api_scopes":["*"]}}`)
	fp := &chownPriv{}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	run := func(_ context.Context, name string, args ...string) ([]byte, error) { return nil, nil }
	minter := &fakeMinter{}
	a := NewSystemdAddons(Root(root), SystemdServices{Run: run})
	a.APITokens = minter
	if p := a.RefreshAddonTokens(context.Background()); len(p) != 0 {
		t.Fatalf("problems %v", p)
	}
	api := filepath.Join(root, AddonTokenDir, "hmm.api")
	b, err := os.ReadFile(api)
	if err != nil || !strings.HasPrefix(string(b), "olt_hmm_") {
		t.Fatalf("api token file: %q %v", b, err)
	}
	if st, _ := os.Stat(api); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", st.Mode().Perm())
	}
	if got := strings.Join(a.Tokens.APIScopes("hmm"), ","); got != "meta:write" {
		t.Errorf("scopes %q", got)
	}
	for _, id := range []string{"redmatic", "mosquitto"} {
		if _, err := os.Stat(filepath.Join(root, AddonTokenDir, id+".api")); !os.IsNotExist(err) {
			t.Errorf("%s has an api token file", id)
		}
		if a.Tokens.APIScopes(id) != nil {
			t.Errorf("%s has scopes", id)
		}
	}
	if strings.Join(minter.minted, " ") != "hmm:meta:write,power mosquitto:*" && strings.Join(minter.minted, " ") != "mosquitto:* hmm:meta:write,power" {
		t.Errorf("minted %v", minter.minted)
	}
	if !strings.Contains(strings.Join(fp.chowns, " "), "hmm.api:30001") {
		t.Errorf("ownership: %v", fp.chowns)
	}
	// the same declaration again: nothing minted anew, the secret stays
	n := len(minter.minted)
	a.RefreshAddonTokens(context.Background())
	if b2, _ := os.ReadFile(api); string(b2) != string(b) || len(minter.minted) != n {
		t.Error("minted again on a refresh without a change")
	}
	// the policy changes the user: the file is rewritten for it, the token stays
	writePolicy("hmm", `{"id":"hmm","mode":"confined","uid":30009,"user":"addon-hmm","runtime":{"api_scopes":["meta:write","power"]}}`)
	fp.chowns = nil
	a.RefreshAddonTokens(context.Background())
	if b2, _ := os.ReadFile(api); string(b2) != string(b) || !strings.Contains(strings.Join(fp.chowns, " "), "hmm.api:30009") {
		t.Errorf("after the policy change: %q %v", b2, fp.chowns)
	}
	// a changed declaration mints anew
	writePolicy("hmm", `{"id":"hmm","mode":"confined","uid":30009,"user":"addon-hmm","runtime":{"api_scopes":["meta:read"]}}`)
	a.RefreshAddonTokens(context.Background())
	if b2, _ := os.ReadFile(api); string(b2) == string(b) || strings.Join(a.Tokens.APIScopes("hmm"), ",") != "meta:read" {
		t.Errorf("after the declaration change: %q %v", b2, a.Tokens.APIScopes("hmm"))
	}
	// the declaration goes: the token and its file go, the control token stays
	writePolicy("hmm", `{"id":"hmm","mode":"confined","uid":30009,"user":"addon-hmm"}`)
	a.RefreshAddonTokens(context.Background())
	if _, err := os.Stat(api); !os.IsNotExist(err) {
		t.Error("the api token file outlived its declaration")
	}
	if a.Tokens.APIScopes("hmm") != nil || len(minter.dropped) == 0 || minter.dropped[len(minter.dropped)-1] != "hmm" {
		t.Errorf("not dropped: %v %v", a.Tokens.APIScopes("hmm"), minter.dropped)
	}
	if _, err := os.Stat(filepath.Join(root, AddonTokenDir, "hmm")); err != nil {
		t.Error("the control token went with it")
	}
	// the stored manifest's word wins over the stored block
	writeManifest(t, Root(root), "redmatic", &AddonRuntime{APIScopes: []string{"meta:read", "rpc:read"}})
	a.RefreshAddonTokens(context.Background())
	if got := strings.Join(a.Tokens.APIScopes("redmatic"), ","); got != "meta:read,rpc:read" {
		t.Errorf("declared by the manifest: %q", got)
	}
	// uninstall forgets both
	minter.dropped = nil
	a.Tokens.forget(Root(root), "redmatic")
	if _, err := os.Stat(filepath.Join(root, AddonTokenDir, "redmatic.api")); !os.IsNotExist(err) {
		t.Error("the api token file outlived the addon")
	}
	if strings.Join(minter.dropped, ",") != "redmatic" || a.Tokens.APIScopes("redmatic") != nil {
		t.Errorf("forget: %v", minter.dropped)
	}
}
