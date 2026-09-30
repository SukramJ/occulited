package linkwatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSys is a sysfs with a Pi 3's LAN9514 (hub 1-1, Ethernet 1-1.1 with smsc95xx) and eth0.
func fakeSys(t *testing.T, carrier string, withEth bool) string {
	t.Helper()
	root := t.TempDir()
	w := func(p, v string) {
		t.Helper()
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(v), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(root, target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(root, target), full); err != nil {
			t.Fatal(err)
		}
	}
	usb := "sys/bus/usb/devices/"
	w(usb+"usb1/idVendor", "1d6b\n")
	w(usb+"usb1/idProduct", "0002\n")
	w(usb+"usb1/product", "DWC OTG Controller\n")
	w(usb+"1-1/idVendor", "0424\n")
	w(usb+"1-1/idProduct", "9514\n")
	w(usb+"1-1/speed", "480\n")
	w(usb+"1-1/authorized", "1\n")
	link("sys/bus/usb/drivers/hub", usb+"1-1:1.0/driver")
	if withEth {
		w(usb+"1-1.1/idVendor", "0424\n")
		w(usb+"1-1.1/idProduct", "ec00\n")
		w(usb+"1-1.1/speed", "480\n")
		link("sys/bus/usb/drivers/smsc95xx", usb+"1-1.1:1.0/driver")
		w("sys/class/net/eth0/carrier", carrier+"\n")
		w("sys/class/net/eth0/operstate", map[string]string{"1": "up", "0": "down"}[carrier]+"\n")
		w("sys/class/net/eth0/flags", "0x1003\n")
		link(usb+"1-1.1:1.0", "sys/class/net/eth0/device")
	}
	w("sys/class/net/lo/operstate", "unknown\n")
	return root
}

// clock is a fake uptime that each sample moves on by step.
type clock struct {
	mu   sync.Mutex
	at   time.Duration
	step time.Duration
}

func (c *clock) uptime() (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.at
	c.at += c.step
	return v, true
}

type saved struct {
	mu   sync.Mutex
	list []Capture
}

func (s *saved) save(c Capture) error {
	s.mu.Lock()
	s.list = append(s.list, c)
	s.mu.Unlock()
	return nil
}

func watcher(root string, c *clock, s *saved) *Watcher {
	return &Watcher{Root: root, Interval: time.Millisecond, Decide: 3 * time.Minute, Until: 6 * time.Minute, Uptime: c.uptime, Save: s.save,
		Kernel: func(context.Context) []string {
			return []string{"3.2 usb 1-1: new high-speed USB device number 2 using dwc_otg", "12.5 smsc95xx 1-1.1:1.0 eth0: register 'smsc95xx'", "13.0 audit: nothing to do with it", "3.6 BUG: KFENCE: memory corruption in hub_port_init+0x580/0xcf0"}
		}}
}

func TestNoCarrierIsRecorded(t *testing.T) {
	root := fakeSys(t, "0", true)
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	watcher(root, c, s).Run(context.Background())
	if len(s.list) != 2 {
		t.Fatalf("%d records, want the decision's and the final one", len(s.list))
	}
	first, last := s.list[0], s.list[1]
	if first.Final || !last.Final || first.UptimeS < 180 || last.UptimeS < 360 || last.CarrierAtS != nil {
		t.Fatalf("records %+v / %+v", first, last)
	}
	if len(last.Samples) < 15 || last.Samples[0].Carrier != "0" || last.Samples[0].Operstate != "down" {
		t.Fatalf("samples %+v", last.Samples)
	}
	if last.LAN9514.Hub != "1-1" || last.LAN9514.Ethernet != "1-1.1" || last.LAN9514.Driver != "smsc95xx" {
		t.Fatalf("chip %+v", last.LAN9514)
	}
	if len(last.Links) != 1 || last.Links[0].Name != "eth0" || last.Links[0].Driver != "smsc95xx" || last.Links[0].Device != "1-1.1:1.0" {
		t.Fatalf("links %+v", last.Links)
	}
	if len(last.USB) != 3 || last.USB[0].ID != "0424:9514" || strings.Join(last.USB[0].Drivers, ",") != "hub" {
		t.Fatalf("usb %+v", last.USB)
	}
	if len(last.Kernel) != 3 || strings.Contains(strings.Join(last.Kernel, "\n"), "audit") {
		t.Fatalf("kernel %q", last.Kernel)
	}
}

