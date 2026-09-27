package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/system"
)

// writeSBK puts a made-up CCU backup under the rig's /usr/local/tmp as /restore/check would.
func writeSBK(t *testing.T, root system.Root, name string, files map[string]string) {
	t.Helper()
	var inner bytes.Buffer
	gz := gzip.NewWriter(&inner)
	tw := tar.NewWriter(gz)
	for n, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = gz.Close()
	var outer bytes.Buffer
	ow := tar.NewWriter(&outer)
	for n, body := range map[string]string{"usr_local.tar.gz": inner.String(), "signature": "sig", "key_index": "0\n", "firmware_version": "VERSION=3.89.11\n"} {
		_ = ow.WriteHeader(&tar.Header{Name: n, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
		_, _ = ow.Write([]byte(body))
	}
	_ = ow.Close()
	dir := filepath.Join(string(root), system.BackupDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), outer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

var ccuBackup = map[string]string{
	"usr/local/etc/config/ids":                                     "BidCoS-Address=0xFF1234\nSerialNumber=1709ADFA00\n",
	"usr/local/etc/config/rfd/JEQ9000001.dev":                      "device",
	"usr/local/etc/config/crRFD/data/3014F711A0001F0000000A03.ap":  "ap",
	"usr/local/etc/config/crRFD/data/3014F711A000010000000A10.dev": "hmip",
	"usr/local/etc/config/hmip_address.conf":                       "Adapter.1.Address=BC0A08\n",
	"usr/local/etc/config/netconfig":                               "HOSTNAME=old\n",
}

// TestRestoreDevices: the view of a checked backup and this system, the refusals, the import with
// the reboot.
func TestRestoreDevices(t *testing.T) {
	rig := newPowerRig(t, true)
	root := string(rig.root)
	_ = os.MkdirAll(root+"/etc/config", 0o755)
	_ = os.WriteFile(root+"/etc/config/ids", []byte("BidCoS-Address=0xFF9999\nSerialNumber=0000000A01\n"), 0o644)
	writeSBK(t, rig.root, "restore-ccu.sbk", ccuBackup)
	writeSBK(t, rig.root, "restore-empty.sbk", map[string]string{"usr/local/etc/config/netconfig": "x"})
	old := system.PairedDevicesTimeout
	system.PairedDevicesTimeout = 2e9
	t.Cleanup(func() { system.PairedDevicesTimeout = old })
	rfd := listDevicesStub(t, "HM-RCV-50 BidCoS-RF", "HM-CC-TC")
	hmipFree := listDevicesStub(t, "HmIP-RFUSB")
	paired := true
	api := &SystemAPI{Root: rig.root, Manager: rig.m, Power: rig.power, RadioInterfaces: func() []interfaces.Interface {
		list := []struct{ Name, URL string }{{"HmIP-RF", "xmlrpc://" + strings.TrimPrefix(hmipFree.URL, "http://")}}
		if paired {
			list = append(list, struct{ Name, URL string }{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(rfd.URL, "http://")})
		}
		return interfaces.FromList(list)
	}}
	mux := http.NewServeMux()
	api.Register(mux)
	rig.srv.Close()
	rig.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mux.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "u", Role: rig.role, Scopes: auth.RoleScopes(rig.role)})))
	}))
	t.Cleanup(rig.srv.Close)

	// the view: the backup's counts and this system's pairing (one BidCos device here)
	st, out := rig.get(t, "/restore/devices?file=restore-ccu.sbk")
	if st != 200 {
		t.Fatalf("view: %d %v", st, out)
	}
	b := out["backup"].(map[string]any)
	if b["bidcos_rf"].(map[string]any)["devices"] != 1.0 || b["bidcos_rf"].(map[string]any)["address"] != "0xFF1234" || b["hmip"].(map[string]any)["devices"] != 1.0 || b["hmip"].(map[string]any)["identity_sgtin"] != "3014F711A0001F0000000A03" {
		t.Errorf("backup: %v", b)
	}
	tg := out["target"].(map[string]any)
	if tg["devices"] != 1.0 || tg["importable"] != false || tg["paired"].(map[string]any)["BidCos-RF"].(map[string]any)["devices"] != 1.0 {
		t.Errorf("target: %v", tg)
	}
	// the refusals: a paired system, a bad name, a missing upload, nothing to import
	if st, e, _ := rig.do(t, "POST", "/restore/import-devices", `{"file":"restore-ccu.sbk"}`); st != 409 || e.Error != "paired" {
		t.Errorf("paired: %d %+v", st, e)
	}
	if st, e, _ := rig.do(t, "POST", "/restore/import-devices", `{"file":"../etc/passwd"}`); st != 400 || e.Error != "invalid" {
		t.Errorf("bad name: %d %+v", st, e)
	}
	if st, _, _ := rig.do(t, "POST", "/restore/import-devices", `{"file":"restore-nope.sbk"}`); st != 404 {
		t.Errorf("missing: %d", st)
	}
	if st, _ := rig.get(t, "/restore/devices?file=x.sbk"); st != 400 {
		t.Errorf("view bad name: %d", st)
	}
	paired = false
	if st, e, _ := rig.do(t, "POST", "/restore/import-devices", `{"file":"restore-empty.sbk"}`); st != 422 || e.Error != "nothing_to_import" {
		t.Errorf("empty: %d %+v", st, e)
	}
	// a user may not
	rig.role = auth.RoleUser
	if st, _ := rig.get(t, "/restore/devices?file=restore-ccu.sbk"); st != 403 {
		t.Errorf("a user's view: %d", st)
	}
	rig.role = auth.RoleAdmin
	// the import onto the free system: the files land, this system's own go aside, the reboot
	st, e, out := rig.do(t, "POST", "/restore/import-devices", `{"file":"restore-ccu.sbk"}`)
	if st != 200 || out["ok"] != true || out["rebooting"] != true {
		t.Fatalf("import: %d %+v %v", st, e, out)
	}
	imp := out["imported"].(map[string]any)
	if imp["key_set"] != false || len(imp["written"].([]any)) != 5 || !strings.HasPrefix(imp["aside"].(string), "/etc/config/.import-devices-aside/") {
		t.Errorf("imported: %v", imp)
	}
	if got, _ := os.ReadFile(root + "/etc/config/ids"); string(got) != "BidCoS-Address=0xFF1234\nSerialNumber=1709ADFA00\n" {
		t.Errorf("ids: %q", got)
	}
	if got, _ := os.ReadFile(root + "/etc/config/rfd/JEQ9000001.dev"); string(got) != "device" {
		t.Errorf("device file: %q", got)
	}
	if _, err := os.Stat(root + "/etc/config/netconfig"); !errors.Is(err, os.ErrNotExist) {
		t.Error("netconfig came out of the backup")
	}
	if got, _ := os.ReadFile(root + imp["aside"].(string) + "/ids"); string(got) != "BidCoS-Address=0xFF9999\nSerialNumber=0000000A01\n" {
		t.Errorf("the old ids aside: %q", got)
	}
	if rig.m.Reboots() == 0 {
		t.Error("no reboot")
	}
}
