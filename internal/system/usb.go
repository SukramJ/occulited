package system

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// task 42: the USB devices, read out of sysfs. The image has no usbutils and busybox's lsusb
// prints bare hex ids, so occulited walks /sys/bus/usb/devices itself, the way the firmware's
// own scripts do (S47InitRFHardware, checkCoProcessor.sh) and the way ReadNetwork walks
// /sys/class/net - through Root, so a fixture tree tests it. Nothing here shells out or touches
// the privilege helper: every attribute is world-readable.

// USBDevice is one device (not an interface) of the USB bus.
type USBDevice struct {
	// Path is the sysfs name, which is the topology: "1-1.3" is bus 1, hub port 1, port 3;
	// "usb1" is the root hub of bus 1.
	Path   string `json:"path"`
	Parent string `json:"parent"` // the hub it hangs on ("1-1", "usb1"); "" for a root hub
	Bus    int    `json:"bus"`
	Dev    int    `json:"dev"`
	// Vendor and Product are the hex ids ("1b1f", "c020").
	Vendor       string `json:"vendor"`
	Product      string `json:"product"`
	VendorName   string `json:"vendor_name,omitempty"`
	ProductName  string `json:"product_name,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Serial       string `json:"serial,omitempty"`
	Speed        string `json:"speed,omitempty"` // Mbit/s as sysfs prints it: "12", "480", "5000"
	Class        string `json:"class"`           // bDeviceClass, hex: "09" is a hub
	Hub          bool   `json:"hub"`
	// Driver is what claimed the device's interfaces: "hub", "hb_rf_usb_2", "cp210x",
	// "usb-storage"; several joined with ", ".
	Driver string `json:"driver,omitempty"`
	// Nodes is what the device produced under /dev: a tty, a raw-uart, a hidraw, a block device.
	Nodes []string `json:"nodes"`
	// Radio is set when a raw-uart's device_type names this device's port: the radio module
	// the firmware assigned, with the protocols /var/hm_mode gave it.
	Radio *USBRadio `json:"radio"`
}

// USBRadio is the raw-uart join of a USB device.
type USBRadio struct {
	DeviceNode string   `json:"device_node"`
	DeviceType string   `json:"device_type"`
	Protocols  []string `json:"protocols"`
}

// knownUSB is the one concession to a name table: the eQ-3 ids the firmware itself knows
// (S47InitRFHardware, S48UpdateRFHardware) and the two HB-RF-USB variants the recovery system
// names. A device that carries its own product string shows that; these are for one that does
// not. No usb.ids is shipped (task 42).
var knownUSB = map[string]string{
	"1b1f:c020": "HmIP-RFUSB",
	"1b1f:c00f": "HM-CFG-USB-2",
	"0403:6f70": "HB-RF-USB",
	"10c4:8c07": "HB-RF-USB-2",
}

var knownUSBVendor = map[string]string{
	"1b1f": "eQ-3",
	"0403": "FTDI",
	"10c4": "Silicon Labs",
	"1d6b": "Linux Foundation",
}

// ReadUSB lists the devices of /sys/bus/usb/devices, root hubs included, sorted by bus and
// topology, with the raw-uart join against /var/hm_mode.
func (r Root) ReadUSB() []USBDevice {
	base := r.join("/sys/bus/usb/devices")
	entries, err := os.ReadDir(base)
	if err != nil {
		return []USBDevice{}
	}
	out := []USBDevice{}
	for _, e := range entries {
		name := e.Name()
		dir := filepath.Join(base, name)
		// an interface is "<device>:<config>.<interface>"; a device has idVendor
		if strings.Contains(name, ":") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err != nil {
			continue
		}
		attr := func(f string) string { return strings.TrimSpace(readFile(filepath.Join(dir, f))) }
		d := USBDevice{
			Path: name, Parent: usbParent(name),
			Vendor: attr("idVendor"), Product: attr("idProduct"),
			Manufacturer: attr("manufacturer"), Serial: attr("serial"), Speed: attr("speed"), Class: attr("bDeviceClass"),
			ProductName: attr("product"), Nodes: []string{},
		}
		d.Bus, _ = strconv.Atoi(attr("busnum"))
		d.Dev, _ = strconv.Atoi(attr("devnum"))
		d.Hub = d.Class == "09"
		if d.ProductName == "" {
			d.ProductName = knownUSB[d.Vendor+":"+d.Product]
		}
		d.VendorName = knownUSBVendor[d.Vendor]
		d.Driver, d.Nodes = usbInterfaces(base, name)
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bus != out[j].Bus {
			return out[i].Bus < out[j].Bus
		}
		// the root hub first, then by topology
		if (out[i].Parent == "") != (out[j].Parent == "") {
			return out[i].Parent == ""
		}
		return usbLess(out[i].Path, out[j].Path)
	})
	r.joinRadios(out)
	return out
}

// usbParent: "usb1" hangs on nothing, "1-1" on usb1, "1-1.3" on 1-1.
func usbParent(name string) string {
	if strings.HasPrefix(name, "usb") {
		return ""
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	if bus, _, ok := strings.Cut(name, "-"); ok {
		return "usb" + bus
	}
	return ""
}

// usbLess orders "1-1.2" before "1-1.10": numerically per port.
func usbLess(a, b string) bool {
	pa, pb := usbPorts(a), usbPorts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func usbPorts(name string) []int {
	_, rest, ok := strings.Cut(name, "-")
	if !ok {
		return nil
	}
	var out []int
	for _, p := range strings.Split(rest, ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// usbInterfaces reads the device's interface directories: the driver each was claimed by and
// the device nodes under them. A tty sits in <if>/tty/<name> (older kernels: <if>/<name>), a
// hidraw in <if>/<hid>/hidraw/<name>, a block device several levels down under host*/; the
// walk is bounded and follows no symlink (sysfs is full of them, all pointing up or sideways).
func usbInterfaces(base, dev string) (string, []string) {
	entries, _ := os.ReadDir(base)
	var drivers []string
	nodes := []string{}
	seen := map[string]bool{}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), dev+":") {
			continue
		}
		ifdir := filepath.Join(base, e.Name())
		if target, err := os.Readlink(filepath.Join(ifdir, "driver")); err == nil {
			d := filepath.Base(target)
			if !seen[d] {
				seen[d] = true
				drivers = append(drivers, d)
			}
		}
		nodes = append(nodes, usbNodes(ifdir, 0)...)
	}
	sort.Strings(nodes)
	return strings.Join(drivers, ", "), nodes
}

var usbNodeClasses = map[string]bool{"tty": true, "hidraw": true, "block": true, "raw-uart": true, "net": true}

func usbNodes(dir string, depth int) []string {
	if depth > 6 {
		return nil
	}
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() { // a symlink is not IsDir: never followed
			continue
		}
		name := e.Name()
		if usbNodeClasses[name] {
			kids, _ := os.ReadDir(filepath.Join(dir, name))
			for _, k := range kids {
				if name == "net" {
					out = append(out, "net: "+k.Name())
				} else {
					out = append(out, "/dev/"+k.Name())
				}
			}
			continue
		}
		// an older kernel puts ttyUSB0 straight into the interface directory
		if strings.HasPrefix(name, "ttyUSB") || strings.HasPrefix(name, "ttyACM") {
			out = append(out, "/dev/"+name)
			continue
		}
		if name == "power" || strings.HasPrefix(name, "ep_") {
			continue
		}
		out = append(out, usbNodes(filepath.Join(dir, name), depth+1)...)
	}
	return out
}

// joinRadios marks the device a raw-uart belongs to. The kernel writes
// /sys/class/raw-uart/<node>/device_type as "<product>@usb-<controller>-<port path>" for a USB
// module (checkCoProcessor.sh parses the same grammar): the port path is the device's devpath
// and the controller is the serial of the bus's root hub. The protocols come from /var/hm_mode,
// which names the node the firmware gave each stack.
func (r Root) joinRadios(devs []USBDevice) {
	class := r.join("/sys/class/raw-uart")
	entries, err := os.ReadDir(class)
	if err != nil {
		return
	}
	kv := r.HMMode()
	rootSerial := map[int]string{}
	for _, d := range devs {
		if d.Parent == "" {
			rootSerial[d.Bus] = d.Serial
		}
	}
	for _, e := range entries {
		node := e.Name()
		devType := strings.TrimSpace(readFile(filepath.Join(class, node, "device_type")))
		controller, port, ok := parseRawUARTUSB(devType)
		if !ok {
			continue
		}
		var protocols []string
		for _, p := range []struct{ prefix, proto string }{{"HM_HMRF", "BidCos-RF"}, {"HM_HMIP", "HmIP-RF"}} {
			if kv[p.prefix+"_DEVNODE"] == "/dev/"+node {
				protocols = append(protocols, p.proto)
			}
		}
		if protocols == nil {
			protocols = []string{}
		}
		for i := range devs {
			d := &devs[i]
			if d.Parent == "" || usbDevpath(d.Path) != port || rootSerial[d.Bus] != controller {
				continue
			}
			d.Radio = &USBRadio{DeviceNode: "/dev/" + node, DeviceType: devType, Protocols: protocols}
			d.Nodes = append(d.Nodes, "/dev/"+node)
			sort.Strings(d.Nodes)
		}
	}
}

// parseRawUARTUSB splits "eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3" into the controller
// ("0000:01:00.0") and the port path ("1.3"); false for a GPIO module ("GPIO@fe201000.serial").
func parseRawUARTUSB(devType string) (controller, port string, ok bool) {
	_, id, found := strings.Cut(devType, "@usb-")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// usbDevpath is the port path of a sysfs name: "1-1.3" → "1.3", the value of its devpath file.
func usbDevpath(name string) string {
	_, rest, _ := strings.Cut(name, "-")
	return rest
}
