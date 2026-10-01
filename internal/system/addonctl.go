package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
)

// ---- addon control tokens (task 28.8) and addon API tokens (task 66) ------------------------
//
// An addon's settings page starts and stops its daemon by calling its own rc.d script. On lite
// that script is fronted by the addon-rc wrapper (the fork's lite-addon-rc), which turns
// start/stop/restart from outside the unit into a call on addon-<id>.service. As root that is
// systemctl; as the addon's own user (D-36) it is a POST to /api/system/v1/addonctl with the
// control token below, which permits exactly that - the addon's own unit, three actions - and
// nothing else of the API. The tokens live in /run (gone at reboot, minted again at every
// start), one file per addon, readable by that addon's user only.
//
// An addon whose catalogue entry declares runtime.api_scopes gets an API token beside it
// (task 66, D-85): <id>.api in the same directory, the same mode and owner, minted by the auth
// store with the declared scopes less the ones an addon never gets (Full access, auth:admin,
// power, backup - refused and logged), never written to users.json, gone with the addon. An
// addon without a declaration has the box's local token alone.

const AddonTokenDir = "/run/occulite/addon-tokens"

// APITokenMinter mints and forgets the addons' API tokens: the auth store.
type APITokenMinter interface {
	// MintAddonToken makes the addon's token from its declared scopes and answers the secret,
	// the scopes granted and the names refused; "" when nothing was granted.
	MintAddonToken(id string, declared []string) (secret string, granted, refused []string, err error)
	DropAddonToken(id string)
	// SyncGateTokens rewrites the gate's token mirror (openccu-lite task 307): the URL segments an
	// addon ingress scope opens follow the addons' drop-ins, so every addon change is followed by it.
	SyncGateTokens()
}

// addonAPIToken is what the box holds of one addon's API token: the secret (to write the file
// again when the addon's user changes), the declaration it came from, the scopes granted.
type addonAPIToken struct {
	secret   string
	declared string
	scopes   []string
}

type AddonTokens struct {
	mu      sync.Mutex
	byToken map[string]string // control token -> addon id
	byID    map[string]string
	api     map[string]addonAPIToken // addon id -> its API token
	// Minter makes the API tokens; nil = none are minted (the busybox products, the tests).
	Minter APITokenMinter
}

// Lookup answers the addon an addonctl token belongs to.
func (t *AddonTokens) Lookup(token string) (string, bool) {
	if t == nil || token == "" {
		return "", false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	id, ok := t.byToken[token]
	return id, ok
}

// APIScopes answers the scopes an addon's API token holds (the Addons page); nil without one.
func (t *AddonTokens) APIScopes(id string) []string {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if a, ok := t.api[id]; ok && a.secret != "" {
		return append([]string(nil), a.scopes...)
	}
	return nil
}

// ensure mints the addon's control token when it has none yet and writes the file for its user
// (uid 0 for a root addon). The file is rewritten when the uid changed - a switch of the policy.
// The API token follows: minted when the declaration is new or changed, its file written the
// same way, removed when the declaration went.
func (t *AddonTokens) ensure(root Root, id string, uid int, declared []string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byToken == nil {
		t.byToken, t.byID, t.api = map[string]string{}, map[string]string{}, map[string]addonAPIToken{}
	}
	tok, had := t.byID[id]
	if !had {
		b := make([]byte, 24)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		tok = hex.EncodeToString(b)
		t.byID[id], t.byToken[tok] = tok, id
	}
	dir := root.join(AddonTokenDir)
	if err := Priv.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// the helper's umask made it 0700 on the lab box; an addon's user must be able to reach its
	// own file, and the files themselves are 0600 - the directory carries no secret
	if err := Priv.Chmod(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, id)
	if err := writeOwned(path, tok, uid); err != nil {
		return err
	}
	err := t.ensureAPI(root, id, uid, declared)
	if t.Minter != nil {
		t.Minter.SyncGateTokens()
	}
	return err
}

// ensureAPI is the API token's half of ensure; the lock is held.
func (t *AddonTokens) ensureAPI(root Root, id string, uid int, declared []string) error {
	path := filepath.Join(root.join(AddonTokenDir), id+".api")
	key := strings.Join(declared, " ")
	cur, have := t.api[id]
	if t.Minter == nil || len(declared) == 0 {
		if have {
			delete(t.api, id)
			if t.Minter != nil {
				t.Minter.DropAddonToken(id)
			}
		}
		_ = Priv.Remove(path)
		return nil
	}
	if !have || cur.declared != key {
		secret, granted, refused, err := t.Minter.MintAddonToken(id, declared)
		if err != nil {
			return err
		}
		if len(refused) > 0 {
			slog.Warn("addon api token: scopes an addon never gets were declared and left out", "addon", id, "refused", refused, "granted", granted)
		}
		// a declaration that grants nothing is remembered too, so it is not minted (and
		// logged) again at every refresh
		cur = addonAPIToken{secret: secret, declared: key, scopes: granted}
		t.api[id] = cur
		if secret != "" {
			slog.Info("addon api token: minted", "addon", id, "scopes", granted)
		}
	}
	if cur.secret == "" {
		_ = Priv.Remove(path)
		return nil
	}
	return writeOwned(path, cur.secret, uid)
}

// writeOwned writes a token file, 0600, owned by the addon's user.
func writeOwned(path, secret string, uid int) error {
	if err := Priv.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		return err
	}
	return Priv.Chown(path, uid, uid, false)
}

