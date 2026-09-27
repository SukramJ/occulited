package wifi

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Ctrl speaks wpa_supplicant's control protocol: one datagram per command over the interface's
// socket in CtrlDir, the answer to the sender's own socket. occulited's socket lives in LocalDir,
// which it can write (its runtime directory).
type Ctrl struct {
	Dir      string // CtrlDir under the root
	Iface    string
	LocalDir string
	Timeout  time.Duration
}

var ctrlSeq atomic.Int64

// Request sends one command and answers wpa_supplicant's reply.
func (c Ctrl) Request(cmd string) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	// occulited B-8: the answer socket only ever in the absolute directory given - an empty one
	// put it into the process's working directory (a test's package directory)
	if !filepath.IsAbs(c.LocalDir) {
		return "", fmt.Errorf("wpa_supplicant on %s: no directory for the answer socket", c.Iface)
	}
	local := filepath.Join(c.LocalDir, fmt.Sprintf("wpa-%d-%d", os.Getpid(), ctrlSeq.Add(1)))
	_ = os.Remove(local)
	// removed however the request ends: DialUnix binds it before it connects, and a connect that
	// fails (wpa_supplicant not running yet, the "starting" state) left the bound file behind, one
	// per request (B-8)
	defer os.Remove(local)
	conn, err := net.DialUnix("unixgram", &net.UnixAddr{Name: local, Net: "unixgram"}, &net.UnixAddr{Name: filepath.Join(c.Dir, c.Iface), Net: "unixgram"})
	if err != nil {
		return "", fmt.Errorf("wpa_supplicant on %s: %w", c.Iface, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("wpa_supplicant %s: %w", firstWord(cmd), err)
	}
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return "", fmt.Errorf("wpa_supplicant %s: %w", firstWord(cmd), err)
		}
		reply := string(buf[:n])
		// an unsolicited event ("<3>CTRL-EVENT-…") is not the answer; this socket is not attached,
		// but a reply is never one
		if strings.HasPrefix(reply, "<") {
			continue
		}
		return reply, nil
	}
}

// the command's name for an error; SET_NETWORK and friends carry secrets after it
func firstWord(cmd string) string {
	w, _, _ := strings.Cut(cmd, " ")
	return w
}

// Status is STATUS's answer, the fields the page shows.
type Status struct {
	State   string `json:"state"` // wpa_state: COMPLETED, SCANNING, ASSOCIATING, DISCONNECTED, INACTIVE, …
	SSID    string `json:"ssid,omitempty"`
	BSSID   string `json:"bssid,omitempty"`
	Freq    int    `json:"freq,omitempty"`
	KeyMgmt string `json:"key_mgmt,omitempty"`
	IP      string `json:"ip,omitempty"`
}

// ParseStatus reads STATUS's key=value lines.
func ParseStatus(text string) Status {
	kv := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	f, _ := strconv.Atoi(kv["freq"])
	return Status{State: kv["wpa_state"], SSID: decodeSSID(kv["ssid"]), BSSID: kv["bssid"], Freq: f, KeyMgmt: kv["key_mgmt"], IP: kv["ip_address"]}
}

// Signal is SIGNAL_POLL's answer.
type Signal struct {
	RSSI      int `json:"rssi"`       // dBm
	LinkSpeed int `json:"link_speed"` // Mbit/s
	Freq      int `json:"freq"`
}

// ParseSignal reads SIGNAL_POLL.
func ParseSignal(text string) Signal {
	var s Signal
	for _, line := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		n, _ := strconv.Atoi(v)
		switch k {
		case "RSSI":
			s.RSSI = n
		case "LINKSPEED":
			s.LinkSpeed = n
		case "FREQUENCY":
			s.Freq = n
		}
	}
	return s
}

// ScanResult is one network in range: the strongest BSS of each SSID.
type ScanResult struct {
	SSID     string `json:"ssid"`
	BSSID    string `json:"bssid"`
	Freq     int    `json:"freq"`
	Signal   int    `json:"signal"`   // dBm
	Security string `json:"security"` // open, wpa2, wpa3, wpa2-wpa3, enterprise, wep
	Hidden   bool   `json:"hidden,omitempty"`
}

// ParseScanResults reads SCAN_RESULTS (bssid, frequency, signal level, flags, ssid - tab
// separated), keeps the strongest BSS per SSID, strongest first. A BSS without an SSID is a hidden
// network: listed once, as hidden.
func ParseScanResults(text string) []ScanResult {
	best := map[string]ScanResult{}
	hidden := false
	for i, line := range strings.Split(text, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue // the header
		}
		f := strings.SplitN(line, "\t", 5)
		if len(f) < 4 {
			continue
		}
		freq, _ := strconv.Atoi(f[1])
		sig, _ := strconv.Atoi(f[2])
		ssid := ""
		if len(f) == 5 {
			ssid = decodeSSID(f[4])
		}
		if ssid == "" || strings.Trim(ssid, "\x00") == "" {
			hidden = true
			continue
		}
		r := ScanResult{SSID: ssid, BSSID: f[0], Freq: freq, Signal: sig, Security: securityOf(f[3])}
		if cur, ok := best[ssid]; !ok || r.Signal > cur.Signal {
			best[ssid] = r
		}
	}
	out := make([]ScanResult, 0, len(best)+1)
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Signal != out[j].Signal {
			return out[i].Signal > out[j].Signal
		}
		return out[i].SSID < out[j].SSID
	})
	if hidden {
		out = append(out, ScanResult{Hidden: true, Security: SecWPA2})
	}
	return out
}

// securityOf reads the flags: [WPA2-PSK-CCMP][SAE-CCMP][ESS] and the like.
func securityOf(flags string) string {
	psk := strings.Contains(flags, "-PSK")
	sae := strings.Contains(flags, "SAE")
	switch {
	case strings.Contains(flags, "-EAP") || strings.Contains(flags, "EAP-SUITE"):
		return "enterprise"
	case psk && sae:
		return SecMix
	case sae:
		return SecWPA3
	case psk:
		return SecWPA2
	case strings.Contains(flags, "WEP"):
		return "wep"
	}
	return SecOpen
}

// decodeSSID undoes wpa_supplicant's printf_encode: \\, \", \e, \n, \r, \t and \xNN.
func decodeSSID(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' || i+1 >= len(s) {
			b = append(b, c)
			continue
		}
		i++
		switch s[i] {
		case '\\', '"':
			b = append(b, s[i])
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'e':
			b = append(b, 0x1b)
		case 'x':
			if i+2 < len(s) {
				if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
					b = append(b, byte(v))
					i += 2
					continue
				}
			}
			b = append(b, '\\', 'x')
		default:
			b = append(b, '\\', s[i])
		}
	}
	return string(b)
}
