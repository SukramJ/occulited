package system

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/shares"
)

// openccu-lite task 228: a network share as ram-sync's target, share:<name>/<dir> - the copies on
// /media/net/<name>/<dir>, read while the share is mounted, never mounted for a read.

func TestParseJournalShareTarget(t *testing.T) {
	for _, tc := range []struct {
		in, name, dir string
		ok            bool
	}{
		{"share:nas/journal", "nas", "journal", true},
		{"share:nas/ccu/journal", "nas", "ccu/journal", true},
		{"share:NAS/journal", "", "", false},
		{"share:nas", "", "", false},
		{"share:nas/", "", "", false},
		{"share:nas/../etc", "", "", false},
		{"share:nas/a/b/c/d/e", "", "", false},
		{"usb:nas/journal", "", "", false},
	} {
		name, dir, ok := ParseJournalShareTarget(tc.in)
		if ok != tc.ok || name != tc.name || dir != tc.dir {
			t.Errorf("%q: %q %q %v", tc.in, name, dir, ok)
		}
	}
}

const shareMounted = "40 22 0:30 / /media/net/nas rw - autofs systemd-1 rw\n41 40 0:31 / /media/net/nas rw,nosuid - cifs //nas/logs rw,vers=3.1.1\n"

func TestJournalShareTarget(t *testing.T) {
	r := journalRoot(t, "rpi4", "STORAGE=ram-sync\nTARGET=share:nas/ccu/journal\n")
	putFile(t, r, "run/log/journal/0123abcd/system.journal", "x")
	// not mounted (idle): ram-sync, the path, no figures, nothing to read from
	c := r.ReadJournalConfig()
	if c.Target != "share:nas/ccu/journal" || c.TargetShare != "nas" || c.TargetDir != "ccu/journal" || c.TargetPath != "/media/net/nas/ccu/journal" {
		t.Fatalf("%q %q %q %q", c.Target, c.TargetShare, c.TargetDir, c.TargetPath)
	}
	if c.Effective != JournalRAMSync || !c.TargetOK || c.TargetUsage != nil || c.Fallback != "" {
		t.Errorf("idle: %q %v %v %q", c.Effective, c.TargetOK, c.TargetUsage, c.Fallback)
	}
	if r.JournalStickDir() != "" {
		t.Errorf("an idle share is read: %q", r.JournalStickDir())
	}
	// mounted: the figures and the copies to read
	putFile(t, r, "proc/self/mountinfo", shareMounted)
	putFile(t, r, "media/net/nas/ccu/journal/0123abcd/system@a-1-1.journal", strings.Repeat("j", 3000))
	c = r.ReadJournalConfig()
	if c.TargetUsage == nil || *c.TargetUsage <= 0 || c.TargetFree == nil {
		t.Errorf("mounted: %v %v", c.TargetUsage, c.TargetFree)
	}
	// B-215: the setting alone (the warnings' timer) does not measure the share
	if s := r.ReadJournalSetting(); s.TargetUsage != nil || s.TargetFree != nil || s.Effective != c.Effective || s.Target != c.Target || !s.TargetOK {
		t.Errorf("the setting measured the share: %v %v %q", s.TargetUsage, s.TargetFree, s.Effective)
	}
	if d := r.JournalStickDir(); d != "/media/net/nas/ccu/journal" || !r.JournalPersistent() {
		t.Errorf("mounted: %q %v", d, r.JournalPersistent())
	}
	// the last copy did not reach it: RAM and the script's reason
	putFile(t, r, "run/occu-journal/fallback", "the share nas cannot be reached, the journal stays in RAM\n")
	if c = r.ReadJournalConfig(); c.Effective != JournalRAM || !strings.Contains(c.Fallback, "cannot be reached") {
		t.Errorf("unreachable: %q %q", c.Effective, c.Fallback)
	}
	// read-only: cannot take the copies
	putFile(t, r, "proc/self/mountinfo", strings.Replace(shareMounted, "41 40 0:31 / /media/net/nas rw,nosuid - cifs //nas/logs rw,", "41 40 0:31 / /media/net/nas ro,nosuid - cifs //nas/logs ro,", 1))
	if c = r.ReadJournalConfig(); c.TargetOK {
		t.Error("a read-only share takes the copies")
	}
}

func TestSetJournalShareTarget(t *testing.T) {
	r := journalRoot(t, "rpi4", "")
	if _, err := r.SetJournalConfig(JournalConfig{Storage: "ram-sync", Target: "share:nas/journal"}); err != nil {
		t.Fatal(err)
	}
	if b := readFile(r.join(journalConfig)); !strings.Contains(b, "TARGET=share:nas/journal\n") {
		t.Errorf("%q", b)
	}
	for _, bad := range []JournalConfig{{Storage: "persistent", Target: "share:nas/journal"}, {Storage: "ram-sync", Target: "share:nas/../x"}} {
		if _, err := r.SetJournalConfig(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	v := journalRoot(t, "ova", "")
	if _, err := v.SetJournalConfig(JournalConfig{Target: "share:nas/journal"}); err == nil || !strings.Contains(err.Error(), "network share") {
		t.Errorf("ova default on a share: %v", err)
	}
}

// B-223: on a root-squashed export the copies are written and the system's own user cannot read
// them: the view says so while the share is mounted, and JournalCopiesUnreadable names the folder
// for the Log page. A share that is not mounted is not looked at.
func TestJournalShareCopiesUnreadable(t *testing.T) {
	r := journalRoot(t, "rpi4", "STORAGE=ram-sync\nTARGET=share:nas/ccu/journal\n")
	var asked []string
	unreadable := true
	old := journalReadBack
	journalReadBack = func(dir string) error {
		asked = append(asked, strings.TrimPrefix(dir, string(r)))
		if unreadable {
			return fmt.Errorf("%w: %s: permission denied", shares.ErrUnreadable, dir)
		}
		return nil
	}
	t.Cleanup(func() { journalReadBack = old })
	if c := r.ReadJournalConfig(); c.TargetUnreadable || len(asked) != 0 {
		t.Fatalf("idle: %v %q", c.TargetUnreadable, asked)
	}
	if d, err := r.JournalCopiesUnreadable(); d != "" || err != nil || len(asked) != 0 {
		t.Fatalf("idle: %q %v %q", d, err, asked)
	}
	putFile(t, r, "proc/self/mountinfo", shareMounted)
	if c := r.ReadJournalConfig(); !c.TargetUnreadable || !c.TargetOK {
		t.Fatalf("mounted: %v %v", c.TargetUnreadable, c.TargetOK)
	}
	if s := r.ReadJournalSetting(); s.TargetUnreadable {
		t.Error("the setting alone looked at the share")
	}
	d, err := r.JournalCopiesUnreadable()
	if d != "/media/net/nas/ccu/journal" || !errors.Is(err, shares.ErrUnreadable) {
		t.Fatalf("%q %v", d, err)
	}
	if asked[len(asked)-1] != "/media/net/nas/ccu/journal" {
		t.Fatalf("%q", asked)
	}
	unreadable = false
	if c := r.ReadJournalConfig(); c.TargetUnreadable {
		t.Error("readable copies reported unreadable")
	}
	if d, err := r.JournalCopiesUnreadable(); d != "" || err != nil {
		t.Fatalf("%q %v", d, err)
	}
	if d, err := Root("").JournalCopiesUnreadable(); d != "" || err != nil {
		t.Fatalf("no root: %q %v", d, err)
	}
}
