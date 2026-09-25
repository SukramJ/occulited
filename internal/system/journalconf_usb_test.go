package system

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// task 216: the journal's copies on a USB stick, named by its label - the target's form, where the
// copies are while the stick is plugged in, the fallback while it is not, and reading them back.

func TestParseJournalUSBTarget(t *testing.T) {
	for _, tc := range []struct {
		in, label, dir string
		ok             bool
	}{
		{"usb:LOGSTICK/journal", "LOGSTICK", "journal", true},
		{" usb:LOGSTICK/journal ", "LOGSTICK", "journal", true},
		{"usb:USB_DISK/ccu/journal", "USB_DISK", "ccu/journal", true},
		{"usb:Übertrag-2.0/j_1", "Übertrag-2.0", "j_1", true},
		{"usb:a#b+c.d:e=f@g_h-i/j", "a#b+c.d:e=f@g_h-i", "j", true},
		{"usb:A/b/c/d/e", "A", "b/c/d/e", true},
		{"usb:A/b/c/d/e/f", "", "", false},
		{"usb:LOGSTICK", "", "", false},
		{"usb:LOGSTICK/", "", "", false},
		{"usb:/journal", "", "", false},
		{"usb:LOG STICK/journal", "", "", false},
		{"usb:LOG'S/journal", "", "", false},
		{"usb:LOG$X/journal", "", "", false},
		{"usb:LOGSTICK/../etc", "", "", false},
		{"usb:LOGSTICK/./j", "", "", false},
		{"usb:LOGSTICK/.hidden", "", "", false},
		{"usb:LOGSTICK/a//b", "", "", false},
		{"usb:LOGSTICK/a b", "", "", false},
		{"/media/usb1/journal", "", "", false},
		{"userfs", "", "", false},
		{"", "", "", false},
		{"usb:" + strings.Repeat("L", 65) + "/j", "", "", false},
	} {
		label, dir, ok := ParseJournalUSBTarget(tc.in)
		if ok != tc.ok || label != tc.label || dir != tc.dir {
			t.Errorf("%q: %q %q %v, want %q %q %v", tc.in, label, dir, ok, tc.label, tc.dir, tc.ok)
		}
	}
}