func TestCarrierInTimeWritesNothing(t *testing.T) {
	root := fakeSys(t, "1", true)
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	watcher(root, c, s).Run(context.Background())
	if len(s.list) != 0 {
		t.Fatalf("records %+v", s.list)
	}
}

func TestLateCarrierIsNoted(t *testing.T) {
	root := fakeSys(t, "0", true)
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	w := watcher(root, c, s)
	save := w.Save
	w.Save = func(cp Capture) error {
		// the link comes right after the decision's record
		_ = os.WriteFile(filepath.Join(root, "sys/class/net/eth0/carrier"), []byte("1\n"), 0o644)
		return save(cp)
	}
	w.Run(context.Background())
	if len(s.list) != 2 || s.list[1].CarrierAtS == nil || *s.list[1].CarrierAtS < 180 {
		t.Fatalf("records %+v", s.list)
	}
}

func TestChipWithoutEthernetIsRecorded(t *testing.T) {
	// the hub enumerated, the Ethernet did not: no eth0 at all
	root := fakeSys(t, "0", false)
	c, s := &clock{at: 20 * time.Second, step: 30 * time.Second}, &saved{}
	watcher(root, c, s).Run(context.Background())
	if len(s.list) != 2 || s.list[1].LAN9514.Hub != "1-1" || s.list[1].LAN9514.Ethernet != "" || s.list[1].Samples[0].Carrier != "" {
		t.Fatalf("records %+v", s.list)
	}
}

func TestSkips(t *testing.T) {
	// started after Decide (occulited restarted later): nothing
	root := fakeSys(t, "0", true)
	s := &saved{}
	watcher(root, &clock{at: 10 * time.Minute, step: time.Second}, s).Run(context.Background())
	// Wi-Fi carries the network: no record
	w := watcher(root, &clock{at: 20 * time.Second, step: 20 * time.Second}, s)
	w.Other = func() bool { return true }
	w.Run(context.Background())
	// no eth0 and no LAN9514 (a VM with another interface name): nothing to watch
	empty := t.TempDir()
	watcher(empty, &clock{at: 20 * time.Second, step: 20 * time.Second}, s).Run(context.Background())
	if len(s.list) != 0 {
		t.Fatalf("records %+v", s.list)
	}
	// ctx ends the watch
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	watcher(root, &clock{at: 20 * time.Second, step: time.Second}, s).Run(ctx)
	if len(s.list) != 0 {
		t.Fatalf("records after cancel %+v", s.list)
	}
}

func TestKernelLine(t *testing.T) {
	for msg, want := range map[string]bool{
		"smsc95xx 1-1.1:1.0 eth0: Link is Up - 100Mbps/Full": true,
		"usb 1-1.1: new high-speed USB device number 3":      true,
		"BUG: KFENCE: memory corruption in hub_port_init":    true,
		"audit: type=1334 prog-id=17 op=LOAD":                false,
		"raw-uart raw-uart: Reset radio module":              false,
	} {
		if KernelLine(msg) != want {
			t.Errorf("%q: %v", msg, !want)
		}
	}
}

func put(t *testing.T, root, p, v string) {
	t.Helper()
	full := filepath.Join(root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(v), 0o644); err != nil {
		t.Fatal(err)
	}
}

// the network start's status files: both present is a working network for the LED
func status(t *testing.T, root string, link, ip bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "var/status"), 0o755); err != nil {
		t.Fatal(err)
	}
	if link {
		put(t, root, "var/status/hasLink", "")
	}
	if ip {
		put(t, root, "var/status/hasIP", "")
	}
}

func TestWorkingNetworkWritesNothing(t *testing.T) {
	root := fakeSys(t, "1", true)
	status(t, root, true, true)
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	watcher(root, c, s).Run(context.Background())
	if len(s.list) != 0 {
		t.Fatalf("records %+v", s.list)
	}
}

