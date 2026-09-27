package wifi

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSettingsRoundTripAndMetrics(t *testing.T) {
	d := ParseSettings("")
	if !reflect.DeepEqual(d, Settings{Iface: "wlan0", Country: "DE", Mode: "dhcp", Preferred: "eth"}) || d.Validate() != nil {
		t.Fatalf("defaults: %+v %v", d, d.Validate())
	}
	s := Settings{Enabled: true, Iface: "wlan0", Country: "AT", Mode: "static", Address: "192.168.1.50", Netmask: "255.255.255.0", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}, Preferred: "wlan"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := ParseSettings(s.Format()); !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip:\n%+v\n%+v\n%s", got, s, s.Format())
	}
	// below dhcp.script's 10 for Ethernet when preferred, far above it when not
	if s.WLANMetric() != 5 || d.WLANMetric() != 600 {
		t.Fatalf("metrics: %d %d", s.WLANMetric(), d.WLANMetric())
	}
	for _, bad := range []Settings{
		{Iface: "eth0", Country: "DE", Mode: "dhcp", Preferred: "eth"},
		{Iface: "wlan0", Country: "de", Mode: "dhcp", Preferred: "eth"},
		{Iface: "wlan0", Country: "DE", Mode: "static", Address: "192.168.1.50", Netmask: "255.255.255.0", Gateway: "10.0.0.1", Preferred: "eth"},
		{Iface: "wlan0", Country: "DE", Mode: "dhcp", Preferred: "both"},
	} {
		if bad.Validate() == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestBootFile(t *testing.T) {
	// Notepad: a BOM, CRLF, quotes around a value with spaces
	b, err := ParseBootFile("openccu-lite-wifi.txt.txt", "\uFEFFssid=\"My Network \"\r\npsk=secret password\r\ncountry=at\r\nhidden=yes\r\n# a comment\r\naddress=192.168.1.50/24\r\ngateway=192.168.1.1\r\ndns=192.168.1.1, 9.9.9.9\r\n")
	if err != nil {
		t.Fatal(err)
	}
	want := BootSetup{SSID: "My Network ", PSK: "secret password", Country: "AT", Hidden: true, Address: "192.168.1.50", Netmask: "255.255.255.0", Gateway: "192.168.1.1", DNS: []string{"192.168.1.1", "9.9.9.9"}}
	if !reflect.DeepEqual(b, want) {
		t.Fatalf("%+v", b)
	}
	s := b.Apply(Defaults())
	if !s.Enabled || s.Country != "AT" || s.Mode != "static" || s.Netmask != "255.255.255.0" || s.Validate() != nil {
		t.Fatalf("applied: %+v %v", s, s.Validate())
	}
	// upstream's SetupWIFI: the first line the SSID, the last the password
	if b, err := ParseBootFile("SetupWIFI", "Home #1\r\nsecret password\r\n"); err != nil || b.SSID != "Home #1" || b.PSK != "secret password" {
		t.Fatalf("SetupWIFI: %+v %v", b, err)
	}
	for name, content := range map[string]string{
		"openccu-lite-wifi.txt": "ssid=\npsk=12345678\n",                  // no SSID
		"x":                     "ssid=a\npsk=short\n",                    // too short
		"y":                     "ssid=a\npsk=secret password\nfoo=bar\n", // an unknown key
		"z":                     "ssid=a\naddress=192.168.1.50\n",         // no prefix
		"SetupWIFI":             "only one line\n",
		"w":                     "ssid=" + strings.Repeat("x", 33) + "\n",
	} {
		if _, err := ParseBootFile(name, content); err == nil {
			t.Fatalf("accepted %q", content)
		}
	}
	// an open network: no psk
	if b, err := ParseBootFile("openccu-lite-wifi.txt", "ssid=Cafe\n"); err != nil || b.PSK != "" {
		t.Fatalf("open: %+v %v", b, err)
	}
}

func TestHashPSKIsWpaPassphrase(t *testing.T) {
	// IEEE 802.11i, H.4.3: passphrase "password", SSID "IEEE"
	got, err := HashPSK("IEEE", "password")
	if err != nil || got != "f42c6fc52df0ebef9ebb4b90b38a5f902e83fe1b135a70e23aed762e9710a12e" {
		t.Fatalf("%s %v", got, err)
	}
	raw := strings.Repeat("AB", 32)
	if got, _ := HashPSK("x", raw); got != strings.ToLower(raw) {
		t.Fatalf("a 64-hex key is a PSK: %s", got)
	}
}

func TestConfRoundTrip(t *testing.T) {
	a, _ := NewNetwork("Home", SecWPA2, "secret password", false, 10)
	b, _ := NewNetwork("Straße \"5\"", SecWPA3, "another secret", true, 20)
	c, _ := NewNetwork("Cafe", SecOpen, "", false, 0)
	m, _ := NewNetwork("Mixed", SecMix, "mixed secret", false, 5)
	conf := Conf("DE", []Network{a, b, c, m})
	for _, want := range []string{"ctrl_interface=DIR=/run/wpa_supplicant GROUP=occulite", "country=DE", "key_mgmt=SAE", "scan_ssid=1", "ieee80211w=2", "key_mgmt=WPA-PSK SAE"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("missing %q in\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "secret password") {
		t.Fatalf("a WPA2 passphrase in clear:\n%s", conf)
	}
	country, nets := ParseConf(conf)
	if country != "DE" || len(nets) != 4 {
		t.Fatalf("%s %+v", country, nets)
	}
	// highest priority first
	if nets[0].Priority != 20 || nets[3].Priority != 0 {
		t.Fatalf("order: %+v", nets)
	}
	byName := map[string]Network{}
	for _, n := range nets {
		byName[n.SSID] = n
	}
	if byName["Straße \"5\""].Security != SecWPA3 || !byName["Straße \"5\""].Hidden || byName["Straße \"5\""].Passphrase != "another secret" {
		t.Fatalf("wpa3: %+v", byName)
	}
	if byName["Home"].PSK != a.PSK || byName["Home"].Security != SecWPA2 || byName["Home"].Priority != 10 {
		t.Fatalf("wpa2: %+v", byName["Home"])
	}
	if byName["Mixed"].Security != SecMix || byName["Cafe"].Security != SecOpen {
		t.Fatalf("mixed, open: %+v", byName)
	}
	// upstream's file: wpa_passphrase's block without key_mgmt
	_, up := ParseConf("ctrl_interface=/var/run/wpa_supplicant\nnetwork={\n\tssid=\"Old\"\n\t#psk=\"x\"\n\tpsk=" + a.PSK + "\n}\n")
	if len(up) != 1 || up[0].Security != SecWPA2 || up[0].SSID != "Old" {
		t.Fatalf("upstream: %+v", up)
	}
}

func TestParsers(t *testing.T) {
	st := ParseStatus("bssid=aa:bb:cc:dd:ee:ff\nfreq=5180\nssid=Caf\\xc3\\xa9 \\\"x\\\"\nid=0\nmode=station\nkey_mgmt=WPA2-PSK\nwpa_state=COMPLETED\nip_address=192.168.1.23\n")
	if st != (Status{State: "COMPLETED", SSID: "Café \"x\"", BSSID: "aa:bb:cc:dd:ee:ff", Freq: 5180, KeyMgmt: "WPA2-PSK", IP: "192.168.1.23"}) {
		t.Fatalf("%+v", st)
	}
	if s := ParseSignal("RSSI=-58\nLINKSPEED=65\nNOISE=9999\nFREQUENCY=2437\n"); s != (Signal{RSSI: -58, LinkSpeed: 65, Freq: 2437}) {
		t.Fatalf("%+v", s)
	}
	res := ParseScanResults("bssid / frequency / signal level / flags / ssid\n" +
		"aa:aa:aa:aa:aa:01\t2437\t-70\t[WPA2-PSK-CCMP][ESS]\tHome\n" +
		"aa:aa:aa:aa:aa:02\t5180\t-55\t[WPA2-PSK-CCMP][ESS]\tHome\n" +
		"aa:aa:aa:aa:aa:03\t2412\t-80\t[RSN-SAE-CCMP][ESS]\tNew\n" +
		"aa:aa:aa:aa:aa:04\t2412\t-60\t[WPA2-PSK+SAE-CCMP][ESS]\tBoth\n" +
		"aa:aa:aa:aa:aa:05\t2412\t-65\t[WPA2-EAP-CCMP][ESS]\tWork\n" +
		"aa:aa:aa:aa:aa:06\t2412\t-40\t[ESS]\tCafe\n" +
		"aa:aa:aa:aa:aa:07\t2412\t-50\t[WPA2-PSK-CCMP][ESS]\t\n")
	got := []string{}
	for _, r := range res {
		got = append(got, r.SSID+"/"+r.Security)
	}
	want := []string{"Cafe/open", "Home/wpa2", "Both/wpa2-wpa3", "Work/enterprise", "New/wpa3", "/wpa2"}
	if !reflect.DeepEqual(got, want) || res[1].Freq != 5180 || !res[5].Hidden {
		t.Fatalf("%v %+v", got, res)
	}
}

// a fake wpa_supplicant: answers PING with PONG, after an unsolicited event
func TestCtrlRequest(t *testing.T) {
	dir := t.TempDir()
	srv, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(dir, "wlan0"), Net: "unixgram"})
	if err != nil {
		t.Skip("no unixgram here:", err)
	}
	defer srv.Close()
	go func() {
		buf := make([]byte, 256)
		for {
			n, from, err := srv.ReadFromUnix(buf)
			if err != nil {
				return
			}
			_, _ = srv.WriteToUnix([]byte("<3>CTRL-EVENT-SCAN-STARTED"), from)
			if string(buf[:n]) == "PING" {
				_, _ = srv.WriteToUnix([]byte("PONG\n"), from)
			}
		}
	}()
	local := filepath.Join(dir, "local")
	if err := os.Mkdir(local, 0o755); err != nil {
		t.Fatal(err)
	}
	c := Ctrl{Dir: dir, Iface: "wlan0", LocalDir: local, Timeout: time.Second}
	got, err := c.Request("PING")
	if err != nil || got != "PONG\n" {
		t.Fatalf("%q %v", got, err)
	}
	if ents, _ := os.ReadDir(local); len(ents) != 0 {
		t.Fatalf("the local socket was left: %v", ents)
	}
	if _, err := (Ctrl{Dir: dir, Iface: "wlan1", LocalDir: local, Timeout: time.Second}).Request("PING"); err == nil {
		t.Fatal("no socket: no error")
	}
	// B-8: the failed connect leaves no bound socket behind
	if ents, _ := os.ReadDir(local); len(ents) != 0 {
		t.Fatalf("a failed request left its socket: %v", ents)
	}
	// B-8: no directory, or a relative one, is an error - never a socket in the working directory
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, ld := range []string{"", "local"} {
		if _, err := (Ctrl{Dir: dir, Iface: "wlan0", LocalDir: ld, Timeout: time.Second}).Request("PING"); err == nil {
			t.Errorf("LocalDir %q: no error", ld)
		}
	}
	if ents, _ := os.ReadDir(cwd); len(ents) != 0 {
		t.Fatalf("a socket in the working directory: %v", ents)
	}
}
