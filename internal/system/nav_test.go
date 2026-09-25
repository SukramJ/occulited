package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// redmaticConf is the real drop-in off the lab box, 2026-09-07:
// /usr/local/etc/config/lighttpd/redmatic.conf, a symlink to the addon's own etc/lighttpd.conf.
// The second block is the addon's Philips-Hue emulation and is not a frontend of the addon.
const redmaticConf = `url.redirect = ("^/addons/red$" => "/addons/red/")

$HTTP["url"] =~ "^/(addons/red/).*" {
  proxy.server = ("/addons/red/" => (( "host" => "127.0.0.1", "port" => 1880 )))
  proxy.header = ( "upgrade" => "enable")
  server.errorfile-prefix  = "/usr/local/addons/redmatic/www/lighttpd-error-"
}

# Proxy rule to redirect request to amazon-echo-hub node from node-red-contrib-amazon-echo
$HTTP["url"] =~ "(^/description.xml)|(^/api/.*/lights)" {
  proxy.server = ( "" => ("localhost" => ("host" => "127.0.0.1", "port" => 6502)))
}
`

func TestFrontendPath(t *testing.T) {
	cases := []struct{ name, conf, want string }{
		{"redmatic, the real file", redmaticConf, "/addons/red/"},
		{"key on the next line", "$HTTP[\"url\"] =~ \"^/(addons/x/).*\" {\n proxy.server = (\n   \"/addons/x/\" => (( \"host\" => \"127.0.0.1\", \"port\" => 1 )))\n}\n", "/addons/x/"},
		{"no trailing slash is normalised", "proxy.server = ( \"/addons/x\" => ( ( \"port\" => 1 ) ) )\n", "/addons/x/"},
		{"catch-all key, path from the block regex", "$HTTP[\"url\"] =~ \"^/(addons/y/).*\" {\n proxy.server = ( \"\" => ((\"port\" => 1)))\n}\n", "/addons/y/"},
		{"the hue block alone is not a frontend", "$HTTP[\"url\"] =~ \"(^/description.xml)|(^/api/.*/lights)\" {\n proxy.server = ( \"\" => ((\"port\" => 6502)))\n}\n", ""},
		{"a proxy outside /addons is not one either", "proxy.server = ( \"/grafana/\" => ((\"port\" => 3000)))\n", ""},
		{"the whole addons directory is not one addon", "proxy.server = ( \"/addons/\" => ((\"port\" => 1)))\n", ""},
		{"traversal is refused", "proxy.server = ( \"/addons/../etc/\" => ((\"port\" => 1)))\n", ""},
		{"a commented-out proxy does not count", "# proxy.server = ( \"/addons/x/\" => ((\"port\" => 1)))\n", ""},
		{"a # inside a string is not a comment", "proxy.server = ( \"/addons/x#y/\" => ((\"port\" => 1)))\n", ""},
		{"no proxy at all: an alias is not a frontend", "alias.url = ( \"/addons/x/\" => \"/usr/local/addons/x/www/\" )\n", ""},
		{"an empty file", "", ""},
	}
	for _, c := range cases {
		if got := frontendPath(c.conf); got != c.want {
			t.Errorf("%s: frontendPath = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAddonFrontends(t *testing.T) {
	r := fakeRoot(t)
	d := r.join("/usr/local/etc/config/lighttpd")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "redmatic.conf"), []byte(redmaticConf), 0o644)
	// an addon that only configures something for itself, with no path of its own
	_ = os.WriteFile(filepath.Join(d, "other.conf"), []byte("server.max-keep-alive-idle = 5\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "Not An Id.conf"), []byte(redmaticConf), 0o644)
	_ = os.WriteFile(filepath.Join(d, "ignored.txt"), []byte(redmaticConf), 0o644)
	got := r.AddonFrontends()
	if len(got) != 1 || got["redmatic"] != "/addons/red/" {
		t.Fatalf("frontends: %+v", got)
	}
}

func TestNavEntries(t *testing.T) {
	r := fakeRoot(t)
	d := r.join("/usr/local/etc/config/nav.d")
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "50-system.json"), []byte(`{"id":"grafana","label":{"de":"Grafana","en":"Grafana"},"href":"/grafana/","target":"iframe","order":50,"icon":"chart","keep_alive":true}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "60-ext.json"), []byte(`{"id":"docs","label":"Docs <b>x</b>","href":"https://example.org/","target":"blank","order":60,"keep_alive":true}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "70-bad.json"), []byte(`{"id":"Bad Id","label":{"en":"x"},"href":"/x/"}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "71-xss.json"), []byte(`{"id":"evil","label":{"en":"e"},"href":"javascript:alert(1)","target":"iframe"}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "72-remote-iframe.json"), []byte(`{"id":"remote","label":{"en":"r"},"href":"https://evil.example/","target":"iframe"}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "80-mosquitto.json"), []byte(`{"id":"mosquitto","label":{"en":"Broker"},"href":"/addons/mosquitto/settings.cgi","target":"blank","order":80}`), 0o644)
	_ = os.WriteFile(filepath.Join(d, "99-broken.json"), []byte(`{not json`), 0o644)
	entries := AddonScripts{Root: r}.NavEntries(t.Context())
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	if len(entries) != 3 || ids[0] != "grafana" || ids[1] != "docs" || ids[2] != "mosquitto" {
		t.Fatalf("entries: %v (%+v)", ids, entries)
	}
	if entries[1].Label["en"] != "Docs bx/b" && entries[1].Label["en"] != "Docs x" {
		t.Fatalf("html stripped from label: %q", entries[1].Label["en"])
	}
	// The drop-in wins over anything synthesised, and naming it after an installed addon is a
	// deliberate declaration that the addon has a frontend: it is an addon entry, not a tab.
	if entries[2].Source != "addon" || entries[2].Target != "blank" || entries[2].Href != "/addons/mosquitto/settings.cgi" || entries[2].Proxied {
		t.Fatalf("the addon's own drop-in must win over the synthesised entry: %+v", entries[2])
	}
	// a drop-in opts in to being kept loaded by the shell; a page in a new tab cannot be kept, and
	// an entry that does not ask is not
	if !entries[0].KeepAlive || entries[1].KeepAlive || entries[2].KeepAlive {
		t.Fatalf("keep_alive: grafana asks (iframe), docs asks (blank), mosquitto does not: %+v", entries)
	}
	// grafana is a drop-in that is not an addon, so it stays a tab of its own
	if entries[0].Source != "nav.d" {
		t.Fatalf("a drop-in that is not an addon stays a nav.d entry: %+v", entries[0])
	}
	// Without the drop-in, a Config-Url alone is *not* a menu entry (task 26's correction):
	// mosquitto has a settings page and no lighttpd drop-in, so it belongs on the Addons page only.
	_ = os.Remove(filepath.Join(d, "80-mosquitto.json"))
	entries = AddonScripts{Root: r}.NavEntries(t.Context())
	for _, e := range entries {
		if e.ID == "mosquitto" {
			t.Fatalf("an addon with only a Config-Url must not be in the menu: %+v", e)
		}
	}
	// With one, the entry is synthesised and points at the frontend, not at the settings page; its
	// route is named after the proxied path (task 88), the addon id beside it.
	ld := r.join("/usr/local/etc/config/lighttpd")
	_ = os.MkdirAll(ld, 0o755)
	_ = os.WriteFile(filepath.Join(ld, "mosquitto.conf"), []byte(redmaticConf), 0o644)
	entries = AddonScripts{Root: r}.NavEntries(t.Context())
	last := entries[len(entries)-1]
	if last.ID != "red" || last.Addon != "mosquitto" || last.Source != "addon" || last.Href != "/addons/red/" || last.Target != "iframe" || last.Label["de"] != "Mosquitto" || !last.Proxied {
		t.Fatalf("synthesised: %+v", last)
	}
	// B-133: a drop-in of the addon's own that points into the proxied path is proxied as well; one
	// that points at a CGI beside it is not
	_ = os.WriteFile(filepath.Join(d, "80-mosquitto.json"), []byte(`{"id":"mosquitto","label":{"en":"Broker"},"href":"/addons/red/ui/","target":"iframe","order":80}`), 0o644)
	entries = AddonScripts{Root: r}.NavEntries(t.Context())
	if e := entries[len(entries)-1]; e.ID != "mosquitto" || !e.Proxied {
		t.Fatalf("a drop-in into the proxied path is proxied: %+v", e)
	}
	_ = os.WriteFile(filepath.Join(d, "80-mosquitto.json"), []byte(`{"id":"mosquitto","label":{"en":"Broker"},"href":"/addons/mosquitto/index.cgi","target":"iframe","order":80}`), 0o644)
	entries = AddonScripts{Root: r}.NavEntries(t.Context())
	if e := entries[len(entries)-1]; e.ID != "mosquitto" || e.Proxied {
		t.Fatalf("a drop-in at a CGI is not proxied: %+v", e)
	}
	_ = os.Remove(filepath.Join(d, "80-mosquitto.json"))
	// a nav.d entry that is no addon carries no addon id
	if entries[0].ID != "grafana" || entries[0].Addon != "" {
		t.Fatalf("nav.d: %+v", entries[0])
	}
}

// TestNavEntryIDs (task 88, D-61): an addon's frontend is routed by its proxied path's segment and
// carries the addon id beside it; the addon id stays where that name is ambiguous - two frontends on
// one path, a nav.d entry of that name, another installed addon of that name - and a nav.d drop-in
// named after an addon keeps its id.
func TestNavEntryIDs(t *testing.T) {
	r := fakeRoot(t)
	ld := r.join("/usr/local/etc/config/lighttpd")
	rc := r.join("/usr/local/etc/config/rc.d")
	nd := r.join("/usr/local/etc/config/nav.d")
	for _, d := range []string{ld, rc, nd} {
		_ = os.MkdirAll(d, 0o755)
	}
	addon := func(id, name, path string) {
		_ = os.WriteFile(filepath.Join(rc, id), []byte("#!/bin/sh\ncase $1 in info) echo 'Name: "+name+"'; echo 'Version: 1.0';; *) echo "+id+" $1;; esac\n"), 0o755)
		if path != "" {
			_ = os.WriteFile(filepath.Join(ld, id+".conf"), []byte("$HTTP[\"url\"] =~ \"^"+path+"\" {\n  proxy.server = (\""+path+"\" => ((\"host\" => \"127.0.0.1\", \"port\" => 1880)))\n}\n"), 0o644)
		}
	}
	addon("redmatic", "RedMatic", "/addons/red/")     // the path's name: red
	addon("hmm", "Homematic-Manager", "/addons/hmm/") // path and id agree
	addon("shared-a", "A", "/addons/shared/")         // two frontends on one path: both keep their id
	addon("shared-b", "B", "/addons/shared/")
	addon("taker", "Taker", "/addons/mosquitto/") // mosquitto is another installed addon: taker keeps its id
	addon("grafana-addon", "G", "/addons/grafana/")
	addon("odd", "Odd", "/addons/Node_RED.v2/")
	_ = os.WriteFile(filepath.Join(nd, "10-grafana.json"), []byte(`{"id":"grafana","label":"Grafana","href":"/grafana/"}`), 0o644)
	_ = os.WriteFile(filepath.Join(nd, "20-hmm.json"), []byte(`{"id":"hmm","label":"HMM","href":"/addons/hmm/x/"}`), 0o644)

	got := map[string]NavEntry{}
	for _, e := range (AddonScripts{Root: r}).NavEntries(t.Context()) {
		got[e.Addon+"|"+e.ID] = e
	}
	for _, want := range []struct{ addon, id, source string }{
		{"redmatic", "red", "addon"},
		{"hmm", "hmm", "addon"}, // the nav.d drop-in named after it: its id, and the addon
		{"shared-a", "shared-a", "addon"},
		{"shared-b", "shared-b", "addon"},
		{"taker", "taker", "addon"},
		{"grafana-addon", "grafana-addon", "addon"}, // grafana is a nav.d entry
		{"odd", "node-red-v2", "addon"},
		{"", "grafana", "nav.d"},
	} {
		e, ok := got[want.addon+"|"+want.id]
		if !ok || e.Source != want.source {
			t.Errorf("%s|%s (%s): missing or wrong - %+v", want.addon, want.id, want.source, got)
		}
	}
	if e := got["hmm|hmm"]; e.Href != "/addons/hmm/x/" {
		t.Errorf("the nav.d drop-in wins: %+v", e)
	}
	if len(got) != 8 {
		t.Errorf("entries: %d %+v", len(got), got)
	}
}

func TestFrontendNavID(t *testing.T) {
	for href, want := range map[string]string{
		"/addons/red/":         "red",
		"/addons/red":          "red",
		"/addons/hmm/web/":     "hmm",
		"/addons/Node_RED.v2/": "node-red-v2",
		"/addons/-x-/":         "x",
		"/addons/_/":           "",
		"/addons/":             "",
		"/grafana/":            "",
		"/addons/" + strings.Repeat("a", 40) + "/": strings.Repeat("a", 32),
	} {
		if got := frontendNavID(href); got != want {
			t.Errorf("%s: %q, want %q", href, got, want)
		}
	}
}

func TestNetworkFirewallTime(t *testing.T) {
	r := fakeRoot(t)
	w := func(p, c string) {
		full := r.join(p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(c), 0o644)
	}
	w("/etc/config/netconfig", "HOSTNAME=openccu\nMODE=MANUAL\nIP=192.0.2.119\nNETMASK=255.255.255.0\nGATEWAY=192.0.2.1\nNAMESERVER1=192.0.2.1\nNAMESERVER2=1.1.1.1\n")
	w("/etc/config/ntpclient", "NTPSERVERS='0.de.pool.ntp.org 1.de.pool.ntp.org'\n")
	w("/etc/config/firewall.conf", "MODE = RESTRICTIVE\nUSERPORTS = 1883 8883\nSERVICES = 1 2\nIPS = 192.0.2.0/24\n")
	w("/sys/class/net/eth0/address", "52:54:00:12:34:56\n")
	w("/sys/class/net/eth0/operstate", "up\n")
	w("/sys/class/net/lo/address", "00:00:00:00:00:00\n")
	n := r.ReadNetwork()
	if n.Mode != "static" || n.Address != "192.0.2.119" || n.Gateway != "192.0.2.1" || len(n.DNS) != 2 || n.Hostname != "openccu" {
		t.Fatalf("network: %+v", n)
	}
	if len(n.Interfaces) != 1 || n.Interfaces[0].Name != "eth0" || !n.Interfaces[0].Up || n.Interfaces[0].MAC != "52:54:00:12:34:56" {
		t.Fatalf("interfaces: %+v", n.Interfaces)
	}
	fw := r.ReadFirewall()
	if fw.Mode != "RESTRICTIVE" || len(fw.UserPorts) != 2 || fw.UserPorts[1] != "8883" || fw.IPs[0] != "192.0.2.0/24" {
		t.Fatalf("firewall: %+v", fw)
	}
	tc := r.ReadTime()
	if tc.TZ != "Europe/Berlin" || len(tc.NTPServers) != 2 {
		t.Fatalf("time: %+v", tc)
	}
}
