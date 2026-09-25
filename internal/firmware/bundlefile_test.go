package firmware

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The version rule on the info files Deployed reads: eQ-3's all-zero placeholder, an empty key and
// a missing key are all "no version"; the info map keeps what the file says.
func TestDeployedVersionRule(t *testing.T) {
	cases := []struct {
		name, info, want string
		raw              string
		hasRaw           bool
	}{
		{"placeholder", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.0.0\n", "", "0.0.0", true},
		{"single zero", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0\n", "", "0", true},
		{"empty", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=\n", "", "", true},
		{"missing", "TypeCode=4107\nName=HmIPW-DRS8\n", "", "", false},
		{"real", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=1.2.6\n", "1.2.6", "1.2.6", true},
		{"zero major", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.9.0\n", "0.9.0", "0.9.0", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			b := filepath.Join(dir, "4107")
			_ = os.MkdirAll(b, 0o755)
			_ = os.WriteFile(filepath.Join(b, "info"), []byte(c.info), 0o644)
			dep, err := Deployed(dir)
			if err != nil || len(dep) != 1 {
				t.Fatalf("deployed: %v %+v", err, dep)
			}
			if dep[0].Version != c.want {
				t.Errorf("version %q, want %q", dep[0].Version, c.want)
			}
			raw, ok := dep[0].Info["FirmwareVersion"]
			if ok != c.hasRaw || raw != c.raw {
				t.Errorf("info keeps %q (%v), want %q (%v)", raw, ok, c.raw, c.hasRaw)
			}
		})
	}
}

// The version an update file's name carries, for a bundle whose info states none: the names
// eQ-3 ships (the three on the lab's Charly among them), and what is not read as a version.
func TestVersionFromName(t *testing.T) {
	cases := []struct {
		name          string
		files         []string
		version, date string
	}{
		{"yymmdd", []string{"HmIPW-DRS8_update_V1_2_6_220928.efw", "changelog.txt", "info"}, "1.2.6", "2022-09-28"},
		{"the first file is a text file", []string{"changelog.txt", "HmIPW-DRI16_update_V1_4_6_250526.efw", "info"}, "1.4.6", "2025-05-26"},
		{"yyyy_mm_dd", []string{"HmIPW-DRAP_update_V3_0_36_2024_12_18.zip"}, "3.0.36", "2024-12-18"},
		{"yyyymmdd", []string{"HM-CC-RT-DN_update_V1_5_20190614.eq3"}, "1.5", "2019-06-14"},
		{"leading zeros", []string{"hm_cc_rt_dn_update_V1_05_001_161212.eq3"}, "1.5.1", "2016-12-12"},
		{"lower-case v", []string{"HmIP-PDT_update_v2_2_4_231123.efw"}, "2.2.4", "2023-11-23"},
		{"no date", []string{"HmIP-BWTH_update_V1_4_8.eq3"}, "1.4.8", ""},
		{"not a date: no version either", []string{"HmIP-X_update_V1_2_991399.efw"}, "", ""},
		{"a date alone is no version", []string{"HmIP-X_update_V2_220928.efw"}, "", ""},
		{"one part is no version", []string{"HmIP-X_update_V7.efw"}, "", ""},
		{"no update name", []string{"dualcopro_update_blhmip-4.4.18.eq3", "info"}, "", ""},
		{"nothing", nil, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, d := VersionFromName(c.files)
			if v != c.version || d != c.date {
				t.Errorf("VersionFromName(%v) = %q, %q; want %q, %q", c.files, v, d, c.version, c.date)
			}
		})
	}
}

// Deployed sends the name's version only for a bundle whose info states none.
func TestDeployedVersionFromName(t *testing.T) {
	cases := []struct {
		name, firmwareVersion, version, fromName, date string
	}{
		{"placeholder", "0.0.0", "", "1.2.6", "2022-09-28"},
		{"stated", "1.2.6", "1.2.6", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			b := filepath.Join(dir, "4107")
			_ = os.MkdirAll(b, 0o755)
			_ = os.WriteFile(filepath.Join(b, "info"), []byte("TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion="+c.firmwareVersion+"\n"), 0o644)
			_ = os.WriteFile(filepath.Join(b, "HmIPW-DRS8_update_V1_2_6_220928.efw"), []byte{0}, 0o644)
			dep, err := Deployed(dir)
			if err != nil || len(dep) != 1 {
				t.Fatalf("deployed: %v %+v", err, dep)
			}
			if got := dep[0]; got.Version != c.version || got.VersionFromName != c.fromName || got.DateFromName != c.date {
				t.Errorf("got %q / %q / %q, want %q / %q / %q", got.Version, got.VersionFromName, got.DateFromName, c.version, c.fromName, c.date)
			}
		})
	}
}

// A bundle as the fetcher leaves it, plus what must be refused: a symlink out of the bundle, a
// file over the cap, a Latin-1 changelog, a directory.
func bundleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	b := filepath.Join(dir, "4107")
	w := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(b, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(b, "docs.txt"), 0o755) // a directory with a text name
	w("info", []byte("TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.0.0\n"))
	w("changelog.txt", []byte("\xef\xbb\xbfVersion 1.2.6\n- fixed things\n"))
	w("HmIPW-DRS8_update_V1_2_6_220928.efw", []byte{0x00, 0x01, 0x02})
	w("alt.log", []byte("Verbesserung f\xfcr Ger\xe4te \x80 \x93quoted\x94 \x81\n"))
	w("big.md", []byte(strings.Repeat("x", MaxViewSize+1)))
	w("edge.md", []byte(strings.Repeat("y", MaxViewSize)))
	w("empty.txt", nil)
	secret := filepath.Join(dir, "secret")
	_ = os.WriteFile(secret, []byte("root only"), 0o644)
	if err := os.Symlink(secret, filepath.Join(b, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	// a bundle directory that is itself a symlink is not a bundle
	_ = os.Symlink(b, filepath.Join(dir, "9999"))
	return dir
}

func TestReadBundleFile(t *testing.T) {
	dir := bundleFixture(t)
	cases := []struct {
		name, typeCode, file string
		want                 string
		err                  error
		tooLarge             bool
	}{
		{"changelog, the byte order mark dropped", "4107", "changelog.txt", "Version 1.2.6\n- fixed things\n", nil, false},
		{"info", "4107", "info", "TypeCode=4107\nName=HmIPW-DRS8\nFirmwareVersion=0.0.0\n", nil, false},
		{"Latin-1 and the Windows-1252 range become UTF-8", "4107", "alt.log", "Verbesserung für Geräte € “quoted” \u0081\n", nil, false},
		{"exactly the cap", "4107", "edge.md", strings.Repeat("y", MaxViewSize), nil, false},
		{"an empty file", "4107", "empty.txt", "", nil, false},
		{"not in the bundle", "4107", "readme.txt", "", ErrNoSuchFile, false},
		{"a name from another bundle's directory", "4107", "secret", "", ErrNoSuchFile, false},
		{"dot dot", "4107", "..", "", ErrNoSuchFile, false},
		{"a slash", "4107", "../secret", "", ErrNoSuchFile, false},
		{"a backslash", "4107", "..\\secret", "", ErrNoSuchFile, false},
		{"a symlink", "4107", "notes.txt", "", ErrNoSuchFile, false},
		{"a directory", "4107", "docs.txt", "", ErrNoSuchFile, false},
		{"a firmware image", "4107", "HmIPW-DRS8_update_V1_2_6_220928.efw", "", ErrNotViewable, false},
		{"over the cap", "4107", "big.md", "", nil, true},
		{"an unknown bundle", "310", "info", "", ErrNoSuchFile, false},
		{"a type code outside the shape", "../4107", "info", "", ErrNoSuchFile, false},
		{"a dot type code", "..", "4107", "", ErrNoSuchFile, false},
		{"a symlinked bundle", "9999", "info", "", ErrNoSuchFile, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ReadBundleFile(dir, c.typeCode, c.file)
			var tl *TooLargeError
			switch {
			case c.tooLarge:
				if !errors.As(err, &tl) || tl.Size != MaxViewSize+1 || tl.Limit != MaxViewSize {
					t.Fatalf("want a too-large error with the size, got %v", err)
				}
			case c.err != nil:
				if !errors.Is(err, c.err) {
					t.Fatalf("want %v, got %v (%q)", c.err, err, got)
				}
			default:
				if err != nil || string(got) != c.want {
					t.Fatalf("got %q, %v; want %q", got, err, c.want)
				}
			}
		})
	}
}

func TestViewable(t *testing.T) {
	for name, want := range map[string]bool{
		"info": true, "changelog.txt": true, "README.MD": true, "update.log": true,
		"fw.efw": false, "fw.eq3": false, "fw.hex": false, "fw.bin": false, "fw.gbl": false, "information": false, "info.bak": false,
		"HMIP_HAP_update_V3_0_18_2023_09_29.zip": false, // B-204: an access point firmware pack
	} {
		if Viewable(name) != want {
			t.Errorf("Viewable(%q) = %v", name, !want)
		}
	}
}
