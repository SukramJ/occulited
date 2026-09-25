package firmware

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

func writeBundle(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, _ := os.Create(path)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(content))
	}
	_ = tw.Close()
	_ = gz.Close()
	_ = f.Close()
}

func TestDeployAndDeployed(t *testing.T) {
	dir := t.TempDir()
	arch := filepath.Join(t.TempDir(), "HmIP-PDT_update_V2_2_4.tgz")
	writeBundle(t, arch, map[string]string{
		"info":                              "TypeCode=310\nName=HmIP-PDT\nFirmwareVersion=2.2.4\nCCU3FirmwareVersionMin=3.73.9\n",
		"HmIP-PDT_update_V2_2_4_231123.efw": "binary",
		"changelog.txt":                     "fixed things",
		"../evil":                           "traversal attempt",
	})
	b, err := Deploy(arch, dir)
	if err != nil || b.TypeCode != "310" || b.Name != "HmIP-PDT" || b.Version != "2.2.4" {
		t.Fatalf("deploy: %v %+v", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "310", "HmIP-PDT_update_V2_2_4_231123.efw")); err != nil {
		t.Fatal("efw must be in place")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("traversal must not escape the bundle directory")
	}
	if _, err := os.Stat(filepath.Join(dir, "310", "evil")); err != nil {
		t.Fatal("the traversal entry is flattened into the bundle, not dropped silently")
	}
	// a newer bundle replaces the old one atomically
	arch2 := filepath.Join(t.TempDir(), "next.tgz")
	writeBundle(t, arch2, map[string]string{"info": "TypeCode=310\nName=HmIP-PDT\nFirmwareVersion=2.2.6\n", "HmIP-PDT_update_V2_2_6.efw": "x"})
	if _, err := Deploy(arch2, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "310", "HmIP-PDT_update_V2_2_4_231123.efw")); err == nil {
		t.Fatal("old bundle files must be gone")
	}
	dep, err := Deployed(dir)
	if err != nil || len(dep) != 1 || dep[0].Version != "2.2.6" || dep[0].Name != "HmIP-PDT" {
		t.Fatalf("deployed: %v %+v", err, dep)
	}
	// bundles without info or without a firmware file are refused
	bad := filepath.Join(t.TempDir(), "bad.tgz")
	writeBundle(t, bad, map[string]string{"readme": "x"})
	if _, err := Deploy(bad, dir); err == nil {
		t.Fatal("no info must be refused")
	}
	bad2 := filepath.Join(t.TempDir(), "bad2.tgz")
	writeBundle(t, bad2, map[string]string{"info": "TypeCode=1\nName=X\n", "changelog.txt": "x"})
	if _, err := Deploy(bad2, dir); err == nil {
		t.Fatal("no firmware file must be refused")
	}
	if _, err := os.Stat(filepath.Join(dir, "1")); err == nil {
		t.Fatal("a refused bundle must leave nothing behind")
	}
	if d, err := Deployed(filepath.Join(dir, "missing")); err != nil || len(d) != 0 {
		t.Fatalf("missing dir: %v %v", err, d)
	}
}

// Deploy unpacks into StagingDir and moves the bundle into place through Priv: /etc/config/firmware
// is root's and occulited is not root since task 17 (B-20). The staging directory has to be on the
// same filesystem as the destination, so the test puts both under one temp root.
func TestDeployThroughStagingAndPriv(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "etc", "config", "firmware")
	staging := filepath.Join(root, "usr", "local", "etc", "occulite", "staging")
	arch := filepath.Join(t.TempDir(), "HmIP-PDT.tgz")
	writeBundle(t, arch, map[string]string{
		"info":          "TypeCode=311\nName=HmIP-XYZ\nFirmwareVersion=1.0.0\n",
		"fw_V1_0_0.efw": "binary",
	})
	rec := &countingPriv{}
	oldPriv, oldStaging := Priv, StagingDir
	Priv, StagingDir = rec, staging
	t.Cleanup(func() { Priv, StagingDir = oldPriv, oldStaging })

	b, err := Deploy(arch, dir)
	if err != nil || b.TypeCode != "311" {
		t.Fatalf("deploy: %v %+v", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "311", "fw_V1_0_0.efw")); err != nil {
		t.Fatal("the bundle must be in place")
	}
	if rec.renames == 0 || rec.mkdirs == 0 || rec.chmods == 0 {
		t.Errorf("the destination was not written through the privilege boundary: %+v", rec)
	}
	if entries, _ := os.ReadDir(staging); len(entries) != 0 {
		t.Errorf("staging not empty afterwards: %v", entries)
	}
}

