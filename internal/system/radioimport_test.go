package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// sbkFixture writes a CCU-shaped .sbk: the outer tar with usr_local.tar.gz (paths usr/local/...,
// owner root as createBackup.sh writes them), key_index, firmware_version and a signature. The
// contents are made up - nothing of a lab box is in it.
func sbkFixture(t *testing.T, files map[string]string, keyIndex string) string {
	t.Helper()
	var inner bytes.Buffer
	gz := gzip.NewWriter(&inner)
	tw := tar.NewWriter(gz)
	dirs := map[string]bool{}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, n := range names {
		d := filepath.Dir(n)
		for d != "." && d != "/" && !dirs[d] {
			dirs[d] = true
			_ = tw.WriteHeader(&tar.Header{Name: d + "/", Typeflag: tar.TypeDir, Mode: 0o755})
			d = filepath.Dir(d)
		}
		body := files[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	var outer bytes.Buffer
	ow := tar.NewWriter(&outer)
	add := func(name, body string) {
		_ = ow.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = ow.Write([]byte(body))
	}
	add("usr_local.tar.gz", inner.String())
	add("signature", "0123456789abcdef")
	add("key_index", keyIndex+"\n")
	add("firmware_version", "VERSION=3.89.11.20260919\n")
	_ = ow.Close()
	p := filepath.Join(t.TempDir(), "restore-fixture.sbk")
	if err := os.WriteFile(p, outer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// ccuFiles is a backup of a CCU with two BidCos-RF devices (one through a LAN gateway), three
// HmIP devices under an identity of an RPI-RF-MOD in local key mode, one wired device, and things
// the import must leave alone.
func ccuFiles() map[string]string {
	c := "usr/local/etc/config/"
	return map[string]string{
		c + "ids":                 "BidCoS-Address=0xFF1234\nSerialNumber=1709ADFA00\n",
		c + "keys":                "not-a-real-key-store-just-bytes",
		c + "crypttool.cfg":       "index=2\n",
		c + "rfd/JEQ9000001.dev":  "device one",
		c + "rfd/JEQ9000001.meta": "meta",
		c + "rfd/KEQ9000003.dev":  "device two",
		c + "rfd.conf":            "Listen IP = 127.0.0.1\n[Interface 0]\nType = CCU2\n[Interface 1]\nType = HMLGW2\nSerial Number = KEQ0123456\nIP Address = 192.0.2.1\n",
		c + "crRFD/data/3014F711A0001F0000000A03.ap":       "ap",
		c + "crRFD/data/3014F711A0001F0000000A03.apkx":     "apkx",
		c + "crRFD/data/3014F711A0001F0000000A03.bbkx":     "bbkx",
		c + "crRFD/data/3014F711A000010000000A10.dev":      "hmip one",
		c + "crRFD/data/3014F711A0000D0000000A11.dev":      "hmip two",
		c + "crRFD/data/30150377DC00030000000A13.dev":      "hmip three",
		c + "crRFD/data/linkData.conf":                     "links",
		c + "crRFD/hmip_user.conf":                         "Adapter.1.Port=/dev/raw-uart\nNetwork.Key=00112233445566778899AABBCCDDEEFF\nKeyServer.Mode=LOCAL\n",
		c + "crRFD/sgtin.map":                              "3014F711A000010000000A10 KEY\n",
		c + "hmip_address.conf":                            "Adapter.1.Address=BC0A08\n",
		c + "hs485d/LEQ0000001.dev":                        "wired one",
		c + "hs485d.conf":                                  "[Interface 0]\nType = HMWLGW\nSerial Number = JEQ0000001\n",
		c + "InterfacesList.xml":                           "<interfaces/>",
		c + "homematic.regadom":                            "rega",
		c + "netconfig":                                    "HOSTNAME=old-ccu\n",
		"usr/local/addons/redmatic/VERSION":                "9.8.0",
		"usr/local/etc/occulite/users.json":                "{}",
		"usr/local/etc/config/.import-devices-aside/x/ids": "never taken",
	}
}

func TestInspectRadioBackup(t *testing.T) {
	sbk := sbkFixture(t, ccuFiles(), "2")
	b, err := InspectRadioBackup(sbk)
	if err != nil {
		t.Fatal(err)
	}
	if b.BidCosRF.Devices != 2 || b.BidCosRF.Address != "0xFF1234" || b.BidCosRF.Serial != "1709ADFA00" || !b.BidCosRF.HasKey || b.BidCosRF.Gateways != 1 {
		t.Errorf("BidCos-RF: %+v", b.BidCosRF)
	}
	if b.HmIP.Devices != 3 || b.HmIP.IdentitySGTIN != "3014F711A0001F0000000A03" || !b.HmIP.LocalKey || !b.HmIP.DeviceKeyMap {
		t.Errorf("HmIP: %+v", b.HmIP)
	}
	if b.BidCosWired.Devices != 1 || b.BidCosWired.Gateways != 1 {
		t.Errorf("Wired: %+v", b.BidCosWired)
	}
	if b.KeyIndex != 2 || b.Version != "3.89.11.20260919" || b.Empty() {
		t.Errorf("index %d version %q empty %v", b.KeyIndex, b.Version, b.Empty())
	}
	for _, f := range b.Files {
		if strings.Contains(f, "regadom") || strings.Contains(f, "InterfacesList") || strings.Contains(f, "netconfig") || strings.Contains(f, "aside") || strings.Contains(f, "addons") {
			t.Errorf("a file the import must not take: %s", f)
		}
	}
	if len(b.Files) != 19 {
		t.Errorf("files: %d %v", len(b.Files), b.Files)
	}
	// a backup without radio files
	empty, err := InspectRadioBackup(sbkFixture(t, map[string]string{"usr/local/etc/config/netconfig": "x"}, "0"))
	if err != nil || !empty.Empty() || empty.KeyIndex != 0 {
		t.Errorf("empty: %+v %v", empty, err)
	}
	// not a backup at all
	notSBK := filepath.Join(t.TempDir(), "x.sbk")
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "hello", Size: 2, Mode: 0o644})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.Close()
	_ = os.WriteFile(notSBK, buf.Bytes(), 0o600)
	if _, err := InspectRadioBackup(notSBK); !errors.Is(err, ErrNotSBK) {
		t.Errorf("not an sbk: %v", err)
	}
	if _, err := InspectRadioBackup(filepath.Join(t.TempDir(), "missing.sbk")); err == nil {
		t.Error("a missing file")
	}
}

func TestRadioMemberOK(t *testing.T) {
	for _, c := range []struct {
		h  tar.Header
		ok bool
	}{
		{tar.Header{Name: "usr/local/etc/config/rfd/a.dev", Typeflag: tar.TypeReg}, true},
		{tar.Header{Name: "usr/local/etc/config/crRFD/data/", Typeflag: tar.TypeDir}, true},
		{tar.Header{Name: "usr/local/etc/config/rfd/../../../../etc/passwd", Typeflag: tar.TypeReg}, false},
		{tar.Header{Name: "usr/local/etc/config/keys", Typeflag: tar.TypeSymlink, Linkname: "/etc/shadow"}, false},
		{tar.Header{Name: "/usr/local/etc/config/ids", Typeflag: tar.TypeReg}, false},
	} {
		if got := radioMemberOK(&c.h); got != c.ok {
			t.Errorf("%s (%c): %v", c.h.Name, c.h.Typeflag, got)
		}
	}
	if radioMember("usr/local/etc/config/netconfig") || radioMember("usr/local/addons/x") || !radioMember("usr/local/etc/config/crRFD/sgtin.map") || !radioMember("usr/local/etc/config/ids") {
		t.Error("radioMember")
	}
}

func TestImportRadio(t *testing.T) {
	old := Priv
	Priv = priv.Local{}
	t.Cleanup(func() { Priv = old })
	root := t.TempDir()
	r := Root(root)
	cfg := filepath.Join(root, "etc/config")
	// the target: its own module's identity, its own address file, no devices
	for p, body := range map[string]string{
		"ids":                                    "BidCoS-Address=0xFF9999\nSerialNumber=0000000A01\n",
		"crRFD/data/3014F711A000040000000A01.ap": "target ap",
		"crRFD/hmip_user.conf":                   "Adapter.1.Port=/dev/raw-uart\n",
		"hmip_address.conf":                      "Adapter.1.Address=AAAAAA\n",
		"rfd.conf":                               "Listen IP = 127.0.0.1\n",
		"netconfig":                              "HOSTNAME=lab\n",
	} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(cfg, p)), 0o755)
		_ = os.WriteFile(filepath.Join(cfg, p), []byte(body), 0o644)
	}
	_ = os.MkdirAll(filepath.Join(cfg, "rfd"), 0o755)
	sbk := sbkFixture(t, ccuFiles(), "2")
	res, err := r.ImportRadio(context.Background(), sbk)
	if err != nil {
		t.Fatal(err)
	}
	// task 278: the backup's key store (key_index 2) comes along and is said so; the target had
	// no key store of its own
	if len(res.Written) != 19 || res.Backup.HmIP.Devices != 3 || !res.NonDefaultKey || res.TargetKeyReplaced || !res.Backup.NonDefaultKey() {
		t.Fatalf("result: %+v", res)
	}
	// the backup's files are in place, the target's aside, the rest untouched
	read := func(p string) string { b, _ := os.ReadFile(filepath.Join(cfg, p)); return string(b) }
	if read("ids") != "BidCoS-Address=0xFF1234\nSerialNumber=1709ADFA00\n" || read("rfd/JEQ9000001.dev") != "device one" || read("crRFD/data/30150377DC00030000000A13.dev") != "hmip three" || read("hs485d/LEQ0000001.dev") != "wired one" || read("crRFD/sgtin.map") == "" || read("hmip_address.conf") != "Adapter.1.Address=BC0A08\n" {
		t.Error("the backup's files are not in place")
	}
	if _, err := os.Stat(filepath.Join(cfg, "crRFD/data/3014F711A000040000000A01.ap")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the target's identity file stayed")
	}
	if read("netconfig") != "HOSTNAME=lab\n" {
		t.Error("netconfig was touched")
	}
	if _, err := os.Stat(filepath.Join(cfg, "InterfacesList.xml")); !errors.Is(err, os.ErrNotExist) {
		t.Error("InterfacesList.xml was taken from the backup")
	}
	if _, err := os.Stat(filepath.Join(cfg, "homematic.regadom")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the ReGa database was taken")
	}
	if res.Aside == "" || !strings.HasPrefix(res.Aside, asideDir+"/") {
		t.Fatalf("aside: %q", res.Aside)
	}
	aside := filepath.Join(root, res.Aside)
	if b, _ := os.ReadFile(filepath.Join(aside, "ids")); string(b) != "BidCoS-Address=0xFF9999\nSerialNumber=0000000A01\n" {
		t.Errorf("the target's ids are not aside: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(aside, "crRFD/data/3014F711A000040000000A01.ap")); string(b) != "target ap" {
		t.Error("the target's identity is not aside")
	}
	if b, _ := os.ReadFile(filepath.Join(aside, "hmip_address.conf")); string(b) != "Adapter.1.Address=AAAAAA\n" {
		t.Error("the target's hmip_address.conf is not aside")
	}
	if st, _ := os.Stat(filepath.Join(cfg, "keys")); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf("keys mode: %v", st)
	}
	if st, _ := os.Stat(filepath.Join(cfg, "crRFD/hmip_user.conf")); st == nil || st.Mode().Perm() != 0o600 {
		t.Errorf("hmip_user.conf mode: %v", st)
	}
	if st, _ := os.Stat(filepath.Join(cfg, "rfd.conf")); st == nil || st.Mode().Perm() != 0o640 {
		t.Errorf("rfd.conf mode: %v", st)
	}
	// an empty backup is refused before anything moves
	root2 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root2, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(root2, "etc/config/ids"), []byte("x"), 0o644)
	if _, err := Root(root2).ImportRadio(context.Background(), sbkFixture(t, map[string]string{"usr/local/etc/config/netconfig": "x"}, "0")); err == nil {
		t.Error("an empty backup went through")
	}
	if b, _ := os.ReadFile(filepath.Join(root2, "etc/config/ids")); string(b) != "x" {
		t.Error("the target's ids moved for an empty backup")
	}
	// a target with a key store of its own: the import says it was replaced (set aside)
	root4 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root4, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(root4, "etc/config/keys"), []byte("the target's own store"), 0o600)
	res4, err := Root(root4).ImportRadio(context.Background(), sbkFixture(t, ccuFiles(), "1"))
	if err != nil || !res4.TargetKeyReplaced || !res4.NonDefaultKey {
		t.Fatalf("target with a key: %+v %v", res4, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root4, res4.Aside, "keys")); string(b) != "the target's own store" {
		t.Errorf("the target's key store is not aside: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root4, "etc/config/keys")); string(b) != "not-a-real-key-store-just-bytes" {
		t.Errorf("the backup's key store is not in place: %q", b)
	}
	// a hostile member never lands
	files := ccuFiles()
	files["usr/local/etc/config/rfd/../../../../../tmp/evil"] = "evil"
	root3 := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root3, "etc/config"), 0o755)
	if _, err := Root(root3).ImportRadio(context.Background(), sbkFixture(t, files, "0")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root3, "tmp/evil")); !errors.Is(err, os.ErrNotExist) {
		t.Error("a climbing member landed")
	}
}
