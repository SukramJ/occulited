package system

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/wifi"
)

// Task 89: the Network page's Wi-Fi. The settings (/etc/config/wifi) and the networks
// (/etc/config/wpa_supplicant.conf, 0600: the helper writes it and reads it back, and the secrets
// never leave occulited) are written here; occu-wifi.service applies them as root - restarted for
// the switch, the interface or the country, reloaded for the addressing and the preference.
// Scanning, the connection's state and a new network's activation go over wpa_supplicant's control
// socket, whose group is occulite. A change made over the Wi-Fi itself is reverted after
// RevertAfter unless confirmed, as the Ethernet settings are (the network transaction).

// ErrWiFiInvalid is a request the rules refuse; the message says which rule.
var ErrWiFiInvalid = errors.New("invalid Wi-Fi request")

// WiFi is the Wi-Fi service.
type WiFi struct {
	Root Root
	// LocalDir is occulited's own directory for its end of the control protocol.
	LocalDir string
	// Run starts and reloads occu-wifi.service; the default goes through the helper.
	Run Runner
	// RevertAfter is the confirmation window of a change made over the Wi-Fi (default 90 s).
	RevertAfter time.Duration
	// ScanWait is how long a scan waits for results (default 8 s).
	ScanWait time.Duration
	Log      *slog.Logger

	mu      sync.Mutex
	pending *wifiPending
}

type wifiPending struct {
	settings []byte // the files as they were
	conf     []byte
	deadline time.Time
	timer    *time.Timer
}

// WiFiChip is one Wi-Fi device, also while its driver is unloaded.
type WiFiChip struct {
	Iface   string `json:"iface"`
	Kind    string `json:"kind"`    // onboard, usb
	Present bool   `json:"present"` // the interface exists (the driver is loaded)
}

// WiFiView is GET /wifi.
type WiFiView struct {
	Chips    []WiFiChip    `json:"chips"`
	Settings wifi.Settings `json:"settings"`
	// State: no-chip, off, starting, not-configured, connecting, connected, disconnected
	State     string         `json:"state"`
	Status    *wifi.Status   `json:"status,omitempty"`
	Signal    *wifi.Signal   `json:"signal,omitempty"`
	Addresses []string       `json:"addresses"`
	Networks  []wifi.Network `json:"networks"`
	// Confirm is the deadline of a change made over the Wi-Fi that waits for its confirmation.
	Confirm *time.Time `json:"confirm,omitempty"`
	// SetupError is why the last setup file on the boot partition was not used.
	SetupError string `json:"setup_error,omitempty"`
}

func (w *WiFi) logger() *slog.Logger {
	if w.Log != nil {
		return w.Log
	}
	return slog.Default()
}

func (w *WiFi) runner() Runner {
	if w.Run != nil {
		return w.Run
	}
	return run
}

func (w *WiFi) ctrl(iface string) wifi.Ctrl {
	return wifi.Ctrl{Dir: w.Root.join(wifi.CtrlDir), Iface: iface, LocalDir: w.LocalDir}
}

// Chips lists the Wi-Fi devices: the onboard SDIO chip (its interface wlan0 appears only with its
// driver) and every interface with a wireless directory, USB sticks marked.
func (w *WiFi) Chips() []WiFiChip {
	var out []WiFiChip
	seen := map[string]bool{}
	ents, _ := os.ReadDir(w.Root.join("/sys/class/net"))
	for _, e := range ents {
		if _, err := os.Stat(w.Root.join(filepath.Join("/sys/class/net", e.Name(), "wireless"))); err != nil {
			continue
		}
		kind := "onboard"
		if dev, err := filepath.EvalSymlinks(w.Root.join(filepath.Join("/sys/class/net", e.Name(), "device"))); err == nil && strings.Contains(dev, "/usb") {
			kind = "usb"
		}
		out = append(out, WiFiChip{Iface: e.Name(), Kind: kind, Present: true})
		seen[e.Name()] = true
	}
	if !seen["wlan0"] {
		sdio, _ := os.ReadDir(w.Root.join("/sys/bus/sdio/devices"))
		for _, e := range sdio {
			if strings.Contains(readFile(w.Root.join(filepath.Join("/sys/bus/sdio/devices", e.Name(), "uevent"))), "OF_NAME=wifi") {
				out = append([]WiFiChip{{Iface: "wlan0", Kind: "onboard"}}, out...)
				break
			}
		}
	}
	return out
}

func (w *WiFi) settings() wifi.Settings {
	return wifi.ParseSettings(readFile(w.Root.join(wifi.SettingsFile)))
}

func (w *WiFi) networks() (string, []wifi.Network) {
	return wifi.ParseConf(readFile(w.Root.join(wifi.ConfFile)))
}

