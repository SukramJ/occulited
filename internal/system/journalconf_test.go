package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// journalRoot is a development root with a /VERSION for the platform and, when given, the file.
func journalRoot(t *testing.T, platform, file string) Root {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	os.MkdirAll(filepath.Join(root, "usr/local"), 0o755)
	os.WriteFile(filepath.Join(root, "VERSION"), []byte("VERSION=3.89.8\nPRODUCT="+platform+"\nPLATFORM="+platform+"\n"), 0o644)
	if file != "" {
		os.WriteFile(filepath.Join(root, "etc/config/journal"), []byte(file), 0o644)
	}
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	return Root(root)
}

// Reading: a valid STORAGE wins; without one PERSIST decides, and persist is derived either way.
func TestJournalConfigRead(t *testing.T) {
	for _, tc := range []struct {
		name, file       string
		storage, persist string
	}{
		{"no file", "", "", ""},
		{"storage ram", "STORAGE=ram\n", "ram", "0"},
		{"storage persistent, quoted", "STORAGE='persistent'\n", "persistent", "1"},
		{"legacy PERSIST=1", "PERSIST=1\n", "persistent", "1"},
		{"legacy PERSIST=0", "PERSIST=0\n", "ram", "0"},
		{"storage wins over PERSIST", "PERSIST=1\nSTORAGE=ram\n", "ram", "0"},
		{"an empty STORAGE leaves it to PERSIST", "STORAGE=\nPERSIST=0\n", "ram", "0"},
		{"storage ram-sync", "STORAGE=ram-sync\n", "ram-sync", "0"},
		{"a mode this version does not know leaves it to PERSIST", "STORAGE=disk\nPERSIST=1\n", "persistent", "1"},
		{"an unknown mode and no PERSIST is the default", "STORAGE=tape\n", "", ""},
		{"a bad PERSIST is the default", "PERSIST=yes\n", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := journalRoot(t, "rpi4", tc.file+"RUNTIME_MAX_USE=16M\n").ReadJournalConfig()
			if c.Storage != tc.storage || c.Persist != tc.persist {
				t.Errorf("storage %q persist %q, want %q %q", c.Storage, c.Persist, tc.storage, tc.persist)
			}
			if c.Target != "userfs" || c.RuntimeMaxUse != "16M" {
				t.Errorf("target %q runtime_max_use %q", c.Target, c.RuntimeMaxUse)
			}
		})
	}
}

// The product default follows PLATFORM= as lite-journal-persist decides it.
func TestJournalDefaultStorage(t *testing.T) {
	for _, tc := range []struct {
		platform, want string
	}{
		{"rpi3", "ram"},
		{"rpi4", "ram"},
		{"rpi5", "ram"},
		{"tinkerboard", "ram"},
		{"", "ram"},
		{"ova", "persistent"},
		{"oci", "ram"}, // no lite product since D-94
		{"lxc", "persistent"},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			c := journalRoot(t, tc.platform, "").ReadJournalConfig()
			if c.DefaultStorage != tc.want || c.DefaultPersistent != (tc.want == "persistent") || c.Platform != tc.platform {
				t.Errorf("%+v, want %s", c, tc.want)
			}
		})
	}
}

// Writing: STORAGE with PERSIST in step, a cleared value takes its line away, a comment and a
// foreign key survive, and what is written reads back.
func TestJournalConfigWrite(t *testing.T) {
	for _, tc := range []struct {
		name, before string
		in           JournalConfig
		after        string
		storage      string
	}{
		{
			name:    "a new file",
			in:      JournalConfig{Storage: "persistent", Target: "userfs", RuntimeMaxUse: "8m", SystemMaxUse: "32m", RateLimitBurst: "500"},
			after:   "PERSIST=1\nRATE_LIMIT_BURST=500\nRUNTIME_MAX_USE=8M\nSTORAGE=persistent\nSYSTEM_MAX_USE=32M\nTARGET=userfs\n",
			storage: "persistent",
		},
		{
			name:    "a legacy file gains STORAGE, PERSIST follows",
			before:  "# mine\nPERSIST=1\nSYSTEM_MAX_USE=64M\nFOO=bar\n",
			in:      JournalConfig{Storage: "ram", SystemMaxUse: "64M"},
			after:   "# mine\nPERSIST=0\nSYSTEM_MAX_USE=64M\nFOO=bar\nSTORAGE=ram\n",
			storage: "ram",
		},
		{
			name:    "the default takes STORAGE, PERSIST and the cleared sizes away",
			before:  "STORAGE=ram\nPERSIST=0\nRUNTIME_MAX_USE=16M\nTARGET=userfs\nFOO=bar\n",
			in:      JournalConfig{},
			after:   "FOO=bar\n",
			storage: "",
		},
		{
			name:    "persist in the struct is not written: storage is",
			in:      JournalConfig{Storage: "ram", Persist: "1"},
			after:   "PERSIST=0\nSTORAGE=ram\n",
			storage: "ram",
		},
		{
			name:  "everything cleared is an empty file",
			in:    JournalConfig{},
			after: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := journalRoot(t, "rpi4", tc.before)
			c, err := r.SetJournalConfig(tc.in)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := os.ReadFile(filepath.Join(string(r), "etc/config/journal"))
			if string(b) != tc.after {
				t.Errorf("file:\n%s\nwant:\n%s", b, tc.after)
			}
			if c.Storage != tc.storage || c.Persist != journalPersistFromStorage(tc.storage) {
				t.Errorf("read back %+v", c)
			}
			if again := r.ReadJournalConfig(); again.Storage != c.Storage || again.RuntimeMaxUse != c.RuntimeMaxUse || again.SystemMaxUse != c.SystemMaxUse {
				t.Errorf("round trip %+v != %+v", again, c)
			}
		})
	}
}

func TestCheckJournalConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    JournalConfig
		err  string // "" = accepted; otherwise a part of the message
	}{
		{"the default", JournalConfig{}, ""},
		{"ram", JournalConfig{Storage: "ram", RuntimeMaxUse: "16M"}, ""},
		{"persistent on the userfs", JournalConfig{Storage: "persistent", Target: "userfs"}, ""},
		{"lower-case sizes", JournalConfig{SystemMaxUse: "32m", SystemMaxFile: "8m", RuntimeMaxUse: "1g"}, ""},
		{"a bad persist is not looked at", JournalConfig{Persist: "yes"}, ""},
		{"ram-sync", JournalConfig{Storage: "ram-sync"}, ""},
		{"a path target", JournalConfig{Storage: "persistent", Target: "/media/usb0/journal"}, "neither userfs nor a USB stick"},
		{"a path target in RAM mode", JournalConfig{Storage: "ram", Target: "/media/usb0/journal"}, "neither userfs nor a USB stick"},
		{"ram-sync to a stick", JournalConfig{Storage: "ram-sync", Target: "usb:LOGSTICK/journal"}, ""},
		{"RAM keeps a stick target", JournalConfig{Storage: "ram", Target: "usb:LOGSTICK/journal"}, ""},
		{"persistent on a stick", JournalConfig{Storage: "persistent", Target: "usb:LOGSTICK/journal"}, "persistent on a USB stick is not possible"},
		{"a stick without a directory", JournalConfig{Storage: "ram-sync", Target: "usb:LOGSTICK"}, "neither userfs nor a USB stick"},
		{"an unknown mode", JournalConfig{Storage: "disk"}, "ram, ram-sync, persistent or empty"},
		{"a RAM limit with a space", JournalConfig{RuntimeMaxUse: "16 M"}, "a size"},
		{"a system size with a space", JournalConfig{SystemMaxUse: "32 M"}, "a size"},
		{"a file size in words", JournalConfig{SystemMaxFile: "big"}, "a size"},
		{"a rate", JournalConfig{RateLimitBurst: "10/s"}, "count"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkJournalConfig(tc.c)
			switch {
			case tc.err == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.err != "" && err == nil:
				t.Errorf("accepted")
			case tc.err != "" && !strings.Contains(err.Error(), tc.err):
				t.Errorf("message %q, want %q in it", err, tc.err)
			}
		})
	}
}

func TestJournalStorageFromPersist(t *testing.T) {
	for _, tc := range []struct {
		persist, want string
		ok            bool
	}{
		{"", "", true},
		{"1", "persistent", true},
		{"0", "ram", true},
		{" 1 ", "persistent", true},
		{"yes", "", false},
		{"2", "", false},
	} {
		got, err := JournalStorageFromPersist(tc.persist)
		if got != tc.want || (err == nil) != tc.ok {
			t.Errorf("%q: %q %v", tc.persist, got, err)
		}
		if tc.ok && journalPersistFromStorage(got) != strings.TrimSpace(tc.persist) {
			t.Errorf("%q does not map back: %q", tc.persist, journalPersistFromStorage(got))
		}
	}
}

