package radio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
)

// Local key mode (task 149, D-103): the HmIP network key is the box's own instead of one only
// eQ-3's key server can re-wrap for another module, so a radio swap needs no internet. hmipserver
// takes the keys from hmip_user.conf, which crRFD.conf includes after its own lines, so a key
// there wins and a backup restored on a stock OpenCCU keeps the network. These are the only
// hmipserver keys lite writes into that file (D-103's exception to D-97).
const (
	NetworkKeyKey      = "Network.Key"
	NetworkKeyBaseKey  = "Network.Key.Base"
	BackboneKeyKey     = "Backbone.Key"
	BackboneKeyBaseKey = "Backbone.Key.Base"
	KeyServerModeKey   = "KeyServer.Mode"
)

// The key-server modes: LOCAL never calls eQ-3; KEYSERVER_LOCAL (the shipped template's) asks the
// key server where no device key is known locally.
const (
	KeyServerLocal         = "LOCAL"
	KeyServerKeyServerOnly = "KEYSERVER"
	KeyServerLocalFallback = "KEYSERVER_LOCAL"
)

// demoNetworkKey is the key example configurations carry; a box never generates or accepts it.
const demoNetworkKey = "0102030405060708090A0B0C0D0E0F10"

// LocalKey is what hmip_user.conf says about the network key. The keys themselves never leave
// occulited: an API answer carries Enabled and the mode only.
type LocalKey struct {
	NetworkKey      string
	NetworkKeyBase  string
	BackboneKey     string
	BackboneKeyBase string
	KeyServerMode   string
}

// Enabled: a network key is configured, so hmipserver writes it into whatever module starts.
func (k LocalKey) Enabled() bool { return k.NetworkKey != "" || k.NetworkKeyBase != "" }

// Pairing (task 192) is what a pairing client may know about the HmIP network without an
// administrator scope: which of the three ways to admit a device can work. It is answered in the
// metadata API's /version (docs/meta-api.md, Feature detection) and carries no key and no SGTIN.
type Pairing struct {
	// KeyServerMode: LOCAL, KEYSERVER or KEYSERVER_LOCAL, as hmip_user.conf says it; an unset
	// mode is the shipped template's KEYSERVER_LOCAL.
	KeyServerMode string `json:"keyserver_mode"`
	// DeviceKeys: how many device keys sgtin.map holds (task 154) - a count, never the list
	// (D-104). hmipserver reads them under LOCAL and KEYSERVER_LOCAL only.
	DeviceKeys int `json:"device_keys"`
	// OfflinePairing: whether a device that has no key on the system can be paired at all. LOCAL
	// never asks eQ-3's key server, so there a device is paired from its own key alone - stored in
	// the map, or scanned from its sticker by the client; in the other two modes the key server
	// stands in (with internet).
	OfflinePairing bool `json:"offline_pairing"`
}

// PairingOf derives the fact from hmip_user.conf and sgtin.map as they are on disk. The count is
// what ParseDeviceKeyMap accepts: a hand-edited line that is no key does not count.
func PairingOf(conf, keyMap string) Pairing {
	mode := keyValue(conf, KeyServerModeKey)
	if mode == "" {
		mode = KeyServerLocalFallback // the shipped template's
	}
	keys, _ := ParseDeviceKeyMap(keyMap)
	return Pairing{KeyServerMode: mode, DeviceKeys: len(keys), OfflinePairing: mode != KeyServerLocal}
}

var keyLineRe = map[string]*regexp.Regexp{}