// forget drops an addon's tokens (uninstall): the control token, the API token, both files.
func (t *AddonTokens) forget(root Root, id string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if tok, ok := t.byID[id]; ok {
		delete(t.byToken, tok)
		delete(t.byID, id)
	}
	if _, ok := t.api[id]; ok {
		delete(t.api, id)
		if t.Minter != nil {
			t.Minter.DropAddonToken(id)
		}
	}
	_ = Priv.Remove(root.join(filepath.Join(AddonTokenDir, id)))
	_ = Priv.Remove(root.join(filepath.Join(AddonTokenDir, id+".api")))
	if t.Minter != nil {
		t.Minter.SyncGateTokens() // a token's addon:<id> opens nothing for a gone addon's segments
	}
}

// RefreshAddonTokens mints or rewrites the tokens of every addon in rc.d, owned by the user its
// policy names, the API token from the addon's declaration (the catalogue's current entry, else
// the stored block). Called at start, after an install and after a policy change.
func (a *SystemdAddons) RefreshAddonTokens(ctx context.Context) []string {
	if a.Tokens == nil {
		a.Tokens = &AddonTokens{}
	}
	if a.APITokens != nil {
		a.Tokens.Minter = a.APITokens
	}
	var problems []string
	for id := range a.rcd() {
		if !addonIDRe.MatchString(id) {
			continue
		}
		uid := 0
		var declared []string
		p := a.Scripts.Root.ReadAddonPolicy(id)
		if p != nil && p.Mode == "confined" {
			uid = p.UID
		}
		var stored *AddonRuntime
		if p != nil {
			stored = p.Runtime
		}
		if rt := MergeRuntime(stored, a.declared(id)); rt != nil {
			declared = rt.APIScopes
		}
		if err := a.Tokens.ensure(a.Scripts.Root, id, uid, declared); err != nil {
			problems = append(problems, id+": "+err.Error())
		}
	}
	return problems
}

// adoptRC puts the addon-rc wrapper in front of the rc.d scripts (28.8) through the fork's
// script; an image without it is not an error, the addons then run as they always did.
func (a *SystemdAddons) adoptRC(ctx context.Context, ids ...string) {
	args := append([]string{"adopt"}, ids...)
	_, _ = a.Systemd.run2(ctx, AddonRCTool, args...)
}

// AddonRCTool is the fork's lite-addon-rc (28.8).
const AddonRCTool = "/usr/libexec/occu/lite-addon-rc"
