package system

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// LAN gateways and the system security key, written the way the CCU WebUI wrote them
// (www/api/methods/bidcosrf/setconfiguration-rf.tcl, bidcoswired/setconfiguration-wired.tcl,
// bidcos/changeLanGatewayKey.tcl, config/cp_security.cgi's set_key). The daemons read their
// config at start, so a change takes effect with the next rfd/hs485d restart.

// GatewayClass selects the daemon: "rf" (rfd.conf, BidCos-RF) or "wired" (hs485d.conf, HM-Wired).
type GatewayClass string

const (
	GatewayRF    GatewayClass = "rf"
	GatewayWired GatewayClass = "wired"
)

// GatewayTypes lists the Type values the WebUI offers per class: HM-CFG-LAN ("Lan Interface"),
// HM-LGW-O-TW-W-EU ("HMLGW2") and the wired HMW-LGW-O-DR-GS-EU ("HMWLGW").
var GatewayTypes = map[GatewayClass][]string{
	GatewayRF:    {"Lan Interface", "HMLGW2"},
	GatewayWired: {"HMWLGW"},
}

// LANGatewaySpec is one gateway as the API takes it. Key empty on an update keeps the key the
// file already has for that serial.
type LANGatewaySpec struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Serial string `json:"serial"`
	Key    string `json:"key,omitempty"`
	IP     string `json:"ip,omitempty"`
}

var (
	serialRe = regexp.MustCompile(`^[A-Za-z0-9]{1,24}$`)
	// A gateway's key is what eQ-3 prints on the device, and that is not limited to letters and
	// digits: the maintainer's HM-LGW-O-TW-W-EU carries "=u6%U!M8e3" (2026-09-20), which the old
	// pattern refused. Every printable character but a space passes now. It survives the way
	// round: the key is written as the value of "Encryption Key = " in rfd.conf/hs485d.conf and as
	// KEY= in <serial>.keychange, both of which are read by cutting at the *first* "=" and
	// trimming the ends (so a leading "=" is kept and a space would be lost), and eq3configcmd
	// setlgwkey takes it as one argument, never through a shell.
	keyRe  = regexp.MustCompile(`^[\x21-\x7e]{0,64}$`)
	nameRe = regexp.MustCompile(`^[^\r\n\[\]=]{0,60}$`)
	hostRe = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
)

func (c GatewayClass) confPath() string {
	if c == GatewayWired {
		return "/etc/config/hs485d.conf"
	}
	return "/etc/config/rfd.conf"
}

// Service returns the daemon the class belongs to, for a restart after writing.
func (c GatewayClass) Service() string {
	if c == GatewayWired {
		return "hs485d"
	}
	return "rfd"
}

func (s LANGatewaySpec) validate(class GatewayClass) error {
	ok := false
	for _, t := range GatewayTypes[class] {
		ok = ok || t == s.Type
	}
	if !ok {
		return fmt.Errorf("type: %q is not one of %s", s.Type, strings.Join(GatewayTypes[class], ", "))
	}
	if !serialRe.MatchString(s.Serial) {
		return fmt.Errorf("serial: letters and digits, up to 24")
	}
	if !keyRe.MatchString(s.Key) {
		return fmt.Errorf("key: up to 64 printable characters without spaces")
	}
	if !nameRe.MatchString(s.Name) {
		return fmt.Errorf("name: up to 60 characters without [ ] =")
	}
	if s.IP != "" && net.ParseIP(s.IP) == nil && !hostRe.MatchString(s.IP) {
		return fmt.Errorf("ip: an IPv4/IPv6 address or a host name")
	}
	return nil
}

// ReadLANGateways lists the gateway sections of the class's config file: the sections of a
// gateway type. The module's section ([Interface 0], CCU2) and an HM-CFG-USB-2's ("USB
// Interface", which the radio plan adds when the adapter is there) are not gateways.
func (r Root) ReadLANGateways(class GatewayClass) []LANGateway {
	out := []LANGateway{}
	for _, g := range r.readInterfaceSections(class.confPath()) {
		if isGatewayType(class, g.Type) {
			out = append(out, g)
		}
	}
	return out
}

func isGatewayType(class GatewayClass, t string) bool {
	for _, x := range GatewayTypes[class] {
		if x == t {
			return true
		}
	}
	return false
}

