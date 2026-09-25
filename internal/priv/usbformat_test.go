package priv

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// openccu-lite task 228: formatting and ejecting a USB stick - which disks the helper touches at
// all, and the fixed steps it runs for one.

type recordRun struct {
	Local
	mu   sync.Mutex
	cmds []string
}

func (r *recordRun) Run(_ context.Context, name string, args []string, stdin []byte) (Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := name + " " + strings.Join(args, " ")
	if len(stdin) > 0 {
		c += " <" + strings.ReplaceAll(string(stdin), "\n", "|")
	}
	r.cmds = append(r.cmds, c)
	return Result{}, nil
}

// usbRoot builds a root with sda on the USB bus (sda1 mounted at /media/usb1), sdb on SATA, sdc on
// USB but holding /usr/local, and loop0.
func usbRoot(t *testing.T) string {
	root := t.TempDir()
	mk := func(dev, bus string, parts ...string) {
		real := filepath.Join(root, "sys/devices/platform/"+bus+"/block", dev)
		for _, p := range parts {
			_ = os.MkdirAll(filepath.Join(real, p), 0o755)
		}
		_ = os.MkdirAll(real, 0o755)
		_ = os.MkdirAll(filepath.Join(root, "sys/block"), 0o755)
		_ = os.Symlink(real, filepath.Join(root, "sys/block", dev))
	}
	mk("sda", "usb1/1-1", "sda1")
	mk("sdb", "ata1", "sdb1")
	mk("sdc", "usb2/2-1", "sdc1")
	_ = os.MkdirAll(filepath.Join(root, "proc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "proc/mounts"), []byte("/dev/mmcblk0p2 / ext4 ro 0 0\n/dev/sda1 /media/usb1 exfat rw 0 0\n/dev/sdc1 /usr/local ext4 rw 0 0\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "proc/swaps"), []byte("Filename Type Size Used Priority\n"), 0o644)
	return root
}

func TestUSBDiskOK(t *testing.T) {
	root := usbRoot(t)
	p := DefaultPolicy(root, "/usr/local/etc/occulite")
	if err := p.usbDiskOK("sda"); err != nil {
		t.Errorf("sda: %v", err)
	}
	for dev, why := range map[string]string{
		"sdb":         "not on the USB bus",
		"sdc":         "runs from",
		"sdz":         "no such disk",
		"sda1":        "not a USB disk's name",
		"mmcblk0":     "not a USB disk's name",
		"nvme0n1":     "not a USB disk's name",
		"loop0":       "only on a lab system",
		"../sda":      "not a USB disk's name",
		"sda; reboot": "not a USB disk's name",
	} {
		if err := p.usbDiskOK(dev); err == nil || !strings.Contains(err.Error(), why) {
			t.Errorf("%s: %v, want %q", dev, err, why)
		}
	}
	_ = os.MkdirAll(filepath.Join(root, "usr/local/etc/occulite"), 0o755)
	_ = os.WriteFile(filepath.Join(root, USBTestLoopMarker), nil, 0o644)
	if err := p.usbDiskOK("loop0"); err != nil {
		t.Errorf("loop0 with the lab marker: %v", err)
	}
	_ = os.WriteFile(filepath.Join(root, "proc/swaps"), []byte("Filename\n/dev/sda2 partition 1 0 -2\n"), 0o644)
	if err := p.usbDiskOK("sda"); err == nil {
		t.Error("a stick holding swap")
	}
}

func TestOnDiskAndLabels(t *testing.T) {
	for src, want := range map[string]bool{"/dev/sda": true, "/dev/sda1": true, "/dev/sda12": true, "/dev/sdab": false, "/dev/sdb1": false, "sda1": false, "/dev/loop0p1": true, "/dev/loop10": false, "/dev/loop0": true} {
		dev := "sda"
		if strings.Contains(src, "loop") {
			dev = "loop0"
		}
		if onDisk(src, dev) != want {
			t.Errorf("%s on %s: %v", src, dev, !want)
		}
	}
	for _, c := range []struct {
		fs, label string
		ok        bool
	}{{"exfat", "OPENCCU", true}, {"exfat", "ABCDEFGHIJKLMNO", true}, {"exfat", "ABCDEFGHIJKLMNOP", false}, {"ext4", "ABCDEFGHIJKLMNOP", true}, {"ext4", "", false}, {"ext4", "a b", false}, {"ext4", "x/y", false}, {"vfat", "OK", false}, {"ntfs", "OK", false}} {
		if (ValidUSBLabel(c.fs, c.label) == nil) != c.ok {
			t.Errorf("%s %q", c.fs, c.label)
		}
	}
}

func usbHelper(t *testing.T, root string) (Client, *recordRun) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ops := &recordRun{}
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite"), Ops: ops}
	go func() { _ = srv.Serve(ctx, l) }()
	return Client{Socket: sock}, ops
}

func TestUSBFormatSteps(t *testing.T) {
	root := usbRoot(t)
	_ = os.MkdirAll(filepath.Join(root, "dev"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "dev/sda1"), nil, 0o644)
	c, ops := usbHelper(t, root)
	ctx := context.Background()
	if err := c.USBFormat(ctx, USBFormatSpec{Device: "sda", FS: "exfat", Label: "OPENCCU"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemctl stop occu-usb-mount@sda.service",
		"systemctl stop occu-usb-mount@sda1.service",
		"umount /media/usb1",
		"sfdisk --wipe always --wipe-partitions always /dev/sda <label: dos|,,7|",
		"udevadm settle --timeout=30",
		"mkfs.exfat -n OPENCCU /dev/sda1",
		"udevadm settle --timeout=30",
		"systemctl start occu-usb-mount@sda1.service",
	}
	if strings.Join(ops.cmds, "\n") != strings.Join(want, "\n") {
		t.Errorf("exfat:\n got  %q\n want %q", ops.cmds, want)
	}
	ops.cmds = nil
	if err := c.USBFormat(ctx, USBFormatSpec{Device: "sda", FS: "ext4", Label: "LOGSTICK"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ops.cmds, "\n"); !strings.Contains(got, "sfdisk --wipe always --wipe-partitions always /dev/sda <label: dos|,,83|") || !strings.Contains(got, "mkfs.ext4 -F -q -m 0 -L LOGSTICK /dev/sda1") {
		t.Errorf("ext4: %q", ops.cmds)
	}
	ops.cmds = nil
	for _, s := range []USBFormatSpec{{Device: "sdb", FS: "exfat", Label: "X"}, {Device: "sdc", FS: "ext4", Label: "X"}, {Device: "sda", FS: "vfat", Label: "X"}, {Device: "sda", FS: "exfat", Label: "WAY_TOO_LONG_LABEL"}, {Device: "sda1", FS: "exfat", Label: "X"}} {
		if err := c.USBFormat(ctx, s); !errors.Is(err, ErrRefused) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	if err := c.USBEject(ctx, "sdb"); !errors.Is(err, ErrRefused) {
		t.Errorf("eject sdb: %v", err)
	}
	if len(ops.cmds) != 0 {
		t.Errorf("a refused request ran %q", ops.cmds)
	}
	if err := c.USBEject(ctx, "sda"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(ops.cmds, "\n") != "systemctl stop occu-usb-mount@sda.service\nsystemctl stop occu-usb-mount@sda1.service\numount /media/usb1" {
		t.Errorf("eject: %q", ops.cmds)
	}
}
