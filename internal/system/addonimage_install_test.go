package system

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonimage"
	"github.com/hobbyquaker/occulited/internal/manifest"
)

// occulited task 11: the install keeps the images the package's manifest declares, out of the
// archive at their path from the package root. OpenCCU-Loom's update_script puts www/ outside the
// addon's tree; the fake installer here copies nothing at all, so whatever the system shows comes
// from the kept copies.
func TestInstallKeepsThePackagesImages(t *testing.T) {
	r, a := manifestBox(t, "loom")
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><circle r="4"/></svg>`
	dark := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="4" height="4"/></svg>`
	body := `{"format": 1, "id": "loom", "name": "Loom", "ui": {"icon": "www/icon.svg", "icon_dark": "www/icon_dark.svg",
		"logo": "www/missing.png", "logo_dark": "www/page.svg"}}`
	res, err := a.Install(context.Background(), packageWithFiles(t, body, map[string]string{
		"www/icon.svg":        svg,
		"./www/icon_dark.svg": dark,
		"www/page.svg":        "<html><script>alert(1)</script></html>",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[manifest] loom: images kept from the package: icon, icon-dark",
		"[manifest] loom: logo www/missing.png: not in the package; the system shows its fallback",
		"[manifest] loom: logo-dark www/page.svg: not an image",
	} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("output lacks %q:\n%s", want, res.Output)
		}
	}
	if b, ct, err := r.AddonImage("loom", addonimage.KindIcon); err != nil || ct != "image/svg+xml" || string(b) != svg {
		t.Fatalf("icon: %q %q %v", b, ct, err)
	}
	if b, _, err := r.AddonImage("loom", addonimage.KindIconDark); err != nil || string(b) != dark {
		t.Fatalf("icon-dark: %q %v", b, err)
	}
	for _, k := range []string{addonimage.KindLogo, addonimage.KindLogoDark} {
		if _, _, err := r.AddonImage("loom", k); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v", k, err)
		}
	}
	st, err := os.Stat(r.join(AddonPolicyDir + "/loom" + AddonImageInfix + addonimage.KindIcon))
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("kept copy: %v %v", st, err)
	}

	// an update whose manifest no longer declares the dark icon takes its copy away
	if _, err := a.Install(context.Background(), packageWithFiles(t, `{"format": 1, "id": "loom", "name": "Loom", "ui": {"icon": "www/icon.svg"}}`,
		map[string]string{"www/icon.svg": svg, "www/icon_dark.svg": dark})); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(r.join(AddonPolicyDir + "/loom" + AddonImageInfix + addonimage.KindIconDark)); !os.IsNotExist(err) {
		t.Errorf("the undeclared dark icon's copy stayed: %v", err)
	}
	if _, _, err := r.AddonImage("loom", addonimage.KindIconDark); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("icon-dark after the update: %v", err)
	}

	// a package without a manifest, the catalogue's word standing in: its images are the
	// catalogue's, the package's copies go
	a.FallbackManifest = func(id string) (*manifest.Manifest, string) {
		return &manifest.Manifest{Format: 1, ID: "loom", Name: manifest.Text{"en": "Loom"}, UI: manifest.UI{Icon: "www/icon.svg"}}, ""
	}
	res, err = a.Install(context.Background(), packageWith(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "the catalogue's declaration applied") {
		t.Fatalf("output:\n%s", res.Output)
	}
	if _, err := os.Lstat(r.join(AddonPolicyDir + "/loom" + AddonImageInfix + addonimage.KindIcon)); !os.IsNotExist(err) {
		t.Errorf("a package copy outlived the package's manifest: %v", err)
	}

	// and an uninstall leaves none behind
	a.FallbackManifest = nil
	if _, err := a.Install(context.Background(), packageWithFiles(t, body, map[string]string{"www/icon.svg": svg})); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Uninstall(context.Background(), "loom"); err != nil {
		t.Logf("uninstall: %v (the cleanup still runs)", err)
	}
	for _, k := range addonimage.Kinds {
		if _, err := os.Lstat(r.join(AddonPolicyDir + "/loom" + AddonImageInfix + k)); !os.IsNotExist(err) {
			t.Errorf("%s outlived the addon: %v", k, err)
		}
	}
}

// A kept copy is served only as what it is: a link, a file too large or a file that is no image
// is refused, and a copy for a kind the stored manifest does not declare is not shown.
func TestKeptAddonImageChecks(t *testing.T) {
	root := Root(t.TempDir())
	if err := os.MkdirAll(root.join(AddonPolicyDir), 0o755); err != nil {
		t.Fatal(err)
	}
	w := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(root.join(AddonPolicyDir+"/"+name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w("a"+AddonManifestSuffix, `{"format": 1, "id": "a", "name": "A", "ui": {"icon": "www/i.svg", "logo": "www/l.png", "icon_dark": "www/d.svg"}}`)
	w("a"+AddonImageInfix+addonimage.KindIcon, "<html></html>")
	w("a"+AddonImageInfix+addonimage.KindLogo, "\x89PNG\r\n\x1a\n"+strings.Repeat("x", addonimage.MaxSize))
	w("a"+AddonImageInfix+addonimage.KindLogoDark, "\x89PNG\r\n\x1a\n")
	if err := os.Symlink("/etc/passwd", root.join(AddonPolicyDir+"/a"+AddonImageInfix+addonimage.KindIconDark)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := root.AddonImage("a", addonimage.KindIcon); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("html: %v", err)
	}
	if _, _, err := root.AddonImage("a", addonimage.KindLogo); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("too large: %v", err)
	}
	if _, _, err := root.AddonImage("a", addonimage.KindIconDark); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a link: %v", err)
	}
	if _, _, err := root.AddonImage("a", addonimage.KindLogoDark); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("undeclared: %v", err)
	}
}