// B-34: a carrier, but the network start never got an address - the LED's no-network
func TestCarrierWithoutIPIsRecorded(t *testing.T) {
	root := fakeSys(t, "1", true)
	status(t, root, true, false)
	put(t, root, "proc/net/route", "Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\n"+
		"eth0\t00000000\t0117A8C0\t0003\t0\t0\t10\t00000000\t0\t0\t0\n"+
		"eth0\t0017A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n")
	put(t, root, "proc/net/ipv6_route", "fe800000000000000000000000000000 40 00000000000000000000000000000000 00 00000000000000000000000000000000 00000100 00000001 00000000 00000001     eth0\n"+
		"00000000000000000000000000000001 80 00000000000000000000000000000000 00 00000000000000000000000000000000 00000000 00000002 00000000 80200001       lo\n")
	for k, v := range map[string]string{"rx_packets": "12", "tx_packets": "40", "rx_errors": "0", "tx_dropped": "3"} {
		put(t, root, "sys/class/net/eth0/statistics/"+k, v+"\n")
	}
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	w := watcher(root, c, s)
	w.Addrs = func() []Addr { return []Addr{{Name: "eth0", Addrs: []string{"fe80::1/64"}}} }
	w.Log = func(context.Context) []string {
		return []string{"19.1 udhcpc: started, v1.38.0", "19.2 udhcpc: broadcasting discover", "25.2 udhcpc: no lease, failing"}
	}
	w.Run(context.Background())
	if len(s.list) != 2 {
		t.Fatalf("%d records, want 2", len(s.list))
	}
	r := s.list[1]
	if strings.Join(r.Reasons, ",") != ReasonNoIP || r.CarrierAtS != nil {
		t.Fatalf("reasons %v, carrier_at %v", r.Reasons, r.CarrierAtS)
	}
	if len(r.Samples) < 15 || r.Samples[0].Carrier != "1" || !r.Samples[0].HasLink || r.Samples[0].HasIP {
		t.Fatalf("samples %+v", r.Samples)
	}
	want := []Route{{Iface: "eth0", Destination: "0.0.0.0/0", Gateway: "192.168.23.1", Metric: 10}, {Iface: "eth0", Destination: "192.168.23.0/24"},
		{Iface: "eth0", Destination: "fe80::/64", Metric: 256}}
	if len(r.Routes) != 3 || r.Routes[0] != want[0] || r.Routes[1] != want[1] || r.Routes[2] != want[2] {
		t.Fatalf("routes %+v", r.Routes)
	}
	if r.Counters["rx_packets"] != 12 || r.Counters["tx_dropped"] != 3 || len(r.Counters) != 4 {
		t.Fatalf("counters %v", r.Counters)
	}
	if len(r.Addresses) != 1 || r.Addresses[0].Addrs[0] != "fe80::1/64" || len(r.NetworkLog) != 3 {
		t.Fatalf("addresses %+v, log %q", r.Addresses, r.NetworkLog)
	}
}

func TestCarrierLostIsRecorded(t *testing.T) {
	root := fakeSys(t, "1", true)
	status(t, root, true, true)
	c, s := &clock{at: 20 * time.Second, step: 20 * time.Second}, &saved{}
	w := watcher(root, c, s)
	n := 0
	w.Uptime = func() (time.Duration, bool) {
		n++
		if n == 4 { // the link goes away after a minute, and the network start takes the status files back
			put(t, root, "sys/class/net/eth0/carrier", "0\n")
			_ = os.Remove(filepath.Join(root, "var/status/hasLink"))
		}
		return c.uptime()
	}
	w.Run(context.Background())
	if len(s.list) != 2 {
		t.Fatalf("%d records", len(s.list))
	}
	r := s.list[0]
	if strings.Join(r.Reasons, ",") != ReasonCarrierLost+","+ReasonNoLink || r.Samples[0].Carrier != "1" || r.Samples[len(r.Samples)-1].Carrier != "0" {
		t.Fatalf("reasons %v, samples %+v", r.Reasons, r.Samples)
	}
}