// A reboot is pending exactly when the setting (or the product's default) says RAM while the
// userfs is still mounted over /var/log/journal.
func TestJournalRebootPending(t *testing.T) {
	const mounted = "40 30 179:3 /var/log/journal /var/log/journal rw,relatime shared:9 - ext4 /dev/mmcblk0p3 rw\n"
	for _, tc := range []struct {
		name, platform, file, mountinfo string
		persistent, pending             bool
	}{
		{"ram, in RAM", "rpi4", "STORAGE=ram\n", "", false, false},
		{"ram, still on the userfs", "rpi4", "STORAGE=ram\n", mounted, true, true},
		{"legacy PERSIST=0, still on the userfs", "ova", "PERSIST=0\n", mounted, true, true},
		{"persistent, mounted", "rpi4", "STORAGE=persistent\n", mounted, true, false},
		{"persistent, not mounted: not a reboot's business", "rpi4", "STORAGE=persistent\n", "", false, false},
		{"the default on a card, still on the userfs", "rpi3", "", mounted, true, true},
		{"the default on the VM, mounted", "ova", "", mounted, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := journalRoot(t, tc.platform, tc.file)
			if tc.mountinfo != "" {
				os.MkdirAll(filepath.Join(string(r), "proc/self"), 0o755)
				os.WriteFile(filepath.Join(string(r), "proc/self/mountinfo"), []byte(tc.mountinfo), 0o644)
			}
			c := r.ReadJournalConfig()
			if c.Persistent != tc.persistent || c.RebootPending != tc.pending {
				t.Errorf("persistent %v pending %v, want %v %v", c.Persistent, c.RebootPending, tc.persistent, tc.pending)
			}
		})
	}
}

// The figures: the RAM journal, the userfs journal (through the bind mount when it is in place),
// the free space, and whether the userfs can take it.
func TestJournalFigures(t *testing.T) {
	r := journalRoot(t, "rpi4", "")
	w := func(p string, n int) {
		full := filepath.Join(string(r), p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, make([]byte, n), 0o640)
	}
	w("run/log/journal/0123abcd/system.journal", 1000)
	w("run/log/journal/0123abcd/user-1000.journal", 24)
	w("usr/local/var/log/journal/0123abcd/system@1.journal", 5000)
	in := func(p ...string) []string {
		for i := range p {
			p[i] = filepath.Join(string(r), p[i])
		}
		return p
	}
	ram := allocated(t, in("run/log/journal/0123abcd/system.journal", "run/log/journal/0123abcd/user-1000.journal")...)
	target := allocated(t, in("usr/local/var/log/journal/0123abcd/system@1.journal")...)
	c := r.ReadJournalConfig()
	if c.RAMUsage == nil || *c.RAMUsage != ram || c.TargetUsage == nil || *c.TargetUsage != target {
		t.Errorf("ram %v target %v, want %d %d", c.RAMUsage, c.TargetUsage, ram, target)
	}
	if c.TargetFree == nil || *c.TargetFree <= 0 || !c.TargetOK {
		t.Errorf("free %v ok %v", c.TargetFree, c.TargetOK)
	}
	// mounted: /var/log/journal is what is measured
	w("var/log/journal/0123abcd/system.journal", 700)
	os.MkdirAll(filepath.Join(string(r), "proc/self"), 0o755)
	os.WriteFile(filepath.Join(string(r), "proc/self/mountinfo"), []byte("40 30 179:3 /var/log/journal /var/log/journal rw - ext4 /dev/mmcblk0p3 rw\n"), 0o644)
	if c, want := r.ReadJournalConfig(), allocated(t, in("var/log/journal/0123abcd/system.journal")...); c.TargetUsage == nil || *c.TargetUsage != want {
		t.Errorf("mounted: target %v, want %d", c.TargetUsage, want)
	}
	// nothing there: zeros, not nulls; no /usr/local: not a target, no free space
	r2 := journalRoot(t, "rpi4", "")
	os.RemoveAll(filepath.Join(string(r2), "usr/local"))
	c = r2.ReadJournalConfig()
	if c.RAMUsage == nil || *c.RAMUsage != 0 || c.TargetUsage == nil || *c.TargetUsage != 0 || c.TargetFree != nil || c.TargetOK {
		t.Errorf("empty root: ram %v target %v free %v ok %v", c.RAMUsage, c.TargetUsage, c.TargetFree, c.TargetOK)
	}
}

