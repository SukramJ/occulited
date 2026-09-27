package system

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
)

// listDevicesServer answers listDevices with the given (address, type, parent) rows, as rfd and
// hmipserver do: the central and the module among the devices, every channel with a parent.
func listDevicesServer(t *testing.T, rows [][3]string) *httptest.Server {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><methodResponse><params><param><value><array><data>`)
	for _, r := range rows {
		fmt.Fprintf(&b, `<value><struct><member><name>ADDRESS</name><value><string>%s</string></value></member><member><name>TYPE</name><value><string>%s</string></value></member><member><name>PARENT</name><value><string>%s</string></value></member></struct></value>`, r[0], r[1], r[2])
	}
	b.WriteString(`</data></array></value></param></params></methodResponse>`)
	body := b.String()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/xml")
		if strings.Contains(string(raw), "<methodName>listDevices</methodName>") {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param><value><string></string></value></param></params></methodResponse>`))
	}))
}

func xmlrpcURL(srv *httptest.Server) string {
	return "xmlrpc://" + strings.TrimPrefix(srv.URL, "http://")
}

// The count per interface: the CCU's own entries and the channels are not devices, an interface
// that does not answer is unknown and says why, and one nobody listed is unknown without a word.
func TestCountPaired(t *testing.T) {
	rfd := listDevicesServer(t, [][3]string{
		{"BidCoS-RF", "HM-RCV-50 BidCoS-RF", ""}, {"BidCoS-RF:1", "VIRTUAL_KEY", "BidCoS-RF"},
		{"JEQ9000001", "HM-CC-TC", ""}, {"JEQ9000001:1", "WEATHER", "JEQ9000001"},
		{"KEQ9000003", "HM-Sec-SC", ""}, {"KEQ9000003:1", "SHUTTER_CONTACT", "KEQ9000003"},
	})
	defer rfd.Close()
	hmip := listDevicesServer(t, [][3]string{
		{"3014F711A000040000000A01", "HmIP-RFUSB", ""}, {"3014F711A000040000000A01:0", "MAINTENANCE", "3014F711A000040000000A01"},
		{"000A1B2C3D4E5F", "HmIP-PDT", ""}, {"000A1B2C3D4E5F:0", "MAINTENANCE", "000A1B2C3D4E5F"},
		{"00160000000A12", "HmIPW-DRS8", ""},
		{"00040000000A01", "HmIP-RCV-50", ""},
	})
	defer hmip.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close() // the port answers nothing: rfd stopped
	old := PairedDevicesTimeout
	PairedDevicesTimeout = 3 * time.Second
	defer func() { PairedDevicesTimeout = old }()

	ifs := interfaces.FromList([]struct{ Name, URL string }{
		{"BidCos-RF", xmlrpcURL(rfd)}, {"HmIP-RF", xmlrpcURL(hmip)}, {"BidCos-Wired", xmlrpcURL(dead)},
	})
	got := CountPaired(context.Background(), ifs)
	if len(got) != 3 {
		t.Fatalf("interfaces: %v", got)
	}
	if p := got["BidCos-RF"]; !p.Known || p.Devices != 2 || p.Error != "" {
		t.Errorf("BidCos-RF: %+v, want 2 known", p)
	}
	if p := got["HmIP-RF"]; !p.Known || p.Devices != 2 || p.Error != "" {
		t.Errorf("HmIP-RF: %+v, want 2 known (the stick and the receiver are the CCU's own)", p)
	}
	if p := got["BidCos-Wired"]; p.Known || p.Devices != 0 || p.Error == "" {
		t.Errorf("a dead interface: %+v, want unknown with a reason", p)
	}
	if got := CountPaired(context.Background(), nil); len(got) != 0 {
		t.Errorf("no interfaces: %v", got)
	}
}

// The marker under the fake root: set, seen, taken away; refused while an update is staged.
func TestFactoryResetMarker(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "usr/local/tmp"), 0o755)
	if root.FactoryResetArmed() {
		t.Fatal("armed before anything happened")
	}
	if err := root.ArmFactoryReset(); err != nil {
		t.Fatal(err)
	}
	if !root.FactoryResetArmed() {
		t.Fatal("not armed after ArmFactoryReset")
	}
	if _, err := os.Stat(filepath.Join(dir, "usr/local/.doFactoryReset")); err != nil {
		t.Fatalf("the marker is not the firmware's: %v", err)
	}
	if err := root.DisarmFactoryReset(); err != nil {
		t.Fatal(err)
	}
	if root.FactoryResetArmed() {
		t.Fatal("still armed after DisarmFactoryReset")
	}
	// a staged update: the recovery system would install it at that boot
	_ = os.WriteFile(filepath.Join(dir, "usr/local/tmp/u.zip"), []byte("zip"), 0o644)
	_ = os.Symlink(filepath.Join(dir, "usr/local/tmp/u.zip"), filepath.Join(dir, "usr/local/.firmwareUpdate"))
	if err := root.ArmFactoryReset(); err != ErrUpdateStaged {
		t.Fatalf("with an update staged: %v, want ErrUpdateStaged", err)
	}
	if root.FactoryResetArmed() {
		t.Fatal("armed although refused")
	}
}

// The local key's presence, and no more: a network key in hmip_user.conf says yes, the shipped
// template and a missing file say no.
func TestHmIPLocalKeyEnabled(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	if root.HmIPLocalKeyEnabled() {
		t.Fatal("no file, yet enabled")
	}
	conf := filepath.Join(dir, "etc/config/crRFD/hmip_user.conf")
	_ = os.MkdirAll(filepath.Dir(conf), 0o755)
	_ = os.WriteFile(conf, []byte("KeyServer.Mode=KEYSERVER_LOCAL\n"), 0o644)
	if root.HmIPLocalKeyEnabled() {
		t.Fatal("a mode line alone is not a key")
	}
	_ = os.WriteFile(conf, []byte("KeyServer.Mode=LOCAL\nNetwork.Key=0123456789ABCDEF0123456789ABCDEF\n"), 0o644)
	if !root.HmIPLocalKeyEnabled() {
		t.Fatal("a network key is set and not seen")
	}
}

// openccu-lite task 275: the pairings on the disk, for a system whose daemons are down
func TestPairedFromFiles(t *testing.T) {
	root := t.TempDir()
	r := Root(root)
	if got := r.PairedFromFiles(); len(got) != 0 {
		t.Fatalf("empty root: %v", got)
	}
	w := func(p string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		_ = os.WriteFile(filepath.Join(root, p), []byte("x"), 0o644)
	}
	w("etc/config/rfd/JEQ0000001.dev")
	w("etc/config/rfd/JEQ0000001.meta")
	w("etc/config/rfd/KEQ0000002.dev")
	w("etc/config/crRFD/data/3014F711A000010000000A10.dev")
	w("etc/config/crRFD/data/3014F711A0001F0000000A03.ap")
	w("etc/config/crRFD/data/linkData.conf")
	w("etc/config/crRFD/data/notasgtin.dev")
	w("etc/config/hs485d/LEQ0000001.dev")
	got := r.PairedFromFiles()
	if got["BidCos-RF"] != 2 || got["HmIP-RF"] != 1 || got["BidCos-Wired"] != 1 || len(got) != 3 {
		t.Errorf("counts: %v", got)
	}
}