// View is the page's picture of Wi-Fi.
func (w *WiFi) View() WiFiView {
	v := WiFiView{Chips: w.Chips(), Settings: w.settings(), Addresses: []string{}, Networks: []wifi.Network{}}
	_, nets := w.networks()
	for _, n := range nets {
		n.PSK, n.Passphrase = "", ""
		v.Networks = append(v.Networks, n)
	}
	v.SetupError = strings.TrimSpace(readFile(w.Root.join(wifi.SetupErrorFile)))
	w.mu.Lock()
	if w.pending != nil {
		d := w.pending.deadline
		v.Confirm = &d
	}
	w.mu.Unlock()
	switch {
	case len(v.Chips) == 0:
		v.State = "no-chip"
		return v
	case !v.Settings.Enabled:
		v.State = "off"
		return v
	}
	if ifc, err := net.InterfaceByName(v.Settings.Iface); err == nil {
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				v.Addresses = append(v.Addresses, ipn.String())
			}
		}
	}
	c := w.ctrl(v.Settings.Iface)
	st, err := c.Request("STATUS")
	if err != nil {
		v.State = "starting"
		return v
	}
	status := wifi.ParseStatus(st)
	v.Status = &status
	switch {
	case len(nets) == 0:
		v.State = "not-configured"
	case status.State == "COMPLETED" && len(v.Addresses) > 0:
		v.State = "connected"
		if sig, err := c.Request("SIGNAL_POLL"); err == nil {
			s := wifi.ParseSignal(sig)
			v.Signal = &s
		}
	case status.State == "COMPLETED", status.State == "ASSOCIATING", status.State == "ASSOCIATED",
		status.State == "AUTHENTICATING", status.State == "4WAY_HANDSHAKE", status.State == "GROUP_HANDSHAKE", status.State == "SCANNING":
		v.State = "connecting"
	default:
		v.State = "disconnected"
	}
	return v
}

// ScanMinWait is how long a scan waits at least before it answers.
var ScanMinWait = 4 * time.Second

// Scan asks the supplicant to scan and answers what is in range, saved networks marked.
func (w *WiFi) Scan(ctx context.Context) ([]wifi.ScanResult, []string, error) {
	s := w.settings()
	if !s.Enabled {
		return nil, nil, fmt.Errorf("%w: Wi-Fi is switched off", ErrWiFiInvalid)
	}
	c := w.ctrl(s.Iface)
	if r, err := c.Request("SCAN"); err != nil {
		return nil, nil, err
	} else if r = strings.TrimSpace(r); r != "OK" && r != "FAIL-BUSY" {
		return nil, nil, fmt.Errorf("wpa_supplicant SCAN: %s", r)
	}
	wait := w.ScanWait
	if wait == 0 {
		wait = 8 * time.Second
	}
	start := time.Now()
	deadline := start.Add(wait)
	var res []wifi.ScanResult
	for time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(time.Second)
		r, err := c.Request("SCAN_RESULTS")
		if err != nil {
			return nil, nil, err
		}
		res = wifi.ParseScanResults(r)
		// the results before the scan (the connected network) are there at once; a full scan of
		// both bands takes the onboard chip 3 to 4 s (the Pi 4, 2026-09-19)
		if len(res) > 0 && time.Since(start) >= ScanMinWait {
			break
		}
	}
	_, nets := w.networks()
	var saved []string
	for _, n := range nets {
		saved = append(saved, n.SSID)
	}
	return res, saved, nil
}

// PutSettings writes the settings and applies them. overWiFi is whether the request came over the
// Wi-Fi interface: then the change waits for its confirmation and is reverted without one.
func (w *WiFi) PutSettings(ctx context.Context, s wifi.Settings, overWiFi bool) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrWiFiInvalid, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	old := w.settings()
	country, nets := w.networks()
	if overWiFi {
		w.armLocked()
	}
	if err := writeFileAtomic(w.Root.join(wifi.SettingsFile), []byte(s.Format()), 0o644); err != nil {
		return err
	}
	restart := old.Enabled != s.Enabled || old.Iface != s.Iface || old.Country != s.Country
	if country != s.Country && (len(nets) > 0 || country != "") {
		if err := writeFileAtomic(w.Root.join(wifi.ConfFile), []byte(wifi.Conf(s.Country, nets)), 0o600); err != nil {
			return err
		}
	}
	return w.apply(ctx, restart)
}

