package system

// openccu-lite task 228 (phase 1): the Storage page's USB sticks - every USB disk, mounted or not
// (a stick with no filesystem is what Format is for), with its partitions as usbmount mounted them.
// The helper decides what may be formatted or ejected (priv.usbDiskOK); this is the listing.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// USBPartition is one partition of a USB disk (or the disk itself when it has no table).
type USBPartition struct {
	Name     string `json:"name"`
	FSType   string `json:"fstype,omitempty"`
	Label    string `json:"label,omitempty"`
	LabelID  string `json:"label_id,omitempty"`
	Mount    string `json:"mount,omitempty"`
	ReadOnly bool   `json:"read_only,omitempty"`
	Size     int64  `json:"size_bytes"`
	Total    int64  `json:"total_bytes,omitempty"`
	Free     int64  `json:"free_bytes,omitempty"`
}

// USBDisk is one USB mass-storage disk.
type USBDisk struct {
	Name       string         `json:"name"`
	Vendor     string         `json:"vendor,omitempty"`
	Model      string         `json:"model,omitempty"`
	Serial     string         `json:"serial,omitempty"`
	Size       int64          `json:"size_bytes"`
	Removable  bool           `json:"removable"`
	Partitions []USBPartition `json:"partitions"`
	// Loop: a lab loop device standing in for a stick (the helper's test marker)
	Loop bool `json:"loop,omitempty"`
}

var usbDiskNameRe = regexp.MustCompile(`^sd[a-z]{1,2}$`)
var loopDiskNameRe = regexp.MustCompile(`^loop[0-9]{1,3}$`)

// USBLoopTestMarker admits loop devices as sticks on a lab system (priv.USBTestLoopMarker).
const USBLoopTestMarker = "/usr/local/etc/occulite/storage-test-loop"

func (r Root) sysAttr(p string) string { return strings.TrimSpace(readFile(r.join(p))) }

// USBDisks lists the USB disks: /sys/block/sd* whose device lies on the USB bus, and with the lab
// marker the loop devices that have a backing file.
func (r Root) USBDisks() []USBDisk {
	out := []USBDisk{}
	ents, err := os.ReadDir(r.join("/sys/block"))
	if err != nil {
		return out
	}
	_, markerErr := os.Stat(r.join(USBLoopTestMarker))
	mounts := r.mountTable()
	for _, e := range ents {
		name := e.Name()
		loop := loopDiskNameRe.MatchString(name)
		switch {
		case usbDiskNameRe.MatchString(name):
			link, err := filepath.EvalSymlinks(r.join("/sys/block/" + name))
			if err != nil || !strings.Contains(link, "/usb") {
				continue
			}
		case loop && markerErr == nil:
			if r.sysAttr("/sys/block/"+name+"/loop/backing_file") == "" {
				continue
			}
		default:
			continue
		}
		d := USBDisk{Name: name, Loop: loop, Removable: r.sysAttr("/sys/block/"+name+"/removable") == "1"}
		d.Vendor = r.sysAttr("/sys/block/" + name + "/device/vendor")
		d.Model = r.sysAttr("/sys/block/" + name + "/device/model")
		if n, err := strconv.ParseInt(r.sysAttr("/sys/block/"+name+"/size"), 10, 64); err == nil {
			d.Size = n * 512
		}
		if props := USBStickProps(r, "/dev/"+name); props != nil {
			d.Serial = props["ID_SERIAL_SHORT"]
			if d.Model == "" {
				d.Model = strings.ReplaceAll(props["ID_MODEL"], "_", " ")
			}
		}
		parts := []string{}
		pents, _ := os.ReadDir(r.join("/sys/block/" + name))
		for _, p := range pents {
			if strings.HasPrefix(p.Name(), name) && p.Name() != name {
				parts = append(parts, p.Name())
			}
		}
		sort.Strings(parts)
		if len(parts) == 0 {
			parts = []string{name}
		}
		for _, pn := range parts {
			p := USBPartition{Name: pn}
			sizePath := "/sys/block/" + name + "/" + pn + "/size"
			if pn == name {
				sizePath = "/sys/block/" + name + "/size"
			}
			if n, err := strconv.ParseInt(r.sysAttr(sizePath), 10, 64); err == nil {
				p.Size = n * 512
			}
			props := USBStickProps(r, "/dev/"+pn)
			p.FSType = props["ID_FS_TYPE"]
			p.Label = unescapeUdev(props["ID_FS_LABEL_ENC"])
			if p.Label == "" {
				p.Label = props["ID_FS_LABEL"]
			}
			p.LabelID = props["ID_FS_LABEL"]
			if m, ok := mounts["/dev/"+pn]; ok {
				p.Mount, p.ReadOnly = m.point, m.ro
				if p.FSType == "" {
					p.FSType = m.fstype
				}
				if st, ok := r.statfs(m.point); ok {
					p.Total, p.Free = st[0], st[1]
				}
			}
			if pn == name && p.FSType == "" && p.Mount == "" {
				continue // a whole disk without a filesystem: no partition to show
			}
			d.Partitions = append(d.Partitions, p)
		}
		if d.Partitions == nil {
			d.Partitions = []USBPartition{}
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

type mountEntry struct {
	point, fstype string
	ro            bool
}

func (r Root) mountTable() map[string]mountEntry {
	out := map[string]mountEntry{}
	for _, line := range strings.Split(readFile(r.join("/proc/mounts")), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		if _, seen := out[f[0]]; seen {
			continue
		}
		ro := false
		for _, o := range strings.Split(f[3], ",") {
			ro = ro || o == "ro"
		}
		out[f[0]] = mountEntry{point: strings.ReplaceAll(f[1], `\040`, " "), fstype: f[2], ro: ro}
	}
	return out
}

// MkfsAvailable is which of the two filesystems the image can make.
func (r Root) MkfsAvailable() []string {
	out := []string{}
	for _, fs := range []string{"exfat", "ext4"} {
		for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin"} {
			if _, err := os.Stat(r.join(dir + "/mkfs." + fs)); err == nil {
				out = append(out, fs)
				break
			}
		}
	}
	return out
}

// statfs is a mount point's size and free space (for the unprivileged user).
func (r Root) statfs(point string) ([2]int64, bool) {
	var st syscall.Statfs_t
	if syscall.Statfs(r.join(point), &st) != nil {
		return [2]int64{}, false
	}
	return [2]int64{int64(st.Blocks) * int64(st.Bsize), int64(st.Bavail) * int64(st.Bsize)}, true
}
