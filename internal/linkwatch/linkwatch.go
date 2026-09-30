// Package linkwatch keeps a record of a boot whose network does not work (openccu-lite B-249,
// occulited B-34). On a Raspberry Pi 3 B the onboard LAN9514 (USB hub 0424:9514 with the Ethernet
// 0424:ec00, driver smsc95xx) sometimes did not come up at all after a warm reboot - no hub, no
// eth0, no USB stick - until the system was power cycled, and nothing of those boots survived:
// the journal is in RAM, and the boot record had no kernel messages. So occulited samples eth0
// during the first minutes after boot, and at Decide it writes what tells the board from the
// image when the network is not working then: eth0 without carrier (or with one that went away
// again), the status LED's condition (no /var/status/hasLink or hasIP), or - on a board that has
// the chip - neither eth0 nor the chip in the USB tree. The record holds the samples, every
// network interface with its addresses, the routes, eth0's counters, the USB tree, the USB host
// controller's port state, the LAN_RUN line and the fork's boot-time reset (lite-lan-reset), the
// network start's journal lines and the kernel's lines about the chip. Nothing is done about the
// network here - the fork's occu-lan-reset.service resets a missing chip before the network start.
package linkwatch

import (
	"bufio"
	"context"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sample is eth0 and the network status files at one moment, seconds after the kernel started.
type Sample struct {
	UptimeS   float64 `json:"uptime_s"`
	Carrier   string  `json:"carrier"`   // "1", "0", or "" when unreadable (down, or no device)
	Operstate string  `json:"operstate"` // up, down, lowerlayerdown, dormant, unknown; "" without the device
	HasLink   bool    `json:"has_link"`  // /var/status/hasLink (the network start's), the LED's condition
	HasIP     bool    `json:"has_ip"`    // /var/status/hasIP
}

// The reasons a record is written.
const (
	ReasonNoCarrier   = "no-carrier"     // eth0 is there and never had a carrier
	ReasonCarrierLost = "carrier-lost"   // eth0 had a carrier and has none at Decide
	ReasonNoEth       = "no-eth0"        // no eth0 (the chip's hub may be there)
	ReasonNoChip      = "no-lan9514"     // a board with the chip, and neither eth0 nor the chip in the USB tree
	ReasonNoLink      = "no-link-status" // /var/status/hasLink missing: the status LED shows no network
	ReasonNoIP        = "no-ip"          // /var/status/hasIP missing
)

// Link is one network interface as sysfs has it.
type Link struct {
	Name      string `json:"name"`
	Operstate string `json:"operstate,omitempty"`
	Carrier   string `json:"carrier,omitempty"`
	Flags     string `json:"flags,omitempty"`  // the hex flags word (IFF_UP = 0x1)
	Driver    string `json:"driver,omitempty"` // the device's driver, smsc95xx for the LAN9514
	Device    string `json:"device,omitempty"` // the device's sysfs name, 1-1.1:1.0 for the LAN9514
}

// USBDevice is one device of the USB tree, lsusb -t's content.
type USBDevice struct {
	Path       string   `json:"path"` // the sysfs name: usb1, 1-1, 1-1.1
	ID         string   `json:"id"`   // vendor:product
	Product    string   `json:"product,omitempty"`
	Speed      string   `json:"speed,omitempty"` // Mbit/s
	Authorized string   `json:"authorized,omitempty"`
	Drivers    []string `json:"drivers,omitempty"` // the interfaces' drivers, "-" for an interface without one
}

// Capture is the record of one boot without a working network.
type Capture struct {
	Interface string `json:"interface"`
	// Reasons say why it was written (the Reason constants), in that order.
	Reasons []string `json:"reasons"`
	// Board is the device tree's model, where there is one.
	Board   string  `json:"board,omitempty"`
	Written string  `json:"written"` // RFC 3339 on the wall clock of the moment
	UptimeS float64 `json:"uptime_s"`
	// Final: the watch is over (Until reached); a record without it was written at Decide and the
	// system went down, or occulited stopped, before the watch ended.
	Final bool `json:"final"`
	// CarrierAtS is when a carrier first came, when that was after Decide; absent otherwise.
	CarrierAtS *float64    `json:"carrier_at_s,omitempty"`
	Samples    []Sample    `json:"samples"`
	Links      []Link      `json:"links"`
	USB        []USBDevice `json:"usb"`
	// LAN9514 says what the tree has of the chip: the hub and the Ethernet device's paths, empty
	// when they are missing (a board without the chip has neither; a chip that did not enumerate has
	// the hub, or nothing).
	LAN9514 LAN9514 `json:"lan9514"`
	// USBEntries is `ls /sys/bus/usb/devices`: the devices and their interfaces as the kernel has them.
	USBEntries []string `json:"usb_entries"`
	// Controller is the USB host controller's port as dwc_otg's sysfs files show it; absent without.
	Controller *Controller `json:"controller,omitempty"`
	// LANRun is the chip's reset line; absent on a board without a GPIO line of that name.
	LANRun *LANRun `json:"lan_run,omitempty"`
	// LANReset is the fork's boot-time reset (/run/lite-lan-reset, key=value): result=present,
	// recovered, hub-only or failed, and what it did.
	LANReset map[string]string `json:"lan_reset,omitempty"`
	// Addresses are the interfaces' addresses (ip addr), Routes the kernel's routes (ip route, IPv4
	// and IPv6), Counters eth0's statistics (packets, errors, drops).
	Addresses []Addr            `json:"addresses,omitempty"`
	Routes    []Route           `json:"routes,omitempty"`
	Counters  map[string]uint64 `json:"counters,omitempty"`
	// NetworkLog are this boot's journal lines of the network start and the chip's reset (udhcpc's
	// among them), the last 200.
	NetworkLog []string `json:"network_log,omitempty"`
	// Kernel are this boot's kernel lines about USB, smsc95xx, eth0 and KFENCE (B-91), the last 200.
	Kernel []string `json:"kernel,omitempty"`
}

// LAN9514 is the chip in the USB tree (on a Pi 3 B+ its LAN7515: hub 0424:2514, Ethernet 0424:7800).
type LAN9514 struct {
	Hub      string `json:"hub,omitempty"`      // the path of 0424:9514 (0424:2514)
	Ethernet string `json:"ethernet,omitempty"` // the path of 0424:ec00 (0424:7800)
	Driver   string `json:"driver,omitempty"`   // the Ethernet interface's driver
}

// Controller is dwc_otg's view of its root port.
type Controller struct {
	Device       string `json:"device"`                 // the platform device, 3f980000.usb
	HPRT0        string `json:"hprt0,omitempty"`        // "HPRT0 = 0x00001005": connect status bit 0, enabled bit 2, power bit 12
	BusConnected string `json:"busconnected,omitempty"` // "Bus Connected = 0x1"
	BusPower     string `json:"buspower,omitempty"`     // "Bus Power = 0x1"
}

// LANRun is the chip's reset line (LAN_RUN on the firmware's GPIO expander), read only: its
// direction and value only while it is exported in sysfs (the reset exports it for the pulse).
type LANRun struct {
	GPIO      int    `json:"gpio"`
	Direction string `json:"direction,omitempty"`
	Value     string `json:"value,omitempty"`
}

// Addr is one interface's addresses in CIDR form.
type Addr struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
}

