package system

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/radio"
)

const rfdConf = "# TCP Port for XmlRpc connections\nListen IP = 127.0.0.1\nListen Port = 32001\n\n[Interface 0]\nType = CCU2\nComPortFile = /dev/mmd_bidcos\nAccessFile = /dev/null\nResetFile = /dev/null\n\n[Interface 1]\nType = Lan Interface\nName = Keller\nSerial Number = KEQ0123456\nEncryption Key = 0011223344556677\nIP Address = 192.168.1.50\n\n"

func TestLANGatewaysReadWrite(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/rfd.conf": rfdConf})
	gws := r.ReadLANGateways(GatewayRF)
	if len(gws) != 1 || gws[0].Type != "Lan Interface" || gws[0].Name != "Keller" || gws[0].Serial != "KEQ0123456" || gws[0].Address != "192.168.1.50" || !gws[0].HasKey {
		t.Fatalf("%+v", gws)
	}
	// keep the first (key omitted = kept), add a second
	out, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{
		{Type: "Lan Interface", Name: "Keller", Serial: "keq0123456", IP: "192.168.1.50"},
		{Type: "HMLGW2", Name: "Garage", Serial: "NEQ9876543", Key: "abcDEF123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[1].Index != 2 || out[1].Serial != "NEQ9876543" || out[1].Address != "" {
		t.Fatalf("%+v", out)
	}
	b, _ := os.ReadFile(r.join("/etc/config/rfd.conf"))
	s := string(b)
	for _, want := range []string{"Listen IP = 127.0.0.1\n", "[Interface 0]\nType = CCU2\nComPortFile = /dev/mmd_bidcos\nAccessFile = /dev/null\nResetFile = /dev/null\n\n", "[Interface 1]\nType = Lan Interface\nName = Keller\nSerial Number = KEQ0123456\nEncryption Key = 0011223344556677\nIP Address = 192.168.1.50\n\n[Interface 2]\nType = HMLGW2\nName = Garage\nSerial Number = NEQ9876543\nEncryption Key = abcDEF123\n\n"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in\n%s", want, s)
		}
	}
	// remove all
	out, err = r.WriteLANGateways(GatewayRF, nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("%v %+v", err, out)
	}
	b, _ = os.ReadFile(r.join("/etc/config/rfd.conf"))
	if !strings.HasSuffix(string(b), "ResetFile = /dev/null\n\n") || strings.Contains(string(b), "Interface 1") {
		t.Errorf("header not kept alone:\n%s", b)
	}
	// validation
	for _, bad := range []LANGatewaySpec{
		{Type: "HMWLGW", Serial: "X"},
		{Type: "HMLGW2", Serial: "no spaces"},
		{Type: "HMLGW2", Serial: "OK1", Name: "a=b"},
		{Type: "HMLGW2", Serial: "OK1", IP: "not a host!"},
	} {
		if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{bad}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "HMLGW2", Serial: "A1"}, {Type: "HMLGW2", Serial: "a1"}}); err == nil {
		t.Error("duplicate serial accepted")
	}
}

