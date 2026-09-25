package wifi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// BootFiles are the headless setup files on the SD card's boot partition, in the order they are
// looked for. Notepad with hidden extensions saves "openccu-lite-wifi.txt.txt"; SetupWIFI is
// upstream OpenCCU's (first line the SSID, last line the password), read for compatibility.
var BootFiles = []string{"/boot/openccu-lite-wifi.txt", "/boot/openccu-lite-wifi.txt.txt", "/boot/SetupWIFI"}

// BootSetup is what a setup file says.
type BootSetup struct {
	SSID    string
	PSK     string // the passphrase as typed; empty for an open network
	Country string
	Hidden  bool
	// the static addressing, when the file names one
	Address, Netmask, Gateway string
	DNS                       []string
}

// ParseBootFile reads a setup file of either format. name is the file's base name: SetupWIFI is the
// two-line format, everything else key=value.
func ParseBootFile(name, content string) (BootSetup, error) {
	content = strings.TrimPrefix(content, "\uFEFF") // a UTF-8 BOM
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	if !utf8.ValidString(content) {
		return BootSetup{}, fmt.Errorf("the file is not UTF-8 text")
	}
	var b BootSetup
	if strings.EqualFold(name, "SetupWIFI") {
		var lines []string
		for _, l := range strings.Split(content, "\n") {
			if strings.TrimSpace(l) != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) < 2 {
			return b, fmt.Errorf("SetupWIFI needs the SSID on the first line and the password on the last")
		}
		b.SSID, b.PSK = lines[0], lines[len(lines)-1]
	} else {
		for i, line := range strings.Split(content, "\n") {
			t := strings.TrimSpace(line)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				return b, fmt.Errorf("line %d: key=value expected", i+1)
			}
			// the value is kept as typed, only the line's outer spaces go: an SSID or a passphrase may
			// begin or end with a space inside quotes
			v = strings.TrimSpace(v)
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "ssid":
				b.SSID = v
			case "psk", "password":
				b.PSK = v
			case "country":
				b.Country = strings.ToUpper(v)
			case "hidden":
				b.Hidden = strings.EqualFold(v, "yes") || strings.EqualFold(v, "true") || v == "1"
			case "address":
				addr, mask, err := cidr(v)
				if err != nil {
					return b, fmt.Errorf("address: %v", err)
				}
				b.Address, b.Netmask = addr, mask
			case "gateway":
				b.Gateway = v
			case "dns":
				b.DNS = append(b.DNS, strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' })...)
			default:
				return b, fmt.Errorf("line %d: unknown key %q", i+1, strings.TrimSpace(k))
			}
		}
	}
	if err := CheckSSID(b.SSID); err != nil {
		return b, err
	}
	if b.PSK != "" {
		if err := CheckPassphrase(b.PSK); err != nil {
			return b, err
		}
	}
	if b.Country != "" && !countryRe.MatchString(b.Country) {
		return b, fmt.Errorf("country: two letters (ISO 3166)")
	}
	return b, nil
}

// CheckSSID: 1 to 32 bytes.
func CheckSSID(s string) error {
	if len(s) == 0 || len(s) > 32 {
		return fmt.Errorf("ssid: 1 to 32 bytes")
	}
	return nil
}

// CheckPassphrase: 8 to 63 printable ASCII characters, or 64 hex digits (a raw PSK).
func CheckPassphrase(p string) error {
	if len(p) == 64 && isHex(p) {
		return nil
	}
	if len(p) < 8 || len(p) > 63 {
		return fmt.Errorf("password: 8 to 63 characters (or 64 hex digits)")
	}
	for _, r := range p {
		if r < 0x20 || r > 0x7e {
			return fmt.Errorf("password: printable ASCII characters only")
		}
	}
	return nil
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// cidr turns 192.168.1.50/24 into the address and its netmask.
func cidr(v string) (string, string, error) {
	a, bits, ok := strings.Cut(v, "/")
	ip := v4(a)
	if !ok || ip == nil {
		return "", "", fmt.Errorf("an IPv4 address with its prefix, such as 192.168.1.50/24")
	}
	var n int
	if _, err := fmt.Sscanf(bits, "%d", &n); err != nil || n < 1 || n > 32 {
		return "", "", fmt.Errorf("the prefix is 1 to 32")
	}
	m := make([]byte, 4)
	for i := 0; i < n; i++ {
		m[i/8] |= 0x80 >> (i % 8)
	}
	return ip.String(), fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3]), nil
}