// WriteLANGateways rewrites the class's config file: the header (everything before the first
// [Interface N] section) and every section that is not a gateway (the module's [Interface 0],
// an HM-CFG-USB-2's) are kept byte for byte, then one [Interface N] section per gateway in the
// WebUI's layout, numbered after the kept sections (from 1 for rf, 0 for wired). Returns the
// gateways as written.
func (r Root) WriteLANGateways(class GatewayClass, gws []LANGatewaySpec) ([]LANGateway, error) {
	existing := map[string]string{}
	for _, g := range r.ReadLANGateways(class) {
		existing[strings.ToUpper(g.Serial)] = g.Key()
	}
	seen := map[string]bool{}
	for i := range gws {
		gws[i].Serial = strings.ToUpper(strings.TrimSpace(gws[i].Serial))
		gws[i].Name, gws[i].IP, gws[i].Key = strings.TrimSpace(gws[i].Name), strings.TrimSpace(gws[i].IP), strings.TrimSpace(gws[i].Key)
		if err := gws[i].validate(class); err != nil {
			return nil, fmt.Errorf("gateway %d: %w", i+1, err)
		}
		if seen[gws[i].Serial] {
			return nil, fmt.Errorf("gateway %d: serial %s listed twice", i+1, gws[i].Serial)
		}
		seen[gws[i].Serial] = true
		if gws[i].Key == "" {
			gws[i].Key = existing[gws[i].Serial]
		}
	}
	path := r.join(class.confPath())
	header, kept, maxKept, err := configHeader(path, class)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(kept)
	n := 1
	if class == GatewayWired {
		n = 0
	}
	if maxKept+1 > n {
		n = maxKept + 1
	}
	for _, g := range gws {
		fmt.Fprintf(&b, "[Interface %d]\nType = %s\nName = %s\nSerial Number = %s\nEncryption Key = %s\n", n, g.Type, g.Name, g.Serial, g.Key)
		if g.IP != "" {
			fmt.Fprintf(&b, "IP Address = %s\n", g.IP)
		}
		b.WriteString("\n")
		n++
	}
	// 0600, as the firmware writes it: the sections carry the gateways' encryption keys, and
	// 0644 would hand them to every addon user and to lighttpd (B-24)
	if err := writeFileAtomic(path, []byte(b.String()), 0o600); err != nil {
		return nil, err
	}
	if class == GatewayWired {
		r.syncHS485DMarker()
		if err := r.syncWiredInterface(); err != nil {
			return nil, fmt.Errorf("InterfacesList.xml: %w", err)
		}
	}
	return r.ReadLANGateways(class), nil
}

// syncHS485DMarker keeps /run/occulite/radio/hs485d.enabled with the file (B-163). The marker is
// hs485d.service's condition, and the radio run writes it at boot from the plan's rule: hs485d
// runs where hs485d.conf has an interface section. A gateway added while the system runs is the
// same decision one boot later, so the marker follows the write - without it `systemctl start
// hs485d` and this page's restart are skipped in silence, which is exactly how the first wired
// gateway in the lab looked. The daemons' units read the marker's existence, never its content.
func (r Root) syncHS485DMarker() {
	if len(r.readInterfaceSections(GatewayWired.confPath())) > 0 {
		_ = touch(r.join(HS485DEnabledMarker), 0o644)
		return
	}
	_ = remove(r.join(HS485DEnabledMarker))
}

// syncWiredInterface keeps InterfacesList.xml's BidCos-Wired entry with hs485d.conf, as the
// marker above (B-166): the boot's radio run lists it exactly when hs485d runs, and hs485d's
// loader adds it when the daemon starts, but nothing took it away when the last wired gateway
// went - clients kept calling 127.0.0.1:32000 with nothing behind it until the next boot. The
// file is edited in place (radio.RenderInterfaces: the other entries byte for byte, the entry in
// the plan's layout), not re-rendered from the template, which is the boot's business. No file
// is left alone too: the next radio run writes it.
func (r Root) syncWiredInterface() error {
	path := r.join("/etc/config/InterfacesList.xml")
	text := readFile(path)
	if text == "" {
		return nil
	}
	wired := len(r.readInterfaceSections(GatewayWired.confPath())) > 0
	list := radio.ParseInterfaces(text)
	has := false
	var without []radio.Interface
	for _, i := range list {
		if i.Name == radio.BidCosWired.Name {
			has = true
			continue
		}
		without = append(without, i)
	}
	switch {
	case wired && !has:
		list = append(list, radio.BidCosWired)
	case !wired && has:
		list = without
	default:
		return nil
	}
	return writeFileAtomic(path, []byte(radio.RenderInterfaces(text, list)), 0o644)
}

