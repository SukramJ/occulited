package priv

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonimage"
)

const svgIcon = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 8 8"><rect width="8" height="8" fill="#c00"/></svg>`

// openccu-lite task 100: the helper reads exactly the images an addon's stored manifest declares,
// out of that addon's tree, and answers them only when they are images. Nothing else of the tree
// comes out: not a file the manifest does not name, not a text file however the manifest names it,
// not a file behind a link out of the tree, nothing too large; and the request is three fields of
// fixed shapes.
func TestAddonImageRead(t *testing.T) {
	root := t.TempDir()
	w := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	manifestOf := func(id, ui string) string {
		return w("usr/local/etc/config/addon-policy/"+id+".manifest.json", `{"format": 1, "id": "`+id+`", "name": "X", "ui": `+ui+`}`)
	}
	tree := func(id string) string { return filepath.Join(root, "usr/local/addons", id) }
	w("etc/shadow", "root:SECRETHASH:::\n")
	// hmm: an svg icon, a png logo, a dark icon that is really the token file, and a logo-dark that is html
	hmm := manifestOf("hmm", `{"icon": "hmm/www/icon.svg", "logo": "hmm/www/logo.png", "icon_dark": "hmm/etc/hmm.env", "logo_dark": "hmm/www/logo.svg"}`)
	w("usr/local/addons/hmm/www/icon.svg", svgIcon)
	w("usr/local/addons/hmm/www/logo.png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR....")
	w("usr/local/addons/hmm/etc/hmm.env", "HMM_TOKEN=secret\n")
	w("usr/local/addons/hmm/www/logo.svg", `<!DOCTYPE html><html><body><svg/></body></html>`)
	// big: too large; outlink: a link out of the tree; outdir: a directory on the way is a link out;
	// dir: the path is a directory; one: a one-segment path lies at the package root, not in the tree
	big := manifestOf("big", `{"icon": "big/www/icon.svg"}`)
	w("usr/local/addons/big/www/icon.svg", "<svg>"+strings.Repeat(" ", addonimage.MaxSize)+"</svg>")
	outlink := manifestOf("outlink", `{"icon": "outlink/www/icon.svg"}`)
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/outlink/www"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc/shadow"), filepath.Join(root, "usr/local/addons/outlink/www/icon.svg")); err != nil {
		t.Fatal(err)
	}
	outdir := manifestOf("outdir", `{"icon": "outdir/www/shadow"}`)
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/outdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "usr/local/addons/outdir/www")); err != nil {
		t.Fatal(err)
	}
	dir := manifestOf("dir", `{"icon": "dir/www"}`)
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/dir/www"), 0o755); err != nil {
		t.Fatal(err)
	}
	one := manifestOf("one", `{"icon": "icon.svg"}`)
	w("usr/local/addons/one/icon.svg", svgIcon)
	// wrong: the stored manifest names another addon
	wrong := w("usr/local/etc/config/addon-policy/wrong.manifest.json", `{"format": 1, "id": "hmm", "name": "X", "ui": {"icon": "wrong/www/icon.svg"}}`)
	w("usr/local/addons/wrong/www/icon.svg", svgIcon)
	c, sock := logListHelper(t, root, nil)

	b, err := c.ReadAddonImage(hmm, tree("hmm"), addonimage.KindIcon)
	if err != nil || string(b) != svgIcon {
		t.Fatalf("the icon: %q %v", b, err)
	}
	if b, err := c.ReadAddonImage(hmm, tree("hmm"), addonimage.KindLogo); err != nil || !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatalf("the logo: %q %v", b, err)
	}
	for name, tc := range map[string]struct {
		manifest, tree, kind string
		want                 error
	}{
		"a declared path that is no image is not answered":   {hmm, tree("hmm"), addonimage.KindIconDark, ErrImageNotImage},
		"html named .svg is no image":                        {hmm, tree("hmm"), addonimage.KindLogoDark, ErrImageNotImage},
		"too large":                                          {big, tree("big"), addonimage.KindIcon, ErrImageTooLarge},
		"a link out of the tree":                             {outlink, tree("outlink"), addonimage.KindIcon, ErrImageLink},
		"a directory on the way that leads out":              {outdir, tree("outdir"), addonimage.KindIcon, ErrImageUnreachable},
		"a directory":                                        {dir, tree("dir"), addonimage.KindIcon, ErrImageNotRegular},
		"a one-segment path is not in the tree":              {one, tree("one"), addonimage.KindIcon, fs.ErrNotExist},
		"a kind the manifest does not declare":               {big, tree("big"), addonimage.KindLogo, fs.ErrNotExist},
		"no stored manifest":                                 {filepath.Join(root, "usr/local/etc/config/addon-policy/none.manifest.json"), tree("none"), addonimage.KindIcon, fs.ErrNotExist},
		"the stored manifest names another addon":            {wrong, tree("wrong"), addonimage.KindIcon, fs.ErrNotExist},
		"a kind that is not one of the four is refused":      {hmm, tree("hmm"), "etc/hmm.env", ErrRefused},
		"another addon's tree with this manifest is refused": {hmm, tree("big"), addonimage.KindIcon, ErrRefused},
		"a manifest outside the policy directory is refused": {filepath.Join(root, "usr/local/addons/hmm/openccu-lite.json"), tree("hmm"), addonimage.KindIcon, ErrRefused},
		"a tree outside the addons directory is refused":     {hmm, filepath.Join(root, "usr/local/etc/config/addons/hmm"), addonimage.KindIcon, ErrRefused},
		"a tree with a traversal is refused":                 {hmm, filepath.Join(root, "usr/local/addons/hmm/../../../etc"), addonimage.KindIcon, ErrRefused},
		"a path outside the policy's root is refused":        {"/usr/local/etc/config/addon-policy/hmm.manifest.json", "/usr/local/addons/hmm", addonimage.KindIcon, ErrRefused},
	} {
		b, err := c.ReadAddonImage(tc.manifest, tc.tree, tc.kind)
		if !errors.Is(err, tc.want) || b != nil {
			t.Errorf("%s: %q %v, want %v", name, b, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), "SECRETHASH") || strings.Contains(err.Error(), "secret")) {
			t.Errorf("%s: the answer quotes the file", name)
		}
	}
	// a request with more than its three fields is refused
	if res, _ := rawLogList(t, sock, request{Op: opAddonImage, Path: hmm, Dir: tree("hmm"), Name: addonimage.KindIcon, Recursive: true}); res.OK || !strings.Contains(res.Error, "nothing else") {
		t.Errorf("with a flag: %+v", res)
	}
	// Local alone: the same reads, the same refusals of what is not an image
	if b, err := (Local{}).ReadAddonImage(hmm, tree("hmm"), addonimage.KindIcon); err != nil || string(b) != svgIcon {
		t.Errorf("Local: %q %v", b, err)
	}
	if _, err := (Local{}).ReadAddonImage(hmm, tree("hmm"), addonimage.KindIconDark); !errors.Is(err, ErrImageNotImage) {
		t.Errorf("Local, the token file: %v", err)
	}
	if _, err := (Local{}).ReadAddonImage(hmm, tree("hmm"), "etc/hmm.env"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Local, a kind that is none: %v", err)
	}
}

// The constants the daemon's side builds the paths from are the ones the policy checks.
func TestAddonImagePolicyPaths(t *testing.T) {
	p := DefaultPolicy("/r", "/usr/local/etc/occulite")
	if !p.addonImageAllowed("/r"+AddonPolicyDir+"/hmm"+AddonManifestSuffix, "/r"+AddonHomeDir+"hmm", addonimage.KindLogoDark) {
		t.Error("the right shapes were refused")
	}
	if p.addonImageAllowed("/r"+AddonPolicyDir+"/hmm.json", "/r"+AddonHomeDir+"hmm", addonimage.KindLogoDark) {
		t.Error("the policy file is not the manifest")
	}
	if p.addonImageAllowed("/r"+AddonPolicyDir+"/hmm"+AddonManifestSuffix, "/r"+AddonHomeDir+"hmm/www", addonimage.KindLogoDark) {
		t.Error("a directory below the tree is not the tree")
	}
}
