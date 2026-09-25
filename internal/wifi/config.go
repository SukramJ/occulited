// Package wifi is openccu-lite's Wi-Fi (task 89): the settings in /etc/config/wifi, the networks
// in /etc/config/wpa_supplicant.conf, the headless setup file on the SD card's boot partition, and
// a client for wpa_supplicant's control socket. occulited owns Wi-Fi beside upstream's network
// hook, which only ever runs one interface: both can be up at once here, each with its own mode,
// and the preferred one carries the default route with the lower metric.
package wifi

import (
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
)

// SettingsFile holds the switch, the interface, the country, the addressing and the preference.
// KEY=VALUE like upstream's netconfig, on the userfs, so an update keeps it and a backup carries it.
const SettingsFile = "/etc/config/wifi"

// Settings is /etc/config/wifi.
type Settings struct {
	// Enabled is the switch: off unloads the driver and blocks the radio (the default).
	Enabled bool `json:"enabled"`
	// Iface is the Wi-Fi interface used, wlan0 unless the user chose a stick's.
	Iface string `json:"iface"`
	// Country is the regulatory domain, ISO 3166 alpha-2; required for the legal channels.
	Country string `json:"country"`
	// Mode is "dhcp" or "static".
	Mode    string   `json:"mode"`
	Address string   `json:"address,omitempty"`
	Netmask string   `json:"netmask,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
	// Preferred is the interface that carries the default route: "eth" (the default) or "wlan".
	Preferred string `json:"preferred"`
}

// Defaults are the settings of a system that never had Wi-Fi.
func Defaults() Settings {
	return Settings{Iface: "wlan0", Country: "DE", Mode: "dhcp", Preferred: "eth"}
}

var (
	ifaceRe   = regexp.MustCompile(`^wl[a-z0-9]{1,13}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Validate rejects what the up step would stumble over.
func (s Settings) Validate() error {
	if !ifaceRe.MatchString(s.Iface) {
		return fmt.Errorf("iface: a wireless interface name such as wlan0")
	}
	if !countryRe.MatchString(s.Country) {
		return fmt.Errorf("country: two capital letters (ISO 3166)")
	}
	switch s.Preferred {
	case "eth", "wlan":
	default:
		return fmt.Errorf("preferred: eth or wlan")
	}
	switch s.Mode {
	case "dhcp":
	case "static":
		ip := v4(s.Address)
		mask := v4(s.Netmask)
		if ip == nil {
			return fmt.Errorf("address: not an IPv4 address")
		}
		if mask == nil || !contiguous(mask) {
			return fmt.Errorf("netmask: not a valid IPv4 netmask")
		}
		if s.Gateway != "" {
			gw := v4(s.Gateway)
			if gw == nil || !ip.Mask(net.IPMask(mask)).Equal(gw.Mask(net.IPMask(mask))) {
				return fmt.Errorf("gateway: an IPv4 address in the subnet of %s/%s", s.Address, s.Netmask)
			}
		}
	default:
		return fmt.Errorf("mode: dhcp or static")
	}
	if len(s.DNS) > 2 {
		return fmt.Errorf("dns: at most two nameservers")
	}
	for _, d := range s.DNS {
		if v4(d) == nil {
			return fmt.Errorf("dns: %q is not an IPv4 address", d)
		}
	}
	return nil
}

func v4(s string) net.IP {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return nil
	}
	return ip.To4()
}

func contiguous(m net.IP) bool {
	ones, bits := net.IPMask(m).Size()
	return bits == 32 && ones > 0
}

// ParseSettings reads /etc/config/wifi; unknown keys are ignored, missing ones take the defaults.
func ParseSettings(text string) Settings {
	s := Defaults()
	kv := parseKV(text)
	s.Enabled = kv["WIFI_ENABLED"] == "1"
	if v := kv["WIFI_IFACE"]; v != "" {
		s.Iface = v
	}
	if v := strings.ToUpper(kv["WIFI_COUNTRY"]); v != "" {
		s.Country = v
	}
	if strings.EqualFold(kv["WIFI_MODE"], "static") {
		s.Mode = "static"
	}
	s.Address, s.Netmask, s.Gateway = kv["WIFI_IP"], kv["WIFI_NETMASK"], kv["WIFI_GATEWAY"]
	for _, k := range []string{"WIFI_NAMESERVER1", "WIFI_NAMESERVER2"} {
		if kv[k] != "" {
			s.DNS = append(s.DNS, kv[k])
		}
	}
	if kv["PREFERRED"] == "wlan" {
		s.Preferred = "wlan"
	}
	return s
}

// Format writes the settings in the file's shape, keys sorted, values quoted for the shell (the
// fork's scripts source the file for the route metric).
func (s Settings) Format() string {
	enabled := "0"
	if s.Enabled {
		enabled = "1"
	}
	kv := map[string]string{
		"WIFI_ENABLED": enabled, "WIFI_IFACE": s.Iface, "WIFI_COUNTRY": s.Country,
		"WIFI_MODE": strings.ToUpper(s.Mode), "PREFERRED": s.Preferred,
	}
	if s.Mode == "static" {
		kv["WIFI_IP"], kv["WIFI_NETMASK"], kv["WIFI_GATEWAY"] = s.Address, s.Netmask, s.Gateway
	}
	for i, d := range s.DNS {
		kv[fmt.Sprintf("WIFI_NAMESERVER%d", i+1)] = d
	}
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# openccu-lite: Wi-Fi (written by occulited)\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s='%s'\n", k, strings.ReplaceAll(kv[k], "'", `'\''`))
	}
	return b.String()
}

// Route metrics. Ethernet's default route comes from upstream's scripts: metric 10 (and up) by
// dhcp.script, none - 0 - in the static branch of eQ3StartNetwork. Wi-Fi's route takes 5 when it
// is preferred and 600 when not, so neither script changes; with Wi-Fi preferred and a static
// Ethernet route at 0, the up step moves that one to EthDemotedMetric.
const (
	WLANPreferredMetric = 5
	WLANBackupMetric    = 600
	EthDemotedMetric    = 700
)

// WLANMetric is the metric of Wi-Fi's default route under the preference.
func (s Settings) WLANMetric() int {
	if s.Preferred == "wlan" {
		return WLANPreferredMetric
	}
	return WLANBackupMetric
}

// parseKV reads KEY=VALUE lines, with or without single or double quotes, # comments ignored.
func parseKV(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' && v[len(v)-1] == '\'' || v[0] == '"' && v[len(v)-1] == '"') {
			v = v[1 : len(v)-1]
			v = strings.ReplaceAll(v, `'\''`, "'")
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}
