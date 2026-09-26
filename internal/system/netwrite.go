package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// NetworkSettings is what the network form submits: the subset of /etc/config/netconfig that a
// user may change. Everything else in that file (CURRENT_*, CRYPT, ...) is preserved on write.
type NetworkSettings struct {
	Hostname string   `json:"hostname"`
	Mode     string   `json:"mode"` // "dhcp" or "static"
	Address  string   `json:"address,omitempty"`
	Netmask  string   `json:"netmask,omitempty"`
	Gateway  string   `json:"gateway,omitempty"`
	DNS      []string `json:"dns"`
}

var hostnameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// Validate rejects what the init scripts would choke on: a bad hostname, a static setup without
// a usable address, a gateway outside the subnet, a nameserver that is not an IPv4 address.
func (s *NetworkSettings) Validate() error {
	if !hostnameRe.MatchString(s.Hostname) {
		return fmt.Errorf("hostname: letters, digits and hyphens only (1-63 characters)")
	}
	switch s.Mode {
	case "dhcp":
	case "static":
		ip := ipv4(s.Address)
		if ip == nil {
			return fmt.Errorf("address: not an IPv4 address")
		}
		mask := ipv4(s.Netmask)
		if mask == nil || !validMask(mask) {
			return fmt.Errorf("netmask: not a valid IPv4 netmask")
		}
		if s.Gateway != "" {
			gw := ipv4(s.Gateway)
			if gw == nil {
				return fmt.Errorf("gateway: not an IPv4 address")
			}
			if !ip.Mask(net.IPMask(mask)).Equal(gw.Mask(net.IPMask(mask))) {
				return fmt.Errorf("gateway: not in the subnet of %s/%s", s.Address, s.Netmask)
			}
		}
	default:
		return fmt.Errorf("mode: dhcp or static")
	}
	if len(s.DNS) > 2 {
		return fmt.Errorf("dns: at most two nameservers (netconfig has NAMESERVER1 and NAMESERVER2)")
	}
	for _, d := range s.DNS {
		if ipv4(d) == nil {
			return fmt.Errorf("dns: %q is not an IPv4 address", d)
		}
	}
	return nil
}

func ipv4(s string) net.IP {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil
	}
	return ip.To4()
}

func validMask(m net.IP) bool {
	ones, bits := net.IPMask(m).Size()
	return bits == 32 && ones > 0 && ones < 32
}

// Settings extracts the user-editable part of a Network view: the durable configuration for a
// static setup, the CURRENT_* values (what DHCP handed out) when the box runs DHCP.
func (n Network) Settings() NetworkSettings {
	s := NetworkSettings{Hostname: n.Hostname, Mode: n.Mode, DNS: []string{}}
	if n.Mode == "static" {
		s.Address, s.Netmask, s.Gateway = n.Raw["IP"], n.Raw["NETMASK"], n.Raw["GATEWAY"]
		for _, k := range []string{"NAMESERVER1", "NAMESERVER2"} {
			if v := n.Raw[k]; v != "" && v != "0.0.0.0" {
				s.DNS = append(s.DNS, v)
			}
		}
	}
	return s
}

// Current is the live state as netconfig records it (dhcp.script and eq3configd keep the
// CURRENT_* keys up to date): the snapshot a confirm-or-revert transaction falls back to.
func (n Network) Current() NetworkSettings {
	s := NetworkSettings{Hostname: n.Hostname, Mode: n.Mode, DNS: []string{}}
	s.Address, s.Netmask, s.Gateway = n.Raw["CURRENT_IP"], n.Raw["CURRENT_NETMASK"], n.Raw["CURRENT_GATEWAY"]
	for _, k := range []string{"CURRENT_NAMESERVER1", "CURRENT_NAMESERVER2"} {
		if v := n.Raw[k]; v != "" && v != "0.0.0.0" {
			s.DNS = append(s.DNS, v)
		}
	}
	if s.Mode == "static" || s.Address == "" {
		// a static box's CURRENT_* may be stale; the configured values are what runs
		if c := n.Settings(); c.Mode == "static" {
			return c
		}
	}
	return s
}

