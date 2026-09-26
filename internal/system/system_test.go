package system

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

func fakeRoot(t *testing.T) Root {
	t.Helper()
	dir := t.TempDir()
	w := func(p, content string) {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	w("VERSION", "VERSION=3.89.8.20260719\nPRODUCT=ova\nPLATFORM=ova\nVARIANT=lite\n")
	// the two account files a confined addon's user is appended to (B-54)
	w("etc/passwd", "root:x:0:0::/:/bin/sh\n")
	w("etc/group", "root:x:0:\n")
	w("var/hm_mode", "HM_MODE='NORMAL'\nHM_HOST='ova-KVM'\nHM_HMIP_DEV='HMIP-RFUSB'\nHM_HMIP_SERIAL='1709ADFA5E'\nHM_HMIP_SGTIN='3014F711A000041709ADFA5E'\nHM_HMIP_VERSION='4.4.18'\nHM_HMIP_ADDRESS='0x128520'\nHM_HMRF_DEV='HMIP-RFUSB'\nHM_HMRF_ADDRESS='0xFF01B6'\nHM_LED_GREEN_MODE2='heartbeat'\n")
	w("etc/config/InterfacesList.xml", "<interfaces v=\"1.0\">\n<ipc>\n <name>BidCos-RF</name>\n <url>xmlrpc_bin://127.0.0.1:32001</url> \n <info>BidCos-RF</info> \n</ipc>\n<ipc><name>HmIP-RF</name><url>xmlrpc://127.0.0.1:32010</url><info>HmIP-RF</info></ipc>\n</interfaces>\n")
	w("etc/config/TZ", "Europe/Berlin\n")
	w("proc/uptime", "12345.67 40000.0\n")
	w("proc/loadavg", "0.52 0.48 0.40 1/200 1234\n")
	w("proc/meminfo", "MemTotal:        1000000 kB\nMemFree:          200000 kB\nMemAvailable:     600000 kB\n")
	w("proc/sys/kernel/hostname", "openccu\n")
	w("etc/init.d/S61rfd", "#!/bin/sh\necho rfd $1\n")
	w("etc/init.d/S62HMServer", "#!/bin/sh\necho hmserver $1\n")
	w("var/run/rfd.pid", "1678\n")
	w("proc/1678/status", "Name:\trfd\n")
	w("var/run/HMIPServer.pid", "99999\n") // stale: no /proc entry
	w("usr/local/etc/config/services.d/hmipserver.disabled", "")
	w("usr/local/etc/config/rc.d/mosquitto", "#!/bin/sh\ncase $1 in info) echo 'Name: Mosquitto'; echo 'Version: 2.1.2+1'; echo 'Info: <div>x</div>'; echo 'Update: /addons/mosquitto/update_check.cgi'; echo 'Config-Url: /addons/mosquitto/settings.cgi'; echo 'Operations: restart uninstall';; *) echo mosquitto $1;; esac\n")
	w("usr/local/etc/config/hm_addons.cfg", "mosquitto {CONFIG_URL /addons/mosquitto/settings.cgi CONFIG_DESCRIPTION {de {<li>Mosquitto MQTT Broker</li>} en {<li>Mosquitto MQTT Broker</li>}} ID mosquitto CONFIG_NAME Mosquitto}\n")
	w("var/log/messages", "Sep  6 03:46:28 openccu daemon.debug node-red[14217]: rpc < http eventSingle\nSep  6 03:46:30 openccu daemon.err rfd[1678]: Address in use\nSep  6 03:46:36 openccu auth.info sshd-session[7086]: Accepted publickey for root\nnot a syslog line\n")
	return Root(dir)
}

func TestVersionAndStatus(t *testing.T) {
	r := fakeRoot(t)
	v := r.ReadVersion()
	if v.Version != "3.89.8.20260719" || v.Product != "ova" || v.Variant != "lite" {
		t.Fatalf("version: %+v", v)
	}
	s := r.ReadStatus()
	if s.Hostname != "openccu" || s.UptimeS != 12345 || len(s.Load) != 3 || s.Load[0] != 0.52 || s.MemTotal != 1000000 || s.MemAvail != 600000 || s.Timezone != "Europe/Berlin" || s.HMMode != "NORMAL" {
		t.Fatalf("status: %+v", s)
	}
	// D-41: the watchdog marker has no consumer on this box unless the Status page is it
	if s.UncleanShutdown != nil {
		t.Errorf("unclean shutdown reported without the marker: %+v", s.UncleanShutdown)
	}
	if err := os.MkdirAll(r.join("/var/status"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.join("/var/status/uncleanShutdown"), nil, 0o664); err != nil {
		t.Fatal(err)
	}
	if u := r.ReadStatus().UncleanShutdown; u == nil || time.Since(u.At) > time.Minute {
		t.Errorf("unclean shutdown: %+v", u)
	}
}

// B-66: the Status page's timezone is the zone name, the POSIX string at most beside it.
func TestStatusTimezone(t *testing.T) {
	const posix = "CET-1CEST-2,M3.5.0/02:00:00,M10.5.0/03:00:00"
	tests := []struct {
		name           string
		files          map[string]string
		wantZone, want string
	}{
		{"the zone name, the POSIX string beside it", map[string]string{"etc/config/timezone": "Europe/Berlin\n", "etc/config/TZ": posix + "\n"}, "Europe/Berlin", posix},
		{"a zone name in both files is said once", map[string]string{"etc/config/timezone": "Europe/Berlin\n", "etc/config/TZ": "Europe/Berlin\n"}, "Europe/Berlin", ""},
		{"a zone name only in TZ", map[string]string{"etc/config/TZ": "Europe/Vienna\n"}, "Europe/Vienna", ""},
		{"only the POSIX string: it is all there is", map[string]string{"etc/config/TZ": posix + "\n"}, posix, ""},
		{"only the name", map[string]string{"etc/config/timezone": "UTC\n"}, "UTC", ""},
		{"neither", map[string]string{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := rootWith(t, tt.files).ReadStatus()
			if s.Timezone != tt.wantZone || s.TZ != tt.want {
				t.Errorf("timezone %q tz %q, want %q and %q", s.Timezone, s.TZ, tt.wantZone, tt.want)
			}
		})
	}
}

func TestRadio(t *testing.T) {
	r := fakeRoot(t)
	rad := r.ReadRadio()
	if rad.Mode != "NORMAL" || len(rad.Modules) != 2 {
		t.Fatalf("radio: %+v", rad)
	}
	if rad.Modules[1].Protocol != "HmIP-RF" || rad.Modules[1].SGTIN != "3014F711A000041709ADFA5E" || rad.Modules[1].Firmware != "4.4.18" {
		t.Fatalf("hmip module: %+v", rad.Modules[1])
	}
	if rad.LEDs["green_mode2"] != "heartbeat" {
		t.Fatalf("leds: %+v", rad.LEDs)
	}
	if len(rad.Interfaces) != 2 || rad.Interfaces[0].Name != "BidCos-RF" || rad.Interfaces[0].URL != "xmlrpc_bin://127.0.0.1:32001" || rad.Interfaces[1].URL != "xmlrpc://127.0.0.1:32010" {
		t.Fatalf("interfaces: %+v", rad.Interfaces)
	}
}

// task 187: without systemd nothing is listed and nothing controlled - the development mode.
func TestNoInit(t *testing.T) {
	list, err := NoInit{}.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := (NoInit{}).Control(t.Context(), "rfd", "restart"); !errors.Is(err, ErrNoInit) {
		t.Fatalf("control: %v", err)
	}
}

func TestAddons(t *testing.T) {
	r := fakeRoot(t)
	// 28.8: an adopted addon keeps its own script as <id>.script behind the wrapper; that file is
	// not an addon and must not be listed as one (it was, on every systemd box)
	if err := os.WriteFile(r.join("usr/local/etc/config/rc.d/mosquitto.script"), []byte("#!/bin/sh\necho mosquitto $1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	b := AddonScripts{Root: r}
	addons, err := b.ListAddons(t.Context())
	if err != nil || len(addons) != 1 {
		t.Fatalf("addons: %v %+v", err, addons)
	}
	a := addons[0]
	if a.Name != "Mosquitto" || a.Version != "2.1.2+1" || a.ConfigURL != "/addons/mosquitto/settings.cgi" || len(a.Operations) != 2 {
		t.Fatalf("info: %+v", a)
	}
	if a.Settings == nil || a.Settings.Name != "Mosquitto" || a.Settings.Description["de"] != "<li>Mosquitto MQTT Broker</li>" {
		t.Fatalf("hm_addons.cfg: %+v", a.Settings)
	}
}

// B-69: the Status warning and the addon menu named the NEO Server by its rc.d id. An addon's name
// is its info's Name:, else its hm_addons.cfg CONFIG_NAME, else a known name; empty otherwise.
func TestAddonNameFallback(t *testing.T) {
	r := fakeRoot(t)
	script := func(id, info string) {
		body := "#!/bin/sh\ncase $1 in info) " + info + ";; esac\n"
		if err := os.WriteFile(r.join("usr/local/etc/config/rc.d/"+id), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script("cfgnamed", "echo 'Version: 1.0'")
	script(NeoServerID, "true")
	script("bare", "echo 'Version: 0.1'")
	script("both", "echo 'Name: From Info'")
	cfg := "mosquitto {CONFIG_URL /addons/mosquitto/settings.cgi ID mosquitto CONFIG_NAME Mosquitto} " +
		"cfgnamed {CONFIG_URL /addons/cfgnamed/index.html ID cfgnamed CONFIG_NAME {Named In Cfg}} " +
		"both {CONFIG_URL /addons/both/index.html ID both CONFIG_NAME {From Cfg}}\n"
	if err := os.WriteFile(r.join("usr/local/etc/config/hm_addons.cfg"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	addons, err := AddonScripts{Root: r}.ListAddons(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range addons {
		got[a.ID] = a.Name
	}
	for _, tt := range []struct{ id, want, why string }{
		{"mosquitto", "Mosquitto", "its info names it"},
		{"both", "From Info", "the info's name wins over hm_addons.cfg"},
		{"cfgnamed", "Named In Cfg", "no Name: in its info, a CONFIG_NAME in hm_addons.cfg"},
		{NeoServerID, "NEO Server", "neither, but a known name"},
		{"bare", "", "nothing to go by: the pages show the id"},
	} {
		if n, ok := got[tt.id]; !ok || n != tt.want {
			t.Errorf("%s: name %q (listed %v), want %q - %s", tt.id, n, ok, tt.want, tt.why)
		}
	}
}

func TestHMAddonsCfgTwoEntries(t *testing.T) {
	cfg := "a {CONFIG_URL /addons/a/s.cgi ID a CONFIG_NAME {A Name} CONFIG_DESCRIPTION {de {<li>x {y}</li>} en {<li>z</li>}}} b {ID b CONFIG_URL /addons/b/s.cgi CONFIG_NAME B CONFIG_DESCRIPTION {}}"
	m := ParseHMAddonsCfg(cfg)
	if len(m) != 2 || m["a"].Name != "A Name" || m["a"].Description["de"] != "<li>x {y}</li>" || m["b"].ConfigURL != "/addons/b/s.cgi" {
		t.Fatalf("parse: %+v", m)
	}
}

// readFile falls back to the privilege helper when the file is root-only (B-13): occulited runs
// as occulite and several of the files the firmware writes are 0600 root:root.
func TestReadFileFallsBackToHelper(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: an unreadable file cannot be produced")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "netconfig")
	if err := os.WriteFile(path, []byte("MODE=DHCP\nIP=192.168.1.225\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	if got := readFile(path); got != "" {
		t.Fatalf("unreadable file read without a helper: %q", got)
	}
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	if got := readFile(path); got != "MODE=DHCP\nIP=192.168.1.225\n" {
		t.Errorf("helper fallback: %q", got)
	}
	if kv := readKV(path); kv["IP"] != "192.168.1.225" || kv["MODE"] != "DHCP" {
		t.Errorf("readKV through the helper: %v", kv)
	}
}

// openccu-lite B-253: hmipserver's data directory is closed to the daemon (0700); the names in it
// come through the helper, and a directory the helper does not answer for is an empty list.
func TestReadDirFallsBackToHelper(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a closed directory cannot be produced")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"3014F711A000041709ADFA5B.ap", "3014F711A000041709ADFA5B.dev"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if names := readDir(dir); names != nil {
		t.Fatalf("a closed directory listed without a helper: %v", names)
	}
	old := Priv
	Priv = rootReader{}
	t.Cleanup(func() { Priv = old })
	if names := readDir(dir); strings.Join(names, " ") != "3014F711A000041709ADFA5B.ap 3014F711A000041709ADFA5B.dev" {
		t.Errorf("helper fallback: %v", names)
	}
	if names := readDir(filepath.Join(dir, "..", "nothing")); names != nil {
		t.Errorf("a missing directory: %v", names)
	}
}

// rootReader stands in for the helper, which reads as root; the allowlist that decides what it
// will read is tested in internal/priv.
type rootReader struct{ priv.Local }

func (rootReader) ListDir(dir string) ([]string, error) {
	st, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chmod(dir, st.Mode()) }()
	return priv.Local{}.ListDir(dir)
}

func (rootReader) ReadFile(path string) ([]byte, error) {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrPermission) {
		// a file in a directory closed to the test user (B-253): root sees through both
		dir := filepath.Dir(path)
		dst, derr := os.Lstat(dir)
		if derr != nil {
			return nil, derr
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, err
		}
		defer func() { _ = os.Chmod(dir, dst.Mode()) }()
		st, err = os.Stat(path)
	}
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chmod(path, st.Mode()) }()
	return os.ReadFile(path)
}
