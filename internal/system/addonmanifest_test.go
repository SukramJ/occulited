package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// D-119: the manifest's runtime block reaches the policy as it was declared - needs absent stays
// absent, [] stays [], the ports keep their descriptions - as copies, never the manifest's slices.
func TestRuntimeFromManifest(t *testing.T) {
	if RuntimeFromManifest(nil) != nil {
		t.Fatal("no block")
	}
	empty := []string{}
	m := &manifest.Runtime{Root: true, Capabilities: []string{"CAP_NET_RAW"}, Groups: []string{"dialout"}, Paths: []string{"/etc/config/x"}, DataDirs: []string{"/usr/local/x"},
		Ports: []int{1883, 8883}, PortInfo: map[string]manifest.PortInfo{"8883": {Proto: "tcp", TLS: true, Label: manifest.Text{"en": "MQTT over TLS"}}},
		Needs: &empty, Start: "early", APIScopes: []string{"meta:write"}}
	rt := RuntimeFromManifest(m)
	if !rt.Root || rt.Capabilities[0] != "CAP_NET_RAW" || rt.Groups[0] != "dialout" || rt.Paths[0] != "/etc/config/x" || rt.DataDirs[0] != "/usr/local/x" || len(rt.Ports) != 2 || !rt.PortInfo["8883"].TLS || rt.PortInfo["8883"].Label["en"] != "MQTT over TLS" || rt.Start != AddonStartEarly || rt.APIScopes[0] != "meta:write" {
		t.Fatalf("%+v", rt)
	}
	if rt.Needs == nil || len(*rt.Needs) != 0 || rt.NeedsLine() != "none" {
		t.Fatalf("needs []: %+v", rt.Needs)
	}
	if RuntimeFromManifest(&manifest.Runtime{Ports: []int{1}}).Needs != nil {
		t.Error("needs absent stays absent")
	}
	two := []string{"rfd", "hmipserver"}
	rt = RuntimeFromManifest(&manifest.Runtime{Needs: &two})
	(*rt.Needs)[0] = "changed"
	if two[0] != "rfd" {
		t.Error("the conversion shares the manifest's slice")
	}
}