// countingPriv counts what Deploy asks the boundary to do and does it locally.
type countingPriv struct {
	priv.Local
	renames, mkdirs, chmods int
}

func (c *countingPriv) Rename(src, dst string) error { c.renames++; return c.Local.Rename(src, dst) }

func (c *countingPriv) MkdirAll(path string, mode os.FileMode) error {
	c.mkdirs++
	return c.Local.MkdirAll(path, mode)
}

func (c *countingPriv) Chmod(path string, mode os.FileMode) error {
	c.chmods++
	return c.Local.Chmod(path, mode)
}

// B-195: a fetched bundle that needs a newer CCU firmware than the system's is refused before
// anything is written; Deploy (the manual upload) takes it.
func TestDeployForRefusesABundleForANewerSystem(t *testing.T) {
	dir := t.TempDir()
	arch := filepath.Join(t.TempDir(), "HmIP-E27.tgz")
	writeBundle(t, arch, map[string]string{"info": "TypeCode=999\nName=HmIP-E27\nFirmwareVersion=1.0.2\nCCU3FirmwareVersionMin=4.0.0\n", "HmIP-E27_update_V1_0_2.efw": "x"})
	_, err := DeployFor(arch, dir, "3.89.9.20260914")
	if !errors.Is(err, ErrNeedsNewerSystem) || !strings.Contains(err.Error(), "4.0.0") {
		t.Fatalf("want the refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "999")); err == nil {
		t.Fatal("nothing may be deployed")
	}
	if _, err := DeployFor(arch, dir, "4.1.0"); err != nil {
		t.Fatalf("a new enough system takes it: %v", err)
	}
	arch2 := filepath.Join(t.TempDir(), "old.tgz")
	writeBundle(t, arch2, map[string]string{"info": "TypeCode=261\nName=HmIP-WRC2\nFirmwareVersion=1.18.2\nCCU3FirmwareVersionMin=3.43.15\n", "HmIP-WRC2_update_V1_18_2_230207.efw": "x"})
	if _, err := DeployFor(arch2, dir, "3.89.9.20260914"); err != nil {
		t.Fatal(err)
	}
	if _, err := Deploy(arch, t.TempDir()); err != nil {
		t.Fatalf("the manual upload is not checked: %v", err)
	}
}

// B-195's lab check: occulited runs with umask 0077; the deployed files must still be readable
// for the interface processes, which do not run as root.
func TestDeployedFilesAreReadableWhateverTheUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	dir := t.TempDir()
	arch := filepath.Join(t.TempDir(), "b.tgz")
	writeBundle(t, arch, map[string]string{"info": "TypeCode=261\nName=HmIP-WRC2\nFirmwareVersion=1.18.2\n", "HmIP-WRC2_update_V1_18_2_230207.efw": "x"})
	if _, err := Deploy(arch, dir); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"info", "HmIP-WRC2_update_V1_18_2_230207.efw"} {
		fi, err := os.Stat(filepath.Join(dir, "261", n))
		if err != nil || fi.Mode().Perm() != 0o644 {
			t.Fatalf("%s: %v %v", n, err, fi.Mode())
		}
	}
}

