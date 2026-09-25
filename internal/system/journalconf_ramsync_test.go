package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// task 85's ram-sync: where journald writes now, when a reboot is pending, the boot script's
// fallback, and what the copy left.

const journalMountedLine = "40 30 179:3 /var/log/journal /var/log/journal rw,relatime shared:9 - ext4 /dev/mmcblk0p3 rw\n"

func putFile(t *testing.T, r Root, p, body string) {
	t.Helper()
	full := filepath.Join(string(r), p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestJournalEffective(t *testing.T) {
	const reason = "the userfs target cannot be mounted, the journal stays in RAM (STORAGE=ram-sync)"
	for _, tc := range []struct {
		name, platform, file string
		mounted, inRAM       bool
		emptyRAMDir          bool
		fallbackFile         string
		effective            string
		pending              bool
		fallback             string
	}{
		{name: "ram-sync in effect", platform: "rpi4", file: "STORAGE=ram-sync\n", mounted: true, inRAM: true, effective: "ram-sync"},
		{name: "persistent in effect", platform: "ova", mounted: true, effective: "persistent"},
		{name: "persistent in effect, the empty runtime directory left behind", platform: "ova", mounted: true, emptyRAMDir: true, effective: "persistent"},
		{name: "ram, nothing mounted", platform: "rpi4", inRAM: true, effective: "ram"},
		{name: "ram-sync switched to ram: the copies stay readable until the reboot", platform: "rpi4", file: "STORAGE=ram\n", mounted: true, inRAM: true, effective: "ram-sync", pending: true},
		{name: "persistent switched to ram: on the userfs until the reboot", platform: "rpi4", file: "STORAGE=ram\n", mounted: true, effective: "persistent", pending: true},
		{name: "ram-sync chosen while journald is still persistent: the script applies it at once", platform: "ova", file: "STORAGE=ram-sync\n", mounted: true, effective: "persistent"},
		{name: "ram-sync that could not be set up", platform: "rpi4", file: "STORAGE=ram-sync\n", inRAM: true, fallbackFile: reason + "\n", effective: "ram", fallback: reason},
		{name: "the VM's default that could not be set up", platform: "ova", fallbackFile: "the userfs target cannot be created\n", effective: "ram", fallback: "the userfs target cannot be created"},
		{name: "no fallback file: the script has not run yet", platform: "ova", effective: "ram"},
		{name: "a fallback file once mounted is not a fallback", platform: "rpi4", file: "STORAGE=ram-sync\n", mounted: true, inRAM: true, fallbackFile: "old\n", effective: "ram-sync"},
		{name: "a fallback file while RAM is wanted is not one", platform: "rpi4", file: "STORAGE=ram\n", inRAM: true, fallbackFile: "old\n", effective: "ram"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := journalRoot(t, tc.platform, tc.file)
			if tc.mounted {
				putFile(t, r, "proc/self/mountinfo", journalMountedLine)
			}
			if tc.inRAM {
				putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
			}
			if tc.emptyRAMDir {
				if err := os.MkdirAll(filepath.Join(string(r), "run/log/journal"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if tc.fallbackFile != "" {
				putFile(t, r, "run/occu-journal/fallback", tc.fallbackFile)
			}
			c := r.ReadJournalConfig()
			if c.Effective != tc.effective || c.RebootPending != tc.pending || c.Fallback != tc.fallback || c.Persistent != tc.mounted {
				t.Errorf("effective %q pending %v fallback %q persistent %v, want %q %v %q %v", c.Effective, c.RebootPending, c.Fallback, c.Persistent, tc.effective, tc.pending, tc.fallback, tc.mounted)
			}
		})
	}
}

func TestJournalSyncState(t *testing.T) {
	r := journalRoot(t, "rpi4", "STORAGE=ram-sync\nSYNC_INTERVAL=1h\nTARGET_MAX_USE=128M\nTARGET_MAX_AGE=30d\n")
	c := r.ReadJournalConfig()
	if c.SyncInterval != "1h" || c.TargetMaxUse != "128M" || c.TargetMaxAge != "30d" || c.Storage != "ram-sync" || c.Persist != "0" {
		t.Errorf("settings %+v", c)
	}
	if c.LastSync != nil || c.NextSync != nil || c.LastSyncResult != "" {
		t.Errorf("no copy yet: %v %v %q", c.LastSync, c.NextSync, c.LastSyncResult)
	}
	putFile(t, r, "run/occu-journal/sync.state", "LAST_SYNC=1789236000\nLAST_RESULT=failed\nLAST_COPIED=2\nLAST_ERROR=copying system@x.journal failed (the userfs full or read-only?)\n")
	putFile(t, r, "run/occu-journal/sync.next", "1789257600\n")
	// not in ram-sync now: the last copy is still told, the next is not
	c = r.ReadJournalConfig()
	if c.LastSync == nil || !c.LastSync.Equal(time.Unix(1789236000, 0)) || c.LastSyncResult != "failed" || c.LastSyncCopied != 2 || !strings.Contains(c.LastSyncError, "read-only?") || c.NextSync != nil {
		t.Errorf("not in effect: %+v", c)
	}
	putFile(t, r, "proc/self/mountinfo", journalMountedLine)
	putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
	c = r.ReadJournalConfig()
	if c.Effective != "ram-sync" || c.NextSync == nil || !c.NextSync.Equal(time.Unix(1789257600, 0)) {
		t.Errorf("in effect: %q %v", c.Effective, c.NextSync)
	}
	// B-205: sync.due on the boot's clock wins over sync.next, which a clock step leaves stale
	putFile(t, r, "proc/uptime", "100.50 80.00\n")
	putFile(t, r, "run/occu-journal/sync.due", "3700\n")
	before := time.Now()
	c = r.ReadJournalConfig()
	if c.NextSync == nil {
		t.Fatal("no next copy with sync.due")
	}
	if want := before.Add(3600 * time.Second); c.NextSync.Before(want.Add(-2*time.Second)) || c.NextSync.After(want.Add(2*time.Second)) {
		t.Errorf("next copy from the uptime: %v, want about %v", c.NextSync, want)
	}
	// an unreadable sync.due or uptime: sync.next as written
	putFile(t, r, "run/occu-journal/sync.due", "soon\n")
	if c = r.ReadJournalConfig(); c.NextSync == nil || !c.NextSync.Equal(time.Unix(1789257600, 0)) {
		t.Errorf("garbage sync.due: %v", c.NextSync)
	}
	putFile(t, r, "run/occu-journal/sync.due", "3700\n")
	putFile(t, r, "proc/uptime", "\n")
	if c = r.ReadJournalConfig(); c.NextSync == nil || !c.NextSync.Equal(time.Unix(1789257600, 0)) {
		t.Errorf("no uptime: %v", c.NextSync)
	}
	// sync.due alone is no next copy: sync.next is the script's sign that the clock is trusted
	putFile(t, r, "proc/uptime", "100.50 80.00\n")

	// a state file that is not one
	putFile(t, r, "run/occu-journal/sync.state", "LAST_SYNC=soon\n")
	putFile(t, r, "run/occu-journal/sync.next", "later\n")
	if c = r.ReadJournalConfig(); c.LastSync != nil || c.NextSync != nil {
		t.Errorf("garbage read as times: %v %v", c.LastSync, c.NextSync)
	}

	// task 85 (D-67): why the last copy ran; a reason the script does not write is none
	for _, tc := range []struct{ file, want string }{
		{"LAST_REASON=early\n", "early"},
		{"LAST_REASON=interval\n", "interval"},
		{"LAST_REASON=shutdown\n", "shutdown"},
		{"LAST_REASON=manual\n", "manual"},
		{"LAST_REASON=sideways\n", ""},
		{"", ""},
	} {
		putFile(t, r, "run/occu-journal/sync.state", "LAST_SYNC=1789236000\nLAST_RESULT=ok\nLAST_COPIED=4\n"+tc.file)
		if c = r.ReadJournalConfig(); c.LastSyncReason != tc.want || c.LastSyncCopied != 4 {
			t.Errorf("%q: reason %q, copied %d", tc.file, c.LastSyncReason, c.LastSyncCopied)
		}
	}
	// before the first copy of this boot: the previous boot's last copy, left beside the copies
	putFile(t, r, "run/occu-journal/sync.state", "")
	putFile(t, r, "usr/local/var/log/journal/.occu-sync.state", "LAST_SYNC=1789230000\nLAST_RESULT=ok\nLAST_COPIED=7\nLAST_ERROR=\nLAST_REASON=shutdown\n")
	c = r.ReadJournalConfig()
	if c.LastSync == nil || !c.LastSync.Equal(time.Unix(1789230000, 0)) || c.LastSyncReason != "shutdown" || c.LastSyncCopied != 7 {
		t.Errorf("the previous boot's copy: %v %q %d", c.LastSync, c.LastSyncReason, c.LastSyncCopied)
	}
	// this boot's copy wins over it
	putFile(t, r, "run/occu-journal/sync.state", "LAST_SYNC=1789236000\nLAST_RESULT=ok\nLAST_COPIED=2\nLAST_REASON=early\n")
	if c = r.ReadJournalConfig(); !c.LastSync.Equal(time.Unix(1789236000, 0)) || c.LastSyncReason != "early" || c.LastSyncCopied != 2 {
		t.Errorf("this boot's copy: %v %q %d", c.LastSync, c.LastSyncReason, c.LastSyncCopied)
	}
}

func TestJournalRAMSyncWrite(t *testing.T) {
	r := journalRoot(t, "rpi4", "STORAGE=persistent\nPERSIST=1\n")
	c, err := r.SetJournalConfig(JournalConfig{Storage: "ram-sync", SyncInterval: " 1h", TargetMaxUse: "128m", TargetMaxAge: "30d"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(string(r), "etc/config/journal"))
	if want := "STORAGE=ram-sync\nPERSIST=0\nSYNC_INTERVAL=1h\nTARGET_MAX_AGE=30d\nTARGET_MAX_USE=128M\n"; string(b) != want {
		t.Errorf("file:\n%s\nwant:\n%s", b, want)
	}
	if c.Storage != "ram-sync" || c.SyncInterval != "1h" || c.TargetMaxUse != "128M" {
		t.Errorf("read back %+v", c)
	}
	// the defaults again: the lines go
	if _, err := r.SetJournalConfig(JournalConfig{Storage: "ram-sync"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(string(r), "etc/config/journal")); string(b) != "STORAGE=ram-sync\nPERSIST=0\n" {
		t.Errorf("cleared:\n%s", b)
	}
}

func TestCheckJournalRAMSync(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    JournalConfig
		err  string
	}{
		{"ram-sync with its settings", JournalConfig{Storage: "ram-sync", SyncInterval: "30min", TargetMaxUse: "128m", TargetMaxAge: "2w"}, ""},
		{"the shortest interval", JournalConfig{SyncInterval: "15min"}, ""},
		{"the longest interval", JournalConfig{SyncInterval: "7d"}, ""},
		{"an interval in hours", JournalConfig{SyncInterval: "168h"}, ""},
		{"too short", JournalConfig{SyncInterval: "14min"}, "15min to 7d"},
		{"too long", JournalConfig{SyncInterval: "8d"}, "15min to 7d"},
		{"with a space", JournalConfig{SyncInterval: "6 h"}, "minutes, hours or days"},
		{"zero", JournalConfig{SyncInterval: "0h"}, "minutes, hours or days"},
		{"seconds", JournalConfig{SyncInterval: "3600"}, "minutes, hours or days"},
		{"a leading zero", JournalConfig{SyncInterval: "06h"}, "minutes, hours or days"},
		{"a bad copy size", JournalConfig{TargetMaxUse: "lots"}, "a size"},
		{"an age in words", JournalConfig{TargetMaxAge: "30 days"}, "hours, days or weeks"},
		{"an age of months", JournalConfig{TargetMaxAge: "1month"}, "hours, days or weeks"},
		{"ram-sync to a path", JournalConfig{Storage: "ram-sync", Target: "/media/usb0/journal"}, "neither userfs nor a USB stick"},
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
	for in, want := range map[string]int64{"30min": 1800, "6h": 21600, "1d": 86400, " 2h ": 7200} {
		if got, ok := JournalIntervalSeconds(in); !ok || got != want {
			t.Errorf("%q: %d %v", in, got, ok)
		}
	}
	if _, ok := JournalIntervalSeconds("6"); ok {
		t.Error("a bare number read as an interval")
	}
}
