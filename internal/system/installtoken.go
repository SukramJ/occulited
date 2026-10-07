package system

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
)

// InstallTokenFile holds the install token (openccu-lite B-274): the credential of the fork's
// /bin/install_addon, which hands an install started outside occulited - on the command line, or
// by an addon's own updater - to occulited's install (POST /api/system/v1/addons/install/local),
// so that the addon ends up in its unit with its policy and manifest instead of running from its
// rc.d as the caller, root and unconfined. Root's alone (0600, gone at reboot, minted again at
// every start of occulited); it permits that one route and nothing else. Root may install any
// archive through /bin/install_addon anyway, so the token grants nothing root does not have.
const InstallTokenFile = "/run/occulite/install-token"

// WriteInstallToken mints a new install token and writes it, root's alone. Returns the secret.
func WriteInstallToken(root Root) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	path := root.join(InstallTokenFile)
	if err := Priv.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := writeOwned(path, tok, 0); err != nil {
		return "", err
	}
	return tok, nil
}

// ConsoleTokenFile holds the console's credential (occulited task 22): the secret of the
// ephemeral token auth.ConsoleTokenName, with which `occulited update` - run as root on the
// system - calls the running occulited's API, so the command line takes the same routes as the
// web UI. Root's alone (0600, gone at reboot, minted again at every start of occulited); the
// token is accepted from the loopback only.
const ConsoleTokenFile = "/run/occulite/console-token"

// WriteConsoleToken writes the console's credential, root's alone.
func WriteConsoleToken(root Root, secret string) error {
	path := root.join(ConsoleTokenFile)
	if err := Priv.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeOwned(path, secret, 0)
}