// zipBytes builds an access point's firmware pack as eQ-3 ships it: a zip of .eq3 images and an
// available_versions.txt with CRLF lines (synthetic contents - no eQ-3 binary in the repository).
func zipBytes(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

var hapPack = map[string]string{
	"HMIP-HAP-application_update.eq3":      "app",
	"HMIP-HAP-coprocessor.eq3":             "cop",
	"HMIP-HAP-local_bootloader_update.eq3": "lbl",
	"HMIP-HAP-web_bootloader_update.eq3":   "wbl",
	"HMIP-HAP-available_versions.txt":      "local_bootloader=3.0.8\r\nweb_bootloader=3.0.18\r\napplication=3.0.18\r\ncoprocessor=4.4.34\r\nhap_firmware_pack=3.0.18\r\n",
}

const hapInfo = "TypeCode=270\nName=HMIP-HAP\nCCUFirmwareVersionMin=3.0.0\nCCU3FirmwareVersionMin=3.73.9\nHCU1FirmwareVersionMin=1.1.0\nFirmwareVersion=3.0.18\n"

// B-204: an access point bundle carries its firmware as one .zip - deployed as it is, into
// <TypeCode>/ with info and changelog; a .zip that is not one, or has no .eq3, is refused and
// nothing is written.
func TestDeployAccessPointZip(t *testing.T) {
	hap := zipBytes(t, hapPack)
	hap2 := zipBytes(t, map[string]string{"HmIP-HAP2-complete.eq3": "img", "HmIP-HAP2-available_versions.txt": "bootloader=2.0.0\r\nsecuremanager=2.0.0\r\napplication=1.0.48\r\nhap2_firmware_pack=1.0.48\r\n"})
	cases := []struct {
		name   string
		files  map[string]string
		code   string
		zip    string
		errStr string
	}{
		{"HmIP-HAP", map[string]string{"info": hapInfo, "changelog.txt": "3.0.18: things", "HMIP_HAP_update_V3_0_18_2023_09_29.zip": hap}, "270", "HMIP_HAP_update_V3_0_18_2023_09_29.zip", ""},
		{"HmIP-HAP2", map[string]string{"info": "TypeCode=572\nName=HmIP-HAP2\nFirmwareVersion=1.0.48\n", "HmIP-HAP2_update_V1_0_48.zip": hap2}, "572", "HmIP-HAP2_update_V1_0_48.zip", ""},
		{"upper-case .ZIP", map[string]string{"info": hapInfo, "HMIP_HAP_UPDATE.ZIP": hap}, "270", "HMIP_HAP_UPDATE.ZIP", ""},
		{"not a zip", map[string]string{"info": hapInfo, "HMIP_HAP_update.zip": "random bytes, not an archive"}, "", "", "unreadable access point firmware (HMIP_HAP_update.zip)"},
		{"a zip without .eq3", map[string]string{"info": hapInfo, "HMIP_HAP_update.zip": zipBytes(t, map[string]string{"readme.txt": "x"})}, "", "", "no .eq3 in it"},
		{"no firmware at all", map[string]string{"info": hapInfo, "changelog.txt": "x"}, "", "", "no firmware file (.efw/.eq3/.zip)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			arch := filepath.Join(t.TempDir(), "bundle.tgz")
			writeBundle(t, arch, c.files)
			b, err := Deploy(arch, dir)
			if c.errStr != "" {
				if err == nil || !strings.Contains(err.Error(), c.errStr) {
					t.Fatalf("want %q, got %v", c.errStr, err)
				}
				if left, _ := os.ReadDir(dir); len(left) != 0 {
					t.Fatalf("a refused bundle left %v", left)
				}
				return
			}
			if err != nil || b.TypeCode != c.code {
				t.Fatalf("deploy: %v %+v", err, b)
			}
			got, err := os.ReadFile(filepath.Join(dir, c.code, c.zip))
			if err != nil || string(got) != c.files[c.zip] {
				t.Fatalf("the zip must be in place byte for byte: %v", err)
			}
			for name := range c.files {
				fi, err := os.Stat(filepath.Join(dir, c.code, name))
				if err != nil || fi.Mode().Perm() != 0o644 {
					t.Fatalf("%s: %v %v", name, err, fi)
				}
			}
		})
	}
	// the name and the version come from info; the system-version guard applies to access points too
	arch := filepath.Join(t.TempDir(), "hap.tgz")
	writeBundle(t, arch, cases[0].files)
	if b, err := Deploy(arch, t.TempDir()); err != nil || b.Name != "HMIP-HAP" || b.Version != "3.0.18" {
		t.Fatalf("info: %v %+v", err, b)
	}
	if _, err := DeployFor(arch, t.TempDir(), "3.73.8"); !errors.Is(err, ErrNeedsNewerSystem) {
		t.Fatalf("an older system: %v", err)
	}
}