// stickRoot is an rpi4 root with two sticks mounted: OTHERSTICK at /media/usb1 and LOGSTICK at
// /media/usb2 (mode "rw" or "ro"); "" mounts only the other one.
func stickRoot(t *testing.T, file, mode string) Root {
	t.Helper()
	r := journalRoot(t, "rpi4", file)
	mounts := "/dev/root / ext4 ro 0 0\n/dev/sda1 /media/usb1 vfat rw,nosuid 0 0\n"
	if mode != "" {
		mounts += "/dev/sdb1 /media/usb2 vfat " + mode + ",nosuid,nodev 0 0\n"
	}
	putFile(t, r, "proc/mounts", mounts)
	for _, d := range []string{"media/usb1", "media/usb2"} {
		if err := os.MkdirAll(r.join(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	old := USBStickProps
	USBStickProps = func(_ Root, dev string) map[string]string {
		return map[string]map[string]string{"/dev/sda1": {"ID_FS_LABEL": "OTHERSTICK"}, "/dev/sdb1": {"ID_FS_LABEL": "LOGSTICK", "ID_FS_LABEL_ENC": "LOGSTICK"}}[dev]
	}
	t.Cleanup(func() { USBStickProps = old })
	return r
}

func TestJournalStickTarget(t *testing.T) {
	const file = "STORAGE=ram-sync\nTARGET=usb:LOGSTICK/journal\n"
	const missing = "the USB stick LOGSTICK is not plugged in, the journal stays in RAM until it is"

	// plugged in: ram-sync, the copies' directory on the stick, its figures; no warning's reason
	r := stickRoot(t, file, "rw")
	putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
	putFile(t, r, "media/usb2/journal/0123abcd/system@a-1-1.journal", strings.Repeat("j", 5000))
	putFile(t, r, "run/occu-journal/fallback", missing+"\n")
	c := r.ReadJournalConfig()
	if c.Target != "usb:LOGSTICK/journal" || c.TargetLabel != "LOGSTICK" || c.TargetDir != "journal" || c.TargetPath != "/media/usb2/journal" {
		t.Fatalf("target %q %q %q %q", c.Target, c.TargetLabel, c.TargetDir, c.TargetPath)
	}
	if c.Effective != JournalRAMSync || c.Fallback != "" || !c.TargetOK || c.Persistent || c.RebootPending {
		t.Errorf("plugged in: effective %q fallback %q ok %v persistent %v pending %v", c.Effective, c.Fallback, c.TargetOK, c.Persistent, c.RebootPending)
	}
	if c.TargetUsage == nil || *c.TargetUsage <= 0 || c.TargetFree == nil {
		t.Errorf("figures %v %v", c.TargetUsage, c.TargetFree)
	}
	if d := r.JournalStickDir(); d != "/media/usb2/journal" || !r.JournalPersistent() {
		t.Errorf("stick dir %q, persistent %v", d, r.JournalPersistent())
	}
	// the previous boot's last copy, beside the copies on the stick; the stick's two reasons
	putFile(t, r, "media/usb2/journal/.occu-sync.state", "LAST_SYNC=1789236000\nLAST_RESULT=ok\nLAST_COPIED=3\nLAST_REASON=unplug\n")
	if c = r.ReadJournalConfig(); c.LastSync == nil || !c.LastSync.Equal(time.Unix(1789236000, 0)) || c.LastSyncReason != "unplug" {
		t.Errorf("state on the stick: %v %q", c.LastSync, c.LastSyncReason)
	}
	putFile(t, r, "run/occu-journal/sync.state", "LAST_SYNC=1789239600\nLAST_RESULT=ok\nLAST_COPIED=5\nLAST_REASON=plug\n")
	if c = r.ReadJournalConfig(); c.LastSyncReason != "plug" || c.LastSyncCopied != 5 {
		t.Errorf("this boot's copy: %q %d", c.LastSyncReason, c.LastSyncCopied)
	}

	// read-only: there, but cannot take the copies
	if c = stickRoot(t, file, "ro").ReadJournalConfig(); c.TargetOK || c.TargetPath != "/media/usb2/journal" {
		t.Errorf("read-only: ok %v path %q", c.TargetOK, c.TargetPath)
	}

	// not plugged in: RAM, the boot script's reason, no figures, nothing to read from
	r = stickRoot(t, file, "")
	putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
	putFile(t, r, "run/occu-journal/fallback", missing+"\n")
	c = r.ReadJournalConfig()
	if c.Effective != JournalRAM || c.Fallback != missing || c.TargetOK || c.TargetPath != "" || c.TargetUsage != nil || c.TargetFree != nil {
		t.Errorf("missing: effective %q fallback %q ok %v path %q usage %v free %v", c.Effective, c.Fallback, c.TargetOK, c.TargetPath, c.TargetUsage, c.TargetFree)
	}
	if r.JournalStickDir() != "" || r.JournalPersistent() {
		t.Errorf("missing: stick dir %q persistent %v", r.JournalStickDir(), r.JournalPersistent())
	}
	// a userfs journal still mounted from an earlier ram-sync does not make the stick's mode
	putFile(t, r, "proc/self/mountinfo", journalMountedLine)
	if c = r.ReadJournalConfig(); c.Effective != JournalRAM || c.Fallback != missing || c.RebootPending {
		t.Errorf("missing, the userfs still mounted: %q %q %v", c.Effective, c.Fallback, c.RebootPending)
	}

	// another label only: never the journal's stick
	r = stickRoot(t, "STORAGE=ram-sync\nTARGET=usb:NOSUCH/journal\n", "rw")
	if d := r.JournalStickDir(); d != "" {
		t.Errorf("another label: %q", d)
	}
	// the stick is only the target's in ram-sync
	r = stickRoot(t, "STORAGE=ram\nTARGET=usb:LOGSTICK/journal\n", "rw")
	if d := r.JournalStickDir(); d != "" {
		t.Errorf("ram: %q", d)
	}
	if c = r.ReadJournalConfig(); c.Effective != JournalRAM || c.Target != "usb:LOGSTICK/journal" {
		t.Errorf("ram with a stick target: %q %q", c.Effective, c.Target)
	}
}

func TestSetJournalStickTarget(t *testing.T) {
	r := stickRoot(t, "", "rw")
	c, err := r.SetJournalConfig(JournalConfig{Storage: "ram-sync", Target: "usb:LOGSTICK/ccu/journal"})
	if err != nil {
		t.Fatal(err)
	}
	if b := readFile(r.join(journalConfig)); !strings.Contains(b, "TARGET=usb:LOGSTICK/ccu/journal\n") {
		t.Errorf("file %q", b)
	}
	if c.TargetPath != "/media/usb2/ccu/journal" || c.TargetLabel != "LOGSTICK" {
		t.Errorf("answer %q %q", c.TargetPath, c.TargetLabel)
	}
	// on the VM the default is persistent: a stick needs ram-sync chosen
	v := stickRoot(t, "", "rw")
	putFile(t, v, "VERSION", "VERSION=3.89.8\nPRODUCT=ova\nPLATFORM=ova\n")
	if _, err := v.SetJournalConfig(JournalConfig{Target: "usb:LOGSTICK/journal"}); err == nil || !strings.Contains(err.Error(), "default is persistent") {
		t.Errorf("ova default on a stick: %v", err)
	}
	// an rpi4's default is RAM, which may keep a stick target
	if _, err := r.SetJournalConfig(JournalConfig{Target: "usb:LOGSTICK/journal"}); err != nil {
		t.Errorf("rpi4 default with a stick target: %v", err)
	}
}

// The Log page reads the RAM journal and the copies on the stick as files, merged by journalctl;
// without the stick, or with no copy on it yet, the default places. Following reads RAM only.
func TestJournalSourceArgs(t *testing.T) {
	r := stickRoot(t, "STORAGE=ram-sync\nTARGET=usb:LOGSTICK/journal\n", "rw")
	putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
	j := JournalLog{Root: r}
	if got := j.sourceArgs(); got != nil {
		t.Errorf("no copy on the stick yet: %v", got)
	}
	putFile(t, r, "media/usb2/journal/0123abcd/system@a-1-1.journal", "c")
	putFile(t, r, "media/usb2/journal/0123abcd/system@a-2-2.journal", "c")
	putFile(t, r, "media/usb2/journal/.occu-sync.state", "x")
	want := []string{"--file=/run/log/journal/0123abcd/system.journal", "--file=/media/usb2/journal/0123abcd/system@a-1-1.journal", "--file=/media/usb2/journal/0123abcd/system@a-2-2.journal"}
	if got := j.sourceArgs(); !slices.Equal(got, want) {
		t.Errorf("sources %v, want %v", got, want)
	}
	var calls [][]string
	j.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return nil, nil
	}
	if _, err := j.Read(LogQuery{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Boots(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = j.DiskUsage(context.Background())
	for _, c := range calls {
		if !slices.Contains(c, want[1]) || !slices.Contains(c, want[0]) {
			t.Errorf("a read without the stick's files: %v", c)
		}
	}
	if len(calls) != 3 {
		t.Errorf("%d calls", len(calls))
	}
	if a := j.args(LogQuery{Follow: true}); namesFiles(a) {
		t.Errorf("following names files: %v", a)
	}
	if a := JournalFileArgs(r); !slices.Equal(a, want) {
		t.Errorf("JournalFileArgs %v", a)
	}
	// a read that fails while files are named is tried once more
	n := 0
	j.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		n++
		if n == 1 {
			return nil, os.ErrNotExist
		}
		return []byte(`{"MESSAGE":"m","__REALTIME_TIMESTAMP":"1789236000000000"}` + "\n"), nil
	}
	if lines, err := j.Read(LogQuery{Limit: 10}); err != nil || len(lines) != 1 || n != 2 {
		t.Errorf("retry: %v %v %d", lines, err, n)
	}
	// the stick pulled: the default places again
	putFile(t, r, "proc/mounts", "/dev/sda1 /media/usb1 vfat rw 0 0\n")
	if got := j.sourceArgs(); got != nil {
		t.Errorf("pulled: %v", got)
	}
	if got := (JournalLog{}).sourceArgs(); got != nil {
		t.Errorf("no root: %v", got)
	}
}