// the hung boots of openccu-lite B-249: no hub, no eth0 on a Pi 3 B - recorded even with Wi-Fi up
func TestMissingChipIsRecorded(t *testing.T) {
	root := t.TempDir()
	put(t, root, "proc/device-tree/model", "Raspberry Pi 3 Model B Rev 1.2\x00")
	put(t, root, "sys/bus/usb/devices/usb1/idVendor", "1d6b\n")
	put(t, root, "sys/bus/usb/devices/usb1/idProduct", "0002\n")
	put(t, root, "sys/bus/usb/devices/1-0:1.0/bInterfaceClass", "09\n")
	put(t, root, "sys/devices/platform/soc/3f980000.usb/hprt0", "HPRT0 = 0x00001000\n")
	put(t, root, "sys/devices/platform/soc/3f980000.usb/busconnected", "Bus Connected = 0x0\n")
	put(t, root, "sys/devices/platform/soc/3f980000.usb/buspower", "Bus Power = 0x1\n")
	put(t, root, "sys/class/gpio/gpiochip512/base", "512\n")
	put(t, root, "sys/class/gpio/gpiochip512/device/of_node/gpio-line-names", "ID_SDA\x00LAN_RUN_BOOT\x00")
	put(t, root, "sys/class/gpio/gpiochip568/base", "568\n")
	put(t, root, "sys/class/gpio/gpiochip568/device/of_node/gpio-line-names", "BT_ON\x00WL_ON\x00STATUS_LED\x00LAN_RUN\x00HDMI_HPD_N\x00")
	put(t, root, "run/lite-lan-reset", "result=failed\nmodel=Raspberry Pi 3 Model B Rev 1.2\nlan_run_gpio=571\nlan_run_before=out/1\ncontroller=dwc_otg\n")
	put(t, root, "sys/class/net/wlan0/carrier", "1\n")
	status(t, root, false, true)
	c, s := &clock{at: 20 * time.Second, step: 30 * time.Second}, &saved{}
	w := watcher(root, c, s)
	w.Other = func() bool { return true }
	w.Run(context.Background())
	if len(s.list) != 2 {
		t.Fatalf("%d records, want 2", len(s.list))
	}
	r := s.list[1]
	if strings.Join(r.Reasons, ",") != ReasonNoChip+","+ReasonNoLink || r.Board != "Raspberry Pi 3 Model B Rev 1.2" {
		t.Fatalf("reasons %v, board %q", r.Reasons, r.Board)
	}
	if r.LAN9514 != (LAN9514{}) || strings.Join(r.USBEntries, " ") != "1-0:1.0 usb1" {
		t.Fatalf("chip %+v, entries %v", r.LAN9514, r.USBEntries)
	}
	if r.Controller == nil || r.Controller.Device != "3f980000.usb" || r.Controller.HPRT0 != "HPRT0 = 0x00001000" || r.Controller.BusConnected != "Bus Connected = 0x0" {
		t.Fatalf("controller %+v", r.Controller)
	}
	if r.LANRun == nil || r.LANRun.GPIO != 571 || r.LANRun.Direction != "" {
		t.Fatalf("lan_run %+v", r.LANRun)
	}
	if r.LANReset["result"] != "failed" || r.LANReset["lan_run_before"] != "out/1" {
		t.Fatalf("lan_reset %v", r.LANReset)
	}
	if r.Counters != nil || len(r.Kernel) != 3 {
		t.Fatalf("counters %v, kernel %q", r.Counters, r.Kernel)
	}
	// exported (the reset's pulse): its state is read
	put(t, root, "sys/class/gpio/gpio571/direction", "out\n")
	put(t, root, "sys/class/gpio/gpio571/value", "1\n")
	if l := w.lanRun(); l == nil || l.Direction != "out" || l.Value != "1" {
		t.Fatalf("exported lan_run %+v", l)
	}
}

// a board without the chip and without eth0 is not watched, whatever the status files say
func TestOtherBoardWithoutEthIsNotWatched(t *testing.T) {
	root := t.TempDir()
	put(t, root, "proc/device-tree/model", "Raspberry Pi 4 Model B Rev 1.1\x00")
	status(t, root, false, false)
	s := &saved{}
	watcher(root, &clock{at: 20 * time.Second, step: 30 * time.Second}, s).Run(context.Background())
	if len(s.list) != 0 {
		t.Fatalf("records %+v", s.list)
	}
}

// Wi-Fi carries the network and eth0 has no cable: no record (the LED's condition alone does not
// override that; only a missing chip does)
func TestWiFiWithoutCableWritesNothing(t *testing.T) {
	root := fakeSys(t, "0", true)
	status(t, root, false, true)
	s := &saved{}
	w := watcher(root, &clock{at: 20 * time.Second, step: 30 * time.Second}, s)
	w.Other = func() bool { return true }
	w.Run(context.Background())
	if len(s.list) != 0 {
		t.Fatalf("records %+v", s.list)
	}
}