func TestDirUsage(t *testing.T) {
	dir := t.TempDir()
	w := func(p string, n int) {
		full := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, make([]byte, n), 0o644)
	}
	w("j/a/system.journal", 4096)
	w("j/a/system@0001.journal~", 100)
	w("j/b/c/user-1000.journal", 10)
	os.Symlink(filepath.Join(dir, "j/a/system.journal"), filepath.Join(dir, "j/link.journal"))
	w("file", 5)
	root := os.Geteuid() == 0 // root reads what the mode forbids
	if !root {
		w("j/locked/x.journal", 1<<20)
		os.Chmod(filepath.Join(dir, "j/locked"), 0)
		t.Cleanup(func() { os.Chmod(filepath.Join(dir, "j/locked"), 0o755) })
		w("closed/x.journal", 1)
		os.Chmod(filepath.Join(dir, "closed"), 0)
		t.Cleanup(func() { os.Chmod(filepath.Join(dir, "closed"), 0o755) })
	}
	tree := allocated(t, filepath.Join(dir, "j/a/system.journal"), filepath.Join(dir, "j/a/system@0001.journal~"), filepath.Join(dir, "j/b/c/user-1000.journal"))
	for _, tc := range []struct {
		name, path string
		n          int64
		ok         bool
		skipRoot   bool
	}{
		{"a tree, symlinks not followed, an unreadable subdirectory left out", "j", tree, true, false},
		{"a missing directory is empty", "none", 0, true, false},
		{"a file is not a directory", "file", 0, false, false},
		{"a directory that cannot be listed is not a figure", "closed", 0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipRoot && root {
				t.Skip("running as root")
			}
			n, ok := dirUsage(filepath.Join(dir, tc.path))
			if n != tc.n || ok != tc.ok {
				t.Errorf("%d %v, want %d %v", n, ok, tc.n, tc.ok)
			}
		})
	}
}

func TestUserfsWritable(t *testing.T) {
	const (
		rootRO   = "22 1 179:2 / / ro,relatime shared:1 - ext4 /dev/root ro\n"
		rootRW   = "22 1 0:40 / / rw,relatime shared:1 - overlay overlay rw,lowerdir=/l,upperdir=/u,workdir=/w\n"
		userfs   = "30 22 179:3 / /usr/local rw,relatime shared:5 - ext4 /dev/mmcblk0p3 rw\n"
		strict   = "30 22 179:3 / /usr/local ro,nosuid,nodev,relatime shared:5 - ext4 /dev/mmcblk0p3 rw\n"
		broken   = "30 22 179:3 / /usr/local rw,relatime shared:5 - ext4 /dev/mmcblk0p3 ro,errors=remount-ro\n"
		occulite = "31 30 179:3 /etc/occulite /usr/local/etc/occulite rw,relatime shared:5 - ext4 /dev/mmcblk0p3 rw\n"
		usr      = "25 22 179:4 / /usr rw,relatime - ext4 /dev/sda4 rw\n"
		usrloc   = "26 22 179:5 / /usr/loc rw,relatime - ext4 /dev/sda5 rw\n"
	)
	for _, tc := range []struct {
		name, mountinfo string
		container, want bool
	}{
		{"the userfs mounted", rootRO + userfs, false, true},
		{"read-only in the daemon's namespace (ProtectSystem=strict), writable itself", rootRO + strict + occulite, false, true},
		{"the filesystem remounted read-only", rootRO + broken, false, false},
		{"a later mount over it wins", rootRO + userfs + broken, false, false},
		{"no userfs on a card: the root filesystem's directory", rootRO + occulite, false, false},
		{"no userfs on a card, a writable root", rootRW, false, false},
		{"a container without a mount of its own", rootRW, true, true},
		{"a container on a read-only root", rootRO, true, false},
		{"a container with a volume", rootRO + userfs, true, true},
		{"a /usr mount holds it, on a card", rootRO + usr, false, false},
		{"a /usr mount holds it, in a container", rootRO + usr, true, true},
		{"/usr/loc is not a parent of /usr/local", rootRO + usrloc, true, false},
		{"an escaped mount point", rootRO + "30 22 179:3 / /usr/local\\040x rw - ext4 /dev/x rw\n", true, false},
		{"no table", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := userfsWritable(tc.mountinfo, tc.container); got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

// allocated is what the fixture files take on this filesystem - what the figures count.
func allocated(t *testing.T, paths ...string) int64 {
	t.Helper()
	var n int64
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		n += allocatedBytes(info)
	}
	return n
}

// A journal file is preallocated: long and mostly holes. The figure is the blocks it takes, as
// journalctl --disk-usage says, not its length - on the lab OVA the length summed to twice the use.
func TestDirUsageCountsAllocatedBlocks(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "system.journal"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(8 << 20); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("LPKSHHRH"), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	n, ok := dirUsage(dir)
	if !ok || n <= 0 || n >= 8<<20 {
		t.Errorf("usage %d %v: want the allocated blocks, more than 0 and less than the 8 MiB length", n, ok)
	}
}