// WriteNetconfig rewrites /etc/config/netconfig the way cp_network.cgi's set_property does:
// existing keys are replaced in place, missing ones appended, everything else kept verbatim.
func (r Root) WriteNetconfig(s NetworkSettings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	path := r.join("/etc/config/netconfig")
	text := readFile(path)
	mode := "DHCP"
	if s.Mode == "static" {
		mode = "MANUAL"
	}
	dns := []string{"0.0.0.0", "0.0.0.0"}
	copy(dns, s.DNS)
	set := [][2]string{{"HOSTNAME", s.Hostname}, {"MODE", mode}}
	if s.Mode == "static" {
		set = append(set, [2]string{"IP", s.Address}, [2]string{"NETMASK", s.Netmask}, [2]string{"GATEWAY", s.Gateway}, [2]string{"NAMESERVER1", dns[0]}, [2]string{"NAMESERVER2", dns[1]})
	}
	for _, kv := range set {
		text = setProperty(text, kv[0], kv[1])
	}
	return writeFileAtomic(path, []byte(text), 0o644)
}

var propertyRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_]+)\s*=(.*)$`)

func setProperty(text, key, value string) string {
	done := false
	out := propertyRe.ReplaceAllStringFunc(text, func(line string) string {
		m := propertyRe.FindStringSubmatch(line)
		if done || m[1] != key {
			return line
		}
		done = true
		return key + "=" + value
	})
	if done {
		return out
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + key + "=" + value + "\n"
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	return Priv.WriteFile(path, data, perm)
}

// Runner executes one external command; the default is exec, tests and --root development
// substitute a recorder. Nothing here is ever a shell line.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner runs the command for real.
func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return run(ctx, name, args...)
}

// DryRunner logs instead of executing: what a developer's box gets when occulited runs with
// --root, so a network form never reconfigures the workstation.
func DryRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	slog.Info("dry run", "cmd", name, "args", args)
	return nil, nil
}

// NetApplier changes the live network configuration of one interface exactly as
// /etc/network/if-up.d/eQ3StartNetwork and /bin/dhcp.script do at boot, without ifdown/ifup
// (which would trip the hasIP/hasLink guard in that script and disable the interface).
type NetApplier struct {
	Root  Root
	Iface string // eth0 unless the box says otherwise
	Run   Runner
	// Systemd: the DHCP client runs inside occu-network.service, and a new lease with a new host
	// name is asked for with the unit's reload (openccu-lite task 62), so the client stays in the
	// unit's cgroup; without systemd the client is killed and started again here.
	Systemd bool
}

// LeaseRenewal is what a rename did about the DHCP lease (task 62): the client restarted with
// the new host name, so the DHCP server - and the router's DNS behind it - learn the new name.
type LeaseRenewal struct {
	// Renewed: the client was restarted with the new name; false with Error when that failed, and
	// false without one on a static setup, where there is no server to tell
	Renewed bool `json:"renewed"`
	// Static: a static address, nothing to tell
	Static bool   `json:"static,omitempty"`
	Error  string `json:"error,omitempty"`
	// Address is the interface's IPv4 address after the renewal, when it could be read
	Address string    `json:"address,omitempty"`
	At      time.Time `json:"at,omitzero"`
}

// Rename is a hostname-only change as Begin applied it: the names and the lease.
type Rename struct {
	Hostname string       `json:"hostname"`
	Previous string       `json:"previous"`
	Lease    LeaseRenewal `json:"lease"`
}

// RenewLease asks the DHCP server for a lease under the new host name. With systemd the unit's
// reload does it (the fork's lite-network-reload: the running udhcpc is stopped without releasing
// the lease and started again with -x hostname:<new> -F <new>, inside occu-network.service);
// without, the client is restarted here as Apply does. The address is read back for the answer.
func (a NetApplier) RenewLease(ctx context.Context, hostname string) LeaseRenewal {
	run := a.run()
	iface := a.iface()
	res := LeaseRenewal{At: time.Now()}
	var err error
	if a.Systemd {
		var out []byte
		out, err = run(ctx, "systemctl", "reload", "occu-network.service")
		if err != nil {
			err = fmt.Errorf("systemctl reload occu-network: %w: %s", err, strings.TrimSpace(string(out)))
		}
	} else {
		if pid := pidFromFile(a.Root.join("/var/run/udhcpc_" + iface + ".pid")); pid > 0 {
			_, _ = run(ctx, "kill", strconv.Itoa(pid))
		}
		var out []byte
		out, err = run(ctx, "/sbin/udhcpc", "-b", "-t", "20", "-T", "3", "-S", "-x", "hostname:"+hostname, "-i", iface, "-F", hostname, "-V", "eQ3-CCU3", "-s", "/bin/dhcp.script", "-p", "/var/run/udhcpc_"+iface+".pid")
		if err != nil {
			err = fmt.Errorf("udhcpc: %w: %s", err, strings.TrimSpace(string(out)))
		}
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Renewed = true
	if out, err := run(ctx, "/sbin/ip", "-4", "-o", "addr", "show", "dev", iface); err == nil {
		for _, f := range strings.Fields(string(out)) {
			if ip, _, ok := strings.Cut(f, "/"); ok && net.ParseIP(ip) != nil && strings.Count(f, ".") == 3 {
				res.Address = ip
				break
			}
		}
	}
	return res
}

// Apply brings the interface to s. It is not transactional; NetTx wraps it.
func (a NetApplier) Apply(ctx context.Context, s NetworkSettings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	run := a.run()
	iface := a.iface()
	// stop a running DHCP client either way: a new lease would overwrite a static address, and a
	// DHCP setup restarts the client with the new hostname
	if pid := pidFromFile(a.Root.join("/var/run/udhcpc_" + iface + ".pid")); pid > 0 {
		_, _ = run(ctx, "kill", strconv.Itoa(pid))
	}
	switch s.Mode {
	case "dhcp":
		_, _ = run(ctx, "/sbin/ip", "-4", "addr", "flush", "dev", iface)
		out, err := run(ctx, "/sbin/udhcpc", "-b", "-t", "20", "-T", "3", "-S", "-x", "hostname:"+s.Hostname, "-i", iface, "-F", s.Hostname, "-V", "eQ3-CCU3", "-s", "/bin/dhcp.script", "-p", "/var/run/udhcpc_"+iface+".pid")
		if err != nil {
			return fmt.Errorf("udhcpc: %w: %s", err, strings.TrimSpace(string(out)))
		}
		// the override replaces the lease's servers, or its record goes (B-167)
		if err := a.setDNSOverride(ctx, s.DNS); err != nil {
			return err
		}
	case "static":
		out, err := run(ctx, "/sbin/ifconfig", iface, s.Address, "netmask", s.Netmask)
		if err != nil {
			return fmt.Errorf("ifconfig: %w: %s", err, strings.TrimSpace(string(out)))
		}
		for i := 0; i < 4; i++ {
			if _, err := run(ctx, "/sbin/ip", "route", "del", "default"); err != nil {
				break
			}
		}
		if s.Gateway != "" {
			if out, err := run(ctx, "/sbin/ip", "route", "add", "default", "via", s.Gateway); err != nil {
				return fmt.Errorf("default route: %w: %s", err, strings.TrimSpace(string(out)))
			}
		}
		_, _ = run(ctx, "/sbin/ip", "route", "add", "224.0.0.0/24", "dev", iface, "scope", "link")
		var resolv strings.Builder
		for _, d := range s.DNS {
			resolv.WriteString("nameserver " + d + "\n")
		}
		if resolv.Len() > 0 {
			if err := a.resolvconfAdd(ctx, []string{"-a", iface}, resolv.String()); err != nil {
				return err
			}
		}
		// an exclusive override record of a DHCP setup would hide the static servers
		if err := a.setDNSOverride(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func (a NetApplier) run() Runner {
	if a.Run == nil {
		return ExecRunner
	}
	return a.Run
}

func (a NetApplier) iface() string {
	if a.Iface == "" {
		return "eth0"
	}
	return a.Iface
}

// resolvconfAdd hands a record to resolvconf on its stdin - through the helper, as root: the
// daemon cannot write /run/resolvconf (it ran resolvconf itself before B-167, which failed as
// occulite). A test's Runner gets the record as a last argument after "<".
func (a NetApplier) resolvconfAdd(ctx context.Context, args []string, record string) error {
	if a.Run != nil {
		_, err := a.Run(ctx, "/sbin/resolvconf", append(args, "<"+strings.TrimSpace(record))...)
		return err
	}
	out, err := priv.AsExitError(Priv.Run(ctx, "/sbin/resolvconf", args, []byte(record)))
	if err != nil {
		return fmt.Errorf("resolvconf %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ApplyHostname sets the running hostname the way eQ3StartNetwork does; the durable part is the
// HOSTNAME key of netconfig.
func (a NetApplier) ApplyHostname(ctx context.Context, name string) error {
	if !hostnameRe.MatchString(name) {
		return errors.New("bad hostname")
	}
	run := a.Run
	if run == nil {
		run = ExecRunner
	}
	if err := writeFileAtomic(a.Root.join("/etc/hostname"), []byte(name+"\n"), 0o644); err != nil {
		return err
	}
	if err := writeFileAtomic(a.Root.join("/etc/hosts"), []byte(HostsWithName(readFile(a.Root.join("/etc/hosts")), name)), 0o644); err != nil {
		return err
	}
	_, err := run(ctx, "hostname", name)
	return err
}

// HostsWithName is /etc/hosts with the system's own line naming name: the 127.0.1.1 line (the
// one eQ3StartNetwork writes for the host) is replaced, every other line - an addon's or the
// user's entries - stays (openccu-lite task 62; ApplyHostname replaced the whole file before).
// A file without the line gets it after the localhost line; an empty or missing file becomes the
// script's five lines.
func HostsWithName(hosts, name string) string {
	if strings.TrimSpace(hosts) == "" {
		return "127.0.0.1 localhost\n127.0.1.1 " + name + "\n::1 ip6-localhost ip6-loopback\nff02::1 ip6-allnodes\nff02::2 ip6-allrouters\n"
	}
	lines := strings.Split(strings.TrimRight(hosts, "\n"), "\n")
	done := false
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		if f := strings.Fields(l); len(f) >= 1 && f[0] == "127.0.1.1" {
			if done {
				continue // a second line of the host's own: dropped
			}
			l = "127.0.1.1 " + name
			done = true
		}
		out = append(out, l)
	}
	if !done {
		// after the localhost line, else first
		at := 0
		for i, l := range out {
			if f := strings.Fields(l); len(f) >= 1 && f[0] == "127.0.0.1" {
				at = i + 1
				break
			}
		}
		out = append(out[:at], append([]string{"127.0.1.1 " + name}, out[at:]...)...)
	}
	return strings.Join(out, "\n") + "\n"
}

func pidFromFile(path string) int {
	n, err := strconv.Atoi(strings.TrimSpace(readFile(path)))
	if err != nil || n <= 1 {
		return 0
	}
	return n
}

// NetTx is the confirm-or-revert transaction of task 8: Begin applies the new settings live and
// starts a timer; Confirm within the window makes them durable, otherwise (or on Revert) the
// previous live state is restored. One transaction at a time; a headless box in a cupboard is
// the reason this exists.
type NetTx struct {
	Root    Root
	Applier NetApplier
	Window  time.Duration // default 90 s
	// Override is the DNS override of a DHCP setup (B-167); its zero value keeps none.
	Override DNSOverride

	mu      sync.Mutex
	pending *NetPending
	timer   *time.Timer
}

// NetPending describes the open transaction.
type NetPending struct {
	Token    string          `json:"token"`
	Settings NetworkSettings `json:"settings"`
	Previous NetworkSettings `json:"previous"`
	Started  time.Time       `json:"started"`
	Deadline time.Time       `json:"deadline"`
	// dnsOnly: a DHCP setup whose override alone changes - applied and reverted as the resolvconf
	// record, without restarting the DHCP client (which flushes the address) (B-167)
	dnsOnly bool
}

// ErrTxPending is returned when Begin is called while another transaction is open.
var ErrTxPending = errors.New("a network change is already waiting for confirmation")

// ErrNoTx is returned by Confirm/Revert with no or a wrong token.
var ErrNoTx = errors.New("no such pending network change")

// Begin validates, records the current state, applies s live and arms the revert timer. A
// hostname-only change is applied at once (no window: it cannot lock anyone out) and answered as
// a Rename - the new name, and the DHCP lease asked for under it (task 62) - with no pending
// transaction.
func (t *NetTx) Begin(ctx context.Context, s NetworkSettings) (*NetPending, *Rename, error) {
	if err := s.Validate(); err != nil {
		return nil, nil, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending != nil {
		return nil, nil, ErrTxPending
	}
	prev := t.current()
	// the same setup: on DHCP the address, mask and gateway are the lease's, not settings, so only
	// the mode and the DNS override count there (a rename on a DHCP box opened a window before)
	sameAddress := s.Mode == "dhcp" || (prev.Address == s.Address && prev.Netmask == s.Netmask && prev.Gateway == s.Gateway)
	if prev.Mode == s.Mode && sameAddress && strings.Join(prev.DNS, " ") == strings.Join(s.DNS, " ") {
		// only the hostname (or nothing) changes: no window needed, it cannot lock anyone out
		if err := t.Root.WriteNetconfig(s); err != nil {
			return nil, nil, err
		}
		if err := t.saveOverride(s); err != nil {
			return nil, nil, err
		}
		if s.Hostname == prev.Hostname {
			return nil, nil, nil
		}
		if err := t.Applier.ApplyHostname(ctx, s.Hostname); err != nil {
			return nil, nil, err
		}
		ren := &Rename{Hostname: s.Hostname, Previous: prev.Hostname}
		if s.Mode == "dhcp" {
			ren.Lease = t.Applier.RenewLease(ctx, s.Hostname)
			if ren.Lease.Error != "" {
				slog.Warn("network: renamed, but the DHCP lease could not be renewed under the new name", "hostname", s.Hostname, "err", ren.Lease.Error)
			}
		} else {
			ren.Lease = LeaseRenewal{Static: true}
		}
		slog.Info("network: renamed", "hostname", s.Hostname, "previous", prev.Hostname, "lease_renewed", ren.Lease.Renewed)
		return nil, ren, nil
	}
	dnsOnly := prev.Mode == "dhcp" && s.Mode == "dhcp" && prev.Hostname == s.Hostname
	if dnsOnly {
		if err := t.Applier.setDNSOverride(ctx, s.DNS); err != nil {
			_ = t.Applier.setDNSOverride(context.Background(), prev.DNS)
			return nil, nil, err
		}
	} else if err := t.Applier.Apply(ctx, s); err != nil {
		// best effort back to where we were
		_ = t.Applier.Apply(context.Background(), prev)
		return nil, nil, err
	}
	window := t.Window
	if window == 0 {
		window = 90 * time.Second
	}
	now := time.Now()
	p := &NetPending{Token: randomToken(), Settings: s, Previous: prev, Started: now, Deadline: now.Add(window), dnsOnly: dnsOnly}
	t.pending = p
	t.timer = time.AfterFunc(window, func() { t.expire(p.Token) })
	slog.Warn("network: applied, waiting for confirmation", "mode", s.Mode, "address", s.Address, "window", window)
	return p, nil, nil
}

func (t *NetTx) expire(token string) {
	t.mu.Lock()
	p := t.pending
	if p == nil || p.Token != token {
		t.mu.Unlock()
		return
	}
	t.pending, t.timer = nil, nil
	t.mu.Unlock()
	slog.Warn("network: not confirmed in time, reverting", "to", p.Previous.Mode, "address", p.Previous.Address)
	if err := t.revert(context.Background(), p); err != nil {
		slog.Error("network: revert failed", "err", err)
	}
}

// Pending returns the open transaction, if any.
func (t *NetTx) Pending() *NetPending {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pending == nil {
		return nil
	}
	p := *t.pending
	return &p
}

// Confirm makes the pending settings durable.
func (t *NetTx) Confirm(ctx context.Context, token string) error {
	t.mu.Lock()
	p := t.pending
	if p == nil || p.Token != token {
		t.mu.Unlock()
		return ErrNoTx
	}
	t.timer.Stop()
	t.pending, t.timer = nil, nil
	t.mu.Unlock()
	if err := t.Root.WriteNetconfig(p.Settings); err != nil {
		return err
	}
	if err := t.saveOverride(p.Settings); err != nil {
		return err
	}
	if p.Settings.Hostname != p.Previous.Hostname {
		if err := t.Applier.ApplyHostname(ctx, p.Settings.Hostname); err != nil {
			return err
		}
	}
	slog.Info("network: confirmed", "mode", p.Settings.Mode, "address", p.Settings.Address)
	return nil
}

// Revert restores the previous live state now.
func (t *NetTx) Revert(ctx context.Context, token string) error {
	t.mu.Lock()
	p := t.pending
	if p == nil || p.Token != token {
		t.mu.Unlock()
		return ErrNoTx
	}
	t.timer.Stop()
	t.pending, t.timer = nil, nil
	t.mu.Unlock()
	slog.Info("network: reverted by the user")
	return t.revert(ctx, p)
}

func (t *NetTx) revert(ctx context.Context, p *NetPending) error {
	if p.dnsOnly {
		return t.Applier.setDNSOverride(ctx, p.Previous.DNS)
	}
	return t.Applier.Apply(ctx, p.Previous)
}

// current is the live state a transaction falls back to. Under DHCP its DNS is the override (none
// when the lease's servers are used), not the lease's servers: a revert that handed those to
// Apply would turn them into an override.
func (t *NetTx) current() NetworkSettings {
	c := t.Root.ReadNetwork().Current()
	if c.Mode == "dhcp" {
		c.DNS = t.overrideServers()
	}
	return c
}

func (t *NetTx) overrideServers() []string {
	if d := t.Override.Servers(); d != nil {
		return d
	}
	return []string{}
}

// saveOverride makes a confirmed setup's override durable: the servers under DHCP, none under a
// static setup (whose servers are netconfig's NAMESERVER1/2).
func (t *NetTx) saveOverride(s NetworkSettings) error {
	if s.Mode == "dhcp" {
		return t.Override.Save(s.DNS)
	}
	return t.Override.Save(nil)
}

// Settings is the form's view of n (Network.Settings) with a DHCP setup's DNS override, and the
// current state with the override as the servers in use.
func (t *NetTx) Settings(n Network) (settings, current NetworkSettings) {
	settings, current = n.Settings(), n.Current()
	if n.Mode == "dhcp" {
		settings.DNS = t.overrideServers()
		if len(settings.DNS) > 0 {
			current.DNS = settings.DNS
		}
	}
	return settings, current
}

func randomToken() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