// the maintainer's HM-LGW-O-TW-W-EU (2026-09-20): the key eQ-3 printed on it is "=u6%U!M8e3",
// which the old pattern refused. It has to survive the write, the read back and the key-change
// file, whose parsers cut at the first "=".
func TestGatewayKeyWithSpecialCharacters(t *testing.T) {
	const key = "=u6%U!M8e3"
	r := rootWith(t, map[string]string{"etc/config/rfd.conf": rfdConf})
	if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "HMLGW2", Name: "Dach", Serial: "NEQ1234567", Key: key, IP: "192.0.2.50"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(r.join("/etc/config/rfd.conf"))
	if !strings.Contains(string(b), "Encryption Key = "+key+"\n") {
		t.Errorf("written:\n%s", b)
	}
	gws := r.ReadLANGateways(GatewayRF)
	if len(gws) != 1 || gws[0].Key() != key {
		t.Fatalf("read back %+v", gws)
	}
	// the queued key change: the file's KEY= and CURKEY= carry it whole
	if err := r.QueueGatewayKeyChange(GatewayRF, "NEQ1234567", "192.0.2.50", key, "old-One!"); err != nil {
		t.Fatal(err)
	}
	f := r.join("/etc/config/NEQ1234567.keychange")
	body, _ := os.ReadFile(f)
	k := radio.ParseKeyChange(f, string(body))
	if k.Key != key || k.CurKey != "old-One!" || k.Serial != "NEQ1234567" {
		t.Errorf("parsed %+v", k)
	}
	// a space or a control character is still refused, since both are lost or break the line
	for _, bad := range []string{"with space", "tab\there", "line\nbreak", strings.Repeat("x", 65)} {
		if err := r.QueueGatewayKeyChange(GatewayRF, "NEQ1234567", "", bad, ""); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestLANGatewaysWiredFresh(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	out, err := r.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMWLGW", Name: "Flur", Serial: "LEQ0000001", Key: "k1"}})
	if err != nil || len(out) != 1 || out[0].Index != 0 {
		t.Fatalf("%v %+v", err, out)
	}
	b, _ := os.ReadFile(r.join("/etc/config/hs485d.conf"))
	// B-164: the generated header puts hs485d on the loopback, as the image's rfd.conf template does
	if !strings.HasPrefix(string(b), "# This File was automatically generated\n# TCP Port for XmlRpc connections\nListen IP = 127.0.0.1\nListen Port = 32000\n") || !strings.Contains(string(b), "[Interface 0]\nType = HMWLGW\nName = Flur\n") {
		t.Errorf("%s", b)
	}
	if _, err := r.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMLGW2", Serial: "X1"}}); err == nil {
		t.Error("rf type accepted for wired")
	}
}

// B-163: hs485d.service's condition is the marker, and the radio run writes it at boot only - so
// the first wired gateway of a system that booted without one could not start its daemon at all.
func TestWiredGatewayKeepsTheHS485DMarker(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	marker := r.join(HS485DEnabledMarker)
	if _, err := r.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMWLGW", Serial: "LEQ0636432", Key: "k1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the marker after the first wired gateway: %v", err)
	}
	if !r.HasHS485D() {
		t.Error("HasHS485D")
	}
	// the last one removed: the daemon has nothing to serve, and the marker goes with it
	if _, err := r.WriteLANGateways(GatewayWired, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the marker without a wired interface: %v", err)
	}
	// the rf class never touches it
	_ = os.WriteFile(r.join("/etc/config/rfd.conf"), []byte(rfdConf), 0o600)
	if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "HMLGW2", Serial: "KEQ1065511", Key: "k2"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("an rf gateway must not enable hs485d")
	}
}

func TestGatewayKeyChange(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	if err := r.QueueGatewayKeyChange(GatewayRF, "neq0000001", "", "newkey", "oldkey"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(r.join("/etc/config/NEQ0000001.keychange"))
	if string(b) != "Class=RF\nSerial=NEQ0000001\nIP=\nKEY=newkey\nCURKEY=oldkey\n" {
		t.Errorf("%q", b)
	}
	if p := r.PendingGatewayKeyChanges(); len(p) != 1 || p[0] != "NEQ0000001" {
		t.Errorf("%v", p)
	}
	if err := r.QueueGatewayKeyChange(GatewayRF, "NEQ0000001", "", "", "x"); err == nil {
		t.Error("empty key accepted")
	}
}

func TestCryptIndex(t *testing.T) {
	out := "Default key = 0\nCurrent user key = 3\nPrevious user key = 2\nTemporary key = 0\n"
	if cryptIndex(out, "Current user key") != 3 || cryptIndex(out, "nope") != 0 {
		t.Error("parse")
	}
}

// The gateways' encryption keys must not leave the box: Raw is serialised into the API answer,
// so the key lives in an unexported field and only the write path reads it (B-23).
func TestLANGatewayKeyNeverInTheAnswer(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/rfd.conf": rfdConf})
	gws := r.ReadLANGateways(GatewayRF)
	if len(gws) != 1 || !gws[0].HasKey {
		t.Fatalf("%+v", gws)
	}
	if gws[0].Key() != "0011223344556677" {
		t.Errorf("the write path cannot see the key: %q", gws[0].Key())
	}
	b, err := json.Marshal(gws)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("0011223344556677")) || bytes.Contains(b, []byte("Encryption Key")) {
		t.Errorf("the key is in the answer: %s", b)
	}
	// and a rewrite that sends no key keeps the one the file has
	if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "Lan Interface", Name: "Keller", Serial: "KEQ0123456", Key: "", IP: "192.168.1.50"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(r.join("/etc/config/rfd.conf")); !strings.Contains(got, "Encryption Key = 0011223344556677") {
		t.Errorf("the stored key was lost:\n%s", got)
	}
	// and the file keeps the mode the firmware gives it: the keys are in it (B-24)
	if st, err := os.Stat(r.join("/etc/config/rfd.conf")); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("rfd.conf mode %v (%v), want 0600", st.Mode().Perm(), err)
	}
}

