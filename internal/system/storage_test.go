package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// task 69: the storage health panel against sysfs trees shaped like the lab boxes (the Pi 4's
// SanDisk card, the Charly's Samsung card, the OVA's QEMU disk behind virtio-scsi, measured on
// 2026-09-12) and like the boards the lab does not have (an eMMC, a USB SSD, an NVMe, a SATA SSD)

type sysTree struct {
	t    *testing.T
	root string
}

func newSysTree(t *testing.T) sysTree { return sysTree{t: t, root: t.TempDir()} }

func (f sysTree) file(p, content string) {
	f.t.Helper()
	full := filepath.Join(f.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content+"\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f sysTree) link(p, target string) {
	f.t.Helper()
	full := filepath.Join(f.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Symlink(target, full); err != nil {
		f.t.Fatal(err)
	}
}

// disk lays out a disk the way sysfs does: the node under /sys/devices/<devpath>/block/<name>, the
// /sys/block link to it, device pointing at <devpath> with the attributes, and the partitions
// (name -> major:minor) as directories with a partition file.
func (f sysTree) disk(name, devpath string, sectors int64, dev, stat string, attrs map[string]string, parts map[string]string) {
	blk := "sys/devices/" + devpath + "/block/" + name
	f.file(blk+"/size", strconv.FormatInt(sectors, 10))
	f.file(blk+"/dev", dev)
	f.file(blk+"/stat", stat)
	for k, v := range attrs {
		f.file("sys/devices/"+devpath+"/"+k, v)
	}
	f.link(blk+"/device", "../..")
	for p, d := range parts {
		f.file(blk+"/"+p+"/dev", d)
		f.file(blk+"/"+p+"/partition", strings.TrimPrefix(strings.TrimLeft(p, "abcdefghijklmnopqrstuvwxyz"), "0p"))
	}
	f.link("sys/block/"+name, "../devices/"+devpath+"/block/"+name)
}

func (f sysTree) ext4(name string, errors, first, last, lifetimeKB int64) {
	d := "sys/fs/ext4/" + name + "/"
	f.file(d+"errors_count", strconv.FormatInt(errors, 10))
	f.file(d+"first_error_time", strconv.FormatInt(first, 10))
	f.file(d+"last_error_time", strconv.FormatInt(last, 10))
	f.file(d+"lifetime_write_kbytes", strconv.FormatInt(lifetimeKB, 10))
}

// the non-disks every box has
func (f sysTree) noise() {
	for _, n := range []string{"loop0", "ram0", "zram0"} {
		f.file("sys/devices/virtual/block/"+n+"/size", "1024")
		f.link("sys/block/"+n, "../devices/virtual/block/"+n)
	}
}

var storageNow = time.Date(2026, 9, 12, 11, 0, 0, 0, time.UTC)

// the Pi 4 of 2026-09-12: a 64 GB SanDisk card, rootfs read-only, the userfs, the unit's bind mount
func pi4Tree(t *testing.T) sysTree {
	f := newSysTree(t)
	f.noise()
	f.disk("mmcblk0", "platform/emmc2bus/fe340000.mmc/mmc_host/mmc0/mmc0:aaaa", 124735488, "179:0",
		"    9547     1609   615793    15642      713      649    18106    13856        0    17336    33053      498      498 115872512     3506      156       47",
		map[string]string{"type": "SD", "name": "SN64G", "manfid": "0x000003", "oemid": "0x5344", "date": "06/2024", "fwrev": "0x6", "hwrev": "0x8", "serial": "0x82823176"},
		map[string]string{"mmcblk0p1": "179:1", "mmcblk0p2": "179:2", "mmcblk0p3": "179:3"})
	f.ext4("mmcblk0p2", 0, 0, 0, 506121)
	f.ext4("mmcblk0p3", 0, 0, 0, 49590013)
	f.file("proc/self/mountinfo", strings.Join([]string{
		"22 1 179:2 / / ro,noatime shared:1 - ext4 /dev/root ro",
		"25 22 179:1 / /boot ro,relatime shared:2 - vfat /dev/mmcblk0p1 ro",
		"30 22 179:3 / /usr/local rw,noatime shared:3 - ext4 /dev/mmcblk0p3 rw,commit=30",
		"31 30 179:3 /etc/occulite /usr/local/etc/occulite rw,noatime shared:3 - ext4 /dev/mmcblk0p3 rw,commit=30",
		"40 22 0:25 / /tmp rw,nosuid shared:5 - tmpfs tmpfs rw",
	}, "\n"))
	f.file("proc/uptime", "4320.51 16000.00")
	f.file("proc/sys/kernel/random/boot_id", "18a54f93-6d88-4373-bf84-11f2686d09d4")
	return f
}

func TestStorageDevicesFromSysfs(t *testing.T) {
	type want struct {
		name, kind, vendor, vendorID, model, manufactured string
		capacity, sinceBoot                               int64
		age                                               int
		virtual                                           bool
		mounts                                            []string
		filesystems                                       []string
		emmc                                              *EMMCHealth
	}
	cases := []struct {
		name  string
		build func(t *testing.T) sysTree
		want  []want
	}{
		{"the Pi 4's SanDisk card", pi4Tree, []want{{
			name: "mmcblk0", kind: "sd", vendor: "SanDisk", vendorID: "0x000003", model: "SN64G", manufactured: "2024-06", age: 27,
			capacity: 124735488 * 512, sinceBoot: 18106 * 512, mounts: []string{"/", "/boot", "/usr/local"}, filesystems: []string{"mmcblk0p2", "mmcblk0p3"},
		}}},
		{"the Charly's Samsung card", func(t *testing.T) sysTree {
			f := newSysTree(t)
			f.disk("mmcblk0", "platform/soc/3f202000.mmc/mmc_host/mmc0/mmc0:0001", 62521344, "179:0",
				"   14768     6185   643333    49793      924     1576    16548    17418        0    25256    67211        0        0        0        0        0        0",
				map[string]string{"type": "SD", "name": "GB1QT", "manfid": "0x00001b", "oemid": "0x534d", "date": "07/2018"},
				map[string]string{"mmcblk0p2": "179:2", "mmcblk0p3": "179:3"})
			f.file("proc/self/mountinfo", "22 1 179:2 / / ro - ext4 /dev/root ro\n30 22 179:3 / /usr/local rw - ext4 /dev/mmcblk0p3 rw")
			return f
		}, []want{{
			name: "mmcblk0", kind: "sd", vendor: "Samsung", vendorID: "0x00001b", model: "GB1QT", manufactured: "2018-07", age: 98,
			capacity: 62521344 * 512, sinceBoot: 16548 * 512, mounts: []string{"/", "/usr/local"}, filesystems: []string{},
		}}},
		{"an eMMC with its wear estimates, boot areas left out", func(t *testing.T) sysTree {
			f := newSysTree(t)
			attrs := map[string]string{"type": "MMC", "name": "DG4016", "manfid": "0x000045", "date": "01/2022", "life_time": "0x02 0x01", "pre_eol_info": "0x02"}
			f.disk("mmcblk1", "platform/fe310000.mmc/mmc_host/mmc1/mmc1:0001", 30535680, "179:32", "1 0 8 0 10 0 400 0 0 0 0", attrs, map[string]string{"mmcblk1p1": "179:33"})
			f.disk("mmcblk1boot0", "platform/fe310000.mmc/mmc_host/mmc1/mmc1:0001", 8192, "179:64", "0 0 0 0 0 0 0 0 0 0 0", nil, nil)
			f.disk("mmcblk1rpmb", "platform/fe310000.mmc/mmc_host/mmc1/mmc1:0001", 8192, "179:96", "0 0 0 0 0 0 0 0 0 0 0", nil, nil)
			return f
		}, []want{{
			name: "mmcblk1", kind: "emmc", vendor: "SanDisk", vendorID: "0x000045", model: "DG4016", manufactured: "2022-01", age: 56,
			capacity: 30535680 * 512, sinceBoot: 400 * 512, mounts: []string{}, filesystems: []string{},
			emmc: &EMMCHealth{LifeTimeA: 2, LifeTimeB: 1, PreEOL: "warning"},
		}}},
		{"the OVA's QEMU disk behind virtio-scsi, and a virtio-blk disk", func(t *testing.T) sysTree {
			f := newSysTree(t)
			f.noise()
			f.disk("sda", "pci0000:00/0000:00:05.0/0000:01:01.0/virtio3/host0/target0:0:0/0:0:0:0", 67108864, "8:0",
				"   35508     3666 23856813   110630    21616    60269 10568960   290982        0   122709   484996      483        0 41888608      151     1960    83232",
				map[string]string{"vendor": "QEMU    ", "model": "QEMU HARDDISK   ", "rev": "2.5+", "ioerr_cnt": "0xd"},
				map[string]string{"sda1": "8:1", "sda2": "8:2", "sda3": "8:3"})
			f.disk("vda", "pci0000:00/0000:00:04.0/virtio1", 2097152, "252:0", "1 0 8 0 3 0 24 0 0 0 0", map[string]string{"vendor": "0x1af4"}, nil)
			f.ext4("sda2", 0, 0, 0, 484961)
			f.ext4("sda3", 0, 0, 0, 136273930)
			// occulited's own mountinfo on the OVA: the journal and the state directory are binds
			f.file("proc/self/mountinfo", strings.Join([]string{
				"20 1 8:2 / / ro,noatime - ext4 /dev/root ro",
				"21 20 8:1 / /boot ro - vfat /dev/sda1 ro",
				"22 20 8:3 /var/log/journal /var/log/journal rw - ext4 /dev/sda3 rw",
				"23 20 8:3 / /usr/local rw - ext4 /dev/sda3 rw",
				"24 23 8:3 /etc/occulite /usr/local/etc/occulite rw - ext4 /dev/sda3 rw",
			}, "\n"))
			return f
		}, []want{
			{name: "sda", kind: "virtio", vendor: "QEMU", model: "QEMU HARDDISK", virtual: true, capacity: 67108864 * 512, sinceBoot: 10568960 * 512,
				mounts: []string{"/", "/boot", "/usr/local"}, filesystems: []string{"sda2", "sda3"}},
			// a virtio-blk disk names no vendor but the PCI id, which is nothing to show
			{name: "vda", kind: "virtio", virtual: true, capacity: 2097152 * 512, sinceBoot: 24 * 512, mounts: []string{}, filesystems: []string{}},
		}},
		{"a USB SSD, a SATA SSD, an NVMe and an empty card reader", func(t *testing.T) sysTree {
			f := newSysTree(t)
			f.disk("sdb", "pci0000:00/0000:00:14.0/usb2/2-1/2-1:1.0/host1/target1:0:0/1:0:0:0", 976773168, "8:16", "5 0 40 0 7 0 56 0 0 0 0",
				map[string]string{"vendor": "Samsung ", "model": "PSSD T7         "}, map[string]string{"sdb1": "8:17"})
			f.disk("sda", "pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0", 468862128, "8:0", "1 0 8 0 2 0 16 0 0 0 0",
				map[string]string{"vendor": "ATA     ", "model": "KINGSTON SA400S3"}, nil)
			f.disk("nvme0n1", "pci0000:00/0000:00:1d.0/0000:03:00.0/nvme/nvme0", 976773168, "259:0", "1 0 8 0 2 0 32 0 0 0 0",
				map[string]string{"model": "WD Blue SN570 500GB  ", "serial": "22000000000000"}, nil)
			f.disk("sdc", "pci0000:00/0000:00:14.0/usb2/2-2/2-2:1.0/host2/target2:0:0/2:0:0:0", 0, "8:32", "0 0 0 0 0 0 0 0 0 0 0",
				map[string]string{"vendor": "Generic-", "model": "SD/MMC"}, nil)
			f.ext4("sdb1", 0, 0, 0, 1000)
			f.file("proc/self/mountinfo", "30 22 8:17 / /media/usb0 rw - ext4 /dev/sdb1 rw")
			return f
		}, []want{
			{name: "sdb", kind: "usb", vendor: "Samsung", model: "PSSD T7", capacity: 976773168 * 512, sinceBoot: 56 * 512, mounts: []string{"/media/usb0"}, filesystems: []string{"sdb1"}},
			{name: "nvme0n1", kind: "nvme", model: "WD Blue SN570 500GB", capacity: 976773168 * 512, sinceBoot: 32 * 512, mounts: []string{}, filesystems: []string{}},
			{name: "sda", kind: "sata", model: "KINGSTON SA400S3", capacity: 468862128 * 512, sinceBoot: 16 * 512, mounts: []string{}, filesystems: []string{}},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := c.build(t)
			devs := Root(f.root).storageDevices(storageNow)
			if len(devs) != len(c.want) {
				names := []string{}
				for _, d := range devs {
					names = append(names, d.Name)
				}
				t.Fatalf("devices %q, want %d", names, len(c.want))
			}
			for i, w := range c.want {
				d := devs[i]
				fs := []string{}
				for _, x := range d.Filesystems {
					fs = append(fs, x.Name)
				}
				age := 0
				if d.AgeMonths != nil {
					age = *d.AgeMonths
				}
				got := want{name: d.Name, kind: d.Kind, vendor: d.Vendor, vendorID: d.VendorID, model: d.Model, manufactured: d.Manufactured, capacity: d.CapacityBytes,
					sinceBoot: d.Writes.SinceBootBytes, age: age, virtual: d.Virtual, mounts: d.Mounts, filesystems: fs, emmc: d.EMMC}
				if !reflect.DeepEqual(got, w) {
					t.Errorf("device %d:\n got %+v\nwant %+v", i, got, w)
				}
			}
		})
	}
}

func TestExt4Counters(t *testing.T) {
	f := pi4Tree(t)
	// the userfs has seen errors: the first and the last time are unix seconds
	f.ext4("mmcblk0p3", 3, 1756000000, 1757000000, 49590013)
	devs := Root(f.root).storageDevices(storageNow)
	fs := devs[0].Filesystems[1]
	// the unit's bind mount of the state directory is not a mount of the filesystem
	if fs.Name != "mmcblk0p3" || fs.Errors != 3 || !reflect.DeepEqual(fs.Mounts, []string{"/usr/local"}) ||
		fs.FirstError == nil || fs.FirstError.Unix() != 1756000000 || fs.LastError == nil || fs.LastError.Unix() != 1757000000 || fs.LifetimeWriteBytes != 49590013*1024 {
		t.Fatalf("userfs: %+v", fs)
	}
	if devs[0].Filesystems[0].FirstError != nil {
		t.Fatalf("a filesystem without errors has no error time: %+v", devs[0].Filesystems[0])
	}
	if devs[0].lifetimeKB != 506121+49590013 {
		t.Fatalf("lifetime: %d", devs[0].lifetimeKB)
	}
	if got := unescapeMount(`/media/my\040stick`); got != "/media/my stick" {
		t.Fatalf("unescape: %q", got)
	}
}

func readSmartFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "smartctl", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseSmartctl(t *testing.T) {
	i := func(n int) *int { return &n }
	i64 := func(n int64) *int64 { return &n }
	yes, no := true, false
	cases := []struct {
		file string
		want SmartHealth
	}{
		// measured: the OVA's QEMU disk has no SMART, and says so without an error message
		{"ova-qemu-scsi.json", SmartHealth{Model: "QEMU QEMU HARDDISK"}},
		// measured: smartctl on the Pi's SD card cannot even tell what it is
		{"pi4-mmcblk0.json", SmartHealth{Message: "/dev/mmcblk0: Unable to detect device type"}},
		{"sata-ssd-passed.json", SmartHealth{Available: true, Passed: &yes, Model: "Samsung SSD 870 EVO 500GB", PowerOnHours: 4211, WearPercent: i(3), Reallocated: i64(0), TemperatureC: 35}},
		{"sata-failed.json", SmartHealth{Available: true, Passed: &no, Model: "KINGSTON SA400S37240G", PowerOnHours: 31022, WearPercent: i(88), Reallocated: i64(1520), Pending: i64(16), TemperatureC: 41}},
		{"nvme-worn.json", SmartHealth{Available: true, Passed: &yes, Model: "WD Blue SN570 500GB", PowerOnHours: 9120, WearPercent: i(83), MediaErrors: i64(0), TemperatureC: 44}},
	}
	for _, c := range cases {
		t.Run(c.file, func(t *testing.T) {
			if got := ParseSmartctl(readSmartFixture(t, c.file)); !reflect.DeepEqual(got, c.want) {
				gj, _ := json.Marshal(got)
				wj, _ := json.Marshal(c.want)
				t.Fatalf("\n got %s\nwant %s", gj, wj)
			}
		})
	}
	if got := ParseSmartctl([]byte("smartctl: not JSON")); got.Available || got.Message == "" {
		t.Fatalf("garbage: %+v", got)
	}
	// an attribute id that means something else on another drive does not become wear
	odd := `{"smart_status":{"passed":true},"ata_smart_attributes":{"table":[{"id":202,"name":"Data_Address_Mark_Errs","value":100,"raw":{"value":0}}]}}`
	if got := ParseSmartctl([]byte(odd)); got.WearPercent != nil {
		t.Fatalf("id 202 of a hard disk read as wear: %+v", got)
	}
}

// the verdict table: every threshold on both sides
func TestJudgeDevice(t *testing.T) {
	i := func(n int) *int { return &n }
	i64 := func(n int64) *int64 { return &n }
	yes, no := true, false
	sd := func(months int, perDay int64, days float64) StorageDevice {
		return StorageDevice{Name: "mmcblk0", Kind: "sd", AgeMonths: &months, Writes: StorageWrites{PerDayBytes: perDay, PerDayDays: days, PerDayBasis: "samples"}}
	}
	cases := []struct {
		name    string
		dev     StorageDevice
		verdict string
		reasons []StorageReason
	}{
		{"a fresh SD card", sd(27, 8_000_000, 14), StorageGood, nil},
		{"SMART passed", StorageDevice{Name: "sda", Kind: "sata", Smart: &SmartHealth{Available: true, Passed: &yes, WearPercent: i(3), Reallocated: i64(0)}}, StorageGood, nil},
		{"SMART failed", StorageDevice{Name: "sda", Kind: "sata", Smart: &SmartHealth{Available: true, Passed: &no}}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonSmartFailed, Device: "sda"}}},
		{"NVMe at 79 %", StorageDevice{Name: "nvme0n1", Kind: "nvme", Smart: &SmartHealth{Passed: &yes, WearPercent: i(79)}}, StorageGood, nil},
		{"NVMe at 80 %", StorageDevice{Name: "nvme0n1", Kind: "nvme", Smart: &SmartHealth{Passed: &yes, WearPercent: i(80)}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonWear, Device: "nvme0n1", Percent: 80}}},
		{"NVMe at 100 %", StorageDevice{Name: "nvme0n1", Kind: "nvme", Smart: &SmartHealth{Passed: &yes, WearPercent: i(100)}}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonWearOver, Device: "nvme0n1", Percent: 100}}},
		{"one reallocated sector", StorageDevice{Name: "sda", Kind: "usb", Smart: &SmartHealth{Passed: &yes, Reallocated: i64(1), Pending: i64(0)}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonReallocated, Device: "sda", Count: 1}}},
		{"pending sectors and media errors", StorageDevice{Name: "sda", Kind: "usb", Smart: &SmartHealth{Passed: &yes, Pending: i64(4), MediaErrors: i64(2)}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonPending, Device: "sda", Count: 4}, {Level: StorageWatch, Code: ReasonMediaErrors, Device: "sda", Count: 2}}},
		{"eMMC life 0x08", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{LifeTimeA: 0x08, LifeTimeB: 0x02, PreEOL: "normal"}}, StorageGood, nil},
		{"eMMC life 0x09 on type B", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{LifeTimeA: 0x02, LifeTimeB: 0x09, PreEOL: "normal"}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonEMMCLife, Device: "mmcblk1", Percent: 80}}},
		{"eMMC life 0x0A", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{LifeTimeA: 0x0A}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonEMMCLife, Device: "mmcblk1", Percent: 90}}},
		{"eMMC life exceeded", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{LifeTimeA: 0x0B}}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonEMMCLifeOver, Device: "mmcblk1"}}},
		{"eMMC pre-EOL warning, the task's fixture", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{LifeTimeA: 0x02, LifeTimeB: 0x01, PreEOL: "warning"}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonEMMCEOLWarn, Device: "mmcblk1"}}},
		{"eMMC pre-EOL urgent", StorageDevice{Name: "mmcblk1", Kind: "emmc", EMMC: &EMMCHealth{PreEOL: "urgent"}}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonEMMCEOLUrgent, Device: "mmcblk1"}}},
		{"an I/O error this boot", StorageDevice{Name: "mmcblk0", Kind: "sd", IOErrors: 1}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonIOErrors, Device: "mmcblk0", Count: 1}}},
		{"ext4 errors ever", StorageDevice{Name: "mmcblk0", Kind: "sd", Filesystems: []FilesystemHealth{{Name: "mmcblk0p2", Mounts: []string{"/"}}, {Name: "mmcblk0p3", Mounts: []string{"/usr/local"}, Errors: 2}}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonExt4Errors, Device: "mmcblk0", Count: 2, Filesystem: "/usr/local"}}},
		{"ext4 errors on an unmounted filesystem name it", StorageDevice{Name: "sdb", Kind: "usb", Filesystems: []FilesystemHealth{{Name: "sdb1", Errors: 1}}}, StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonExt4Errors, Device: "sdb", Count: 1, Filesystem: "sdb1"}}},
		{"an SD card of five years with heavy writes", sd(60, 600_000_000, 7), StorageWatch,
			[]StorageReason{{Level: StorageWatch, Code: ReasonSDAgeWrites, Device: "mmcblk0", Years: 5, BytesPerDay: 600_000_000}}},
		{"just under five years", sd(59, 600_000_000, 7), StorageGood, nil},
		{"old but light", sd(98, 17_000_000, 28), StorageGood, nil},
		{"old, heavy, but not three days of figures yet", sd(98, 900_000_000, 2.5), StorageGood, nil},
		{"an old eMMC with heavy writes is judged by its own wear", StorageDevice{Name: "mmcblk1", Kind: "emmc", AgeMonths: i(98), Writes: StorageWrites{PerDayBytes: 900_000_000, PerDayDays: 10}, EMMC: &EMMCHealth{LifeTimeA: 1}}, StorageGood, nil},
		{"replace wins, and only its reasons are given", StorageDevice{Name: "sda", Kind: "sata", IOErrors: 3, Smart: &SmartHealth{Passed: &no, WearPercent: i(88), Reallocated: i64(1520)}}, StorageReplace,
			[]StorageReason{{Level: StorageReplace, Code: ReasonSmartFailed, Device: "sda"}, {Level: StorageReplace, Code: ReasonIOErrors, Device: "sda", Count: 3}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.dev
			judgeDevice(&d)
			want := c.reasons
			if want == nil {
				want = []StorageReason{}
			}
			if d.Verdict != c.verdict || !reflect.DeepEqual(d.Reasons, want) {
				t.Fatalf("verdict %s %+v, want %s %+v", d.Verdict, d.Reasons, c.verdict, want)
			}
		})
	}
}