// Apply fills the settings from a setup file: the switch on, the country when named, the static
// addressing when named. The network itself goes into wpa_supplicant.conf.
func (b BootSetup) Apply(s Settings) Settings {
	s.Enabled = true
	if b.Country != "" {
		s.Country = b.Country
	}
	if b.Address != "" {
		s.Mode, s.Address, s.Netmask, s.Gateway, s.DNS = "static", b.Address, b.Netmask, b.Gateway, b.DNS
	}
	return s
}

// SetupErrorFile holds why the last setup file on the boot partition could not be used, for the
// Status page; it goes with the next file that works.
const SetupErrorFile = "/run/occulite/wifi-setup-error"

// TakeBootFile uses a setup file on the boot partition: the settings switched on (with its country
// and addressing), the network saved - as WPA2, which WPA2 and WPA2/WPA3 networks both accept, so
// that no passphrase is stored in clear - and the file deleted, with /boot remounted rw for that.
// A file that cannot be read stays, and why is in SetupErrorFile.
func (b Box) TakeBootFile(ctx context.Context) error {
	for _, f := range BootFiles {
		data, err := os.ReadFile(b.path(f))
		if err != nil {
			continue
		}
		setup, err := ParseBootFile(filepath.Base(f), string(data))
		if err != nil {
			_ = writeFile(b.path(SetupErrorFile), fmt.Sprintf("%s: %v\n", filepath.Base(f), err), 0o644)
			return fmt.Errorf("%s: %w", f, err)
		}
		s := setup.Apply(b.Settings())
		if err := s.Validate(); err != nil {
			_ = writeFile(b.path(SetupErrorFile), fmt.Sprintf("%s: %v\n", filepath.Base(f), err), 0o644)
			return fmt.Errorf("%s: %w", f, err)
		}
		conf, _ := os.ReadFile(b.path(ConfFile))
		_, nets := ParseConf(string(conf))
		sec := SecWPA2
		if setup.PSK == "" {
			sec = SecOpen
		}
		top := 0
		kept := nets[:0]
		for _, n := range nets {
			if n.SSID == setup.SSID {
				continue
			}
			if n.Priority >= top {
				top = n.Priority + 1
			}
			kept = append(kept, n)
		}
		n, err := NewNetwork(setup.SSID, sec, setup.PSK, setup.Hidden, top)
		if err != nil {
			return err
		}
		if err := writeFile(b.path(ConfFile), Conf(s.Country, append(kept, n)), 0o600); err != nil {
			return err
		}
		if err := writeFile(b.path(SettingsFile), s.Format(), 0o644); err != nil {
			return err
		}
		// the passphrase in clear goes from the card
		rw := strings.HasPrefix(f, "/boot/")
		if rw {
			b.quiet(ctx, "mount", "-o", "remount,rw", "/boot")
		}
		rmErr := os.Remove(b.path(f))
		if rw {
			b.quiet(ctx, "mount", "-o", "remount,ro", "/boot")
		}
		_ = os.Remove(b.path(SetupErrorFile))
		b.logf("wifi: %s used: network %q saved, Wi-Fi switched on", filepath.Base(f), setup.SSID)
		if rmErr != nil {
			return fmt.Errorf("the setup file could not be deleted: %w", rmErr)
		}
		return nil
	}
	return nil
}
