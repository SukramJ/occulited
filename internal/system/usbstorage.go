package system

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// openccu-lite task 161: the USB sticks the system has mounted. The fork's lite-usb-mount@ units
// mount a stick's partition in the host's namespace at /media/usb1…8 (upstream's usbmount, run
// from a unit instead of inside udevd's private mounts) and link the first one as /media/usb0 -
// the path upstream's backup and the directory backup target use. The Backup page lists them by
// label so the directory target can be pointed at one.

// USBStick is one mounted stick.
type USBStick struct {
	// Mount is where it is mounted (/media/usb1); Path is what to use in a target, /media/usb0
	// for the stick the link points at, the mount point otherwise.
	Mount string `json:"mount"`
	Path  string `json:"path"`
	// Linked: /media/usb0 points at this stick.
	Linked bool   `json:"linked"`
	Device string `json:"device"`
	FSType string `json:"fstype"`
	// Label, Vendor and Model come from udev's database for the partition; empty when unknown.
	Label string `json:"label,omitempty"`
	// LabelID is udev's ID_FS_LABEL: the label with spaces and odd characters made "_", which is
	// how the journal's target names a stick (task 216) - the fork's scripts read the same value.
	LabelID  string `json:"label_id,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Model    string `json:"model,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Total    int64  `json:"total_bytes"`
	Free     int64  `json:"free_bytes"`
}

var usbMountRe = regexp.MustCompile(`^/media/usb[1-8]$`)

// USBStickProps reads a partition's udev properties; a variable for the tests, which have no block
// devices.
var USBStickProps = func(r Root, dev string) map[string]string { return r.udevProps(dev) }

// USBSticks lists the sticks mounted at /media/usb1…8, in mount order.
func (r Root) USBSticks() []USBStick {
	out := []USBStick{}
	f, err := os.Open(r.join("/proc/mounts"))
	if err != nil {
		return out
	}
	defer f.Close()
	link, _ := os.Readlink(r.join("/media/usb0"))
	// occulited's own namespace sees /media read-only (ProtectSystem=strict): the per-mount options
	// in /proc/mounts say ro for every stick, the filesystem's own (mountinfo's super options) say
	// whether the stick itself is
	mountinfo := readFile(r.join("/proc/self/mountinfo"))
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || !usbMountRe.MatchString(fields[1]) || seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		s := USBStick{Mount: fields[1], Path: fields[1], Device: fields[0], FSType: fields[2]}
		if ro, ok := superReadOnly(mountinfo, fields[1]); ok {
			s.ReadOnly = ro
		} else {
			for _, o := range strings.Split(fields[3], ",") {
				if o == "ro" {
					s.ReadOnly = true
				}
			}
		}
		if link != "" && filepath.Clean(link) == s.Mount {
			s.Linked, s.Path = true, "/media/usb0"
		}
		props := USBStickProps(r, s.Device)
		s.Label = unescapeUdev(props["ID_FS_LABEL_ENC"])
		if s.Label == "" {
			s.Label = props["ID_FS_LABEL"]
		}
		s.LabelID = props["ID_FS_LABEL"]
		s.Vendor, s.Model = props["ID_VENDOR"], strings.ReplaceAll(props["ID_MODEL"], "_", " ")
		var st syscall.Statfs_t
		if syscall.Statfs(r.join(s.Mount), &st) == nil {
			s.Total, s.Free = int64(st.Blocks)*int64(st.Bsize), int64(st.Bavail)*int64(st.Bsize)
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Mount < out[j].Mount })
	return out
}

// superReadOnly reads whether the filesystem mounted on point is read-only by its super options (the
// fields after mountinfo's " - "), which do not change with a namespace's read-only view; ok is
// false when mountinfo has no mount there.
func superReadOnly(mountinfo, point string) (ro, ok bool) {
	for _, line := range strings.Split(mountinfo, "\n") {
		f := strings.Fields(line)
		if len(f) < 5 || unescapeMount(f[4]) != point {
			continue
		}
		for i := 5; i < len(f); i++ {
			if f[i] == "-" && i+3 < len(f) {
				ro, ok = hasMountOption(f[i+3], "ro"), true
				break
			}
		}
	}
	return ro, ok
}

// USBStickByLabel is the first mounted stick whose udev label (LabelID) is id.
func (r Root) USBStickByLabel(id string) (USBStick, bool) {
	if id == "" {
		return USBStick{}, false
	}
	for _, s := range r.USBSticks() {
		if s.LabelID == id {
			return s, true
		}
	}
	return USBStick{}, false
}

// udevProps reads udev's database entry of a block device (/run/udev/data/b<major>:<minor>, the
// E: lines); empty when there is none.
func (r Root) udevProps(dev string) map[string]string {
	props := map[string]string{}
	var st syscall.Stat_t
	if !strings.HasPrefix(dev, "/dev/") || syscall.Stat(r.join(dev), &st) != nil || st.Mode&syscall.S_IFMT != syscall.S_IFBLK {
		return props
	}
	major, minor := (st.Rdev>>8)&0xfff|(st.Rdev>>32)&^0xfff, st.Rdev&0xff|(st.Rdev>>12)&^0xff
	f, err := os.Open(r.join(fmt.Sprintf("/run/udev/data/b%d:%d", major, minor)))
	if err != nil {
		return props
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimPrefix(sc.Text(), "E:"), "="); ok && strings.HasPrefix(sc.Text(), "E:") {
			props[k] = v
		}
	}
	return props
}

// unescapeUdev turns udev's \xNN escapes (ID_FS_LABEL_ENC) back into bytes.
func unescapeUdev(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			var v byte
			if _, err := fmt.Sscanf(s[i+2:i+4], "%02x", &v); err == nil {
				b.WriteByte(v)
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