// Route is one route of /proc/net/route or /proc/net/ipv6_route.
type Route struct {
	Iface       string `json:"iface"`
	Destination string `json:"destination"` // CIDR
	Gateway     string `json:"gateway,omitempty"`
	Metric      int    `json:"metric"`
}

// the chip's USB ids: the Pi 3 B's LAN9514 and the 3 B+'s LAN7515
var (
	hubIDs = map[string]bool{"0424:9514": true, "0424:2514": true}
	ethIDs = map[string]bool{"0424:ec00": true, "0424:7800": true}
)

// Watcher watches one interface after boot.
type Watcher struct {
	// Root is the file system root (the tests' fake sysfs); "" = /.
	Root string
	// Iface is the interface watched; "" = eth0.
	Iface string
	// Interval between samples (5 s), Decide the uptime by which a carrier must have come (3 min;
	// the Charly's comes at about 20 s), Until the uptime the watch ends at (6 min).
	Interval, Decide, Until time.Duration
	// Uptime is the time since the kernel started; nil = /proc/uptime under Root.
	Uptime func() (time.Duration, bool)
	// Other says whether another interface carries the network (Wi-Fi with a carrier): a system
	// without a cable is not a lost link - unless the chip itself is missing. nil = never.
	Other func() bool
	// Kernel reads this boot's kernel lines; nil = none.
	Kernel func(ctx context.Context) []string
	// Log reads this boot's journal lines of the network start and the chip's reset; nil = none.
	Log func(ctx context.Context) []string
	// Addrs lists the interfaces' addresses; nil = the system's (net.Interfaces).
	Addrs func() []Addr
	// Save writes the record; it is called at Decide and again at the end.
	Save func(c Capture) error
	// Now is the wall clock; nil = time.Now.
	Now func() time.Time
}

func (w *Watcher) path(p string) string { return filepath.Join(w.root(), p) }

