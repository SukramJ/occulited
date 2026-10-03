package system

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonimage"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// imageHelper stands in for the helper's addon-image read, which reads as root: it opens the closed
// tree for the moment of the read and counts the calls.
type imageHelper struct {
	priv.Local
	calls *int
}

func (h imageHelper) ReadAddonImage(manifestPath, tree, kind string) ([]byte, error) {
	*h.calls++
	www := filepath.Join(tree, "www")
	if err := os.Chmod(www, 0o755); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chmod(www, 0o311) }()
	return priv.Local{}.ReadAddonImage(manifestPath, tree, kind)
}

// openccu-lite task 100: an installed addon's images come out of its tree at the paths its stored
// manifest declares, typed by content; a confined addon's closed tree is read through the helper.
func TestAddonImage(t *testing.T) {
	root := Root(t.TempDir())
	w := func(rel, content string) {
		t.Helper()
		p := root.join(rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	w(AddonPolicyDir+"/mosquitto"+AddonManifestSuffix, `{"format": 1, "id": "mosquitto", "name": "Mosquitto", "ui": {"icon": "mosquitto/www/icon.svg", "logo_dark": "mosquitto/www/logo-dark.png", "logo": "mosquitto/www/missing.png"}}`)
	w("/usr/local/addons/mosquitto/www/icon.svg", svg)
	w("/usr/local/addons/mosquitto/www/logo-dark.png", "\x89PNG\r\n\x1a\n")
	w(AddonPolicyDir+"/plain"+AddonManifestSuffix, `{"format": 1, "id": "plain", "name": "Plain"}`)

	if got := strings.Join(root.AddonImageKinds("mosquitto"), ","); got != "icon,logo,logo-dark" {
		t.Errorf("kinds = %q", got)
	}
	if root.AddonImageKinds("plain") != nil || root.AddonImageKinds("none") != nil || root.AddonImageKinds("../x") != nil {
		t.Error("kinds for an addon without images")
	}
	// an svg with a script is still served - as an image; the policy on the answer is the API's
	b, ct, err := root.AddonImage("mosquitto", addonimage.KindIcon)
	if err != nil || ct != "image/svg+xml" || string(b) != svg {
		t.Fatalf("icon: %q %q %v", b, ct, err)
	}
	if _, ct, err := root.AddonImage("mosquitto", addonimage.KindLogoDark); err != nil || ct != "image/png" {
		t.Fatalf("logo-dark: %q %v", ct, err)
	}
	for _, c := range []struct{ id, kind string }{{"mosquitto", addonimage.KindLogo}, {"mosquitto", addonimage.KindIconDark}, {"plain", addonimage.KindIcon}, {"none", addonimage.KindIcon}, {"../etc", addonimage.KindIcon}, {"mosquitto", "nope"}} {
		if _, _, err := root.AddonImage(c.id, c.kind); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s %s: %v", c.id, c.kind, err)
		}
	}
	// a closed tree (openccu-lite B-252): the daemon cannot open it and asks the helper
	if os.Getuid() == 0 {
		t.Skip("root reads a closed tree")
	}
	if err := os.Chmod(root.join("/usr/local/addons/mosquitto/www"), 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root.join("/usr/local/addons/mosquitto/www"), 0o755) })
	calls := 0
	old := Priv
	Priv = imageHelper{calls: &calls}
	t.Cleanup(func() { Priv = old })
	if b, ct, err := root.AddonImage("mosquitto", addonimage.KindIcon); err != nil || ct != "image/svg+xml" || string(b) != svg || calls != 1 {
		t.Fatalf("through the helper: %q %q %v calls=%d", b, ct, err, calls)
	}
}
