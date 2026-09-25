package wifi

import (
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ConfFile is the networks' file. 0600 root: it holds the hashed PSKs, and for a WPA3-only network
// the passphrase itself (SAE needs it, not the hash).
const ConfFile = "/etc/config/wpa_supplicant.conf"

// CtrlDir is wpa_supplicant's control socket directory; its group is occulite, so occulited talks
// to it without the privilege helper.
const CtrlDir = "/run/wpa_supplicant"

// Security of a network.
const (
	SecOpen = "open"
	SecWPA2 = "wpa2" // WPA/WPA2-PSK, the PSK hashed
	SecWPA3 = "wpa3" // SAE only: sae_password in clear
	SecMix  = "wpa2-wpa3"
)

// Network is one saved network. The secret fields are only ever written, never answered.
type Network struct {
	SSID     string `json:"ssid"`
	Security string `json:"security"`
	Hidden   bool   `json:"hidden"`
	Priority int    `json:"priority"`
	// PSK is the hashed WPA2 key (64 hex); Passphrase the SAE password. Neither leaves the box.
	PSK        string `json:"-"`
	Passphrase string `json:"-"`
}

// HashPSK is wpa_passphrase's derivation: PBKDF2-HMAC-SHA1 over the passphrase, the SSID as the
// salt, 4096 rounds, 32 bytes. A 64-hex passphrase is already a PSK.
func HashPSK(ssid, passphrase string) (string, error) {
	if len(passphrase) == 64 && isHex(passphrase) {
		return strings.ToLower(passphrase), nil
	}
	k, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(k), nil
}

// NewNetwork makes a network from what the user typed: the security decides what is kept.
func NewNetwork(ssid, security, passphrase string, hidden bool, priority int) (Network, error) {
	if err := CheckSSID(ssid); err != nil {
		return Network{}, err
	}
	n := Network{SSID: ssid, Security: security, Hidden: hidden, Priority: priority}
	switch security {
	case SecOpen:
		return n, nil
	case SecWPA2, SecMix:
		if err := CheckPassphrase(passphrase); err != nil {
			return n, err
		}
		psk, err := HashPSK(ssid, passphrase)
		if err != nil {
			return n, err
		}
		n.PSK = psk
		if security == SecMix && !(len(passphrase) == 64 && isHex(passphrase)) {
			// the transition mode: WPA2 with the hash, WPA3 with the passphrase
			n.Passphrase = passphrase
		}
	case SecWPA3:
		if err := CheckPassphrase(passphrase); err != nil || (len(passphrase) == 64 && isHex(passphrase)) {
			return n, fmt.Errorf("password: WPA3 needs the passphrase itself, 8 to 63 characters")
		}
		n.Passphrase = passphrase
	default:
		return n, fmt.Errorf("security: open, wpa2, wpa3 or wpa2-wpa3")
	}
	return n, nil
}

// quote writes a string for wpa_supplicant.conf: "..." when it is plain printable ASCII without a
// quote, otherwise the hex form (P"..." is not understood by every version).
func quote(s string) string {
	plain := true
	for _, r := range s {
		if r < 0x20 || r > 0x7e || r == '"' {
			plain = false
			break
		}
	}
	if plain {
		return `"` + s + `"`
	}
	return hex.EncodeToString([]byte(s))
}

// Conf writes the whole file: the control socket, the country, and the networks, highest priority
// first.
func Conf(country string, nets []Network) string {
	var b strings.Builder
	b.WriteString("# openccu-lite: Wi-Fi networks (written by occulited; edits here are overwritten)\n")
	fmt.Fprintf(&b, "ctrl_interface=DIR=%s GROUP=occulite\n", CtrlDir)
	b.WriteString("update_config=0\n")
	fmt.Fprintf(&b, "country=%s\n", country)
	b.WriteString("sae_pwe=2\n")
	sorted := append([]Network(nil), nets...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority > sorted[j].Priority })
	for _, n := range sorted {
		b.WriteString("\nnetwork={\n")
		fmt.Fprintf(&b, "\tssid=%s\n", quote(n.SSID))
		if n.Hidden {
			b.WriteString("\tscan_ssid=1\n")
		}
		switch n.Security {
		case SecOpen:
			b.WriteString("\tkey_mgmt=NONE\n")
		case SecWPA2:
			b.WriteString("\tkey_mgmt=WPA-PSK\n")
			fmt.Fprintf(&b, "\tpsk=%s\n", n.PSK)
		case SecMix:
			b.WriteString("\tkey_mgmt=WPA-PSK SAE\n")
			fmt.Fprintf(&b, "\tpsk=%s\n", n.PSK)
			if n.Passphrase != "" {
				fmt.Fprintf(&b, "\tsae_password=%s\n", quote(n.Passphrase))
			}
			b.WriteString("\tieee80211w=1\n")
		case SecWPA3:
			b.WriteString("\tkey_mgmt=SAE\n")
			fmt.Fprintf(&b, "\tsae_password=%s\n", quote(n.Passphrase))
			b.WriteString("\tieee80211w=2\n")
		}
		fmt.Fprintf(&b, "\tpriority=%d\n", n.Priority)
		b.WriteString("}\n")
	}
	return b.String()
}

// ParseConf reads the networks back, secrets included, so that a change of one network keeps the
// others as they were (the caller never answers the secrets).
func ParseConf(text string) (country string, nets []Network) {
	var cur *Network
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "network={" {
			cur = &Network{Security: SecOpen}
			continue
		}
		if line == "}" && cur != nil {
			nets = append(nets, *cur)
			cur = nil
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if cur == nil {
			if k == "country" {
				country = v
			}
			continue
		}
		switch k {
		case "ssid":
			cur.SSID = unquoteSSID(v)
		case "scan_ssid":
			cur.Hidden = v == "1"
		case "priority":
			cur.Priority, _ = strconv.Atoi(v)
		case "psk":
			cur.PSK = unquote(v)
		case "sae_password":
			cur.Passphrase = unquote(v)
		case "key_mgmt":
			switch {
			case strings.Contains(v, "WPA-PSK") && strings.Contains(v, "SAE"):
				cur.Security = SecMix
			case strings.Contains(v, "SAE"):
				cur.Security = SecWPA3
			case strings.Contains(v, "WPA-PSK"):
				cur.Security = SecWPA2
			default:
				cur.Security = SecOpen
			}
		}
	}
	// upstream's file (wpa_passphrase's block) names no key_mgmt: a psk means WPA2
	for i := range nets {
		if nets[i].Security == SecOpen && nets[i].PSK != "" {
			nets[i].Security = SecWPA2
		}
	}
	return country, nets
}

// unquote strips a value's double quotes.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

// unquoteSSID: a quoted SSID as it is, an unquoted one is the hex form (quote writes it for a name
// that is not plain printable ASCII).
func unquoteSSID(v string) string {
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	if b, err := hex.DecodeString(v); err == nil {
		return string(b)
	}
	return v
}