func (w *Watcher) root() string {
	if w.Root == "" {
		return "/"
	}
	return w.Root
}

func (w *Watcher) iface() string {
	if w.Iface == "" {
		return "eth0"
	}
	return w.Iface
}

func (w *Watcher) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

func (w *Watcher) durations() (interval, decide, until time.Duration) {
	interval, decide, until = w.Interval, w.Decide, w.Until
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if decide <= 0 {
		decide = 3 * time.Minute
	}
	if until <= decide {
		until = decide + 3*time.Minute
	}
	return
}

func (w *Watcher) uptime() (time.Duration, bool) {
	if w.Uptime != nil {
		return w.Uptime()
	}
	f := strings.Fields(read(w.path("/proc/uptime")))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// Run watches until Until or the end of ctx. It samples from the start: a carrier that comes and
// goes again before Decide is in the samples. At Decide it writes the record when the network is
// not working (Reasons), and again at Until with the samples since; otherwise it ends there. A
// start later than Decide (occulited restarted in a running system) watches nothing: that boot's
// first minutes are gone.
func (w *Watcher) Run(ctx context.Context) {
	interval, decide, until := w.durations()
	up, ok := w.uptime()
	if !ok || up >= decide || w.Save == nil {
		return
	}
	board, chipBoard := w.board()
	if !w.exists("/sys/class/net/"+w.iface()) && !w.hasChip() && !chipBoard {
		return // no such interface and no chip that should make one: nothing to watch
	}
	var samples []Sample
	var carrierAt *float64
	var reasons []string
	written, hadCarrier := false, false
	for {
		up, _ = w.uptime()
		s := w.sample(up)
		samples = append(samples, s)
		if s.Carrier == "1" {
			if !hadCarrier && written {
				v := s.UptimeS
				carrierAt = &v // the first carrier of the boot, after the record
			}
			hadCarrier = true
		}
		if !written && up >= decide {
			reasons = w.reasons(samples, chipBoard)
			if len(reasons) == 0 {
				return // the network works: no record
			}
			if w.Other != nil && w.Other() && !contains(reasons, ReasonNoChip) {
				return // another interface carries the network
			}
			_ = w.Save(w.capture(ctx, samples, carrierAt, up, false, reasons, board))
			written = true
		}
		if written && up >= until {
			_ = w.Save(w.capture(ctx, samples, carrierAt, up, true, reasons, board))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// reasons says why the network is not working at the last sample; none when it works.
func (w *Watcher) reasons(samples []Sample, chipBoard bool) []string {
	cur := samples[len(samples)-1]
	var r []string
	if w.exists("/sys/class/net/" + w.iface()) {
		had := false
		for _, s := range samples {
			if s.Carrier == "1" {
				had = true
			}
		}
		switch {
		case !had:
			r = append(r, ReasonNoCarrier)
		case cur.Carrier != "1":
			r = append(r, ReasonCarrierLost)
		}
	} else if chipBoard && !w.hasChip() {
		r = append(r, ReasonNoChip)
	} else {
		r = append(r, ReasonNoEth)
	}
	if w.exists("/var/status") {
		if !cur.HasLink {
			r = append(r, ReasonNoLink)
		}
		if !cur.HasIP {
			r = append(r, ReasonNoIP)
		}
	}
	return r
}

// board is the device tree's model and whether that board has the chip (a Raspberry Pi 2 or 3
// Model B, the B+ included).
func (w *Watcher) board() (string, bool) {
	m := strings.TrimRight(strings.TrimSpace(read(w.path("/proc/device-tree/model"))), "\x00")
	return m, strings.HasPrefix(m, "Raspberry Pi 3 Model B") || strings.HasPrefix(m, "Raspberry Pi 2 Model B")
}

func (w *Watcher) exists(p string) bool {
	_, err := os.Stat(w.path(p))
	return err == nil
}

func contains(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

func (w *Watcher) sample(up time.Duration) Sample {
	d := w.path("/sys/class/net/" + w.iface())
	return Sample{UptimeS: round(up.Seconds()), Carrier: strings.TrimSpace(read(d + "/carrier")), Operstate: strings.TrimSpace(read(d + "/operstate")),
		HasLink: w.exists("/var/status/hasLink"), HasIP: w.exists("/var/status/hasIP")}
}

func (w *Watcher) capture(ctx context.Context, samples []Sample, carrierAt *float64, up time.Duration, final bool, reasons []string, board string) Capture {
	c := Capture{Interface: w.iface(), Reasons: reasons, Board: board, Written: w.now().UTC().Format(time.RFC3339), UptimeS: round(up.Seconds()), Final: final,
		CarrierAtS: carrierAt, Samples: append([]Sample(nil), samples...), Links: w.links(), USB: w.usb(), USBEntries: w.usbEntries(),
		Controller: w.controller(), LANRun: w.lanRun(), LANReset: w.lanReset(), Addresses: w.addrs(), Routes: w.routes(), Counters: w.counters()}
	for _, d := range c.USB {
		switch {
		case hubIDs[d.ID]:
			c.LAN9514.Hub = d.Path
		case ethIDs[d.ID]:
			c.LAN9514.Ethernet = d.Path
			for _, drv := range d.Drivers {
				if drv != "-" {
					c.LAN9514.Driver = drv
				}
			}
		}
	}
	if w.Kernel != nil {
		kctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		lines := w.Kernel(kctx)
		cancel()
		var keep []string
		for _, l := range lines {
			if KernelLine(l) {
				keep = append(keep, l)
			}
		}
		c.Kernel = last(keep, 200)
	}
	if w.Log != nil {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		c.NetworkLog = last(w.Log(lctx), 200)
		cancel()
	}
	return c
}

func last(l []string, n int) []string {
	if len(l) > n {
		return l[len(l)-n:]
	}
	return l
}

// KernelLine says whether a kernel message is one the record keeps.
func KernelLine(msg string) bool {
	m := strings.ToLower(msg)
	for _, k := range []string{"usb", "smsc95xx", "eth0", "kfence", "lan78xx", "dwc_otg", "dwc2", "hub "} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// hasChip: the chip's hub or Ethernet is in the USB tree.
func (w *Watcher) hasChip() bool {
	for _, d := range w.usb() {
		if hubIDs[d.ID] || ethIDs[d.ID] {
			return true
		}
	}
	return false
}

func (w *Watcher) usbEntries() []string {
	entries, _ := os.ReadDir(w.path("/sys/bus/usb/devices"))
	out := []string{}
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// controller reads dwc_otg's port files of the first platform USB controller that has them.
func (w *Watcher) controller() *Controller {
	m, _ := filepath.Glob(w.path("/sys/devices/platform/soc/*.usb/hprt0"))
	sort.Strings(m)
	for _, f := range m {
		d := filepath.Dir(f)
		return &Controller{Device: filepath.Base(d), HPRT0: strings.TrimSpace(read(f)), BusConnected: strings.TrimSpace(read(d + "/busconnected")),
			BusPower: strings.TrimSpace(read(d + "/buspower"))}
	}
	return nil
}

// lanRun finds the line named LAN_RUN in the GPIO chips' device-tree line names (the same lookup as
// the fork's lite-lan-reset) and reads it where it is exported. It never exports it.
func (w *Watcher) lanRun() *LANRun {
	chips, _ := filepath.Glob(w.path("/sys/class/gpio/gpiochip*"))
	sort.Strings(chips)
	for _, c := range chips {
		names := strings.Split(read(filepath.Join(c, "device/of_node/gpio-line-names")), "\x00")
		for i, n := range names {
			if n != "LAN_RUN" {
				continue
			}
			base, err := strconv.Atoi(strings.TrimSpace(read(filepath.Join(c, "base"))))
			if err != nil {
				return nil
			}
			l := &LANRun{GPIO: base + i}
			g := w.path("/sys/class/gpio/gpio" + strconv.Itoa(l.GPIO))
			l.Direction, l.Value = strings.TrimSpace(read(g+"/direction")), strings.TrimSpace(read(g+"/value"))
			return l
		}
	}
	return nil
}

func (w *Watcher) lanReset() map[string]string {
	b := read(w.path("/run/lite-lan-reset"))
	if b == "" {
		return nil
	}
	out := map[string]string{}
	for _, l := range strings.Split(b, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(l), "="); ok && k != "" {
			out[k] = v
		}
	}
	return out
}

func (w *Watcher) addrs() []Addr {
	if w.Addrs != nil {
		return w.Addrs()
	}
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []Addr
	for _, i := range ifs {
		if i.Flags&net.FlagLoopback != 0 {
			continue
		}
		a := Addr{Name: i.Name, Addrs: []string{}}
		if as, err := i.Addrs(); err == nil {
			for _, x := range as {
				a.Addrs = append(a.Addrs, x.String())
			}
		}
		out = append(out, a)
	}
	return out
}

// routes reads the kernel's IPv4 and IPv6 routes (ip route's content) from /proc/net.
func (w *Watcher) routes() []Route {
	var out []Route
	if f, err := os.Open(w.path("/proc/net/route")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// Iface Destination Gateway Flags RefCnt Use Metric Mask ...
			x := strings.Fields(sc.Text())
			if len(x) < 8 || x[0] == "Iface" {
				continue
			}
			dst, gw, mask := ip4(x[1]), ip4(x[2]), ip4(x[7])
			if dst == nil || mask == nil {
				continue
			}
			ones, _ := net.IPMask(mask.To4()).Size()
			r := Route{Iface: x[0], Destination: dst.String() + "/" + strconv.Itoa(ones)}
			if gw != nil && !gw.Equal(net.IPv4zero) {
				r.Gateway = gw.String()
			}
			r.Metric, _ = strconv.Atoi(x[6])
			out = append(out, r)
		}
		f.Close()
	}
	if f, err := os.Open(w.path("/proc/net/ipv6_route")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			// dest destlen src srclen nexthop metric refcnt use flags iface
			x := strings.Fields(sc.Text())
			if len(x) < 10 || x[9] == "lo" {
				continue
			}
			dst, gw := ip6(x[0]), ip6(x[4])
			plen, err := strconv.ParseInt(x[1], 16, 32)
			if dst == nil || err != nil {
				continue
			}
			r := Route{Iface: x[9], Destination: dst.String() + "/" + strconv.Itoa(int(plen))}
			if gw != nil && !gw.Equal(net.IPv6zero) {
				r.Gateway = gw.String()
			}
			m, _ := strconv.ParseUint(x[5], 16, 32)
			r.Metric = int(m)
			out = append(out, r)
		}
		f.Close()
	}
	return out
}

// ip4 is /proc/net/route's little-endian hex address.
func ip4(h string) net.IP {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 4 {
		return nil
	}
	return net.IPv4(b[3], b[2], b[1], b[0])
}

func ip6(h string) net.IP {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 16 {
		return nil
	}
	return net.IP(b)
}

// counters are eth0's packet, error and drop counters.
func (w *Watcher) counters() map[string]uint64 {
	d := w.path("/sys/class/net/" + w.iface() + "/statistics")
	out := map[string]uint64{}
	for _, k := range []string{"rx_packets", "tx_packets", "rx_bytes", "tx_bytes", "rx_errors", "tx_errors", "rx_dropped", "tx_dropped", "rx_crc_errors", "rx_frame_errors", "collisions"} {
		if v, err := strconv.ParseUint(strings.TrimSpace(read(filepath.Join(d, k))), 10, 64); err == nil {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (w *Watcher) links() []Link {
	base := w.path("/sys/class/net")
	entries, _ := os.ReadDir(base)
	out := []Link{}
	for _, e := range entries {
		if e.Name() == "lo" {
			continue
		}
		d := filepath.Join(base, e.Name())
		l := Link{Name: e.Name(), Operstate: strings.TrimSpace(read(d + "/operstate")), Carrier: strings.TrimSpace(read(d + "/carrier")), Flags: strings.TrimSpace(read(d + "/flags"))}
		if t, err := filepath.EvalSymlinks(d + "/device/driver"); err == nil {
			l.Driver = filepath.Base(t)
		}
		if t, err := filepath.EvalSymlinks(d + "/device"); err == nil {
			l.Device = filepath.Base(t)
		}
		out = append(out, l)
	}
	return out
}

func (w *Watcher) usb() []USBDevice {
	base := w.path("/sys/bus/usb/devices")
	entries, _ := os.ReadDir(base)
	byDev := map[string]*USBDevice{}
	var order []string
	for _, e := range entries {
		n := e.Name()
		if strings.Contains(n, ":") {
			continue // an interface, read below
		}
		d := filepath.Join(base, n)
		v, p := strings.TrimSpace(read(d+"/idVendor")), strings.TrimSpace(read(d+"/idProduct"))
		if v == "" {
			continue
		}
		byDev[n] = &USBDevice{Path: n, ID: v + ":" + p, Product: strings.TrimSpace(read(d + "/product")), Speed: strings.TrimSpace(read(d + "/speed")), Authorized: strings.TrimSpace(read(d + "/authorized"))}
		order = append(order, n)
	}
	for _, e := range entries {
		n := e.Name()
		dev, _, ok := strings.Cut(n, ":")
		if !ok || byDev[dev] == nil {
			continue
		}
		drv := "-"
		if t, err := filepath.EvalSymlinks(filepath.Join(base, n, "driver")); err == nil {
			drv = filepath.Base(t)
		}
		byDev[dev].Drivers = append(byDev[dev].Drivers, drv)
	}
	sort.Strings(order)
	out := make([]USBDevice, 0, len(order))
	for _, n := range order {
		out = append(out, *byDev[n])
	}
	return out
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func round(s float64) float64 { return float64(int64(s*10+0.5)) / 10 }
