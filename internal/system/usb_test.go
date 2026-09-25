package system

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// usbFixture builds a sysfs tree from a table. The values are the ones read off the lab Pi 4
// (ccu-arm64, 2026-09-10): an HmIP-RFUSB on the on-board hub's port 3, claimed by piVCCU's
// hb_rf_usb_2, and the raw-uart1 it produced; plus a second bus with nothing on it. A sysfs
// interface directory name carries a colon, which is why this is a table and not a checked-in
// testdata tree (the fork's pre-pin check refuses such a name).
type usbFixtureDev struct {
	name  string
	attrs map[string]string
	// interfaces: name -> driver; a nested node directory, e.g. "tty/ttyUSB0", as extra entries
	ifaces map[string]string
	nodes  map[string][]string // interface -> paths of node directories under it
}

func writeUSBFixture(t *testing.T, root string, devs []usbFixtureDev) {
	t.Helper()
	base := filepath.Join(root, "sys/bus/usb/devices")
	for _, d := range devs {
		dir := filepath.Join(base, d.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for k, v := range d.attrs {
			if err := os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for name, driver := range d.ifaces {
			ifdir := filepath.Join(base, name)
			if err := os.MkdirAll(ifdir, 0o755); err != nil {
				t.Fatal(err)
			}
			_ = os.WriteFile(filepath.Join(ifdir, "bInterfaceClass"), []byte("ff\n"), 0o644)
			if driver != "" {
				// the real link points up into /sys/bus/usb/drivers; only its base name matters,
				// and a dangling relative link is what a fixture can afford
				if err := os.Symlink("../../../../bus/usb/drivers/"+driver, filepath.Join(ifdir, "driver")); err != nil {
					t.Fatal(err)
				}
			}
			// sysfs links that must never be followed
			_ = os.Symlink("../../../../bus/usb", filepath.Join(ifdir, "subsystem"))
			for _, n := range d.nodes[name] {
				if err := os.MkdirAll(filepath.Join(ifdir, n), 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func rpi4USB() []usbFixtureDev {
	return []usbFixtureDev{
		{name: "usb1", attrs: map[string]string{"idVendor": "1d6b", "idProduct": "0002", "busnum": "1", "devnum": "1", "devpath": "0", "speed": "480", "bDeviceClass": "09", "product": "xHCI Host Controller", "manufacturer": "Linux 6.18.34 xhci-hcd", "serial": "0000:01:00.0"},
			ifaces: map[string]string{"1-0:1.0": "hub"}, nodes: map[string][]string{"1-0:1.0": {"usb1-port1"}}},
		{name: "usb2", attrs: map[string]string{"idVendor": "1d6b", "idProduct": "0003", "busnum": "2", "devnum": "1", "devpath": "0", "speed": "5000", "bDeviceClass": "09", "product": "xHCI Host Controller", "manufacturer": "Linux 6.18.34 xhci-hcd", "serial": "0000:01:00.0"},
			ifaces: map[string]string{"2-0:1.0": "hub"}},
		{name: "1-1", attrs: map[string]string{"idVendor": "2109", "idProduct": "3431", "busnum": "1", "devnum": "2", "devpath": "1", "speed": "480", "bDeviceClass": "09", "product": "USB2.0 Hub"},
			ifaces: map[string]string{"1-1:1.0": "hub"}, nodes: map[string][]string{"1-1:1.0": {"1-1-port1", "1-1-port2", "1-1-port3", "1-1-port4"}}},
		{name: "1-1.3", attrs: map[string]string{"idVendor": "1b1f", "idProduct": "c020", "busnum": "1", "devnum": "3", "devpath": "1.3", "speed": "12", "bDeviceClass": "00", "product": "eQ-3 HmIP-RFUSB", "manufacturer": "Silicon Labs", "serial": "3014F711A061A7C00010CECF"},
			ifaces: map[string]string{"1-1.3:1.0": "hb_rf_usb_2"}},
	}
}

func TestReadUSB(t *testing.T) {
	root := t.TempDir()
	writeUSBFixture(t, root, rpi4USB())
	w := func(p, c string) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// the on-board GPIO module's class device and the stick's, as the Pi has them
	w("sys/class/raw-uart/raw-uart/device_type", "GPIO@fe201000.serial\n")
	w("sys/class/raw-uart/raw-uart1/device_type", "eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3\n")
	w("var/hm_mode", "HM_HMIP_DEV='HMIP-RFUSB'\nHM_HMIP_DEVNODE='/dev/raw-uart1'\nHM_HMRF_DEV='HMIP-RFUSB'\nHM_HMRF_DEVNODE='/dev/raw-uart1'\nHM_MODE='NORMAL'\n")

	devs := Root(root).ReadUSB()
	paths := []string{}
	for _, d := range devs {
		paths = append(paths, d.Path)
	}
	if want := []string{"usb1", "1-1", "1-1.3", "usb2"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("order: %v, want %v", paths, want)
	}
	byPath := map[string]USBDevice{}
	for _, d := range devs {
		byPath[d.Path] = d
	}
	stick := byPath["1-1.3"]
	if stick.Parent != "1-1" || stick.Bus != 1 || stick.Dev != 3 || stick.Vendor != "1b1f" || stick.Product != "c020" {
		t.Fatalf("stick: %+v", stick)
	}
	if stick.ProductName != "eQ-3 HmIP-RFUSB" || stick.VendorName != "eQ-3" || stick.Manufacturer != "Silicon Labs" || stick.Serial != "3014F711A061A7C00010CECF" || stick.Speed != "12" || stick.Hub {
		t.Fatalf("stick names: %+v", stick)
	}
	if stick.Driver != "hb_rf_usb_2" {
		t.Fatalf("stick driver: %q", stick.Driver)
	}
	if stick.Radio == nil || stick.Radio.DeviceNode != "/dev/raw-uart1" || !reflect.DeepEqual(stick.Radio.Protocols, []string{"BidCos-RF", "HmIP-RF"}) || stick.Radio.DeviceType != "eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3" {
		t.Fatalf("stick radio: %+v", stick.Radio)
	}
	if !reflect.DeepEqual(stick.Nodes, []string{"/dev/raw-uart1"}) {
		t.Fatalf("stick nodes: %v", stick.Nodes)
	}
	hub := byPath["1-1"]
	if !hub.Hub || hub.Parent != "usb1" || hub.Driver != "hub" || hub.Radio != nil || len(hub.Nodes) != 0 {
		t.Fatalf("hub: %+v", hub)
	}
	rootHub := byPath["usb1"]
	if rootHub.Parent != "" || !rootHub.Hub || rootHub.Serial != "0000:01:00.0" || rootHub.VendorName != "Linux Foundation" {
		t.Fatalf("root hub: %+v", rootHub)
	}
	// the second bus's root hub has the same controller serial (an xHCI has two): the port
	// path decides, and bus 2 has no "2-1.3"
	if byPath["usb2"].Radio != nil {
		t.Fatalf("usb2 must not be the radio")
	}
}

func TestReadUSBNamesAndNodes(t *testing.T) {
	root := t.TempDir()
	writeUSBFixture(t, root, []usbFixtureDev{
		{name: "usb1", attrs: map[string]string{"idVendor": "1d6b", "idProduct": "0002", "busnum": "1", "devnum": "1", "devpath": "0", "bDeviceClass": "09", "serial": "0000:00:14.0"},
			ifaces: map[string]string{"1-0:1.0": "hub"}},
		// a stick with no product string: the built-in map names it
		{name: "1-2", attrs: map[string]string{"idVendor": "10c4", "idProduct": "8c07", "busnum": "1", "devnum": "4", "devpath": "2", "bDeviceClass": "00"},
			ifaces: map[string]string{"1-2:1.0": "cp210x"}, nodes: map[string][]string{"1-2:1.0": {"ttyUSB0"}}},
		// an unknown one keeps its hex id and gets no name
		{name: "1-3", attrs: map[string]string{"idVendor": "abcd", "idProduct": "0001", "busnum": "1", "devnum": "5", "devpath": "3", "bDeviceClass": "00"},
			ifaces: map[string]string{"1-3:1.0": "usb-storage"}, nodes: map[string][]string{"1-3:1.0": {"host0/target0:0:0/0:0:0:0/block/sda"}}},
		// two interfaces, two drivers, a tty under tty/ and a hidraw deeper down; port 10 sorts after 3
		{name: "1-10", attrs: map[string]string{"idVendor": "0403", "idProduct": "6001", "busnum": "1", "devnum": "6", "devpath": "10", "bDeviceClass": "00", "product": "FT232R USB UART"},
			ifaces: map[string]string{"1-10:1.0": "ftdi_sio", "1-10:1.1": "usbhid"}, nodes: map[string][]string{"1-10:1.0": {"ttyUSB1/tty/ttyUSB1"}, "1-10:1.1": {"0003:0403:6001.0001/hidraw/hidraw0"}}},
	})
	devs := Root(root).ReadUSB()
	paths := []string{}
	for _, d := range devs {
		paths = append(paths, d.Path)
	}
	if want := []string{"usb1", "1-2", "1-3", "1-10"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("order: %v, want %v", paths, want)
	}
	cases := []struct {
		path, name, vendor, driver string
		nodes                      []string
	}{
		{"1-2", "HB-RF-USB-2", "Silicon Labs", "cp210x", []string{"/dev/ttyUSB0"}},
		{"1-3", "", "", "usb-storage", []string{"/dev/sda"}},
		{"1-10", "FT232R USB UART", "FTDI", "ftdi_sio, usbhid", []string{"/dev/hidraw0", "/dev/ttyUSB1"}},
	}
	for _, c := range cases {
		var d USBDevice
		for _, x := range devs {
			if x.Path == c.path {
				d = x
			}
		}
		if d.ProductName != c.name || d.VendorName != c.vendor || d.Driver != c.driver || !reflect.DeepEqual(d.Nodes, c.nodes) || d.Radio != nil {
			t.Errorf("%s: %+v", c.path, d)
		}
	}
	// no controller at all (a container, a VM without USB): an empty list, no error
	if got := Root(t.TempDir()).ReadUSB(); len(got) != 0 || got == nil {
		t.Fatalf("empty root: %#v", got)
	}
}

func TestParseRawUARTUSB(t *testing.T) {
	cases := []struct {
		in, controller, port string
		ok                   bool
	}{
		{"eQ-3 HmIP-RFUSB@usb-0000:01:00.0-1.3", "0000:01:00.0", "1.3", true},
		{"eQ-3 HmIP-RFUSB@usb-0000:02:1b.0-1", "0000:02:1b.0", "1", true}, // the OVA box
		{"GPIO@fe201000.serial", "", "", false},
		{"HB-RF-ETH@192.0.2.9", "", "", false},
		{"", "", "", false},
		{"x@usb-", "", "", false},
	}
	for _, c := range cases {
		controller, port, ok := parseRawUARTUSB(c.in)
		if controller != c.controller || port != c.port || ok != c.ok {
			t.Errorf("%q: %q %q %v", c.in, controller, port, ok)
		}
	}
	if usbParent("usb1") != "" || usbParent("1-1") != "usb1" || usbParent("1-1.3") != "1-1" || usbParent("2-1.4.2") != "2-1.4" {
		t.Fatal("usbParent")
	}
}
