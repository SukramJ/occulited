package system

import (
	"os"
	"strings"
	"testing"
)

// task 161: the sticks at /media/usb1…8 from /proc/mounts, the one /media/usb0 points at marked
// and given as the path to use; nothing else under /media, no duplicate.
func TestUSBSticks(t *testing.T) {
	r := Root(t.TempDir())
	for _, d := range []string{"proc", "media/usb1", "media/usb2", "media/net/x"} {
		if err := os.MkdirAll(r.join(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mounts := "/dev/root / ext4 ro 0 0\n" +
		"/dev/sda1 /media/usb1 vfat rw,nosuid,nodev,noexec,noatime,uid=0,gid=8100 0 0\n" +
		"/dev/sdb1 /media/usb2 ext4 ro,nosuid 0 0\n" +
		"/dev/sdb1 /media/usb2 ext4 ro,nosuid 0 0\n" +
		"host:/x /media/net/x nfs rw 0 0\n"
	if err := os.WriteFile(r.join("proc/mounts"), []byte(mounts), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/media/usb1", r.join("media/usb0")); err != nil {
		t.Fatal(err)
	}
	s := r.USBSticks()
	if len(s) != 2 || s[0].Mount != "/media/usb1" || !s[0].Linked || s[0].Path != "/media/usb0" || s[0].FSType != "vfat" || s[0].Device != "/dev/sda1" {
		t.Fatalf("%+v", s)
	}
	if s[1].Linked || s[1].Path != "/media/usb2" || !s[1].ReadOnly || s[1].Total <= 0 {
		t.Fatalf("%+v", s[1])
	}
	// occulited's namespace sees /media read-only (ProtectSystem=strict): the stick's own
	// filesystem, mountinfo's super options, decides (the Charly, 2026-09-25: every stick "read-only")
	_ = os.MkdirAll(r.join("proc/self"), 0o755)
	info := "584 583 8:1 / /media/usb1 ro,nosuid,nodev,relatime shared:226 master:439 - fuseblk /dev/sda1 rw,user_id=0,group_id=0\n" +
		"585 583 8:17 / /media/usb2 ro,nosuid shared:227 - ext4 /dev/sdb1 ro\n"
	_ = os.WriteFile(r.join("proc/self/mountinfo"), []byte(info), 0o644)
	mounts = strings.Replace(mounts, "vfat rw,nosuid", "vfat ro,nosuid", 1)
	_ = os.WriteFile(r.join("proc/mounts"), []byte(mounts), 0o644)
	s = r.USBSticks()
	if s[0].ReadOnly || !s[1].ReadOnly {
		t.Fatalf("super options: %+v %+v", s[0], s[1])
	}
	if (Root(t.TempDir())).USBSticks() == nil {
		t.Fatal("no mounts: an empty list, not nil")
	}
	if got := unescapeUdev(`BACKUP\x20STICK\x2f1`); got != "BACKUP STICK/1" {
		t.Fatalf("%q", got)
	}
}