// configHeader splits the file for a rewrite: the header (everything before the first active
// [Interface N] line), the sections that are not gateways, verbatim and in order (with the lines
// that follow them up to the next section), and the highest number among those (-1 for none). A
// missing hs485d.conf gets the header the WebUI generated.
func configHeader(path string, class GatewayClass) (string, string, int, error) {
	// through readFile, not os.Open: rfd.conf and hs485d.conf are 0600 root:root and occulited
	// is not root (B-24). A missing rf config is an error; a missing wired one is normal.
	text := readFile(path)
	if text == "" {
		if _, err := os.Stat(path); err != nil {
			if class == GatewayWired && errors.Is(err, os.ErrNotExist) {
				// B-164: the loopback line as the image's rfd.conf template carries it - hs485d
				// has no template, and without it the daemon listens on every interface (D-29).
				// The render forces it too, for a file that was written before this.
				return "# This File was automatically generated\n# TCP Port for XmlRpc connections\nListen IP = 127.0.0.1\nListen Port = 32000\n\nLog Destination = Syslog\nLog Identifier = hs485d\n\n", "", -1, nil
			}
			return "", "", -1, err
		}
		return "", "", -1, fmt.Errorf("%s is empty or unreadable", path)
	}
	section := regexp.MustCompile(`^\s*\[Interface ([0-9]+)\]`)
	typeLine := regexp.MustCompile(`^\s*Type\s*=\s*(.*?)\s*$`)
	var header, kept strings.Builder
	maxKept := -1
	var block []string
	blockNum, inBlock := -1, false
	flush := func() {
		if !inBlock {
			return
		}
		gw := false
		for _, l := range block {
			if m := typeLine.FindStringSubmatch(l); m != nil {
				gw = isGatewayType(class, m[1])
				break
			}
		}
		if !gw {
			for _, l := range block {
				kept.WriteString(l)
				kept.WriteString("\n")
			}
			if blockNum > maxKept {
				maxKept = blockNum
			}
		}
		block = nil
	}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		l := sc.Text()
		if m := section.FindStringSubmatch(l); m != nil {
			flush()
			inBlock = true
			blockNum, _ = strconv.Atoi(m[1])
		}
		if inBlock {
			block = append(block, l)
		} else {
			header.WriteString(l)
			header.WriteString("\n")
		}
	}
	flush()
	return header.String(), kept.String(), maxKept, sc.Err()
}

// QueueGatewayKeyChange writes /etc/config/<serial>.keychange; the firmware's S59SetLGWKey runs
// setlgwkey.sh at the next boot, which tells the gateway its new access key (eq3configcmd
// setlgwkey) and deletes the file on success. Exactly what changeLanGatewayKey.tcl did.
func (r Root) QueueGatewayKeyChange(class GatewayClass, serial, ip, newKey, currentKey string) error {
	serial = strings.ToUpper(strings.TrimSpace(serial))
	if !serialRe.MatchString(serial) {
		return fmt.Errorf("serial: letters and digits, up to 24")
	}
	if !keyRe.MatchString(newKey) || newKey == "" || !keyRe.MatchString(currentKey) {
		return fmt.Errorf("key: up to 64 printable characters without spaces")
	}
	if ip != "" && net.ParseIP(ip) == nil && !hostRe.MatchString(ip) {
		return fmt.Errorf("ip: an IPv4/IPv6 address or a host name")
	}
	cls := "RF"
	if class == GatewayWired {
		cls = "Wired"
	}
	body := fmt.Sprintf("Class=%s\nSerial=%s\nIP=%s\nKEY=%s\nCURKEY=%s\n", cls, serial, ip, newKey, currentKey)
	return writeFileAtomic(r.join("/etc/config/"+serial+".keychange"), []byte(body), 0o600)
}

// PendingGatewayKeyChanges lists the serials with a queued key change.
func (r Root) PendingGatewayKeyChanges() []string {
	m, _ := os.ReadDir(r.join("/etc/config"))
	out := []string{}
	for _, e := range m {
		if strings.HasSuffix(e.Name(), ".keychange") {
			out = append(out, strings.TrimSuffix(e.Name(), ".keychange"))
		}
	}
	return out
}

var securityKeyRe = regexp.MustCompile(`^[0-9a-zA-Z_]{5,}$`)

// ErrSameKey says the key given is the one already in use.
var ErrSameKey = errors.New("this is already the current security key")

// SetSecurityKey sets the system security key (Zentralenschlüssel) as cp_security.cgi's set_key:
// the key is validated (at least 5 of [0-9a-zA-Z_]), refused when it is the current one
// (crypttool -v -t 3), sent to rfd as changeKey (rfd tells every AES-capable device), and stored
// with crypttool -S under the next index. changeKey is the rfd call; it may be nil on a box
// without rfd, then only crypttool runs (what the WebUI's failure path would have skipped).
func (r Root) SetSecurityKey(ctx context.Context, key string, changeKey func(ctx context.Context, key string) error) error {
	if !securityKeyRe.MatchString(key) {
		return fmt.Errorf("key: at least 5 characters, letters, digits and _ only")
	}
	tool := r.join("/bin/crypttool")
	if _, err := os.Stat(tool); err != nil {
		return fmt.Errorf("crypttool: %w", err)
	}
	if r, err := Priv.Run(ctx, tool, []string{"-v", "-t", "3", "-k", key}, nil); err == nil && r.Exit == 0 {
		return ErrSameKey
	}
	if changeKey != nil {
		if err := changeKey(ctx, key); err != nil {
			return fmt.Errorf("rfd changeKey: %w (the current key may not have reached every device yet - see the service messages)", err)
		}
	}
	out, _ := runOutput(ctx, tool, "-g")
	index := cryptIndex(string(out), "Current user key") + 1
	if b, err := run(ctx, tool, "-S", "-k", key, "-i", strconv.Itoa(index)); err != nil {
		return fmt.Errorf("crypttool -S: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// cryptIndex parses one "<name> = <n>" line of crypttool -g.
func cryptIndex(out, name string) int {
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == name {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			return n
		}
	}
	return 0
}