func keyValue(conf, key string) string {
	re, ok := keyLineRe[key]
	if !ok {
		re = regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=[ \t]*(\S*)[ \t]*\r?$`)
		keyLineRe[key] = re
	}
	if m := re.FindStringSubmatch(conf); m != nil {
		return m[1]
	}
	return ""
}

func init() {
	for _, k := range []string{NetworkKeyKey, NetworkKeyBaseKey, BackboneKeyKey, BackboneKeyBaseKey, KeyServerModeKey, MappingFileKey} {
		keyValue("", k)
	}
}

// ReadLocalKey takes the key lines from hmip_user.conf.
func ReadLocalKey(conf string) LocalKey {
	return LocalKey{
		NetworkKey:      keyValue(conf, NetworkKeyKey),
		NetworkKeyBase:  keyValue(conf, NetworkKeyBaseKey),
		BackboneKey:     keyValue(conf, BackboneKeyKey),
		BackboneKeyBase: keyValue(conf, BackboneKeyBaseKey),
		KeyServerMode:   keyValue(conf, KeyServerModeKey),
	}
}

// withoutKeys drops the given keys' lines and keeps every other line as it was.
func withoutKeys(conf string, keys ...string) string {
	var out []string
	for _, l := range strings.SplitAfter(conf, "\n") {
		t := strings.TrimSpace(l)
		drop := false
		for _, k := range keys {
			if name, _, ok := strings.Cut(t, "="); ok && strings.TrimSpace(name) == k {
				drop = true
			}
		}
		if !drop && l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "")
}

func appendLines(conf string, lines ...string) string {
	if conf != "" && !strings.HasSuffix(conf, "\n") {
		conf += "\n"
	}
	for _, l := range lines {
		conf += l + "\n"
	}
	return conf
}

var allKeyLines = []string{NetworkKeyKey, NetworkKeyBaseKey, BackboneKeyKey, BackboneKeyBaseKey, KeyServerModeKey}

// SetLocalKey returns hmip_user.conf with the two keys and KeyServer.Mode=LOCAL, every other line
// kept; a Network.Key.Base or Backbone.Key.Base left from a hand edit goes, the full keys win.
//
// An empty backbone key leaves Backbone.Key out: a user who knows the network key only keeps the
// module's backbone key, since a new one would cut off the HmIP-HAPs and DRAPs paired under it.
func SetLocalKey(conf, networkKey, backboneKey string) string {
	lines := []string{NetworkKeyKey + "=" + networkKey}
	if backboneKey != "" {
		lines = append(lines, BackboneKeyKey+"="+backboneKey)
	}
	return appendLines(withoutKeys(conf, allKeyLines...), append(lines, KeyServerModeKey+"="+KeyServerLocal)...)
}

// SetKeyServerMode returns hmip_user.conf with KeyServer.Mode set (the pairing override).
func SetKeyServerMode(conf, mode string) string {
	return appendLines(withoutKeys(conf, KeyServerModeKey), KeyServerModeKey+"="+mode)
}

// RestoreKeyLines returns conf with its key lines replaced by those of before (the revert: the
// snapshot's keys, or none, while the choices made since stay).
func RestoreKeyLines(conf, before string) string {
	out := withoutKeys(conf, allKeyLines...)
	var lines []string
	for _, k := range allKeyLines {
		if v := keyValue(before, k); v != "" {
			lines = append(lines, k+"="+v)
		}
	}
	if len(lines) == 0 {
		return out
	}
	return appendLines(out, lines...)
}

// ErrBadKey is a key that is not 16 bytes of hex, or the example configurations' demo key.
var ErrBadKey = errors.New("a key is 32 hexadecimal digits (16 bytes), and not the example key 0102…0F10")

// NormalizeKey checks a key the user entered and returns it upper-case, spaces and dashes removed.
func NormalizeKey(s string) (string, error) {
	k := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", ":", "").Replace(strings.TrimSpace(s)))
	if len(k) != 32 {
		return "", ErrBadKey
	}
	if _, err := hex.DecodeString(k); err != nil {
		return "", ErrBadKey
	}
	if k == demoNetworkKey || strings.Trim(k, "0") == "" {
		return "", ErrBadKey
	}
	return k, nil
}

// GenerateKey returns 16 random bytes as 32 upper-case hex digits.
func GenerateKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	k := strings.ToUpper(hex.EncodeToString(b))
	if _, err := NormalizeKey(k); err != nil {
		return GenerateKey() // the demo key or all zeros, once in 2^127
	}
	return k, nil
}

// ExchangeIDSet: hmip_address.conf carries accesspoint.exchange.id, and hmipserver then ignores a
// configured key for that access point.
func ExchangeIDSet(hmipAddressConf string) bool {
	for _, l := range strings.Split(hmipAddressConf, "\n") {
		if name, _, ok := strings.Cut(strings.TrimSpace(l), "="); ok && strings.TrimSpace(name) == "accesspoint.exchange.id" {
			return true
		}
	}
	return false
}