// task 129: an HM-CFG-USB-2's section (the radio plan adds it when the adapter is there) is no LAN
// gateway: not listed, and kept byte for byte when the list is written - on .170 the page listed
// it as a gateway row, and saving the list would have dropped it
func TestLANGatewaysKeepTheUSBAdapter(t *testing.T) {
	usb := "[Interface 1]\nType = USB Interface\nName = HM-CFG-USB\nSerial Number = JEQ0534849\nEncryption Key =\n"
	conf := "# occulite.bidcos.module=JEQ0534849\n# TCP Port for XmlRpc connections\nListen Port = 32001\n\n#[Interface 0]\n#Type = CCU2\n#ComPortFile = /dev/mmd_bidcos\n#\n" + usb
	r := rootWith(t, map[string]string{"etc/config/rfd.conf": conf})
	if gws := r.ReadLANGateways(GatewayRF); len(gws) != 0 {
		t.Fatalf("the adapter listed as a gateway: %+v", gws)
	}
	out, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "HMLGW2", Name: "Garage", Serial: "NEQ9876543", Key: "k"}})
	if err != nil || len(out) != 1 || out[0].Index != 2 {
		t.Fatalf("%v %+v", err, out)
	}
	b, _ := os.ReadFile(r.join("/etc/config/rfd.conf"))
	want := conf + "[Interface 2]\nType = HMLGW2\nName = Garage\nSerial Number = NEQ9876543\nEncryption Key = k\n\n"
	if string(b) != want {
		t.Fatalf("rfd.conf:\n%s\nwant\n%s", b, want)
	}
	if r.PendingGatewayKeyChanges() == nil {
		t.Fatal("no queued key change is an empty list, not null (the page calls includes on it)")
	}
}

// B-166: InterfacesList.xml follows hs485d.conf while the system runs, as the marker does - the
// last wired gateway removed takes BidCos-Wired out, the first one puts it in, the other entries
// stay byte for byte, and a system without the file gets none.
func TestWiredGatewayKeepsInterfacesList(t *testing.T) {
	const head = "<?xml version=\"1.0\" encoding=\"utf-8\" ?> \n<interfaces v=\"1.0\">\n" +
		"\t<ipc>\n\t \t<name>BidCos-RF</name>\n\t \t<url>xmlrpc_bin://127.0.0.1:32001</url> \n\t \t<info>BidCos-RF</info> \n\t</ipc>\n" +
		"\t<ipc>\n\t \t<name>VirtualDevices</name>\n\t \t<url>xmlrpc://127.0.0.1:39292/groups</url> \n\t \t<info>Virtual Devices</info> \n\t</ipc>\n" +
		"\t<ipc>\n\t \t<name>HmIP-RF</name>\n\t \t<url>xmlrpc://127.0.0.1:32010</url>\n\t \t<info>HmIP-RF</info>\n\t</ipc>\n"
	const wired = "\t<ipc>\n\t \t<name>BidCos-Wired</name>\n\t \t<url>xmlrpc_bin://127.0.0.1:32000</url> \n\t \t<info>BidCos-Wired</info> \n\t</ipc>\n"
	const without, with = head + "</interfaces>\n", head + wired + "</interfaces>\n"
	r := rootWith(t, map[string]string{"etc/config/InterfacesList.xml": without})
	list := r.join("/etc/config/InterfacesList.xml")
	read := func() string { b, _ := os.ReadFile(list); return string(b) }

	if _, err := r.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMWLGW", Serial: "LEQ0636432", Key: "k1"}}); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != with {
		t.Fatalf("after the first wired gateway:\n%q\nwant\n%q", got, with)
	}
	// a second write with a gateway leaves the file as it is
	if _, err := r.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMWLGW", Serial: "LEQ0636432"}}); err != nil || read() != with {
		t.Fatalf("a second write: %v\n%q", err, read())
	}
	if _, err := r.WriteLANGateways(GatewayWired, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != without {
		t.Fatalf("after the last wired gateway went:\n%q\nwant\n%q", got, without)
	}
	// the rf class never touches it
	_ = os.WriteFile(r.join("/etc/config/rfd.conf"), []byte(rfdConf), 0o600)
	if _, err := r.WriteLANGateways(GatewayRF, []LANGatewaySpec{{Type: "HMLGW2", Serial: "KEQ1065511", Key: "k2"}}); err != nil || read() != without {
		t.Fatalf("an rf gateway: %v\n%q", err, read())
	}

	// no file: none is made (the boot's radio run writes it)
	r2 := rootWith(t, map[string]string{"etc/config/.keep": ""})
	if _, err := r2.WriteLANGateways(GatewayWired, []LANGatewaySpec{{Type: "HMWLGW", Serial: "LEQ0636432", Key: "k1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r2.join("/etc/config/InterfacesList.xml")); !os.IsNotExist(err) {
		t.Fatalf("a list was made: %v", err)
	}
}