func TestKernelErrorAttribution(t *testing.T) {
	devs := []StorageDevice{
		{Name: "mmcblk0", partitions: []string{"mmcblk0p1", "mmcblk0p2", "mmcblk0p3"}, mmcHost: "mmc0"},
		{Name: "sda", partitions: []string{"sda1"}},
	}
	lines := []LogLine{
		{Message: "I/O error, dev mmcblk0, sector 123 op 0x1:(WRITE) flags 0x0 phys_seg 1 prio class 0", Timestamp: "2026-09-12T10:00:00Z"},
		{Message: "Buffer I/O error on dev mmcblk0p3, logical block 42, lost async page write"},
		{Message: "mmc0: Timeout waiting for hardware interrupt."},
		{Message: "mmcblk0: error -110 transferring data, sector 2048, nr 8, cmd response 0x900, card status 0xb00", Timestamp: "2026-09-12T10:05:00Z"},
		// the Pi's SDIO wifi is on mmc1, which is nobody's disk
		{Message: "mmc1: error -110 whilst initialising SDIO card"},
		{Message: "I/O error, dev loop0, sector 0 op 0x0:(READ) flags 0x80700 phys_seg 1 prio class 0"},
		// counted by the filesystem's errors_count, not here
		{Message: "EXT4-fs error (device mmcblk0p3): ext4_find_entry:1455: inode #2: comm ls: reading directory lblock 0"},
		{Message: "operation not supported error, dev sda, sector 0 op 0x9:(WRITE_ZEROES) flags 0x0 phys_seg 0 prio class 0"},
		{Message: "mmc0: new UHS-I speed DDR50 SDXC card at address aaaa"},
		{Message: "critical medium error, dev sda1, sector 1234 op 0x0:(READ) flags 0x0 phys_seg 1 prio class 0", Timestamp: "2026-09-12T10:07:00Z"},
	}
	attributeKernelErrors(devs, lines)
	if devs[0].IOErrors != 4 || !strings.HasPrefix(devs[0].LastIOError, "mmcblk0: error -110") || devs[0].LastIOErrorAt != "2026-09-12T10:05:00Z" {
		t.Errorf("the card: %d %q %q", devs[0].IOErrors, devs[0].LastIOError, devs[0].LastIOErrorAt)
	}
	if devs[1].IOErrors != 1 || devs[1].LastIOErrorAt != "2026-09-12T10:07:00Z" {
		t.Errorf("the SATA disk: %d %q", devs[1].IOErrors, devs[1].LastIOError)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// the whole report on the Pi 4 with a USB SSD beside it: SMART only for the SSD, at most once an
// hour; the kernel log at most every five minutes; the SD card's notes come from its kind
func TestStorageReport(t *testing.T) {
	f := pi4Tree(t)
	f.disk("sda", "platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb2/2-2/2-2:1.0/host0/target0:0:0/0:0:0:0", 976773168, "8:0", "1 0 8 0 2 0 16 0 0 0 0",
		map[string]string{"vendor": "Samsung ", "model": "PSSD T7"}, nil)
	clk := &clock{t: storageNow}
	var smartCalls []string
	kernelCalls := 0
	st := &Storage{Root: Root(f.root), StateFile: filepath.Join(t.TempDir(), "storage-writes.json"), Now: clk.now,
		Smartctl: func(_ context.Context, dev string) ([]byte, error) {
			smartCalls = append(smartCalls, dev)
			return readSmartFixture(t, "sata-ssd-passed.json"), nil
		},
		Kernel: func(context.Context) ([]LogLine, error) {
			kernelCalls++
			return []LogLine{{Message: "mmc1: error -110 whilst initialising SDIO card"}}, nil
		},
	}
	rep := st.Report(context.Background())
	if rep.Verdict != StorageGood || len(rep.Reasons) != 0 || !rep.KernelLog || len(rep.Devices) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	card, ssd := rep.Devices[0], rep.Devices[1]
	if card.Name != "mmcblk0" || card.Smart != nil || card.IOErrors != 0 || card.Writes.PerDayBasis != "boot" || card.Writes.SinceBootBytes != 18106*512 {
		t.Fatalf("the card: %+v", card)
	}
	if ssd.Name != "sda" || ssd.Kind != "usb" || ssd.Smart == nil || !ssd.Smart.Available || ssd.Smart.WearPercent == nil || *ssd.Smart.WearPercent != 3 || !ssd.Smart.Read.Equal(storageNow) {
		t.Fatalf("the SSD: %+v %+v", ssd, ssd.Smart)
	}
	if !reflect.DeepEqual(smartCalls, []string{"/dev/sda"}) || kernelCalls != 1 {
		t.Fatalf("reads: smart %q, kernel %d", smartCalls, kernelCalls)
	}
	clk.add(4 * time.Minute)
	st.Report(context.Background())
	if len(smartCalls) != 1 || kernelCalls != 1 {
		t.Fatalf("cached reads ran again: smart %q, kernel %d", smartCalls, kernelCalls)
	}
	clk.add(2 * time.Minute)
	st.Report(context.Background())
	if len(smartCalls) != 1 || kernelCalls != 2 {
		t.Fatalf("after six minutes: smart %q, kernel %d", smartCalls, kernelCalls)
	}
	clk.add(55 * time.Minute)
	rep = st.Report(context.Background())
	if len(smartCalls) != 2 || !rep.Devices[1].Smart.Read.Equal(clk.now()) {
		t.Fatalf("after an hour: smart %q", smartCalls)
	}
	// a failing card: I/O errors this boot replace it, whatever else is fine
	st2 := &Storage{Root: Root(f.root), Now: clk.now, Smartctl: st.Smartctl, Kernel: func(context.Context) ([]LogLine, error) {
		return []LogLine{{Message: "mmc0: Timeout waiting for hardware interrupt."}, {Message: "I/O error, dev mmcblk0, sector 9 op 0x1:(WRITE) flags 0x0 phys_seg 1 prio class 0"}}, nil
	}}
	rep = st2.Report(context.Background())
	if rep.Verdict != StorageReplace || len(rep.Reasons) != 1 || rep.Reasons[0] != (StorageReason{Level: StorageReplace, Code: ReasonIOErrors, Device: "mmcblk0", Count: 2}) {
		t.Fatalf("failing card: %s %+v", rep.Verdict, rep.Reasons)
	}
	// without a kernel log the count is unknown, and said so
	st3 := &Storage{Root: Root(f.root), Now: clk.now, Kernel: func(context.Context) ([]LogLine, error) { return nil, os.ErrPermission }}
	if rep := st3.Report(context.Background()); rep.KernelLog || rep.Devices[1].Smart != nil {
		t.Fatalf("no kernel log, no SMART reader off a real box: %+v", rep)
	}
	// and a fixture root is never read with the real programs
	if rep := (&Storage{Root: Root(f.root), Now: clk.now}).Report(context.Background()); rep.KernelLog || rep.Devices[1].Smart != nil {
		t.Fatalf("a development root ran smartctl or journalctl: %+v", rep)
	}
}

// the daily sample: a sample a day, writes per day across a reboot, a new card starts a new series,
// the file survives a restart of the daemon, old samples go
func TestWriteSamples(t *testing.T) {
	f := newSysTree(t)
	f.file("proc/uptime", "7200.00 0")
	f.file("proc/sys/kernel/random/boot_id", "b1")
	state := filepath.Join(t.TempDir(), "storage-writes.json")
	clk := &clock{t: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)}
	st := &Storage{Root: Root(f.root), StateFile: state, Now: clk.now}
	card := func(bootKB, lifetimeKB int64) []StorageDevice {
		return []StorageDevice{{Name: "mmcblk0", Kind: "sd", VendorID: "0x000003", Model: "SN64G", serial: "0x82823176", CapacityBytes: 63864569856,
			Writes: StorageWrites{SinceBootBytes: bootKB * 1024}, lifetimeKB: lifetimeKB}}
	}
	samples := func() []writeSample {
		var wf writeFile
		b, err := os.ReadFile(state)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &wf); err != nil {
			t.Fatal(err)
		}
		return wf.Devices["mmcblk0"].Samples
	}

	// day 0, two hours after the boot: the first sample, and the rate since the boot
	devs := card(100_000, 5_000_000)
	st.applyWrites(devs, clk.now())
	if w := devs[0].Writes; w.PerDayBasis != "boot" || w.PerDayBytes != 100_000*1024*12 || len(samples()) != 1 {
		t.Fatalf("first: %+v %d", w, len(samples()))
	}
	// ten hours later: no sample yet
	clk.add(10 * time.Hour)
	devs = card(120_000, 5_020_000)
	st.applyWrites(devs, clk.now())
	if len(samples()) != 1 || devs[0].Writes.PerDayBasis != "boot" {
		t.Fatalf("not due: %d %+v", len(samples()), devs[0].Writes)
	}
	// a day and half an hour after the first: the second sample, the rate over the samples
	clk.add(14*time.Hour + 30*time.Minute)
	devs = card(150_000, 5_050_000)
	st.applyWrites(devs, clk.now())
	day1KB := int64(50_000)
	if w := devs[0].Writes; len(samples()) != 2 || w.PerDayBasis != "samples" || w.PerDayBytes != int64(float64(day1KB*1024)/(24.5/24)) {
		t.Fatalf("second: %d %+v", len(samples()), w)
	}
	// a reboot: the device counter starts again, ext4's lifetime counter carries the delta; a new
	// daemon reads the file
	f.file("proc/sys/kernel/random/boot_id", "b2")
	clk.add(24*time.Hour + 30*time.Minute)
	st = &Storage{Root: Root(f.root), StateFile: state, Now: clk.now}
	devs = card(20_000, 5_080_000)
	st.applyWrites(devs, clk.now())
	day2KB := day1KB + 30_000
	if w := devs[0].Writes; len(samples()) != 3 || w.PerDayBytes != int64(float64(day2KB*1024)/(49.0/24)) || w.PerDayDays != 49.0/24 {
		t.Fatalf("across the reboot: %d %+v", len(samples()), w)
	}
	// writeDelta's last resort: neither counter usable - at least what the new boot wrote
	if d := writeDelta(writeSample{Boot: "b1", BootKB: 900}, writeSample{Boot: "b2", BootKB: 70}); d != 70 {
		t.Fatalf("no lifetime counter across a reboot: %d", d)
	}
	// another card in the slot starts again
	clk.add(time.Hour)
	other := card(1_000, 10)
	other[0].Model = "GB1QT"
	st.applyWrites(other, clk.now())
	if len(samples()) != 1 {
		t.Fatalf("a new card kept the old series: %d", len(samples()))
	}
	// samples beyond the keep period go, and a disk not seen for that long is forgotten
	clk.add(40 * 24 * time.Hour)
	st.applyWrites(card(5, 5), clk.now())
	if got := samples(); len(got) != 1 || !got[0].T.Equal(clk.now()) {
		t.Fatalf("old samples: %+v", got)
	}
	clk.add(36 * 24 * time.Hour)
	st.applyWrites([]StorageDevice{}, clk.now())
	var wf writeFile
	b, _ := os.ReadFile(state)
	_ = json.Unmarshal(b, &wf)
	if len(wf.Devices) != 0 {
		t.Fatalf("a gone disk is kept: %+v", wf.Devices)
	}
	// less than an hour of uptime and no day of samples: no figure at all
	if pd, days, basis := writeRate(nil, writeSample{T: clk.now(), BootKB: 10}, 1800, clk.now()); pd != 0 || days != 0 || basis != "" {
		t.Fatalf("too early: %d %f %q", pd, days, basis)
	}
}