// package builds an addon tarball with update_script and, when body is not "", openccu-lite.json at
// the root.
func packageWith(t *testing.T, body string) *bytes.Reader {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, content string) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	add("update_script", "#!/bin/sh\nexit 0\n")
	add("mosq/bin/x", "x")
	if body != "" {
		add(manifest.FileName, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(buf.Bytes())
}

// manifestBox is a systemd box whose fake install_addon creates the rc.d entry newID (and touches
// nothing else).
func manifestBox(t *testing.T, newID string) (Root, *SystemdAddons) {
	t.Helper()
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": "", "etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n", "usr/local/etc/config/rc.d/old": "#!/bin/sh\n"})
	rc := r.join("/usr/local/etc/config/rc.d/" + newID)
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > "+rc+"\nchmod +x "+rc+"\nrm -f "+r.join("/usr/local/tmp/new_addon.tar.gz")+"\nexit 0\n"), 0o755)
	run := func(context.Context, string, ...string) ([]byte, error) { return []byte("ok"), nil }
	return r, NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
}

// The package's manifest is read before the installer runs and applied to the addon it names: the
// policy is written from its runtime block (root as declared), the copy is stored beside it, and the
// pages see the addon as declared.
func TestInstallAppliesThePackageManifest(t *testing.T) {
	r, a := manifestBox(t, "mosq")
	body := `{"format": 1, "id": "mosq", "name": "Mosquitto", "ui": {"session_header": true, "settings_url": "/addons/mosq/settings.cgi"},
		"runtime": {"needs": [], "ports": [1883, 8883], "port_info": {"8883": {"tls": true, "label": {"en": "MQTT over TLS"}}}, "groups": ["dialout"]}}`
	res, err := a.Install(context.Background(), packageWith(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "[manifest] mosq: the package's openccu-lite.json applied") {
		t.Errorf("output:\n%s", res.Output)
	}
	p := r.ReadAddonPolicy("mosq")
	if p == nil || p.Mode != "confined" || p.Source != SourceManifest || p.Runtime == nil || len(p.Runtime.Ports) != 2 || p.Runtime.Groups[0] != "dialout" || p.Runtime.NeedsLine() != "none" {
		t.Fatalf("policy: %+v", p)
	}
	m := r.ReadAddonManifest("mosq")
	if m == nil || !m.UI.SessionHeader || m.UI.SettingsURL != "/addons/mosq/settings.cgi" {
		t.Fatalf("stored manifest: %+v", m)
	}
	if v := a.PolicyView("mosq"); v.Undeclared || v.Source != SourceManifest {
		t.Errorf("view: %+v", v)
	}
	if got := readFile(r.join(AddonPolicyDir + "/mosq.needs")); got != "none\n" {
		t.Errorf("needs file: %q", got)
	}
	if rt := r.DeclaredRuntime("mosq"); rt == nil || !rt.Declares(8883) || !rt.DeclaredPorts()[1].TLS {
		t.Errorf("declared: %+v", rt)
	}
	// the addon runs without the ReGa: its manifest says so without a marker file
	if dep, _ := r.RegaDependence("mosq"); dep {
		t.Error("an addon with a manifest is ReGa-compatible")
	}
	// uninstall forgets the stored copy
	_ = os.WriteFile(r.join("/usr/local/etc/config/rc.d/mosq"), []byte("#!/bin/sh\nexit 0\n"), 0o755)
	if _, err := a.Uninstall(context.Background(), "mosq"); err != nil {
		t.Logf("uninstall: %v (the fake script has no uninstall; the cleanup still runs)", err)
	}
	if r.ReadAddonManifest("mosq") != nil {
		t.Error("the stored manifest outlived the addon")
	}
}

// root: true runs the addon as root, as declared, and a manifest without a runtime block leaves
// the addon on the box default, undeclared
func TestInstallManifestRoot(t *testing.T) {
	r, a := manifestBox(t, "mounter")
	if _, err := a.Install(context.Background(), packageWith(t, `{"format": 1, "id": "mounter", "name": "M", "runtime": {"root": true}}`)); err != nil {
		t.Fatal(err)
	}
	if p := r.ReadAddonPolicy("mounter"); p == nil || p.Mode != "root" || p.Source != SourceManifest {
		t.Fatalf("%+v", p)
	}
	r, a = manifestBox(t, "plain")
	if _, err := a.Install(context.Background(), packageWith(t, `{"format": 1, "id": "plain", "name": "P"}`)); err != nil {
		t.Fatal(err)
	}
	if p := r.ReadAddonPolicy("plain"); p == nil || p.Mode != "confined" || p.Source != SourceManifest || !a.PolicyView("plain").Undeclared {
		t.Fatalf("%+v", p)
	}
}

// A manifest that names an addon the install did not touch is not applied, and the log says so;
// a broken manifest is ignored with a note; a package without one takes the box default - or the
// catalogue's word when it has one.
func TestInstallManifestMismatchBrokenAndFallback(t *testing.T) {
	r, a := manifestBox(t, "mosq")
	res, err := a.Install(context.Background(), packageWith(t, `{"format": 1, "id": "other", "name": "O", "runtime": {"root": true}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "names other, which the installer neither created nor changed") {
		t.Errorf("output:\n%s", res.Output)
	}
	if p := r.ReadAddonPolicy("mosq"); p == nil || p.Mode != "confined" || p.Source != "default" || r.ReadAddonManifest("mosq") != nil || r.ReadAddonManifest("other") != nil {
		t.Fatalf("a mismatched manifest changed something: %+v", p)
	}

	r, a = manifestBox(t, "mosq")
	res, err = a.Install(context.Background(), packageWith(t, `{"format": 1, "id": "Mosq!"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "is not usable and was ignored") || r.ReadAddonPolicy("mosq").Source != "default" {
		t.Errorf("a broken manifest: %s", res.Output)
	}

	r, a = manifestBox(t, "jp")
	a.FallbackManifest = func(id string) *manifest.Manifest {
		if id == "jp" {
			return manifestFor("jp", &AddonRuntime{Root: true})
		}
		return nil
	}
	res, err = a.Install(context.Background(), packageWith(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "the catalogue's declaration applied") {
		t.Errorf("output:\n%s", res.Output)
	}
	if p := r.ReadAddonPolicy("jp"); p == nil || p.Mode != "root" || p.Source != SourceCatalog || r.ReadAddonManifest("jp") == nil {
		t.Fatalf("the fallback: %+v", p)
	}
	// the fallback's id must be the addon's
	r, a = manifestBox(t, "x")
	a.FallbackManifest = func(string) *manifest.Manifest { return manifestFor("y", nil) }
	if _, err := a.Install(context.Background(), packageWith(t, "")); err != nil {
		t.Fatal(err)
	}
	if p := r.ReadAddonPolicy("x"); p.Source != "default" {
		t.Errorf("a fallback for another id was applied: %+v", p)
	}
}

// ApplyManifest keeps a mode the user chose on the Services page; the block follows the package.
func TestApplyManifestKeepsTheUsersMode(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n"})
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: func(context.Context, string, ...string) ([]byte, error) { return nil, nil }})
	if _, err := a.SetPolicy(context.Background(), "mosq", "root", "user", nil); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyManifest(context.Background(), "mosq", manifestFor("mosq", &AddonRuntime{Ports: []int{1883}}), SourceManifest); err != nil {
		t.Fatal(err)
	}
	if p := r.ReadAddonPolicy("mosq"); p.Mode != "root" || p.Source != "user" || p.Runtime == nil || !p.Runtime.Declares(1883) {
		t.Errorf("%+v", p)
	}
	if err := a.ApplyManifest(context.Background(), "mosq", manifestFor("other", nil), SourceManifest); err == nil {
		t.Error("a manifest for another id was applied")
	}
	if err := a.ApplyManifest(context.Background(), "mosq", nil, SourceManifest); err == nil {
		t.Error("no manifest")
	}
}

// requires.rega decides the ReGa verdict where a manifest is known: the stored copy first, the file
// in the addon's own tree for this one flag; the older marker file stays accepted.
func TestRegaFromManifest(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/addons/tree/" + manifest.FileName:      `{"format": 1, "id": "tree", "name": "T"}`,
		"usr/local/addons/needy/" + manifest.FileName:     `{"format": 1, "id": "needy", "name": "N", "requires": {"rega": true}}`,
		"usr/local/addons/marked/" + RegaCompatibleMarker: "",
		"usr/local/addons/cuxd/x.tcl":                     "dom.GetObject",
	})
	writeManifest(t, r, "stored", nil)
	for id, want := range map[string]bool{"tree": false, "needy": true, "marked": false, "stored": false, "cuxd": true} {
		if dep, _ := r.RegaDependence(id); dep != want {
			t.Errorf("%s: dependent %v, want %v", id, dep, want)
		}
	}
}