// Connect saves a network (it replaces one of the same SSID) with the highest priority, switches
// Wi-Fi on when it is off, and has the supplicant re-read its file.
func (w *WiFi) Connect(ctx context.Context, ssid, security, password string, hidden, overWiFi bool) error {
	n, err := wifi.NewNetwork(ssid, security, password, hidden, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrWiFiInvalid, err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.settings()
	_, nets := w.networks()
	top := 0
	kept := make([]wifi.Network, 0, len(nets))
	for _, x := range nets {
		if x.SSID == ssid {
			continue
		}
		if x.Priority >= top {
			top = x.Priority + 1
		}
		kept = append(kept, x)
	}
	n.Priority = top
	if overWiFi {
		w.armLocked()
	}
	if err := writeFileAtomic(w.Root.join(wifi.ConfFile), []byte(wifi.Conf(s.Country, append(kept, n))), 0o600); err != nil {
		return err
	}
	w.logger().Info("wifi: network saved", "ssid", ssid, "security", security)
	if !s.Enabled {
		s.Enabled = true
		if err := writeFileAtomic(w.Root.join(wifi.SettingsFile), []byte(s.Format()), 0o644); err != nil {
			return err
		}
		return w.apply(ctx, true)
	}
	return w.reconfigure(ctx, s.Iface)
}

// Forget drops a saved network.
func (w *WiFi) Forget(ctx context.Context, ssid string, overWiFi bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.settings()
	_, nets := w.networks()
	kept := make([]wifi.Network, 0, len(nets))
	found := false
	for _, x := range nets {
		if x.SSID == ssid {
			found = true
			continue
		}
		kept = append(kept, x)
	}
	if !found {
		return fmt.Errorf("%w: no saved network %q", ErrWiFiInvalid, ssid)
	}
	if overWiFi {
		w.armLocked()
	}
	if err := writeFileAtomic(w.Root.join(wifi.ConfFile), []byte(wifi.Conf(s.Country, kept)), 0o600); err != nil {
		return err
	}
	w.logger().Info("wifi: network forgotten", "ssid", ssid)
	if !s.Enabled {
		return nil
	}
	return w.reconfigure(ctx, s.Iface)
}

// reconfigure has the supplicant re-read its file; when its socket is not there (it is starting),
// occu-wifi is restarted instead.
func (w *WiFi) reconfigure(ctx context.Context, iface string) error {
	if r, err := w.ctrl(iface).Request("RECONFIGURE"); err == nil && strings.TrimSpace(r) == "OK" {
		return nil
	}
	return w.apply(ctx, true)
}

func (w *WiFi) apply(ctx context.Context, restart bool) error {
	verb := "reload"
	if restart {
		verb = "restart"
	}
	if out, err := w.runner()(ctx, "systemctl", verb, "--no-pager", "--", "occu-wifi.service"); err != nil {
		return fmt.Errorf("occu-wifi %s: %v: %s", verb, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// armLocked keeps the files as they are now and reverts to them after the window, unless Confirm
// comes first; a second change inside the window keeps the first state as the one to go back to.
func (w *WiFi) armLocked() {
	after := w.RevertAfter
	if after == 0 {
		after = 90 * time.Second
	}
	if w.pending == nil {
		w.pending = &wifiPending{settings: []byte(readFile(w.Root.join(wifi.SettingsFile))), conf: []byte(readFile(w.Root.join(wifi.ConfFile)))}
	} else if w.pending.timer != nil {
		w.pending.timer.Stop()
	}
	w.pending.deadline = time.Now().Add(after)
	p := w.pending
	p.timer = time.AfterFunc(after, func() { w.revert(p) })
}

func (w *WiFi) revert(p *wifiPending) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != p {
		return
	}
	w.pending = nil
	w.logger().Warn("wifi: the change made over Wi-Fi was not confirmed; the previous settings are back")
	_ = writeFileAtomic(w.Root.join(wifi.SettingsFile), p.settings, 0o644)
	_ = writeFileAtomic(w.Root.join(wifi.ConfFile), p.conf, 0o600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_ = w.apply(ctx, true)
}

// Confirm keeps a change made over the Wi-Fi.
func (w *WiFi) Confirm() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending == nil {
		return false
	}
	if w.pending.timer != nil {
		w.pending.timer.Stop()
	}
	w.pending = nil
	return true
}

// OverWiFi says whether a client address is in one of the Wi-Fi interface's subnets: its requests
// reach the system over the Wi-Fi, and a change there may cut it off.
func (w *WiFi) OverWiFi(client string) bool {
	ip := net.ParseIP(strings.TrimSpace(client))
	if ip == nil {
		return false
	}
	ifc, err := net.InterfaceByName(w.settings().Iface)
	if err != nil {
		return false
	}
	addrs, _ := ifc.Addrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.Contains(ip) {
			return true
		}
	}
	return false
}