// Start takes the sample without anybody asking for the report
func TestStorageStartSamples(t *testing.T) {
	f := pi4Tree(t)
	state := filepath.Join(t.TempDir(), "storage-writes.json")
	st := &Storage{Root: Root(f.root), StateFile: state, Now: func() time.Time { return storageNow }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st.Start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(state); err == nil && strings.Contains(string(b), `"mmcblk0"`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no sample was written")
}

// task 111: who watches the disks' health. The host - in a container, on the VM product, where every
// disk is a hypervisor's, and where the helper answers that smartctl is not installed: then no SMART
// is read, a missing one is no error, and the verdict is what the other facts say. The box itself on
// hardware, where a USB stick without SMART still says so and an SD card keeps its own facts.
func TestStorageHealthSource(t *testing.T) {
	usbDisk := func(f sysTree, vendor, model string) {
		f.disk("sda", "platform/scb/fd500000.pcie/pci0000:00/0000:00:00.0/0000:01:00.0/usb2/2-2/2-2:1.0/host0/target0:0:0/0:0:0:0", 976773168, "8:0", "1 0 8 0 2 0 16 0 0 0 0",
			map[string]string{"vendor": vendor, "model": model}, nil)
	}
	noKernelErrors := func(context.Context) ([]LogLine, error) { return []LogLine{}, nil }
	counting := func(calls *int, answer []byte, err error) func(context.Context, string) ([]byte, error) {
		return func(context.Context, string) ([]byte, error) {
			*calls++
			return answer, err
		}
	}

	t.Run("hardware without smartctl", func(t *testing.T) {
		f := pi4Tree(t)
		usbDisk(f, "Samsung ", "PSSD T7")
		clk := &clock{t: storageNow}
		calls := 0
		st := &Storage{Root: Root(f.root), Now: clk.now, Kernel: noKernelErrors,
			Smartctl: counting(&calls, nil, fmt.Errorf("%w: /usr/bin/smartctl is not installed", priv.ErrNotAvailable))}
		rep := st.Report(context.Background())
		if rep.HealthSource != HealthHost || rep.Verdict != StorageGood || len(rep.Reasons) != 0 || len(rep.Devices) != 2 || rep.Devices[1].Smart != nil || calls != 1 {
			t.Fatalf("report: %+v, calls %d", rep, calls)
		}
		// not asked again, not after the hour either: the image does not grow a smartctl
		clk.add(2 * time.Hour)
		if rep := st.Report(context.Background()); rep.HealthSource != HealthHost || rep.Devices[1].Smart != nil || calls != 1 {
			t.Fatalf("later: %+v, calls %d", rep, calls)
		}
		b, _ := json.Marshal(rep)
		if !strings.Contains(string(b), `"health_source":"host"`) || strings.Contains(string(b), `"smart"`) {
			t.Fatalf("JSON: %s", b)
		}
	})

	t.Run("the VM product reads no SMART, not even for a passed-through disk", func(t *testing.T) {
		f := newSysTree(t)
		f.noise()
		f.disk("sda", "pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0", 1953525168, "8:0", "1 0 8 0 2 0 16 0 0 0 0",
			map[string]string{"vendor": "ATA     ", "model": "Samsung SSD 870"}, nil)
		f.file("VERSION", "VERSION=3.89.8.20260719\nPRODUCT=ova\nPLATFORM=ova\nVARIANT=lite\nLITE=1.0.0-alpha.0")
		calls := 0
		st := &Storage{Root: Root(f.root), Now: func() time.Time { return storageNow }, Kernel: noKernelErrors,
			Smartctl: counting(&calls, readSmartFixture(t, "sata-ssd-passed.json"), nil)}
		rep := st.Report(context.Background())
		if rep.HealthSource != HealthHost || len(rep.Devices) != 1 || rep.Devices[0].Kind != "sata" || rep.Devices[0].Virtual || rep.Devices[0].Smart != nil || calls != 0 {
			t.Fatalf("report: %+v, calls %d", rep, calls)
		}
	})

	t.Run("a container reads no SMART for the host's disks it lists", func(t *testing.T) {
		f := pi4Tree(t)
		usbDisk(f, "Samsung ", "PSSD T7")
		f.file("run/systemd/container", "lxc")
		calls := 0
		st := &Storage{Root: Root(f.root), Now: func() time.Time { return storageNow }, Kernel: noKernelErrors,
			Smartctl: counting(&calls, readSmartFixture(t, "sata-ssd-passed.json"), nil)}
		rep := st.Report(context.Background())
		if rep.HealthSource != HealthHost || rep.Verdict != StorageGood || rep.Devices[1].Smart != nil || calls != 0 {
			t.Fatalf("report: %+v, calls %d", rep, calls)
		}
	})

	t.Run("every disk a hypervisor's", func(t *testing.T) {
		f := newSysTree(t)
		f.noise()
		f.disk("vda", "pci0000:00/0000:00:04.0/virtio2", 67108864, "254:0", "1 0 8 0 2 0 16 0 0 0 0", nil, nil)
		calls := 0
		st := &Storage{Root: Root(f.root), Now: func() time.Time { return storageNow }, Kernel: noKernelErrors,
			Smartctl: counting(&calls, nil, nil)}
		if rep := st.Report(context.Background()); rep.HealthSource != HealthHost || len(rep.Devices) != 1 || !rep.Devices[0].Virtual || calls != 0 {
			t.Fatalf("report: %+v, calls %d", rep, calls)
		}
	})

	t.Run("hardware with a USB stick without SMART says so, as before", func(t *testing.T) {
		f := pi4Tree(t)
		usbDisk(f, "SanDisk ", "Extreme 55AE")
		calls := 0
		stick := []byte(`{"smartctl":{"messages":[{"string":"/dev/sda: Unknown USB bridge [0x0781:0x55b1 (0x100)]","severity":"error"}],"exit_status":1},"smart_support":{"available":false}}`)
		st := &Storage{Root: Root(f.root), Now: func() time.Time { return storageNow }, Kernel: noKernelErrors,
			Smartctl: counting(&calls, stick, nil)}
		rep := st.Report(context.Background())
		card, sda := rep.Devices[0], rep.Devices[1]
		if rep.HealthSource != HealthDevice || rep.Verdict != StorageGood || calls != 1 || card.Kind != "sd" || card.Smart != nil ||
			sda.Smart == nil || sda.Smart.Available || !strings.Contains(sda.Smart.Message, "Unknown USB bridge") {
			t.Fatalf("report: %+v %+v, calls %d", rep, sda.Smart, calls)
		}
	})

	t.Run("an SD card alone", func(t *testing.T) {
		f := pi4Tree(t)
		calls := 0
		st := &Storage{Root: Root(f.root), Now: func() time.Time { return storageNow }, Kernel: noKernelErrors,
			Smartctl: counting(&calls, nil, nil)}
		rep := st.Report(context.Background())
		b, _ := json.Marshal(rep)
		if rep.HealthSource != HealthDevice || calls != 0 || !strings.Contains(string(b), `"health_source":"device"`) {
			t.Fatalf("report: %s, calls %d", b, calls)
		}
	})
}
