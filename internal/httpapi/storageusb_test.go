package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/system"
)

type fakeUSBHelper struct{ calls []string }

func (f *fakeUSBHelper) USBFormat(_ context.Context, s priv.USBFormatSpec) error {
	f.calls = append(f.calls, "format "+s.Device+" "+s.FS+" "+s.Label)
	return nil
}
func (f *fakeUSBHelper) USBEject(_ context.Context, dev string) error {
	f.calls = append(f.calls, "eject "+dev)
	return nil
}

// openccu-lite task 228: the list, Format and Safely remove through the helper.
func TestStorageUSBRoutes(t *testing.T) {
	r := fakeRoot(t)
	real := filepath.Join(string(r), "sys/devices/pci0/usb1/1-1/host0/block/sda")
	_ = os.MkdirAll(filepath.Join(real, "sda1"), 0o755)
	_ = os.WriteFile(filepath.Join(real, "size"), []byte("62521344\n"), 0o644)
	_ = os.WriteFile(filepath.Join(real, "sda1", "size"), []byte("62519296\n"), 0o644)
	_ = os.WriteFile(filepath.Join(real, "removable"), []byte("1\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(real, "device"), 0o755)
	_ = os.WriteFile(filepath.Join(real, "device", "model"), []byte("Ultra Fit       \n"), 0o644)
	_ = os.MkdirAll(filepath.Join(string(r), "sys/block"), 0o755)
	_ = os.Symlink(real, filepath.Join(string(r), "sys/block/sda"))
	sata := filepath.Join(string(r), "sys/devices/pci0/ata1/host1/block/sdb")
	_ = os.MkdirAll(sata, 0o755)
	_ = os.Symlink(sata, filepath.Join(string(r), "sys/block/sdb"))
	_ = os.MkdirAll(filepath.Join(string(r), "usr/bin"), 0o755)
	_ = os.WriteFile(filepath.Join(string(r), "usr/bin/mkfs.exfat"), nil, 0o755)

	h := &fakeUSBHelper{}
	old := usbHelperOf
	usbHelperOf = func() (usbDiskHelper, bool) { return h, true }
	t.Cleanup(func() { usbHelperOf = old })
	mux := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	st, out, _ := do(t, srv, "GET", "/api/system/v1/storage/usb", "", nil)
	disks, _ := out["disks"].([]any)
	if st != 200 || len(disks) != 1 {
		t.Fatalf("list: %d %v", st, out)
	}
	d := disks[0].(map[string]any)
	if d["name"] != "sda" || d["model"] != "Ultra Fit" || d["size_bytes"] != float64(62521344*512) || d["removable"] != true {
		t.Errorf("disk: %v", d)
	}
	if fs := out["filesystems"].([]any); len(fs) != 1 || fs[0] != "exfat" {
		t.Errorf("filesystems: %v", fs)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/storage/usb/sda/format", `{"fs":"exfat","label":"THIS_LABEL_IS_TOO_LONG"}`, nil); st != 422 {
		t.Errorf("a long label: %d", st)
	}
	if st, _, _ := do(t, srv, "POST", "/api/system/v1/storage/usb/sdb/format", `{"fs":"exfat","label":"X"}`, nil); st != 404 {
		t.Errorf("a SATA disk: %d", st)
	}
	st, out, _ = do(t, srv, "POST", "/api/system/v1/storage/usb/sda/format", `{"fs":"ext4","label":"LOGSTICK"}`, nil)
	if st != 200 || out["formatted"] != "sda" {
		t.Fatalf("format: %d %v", st, out)
	}
	st, out, _ = do(t, srv, "POST", "/api/system/v1/storage/usb/sda/eject", "", nil)
	if st != 200 || out["ejected"] != "sda" {
		t.Fatalf("eject: %d %v", st, out)
	}
	if len(h.calls) != 2 || h.calls[0] != "format sda ext4 LOGSTICK" || h.calls[1] != "eject sda" {
		t.Errorf("helper: %q", h.calls)
	}
	var _ = system.USBLoopTestMarker
}
