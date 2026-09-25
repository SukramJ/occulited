package radio

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// HmIP device keys (task 154, D-103): the device local key (DLK) printed on every HmIP device's
// sticker, as a QR code and as a 26-character KEY. hmipserver reads a map of SGTIN to key at its
// start when SGTIN.LocalKey.MappingFile names one (and KeyServer.Mode is LOCAL or
// KEYSERVER_LOCAL, the shipped template's), and on an inclusion request from a mapped device it
// includes the device with that key by itself: a plain install mode pairs it, without the key
// server.
const (
	// DeviceKeyMapFile is the map, beside hmipserver's other files, so a backup carries it.
	DeviceKeyMapFile = "/etc/config/crRFD/sgtin.map"
	// MappingFileKey is the property in hmip_user.conf that names the map.
	MappingFileKey = "SGTIN.LocalKey.MappingFile"
)

// DeviceKey is one map entry: the SGTIN (24 upper-case hex digits) and the key (32).
type DeviceKey struct {
	SGTIN string
	Key   string
}

// Payload is the QR code's text for the entry, as the sticker has it.
func (k DeviceKey) Payload() string { return "EQ01SG" + k.SGTIN + "DLK" + k.Key }

// printedKeyChars is eQ-3's 5-bit alphabet for the printed KEY: no D, I, O and V.
const printedKeyChars = "0123456789ABCEFGHJKLMNPQRSTUWXYZ"

// PrintedKeyToHex converts the printed 26-character KEY to the 32 hex digits of the same key. It is
// the WebUI's conversion (convertHmIPKeyBase32ToBase16), step for step: the characters from the
// last, 5 bits each, into the bytes from the last.
func PrintedKeyToHex(printed string) string {
	var key [16]byte
	value, bits, at := 0, 0, len(key)-1
	for i := len(printed) - 1; i >= 0; i-- {
		if d := strings.IndexByte(printedKeyChars, printed[i]); d >= 0 {
			value |= d << bits
		}
		bits += 5
		for bits > 8 && at >= 0 {
			key[at] = byte(value)
			value >>= 8
			bits -= 8
			at--
		}
	}
	return strings.ToUpper(hex.EncodeToString(key[:]))
}

var (
	payloadRe = regexp.MustCompile(`EQ01SG([0-9A-F]{24})DLK([0-9A-F]{32})`)
	sgtin24Re = regexp.MustCompile(`^[0-9A-F]{24}$`)
	hex32Re   = regexp.MustCompile(`^[0-9A-F]{32}$`)
	printedRe = regexp.MustCompile(`^[` + printedKeyChars + `]{26}$`)
)

// ErrDeviceCode is input that is not a device's QR code, SGTIN or key; its message says which.
type ErrDeviceCode struct{ Msg string }

func (e ErrDeviceCode) Error() string { return e.Msg }

// squeeze drops what a sticker or a copy puts between the characters: spaces, dashes, line breaks.
func squeeze(s string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '\t', '\r', '\n':
			return -1
		}
		return r
	}, s))
}

// ParseDeviceCode reads a scanned or pasted QR code: EQ01SG<SGTIN>DLK<key>, anywhere in the text.
func ParseDeviceCode(code string) (DeviceKey, error) {
	m := payloadRe.FindStringSubmatch(squeeze(code))
	if m == nil {
		return DeviceKey{}, ErrDeviceCode{"not an HmIP device code (EQ01SG…DLK…)"}
	}
	return DeviceKey{SGTIN: m[1], Key: m[2]}, nil
}

// ParseDeviceKey reads a typed SGTIN and key: the SGTIN with or without its dashes, the key as the
// printed 26 characters or as 32 hex digits.
func ParseDeviceKey(sgtin, key string) (DeviceKey, error) {
	s := squeeze(sgtin)
	if !sgtin24Re.MatchString(s) {
		return DeviceKey{}, ErrDeviceCode{"the SGTIN is 24 characters, 0-9 and A-F (the dashes do not count)"}
	}
	k := squeeze(key)
	switch {
	case hex32Re.MatchString(k):
	case printedRe.MatchString(k):
		k = PrintedKeyToHex(k)
	case len(k) == 26:
		return DeviceKey{}, ErrDeviceCode{"the printed key has no D, I, O or V: a 0 or 1 was probably meant"}
	default:
		return DeviceKey{}, ErrDeviceCode{fmt.Sprintf("the key is the 26 printed characters or 32 hex digits, not %d characters", len(k))}
	}
	if strings.Trim(k, "0") == "" {
		return DeviceKey{}, ErrDeviceCode{"the key is all zeros"}
	}
	return DeviceKey{SGTIN: s, Key: k}, nil
}

// ParseDeviceKeyMap reads the map (Java properties: SGTIN=KEY per line, # and ! comments). Entries
// that are not an SGTIN and a 16-byte key are skipped - hmipserver refuses such a key at the
// inclusion - and counted in bad.
func ParseDeviceKeyMap(text string) (keys []DeviceKey, bad int) {
	seen := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || line[0] == '#' || line[0] == '!' {
			continue
		}
		i := strings.IndexAny(line, "=:")
		if i < 0 {
			bad++
			continue
		}
		s, k := strings.ToUpper(strings.TrimSpace(line[:i])), strings.ToUpper(strings.TrimSpace(line[i+1:]))
		if !sgtin24Re.MatchString(s) || !hex32Re.MatchString(k) {
			bad++
			continue
		}
		// a later line wins, as in java.util.Properties
		if at, ok := seen[s]; ok {
			keys[at].Key = k
			continue
		}
		seen[s] = len(keys)
		keys = append(keys, DeviceKey{SGTIN: s, Key: k})
	}
	sort.Slice(keys, func(a, b int) bool { return keys[a].SGTIN < keys[b].SGTIN })
	return keys, bad
}

// FormatDeviceKeyMap writes the map, sorted by SGTIN.
func FormatDeviceKeyMap(keys []DeviceKey) string {
	sorted := append([]DeviceKey{}, keys...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].SGTIN < sorted[b].SGTIN })
	var b strings.Builder
	b.WriteString("# HmIP device keys: SGTIN=key, read by HmIP-RF at its start (written by occulited)\n")
	for _, k := range sorted {
		b.WriteString(k.SGTIN + "=" + k.Key + "\n")
	}
	return b.String()
}

// MappingFile is the map hmip_user.conf names, "" when none.
func MappingFile(conf string) string { return keyValue(conf, MappingFileKey) }

// SetMappingFile returns hmip_user.conf naming path as the map, every other line kept.
func SetMappingFile(conf, path string) string {
	return appendLines(withoutKeys(conf, MappingFileKey), MappingFileKey+"="+path)
}

// ErrNoDeviceKeyMap: the key-server mode keeps hmipserver from reading the map (KEYSERVER).
var ErrNoDeviceKeyMap = errors.New("KeyServer.Mode is KEYSERVER: HmIP-RF does not read the device keys")

// DeviceKeysRead says whether hmipserver reads the map under the configured key-server mode; mode
// "" is the shipped template's KEYSERVER_LOCAL.
func DeviceKeysRead(mode string) bool { return mode != KeyServerKeyServerOnly }

// DeviceAddressOf is the device address an SGTIN has in hmipserver's device list: its last 14 hex
// digits, whatever its company prefix (the radio module's own entry shows the same, see
// interfaces.SGTINDevice).
func DeviceAddressOf(sgtin string) string {
	if len(sgtin) != 24 {
		return ""
	}
	return sgtin[10:]
}
