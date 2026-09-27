package system

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// rfdInterfacesStub answers listBidcosInterfaces with rfd's local CCU2 entry under the serial rfd
// runs with, connected or not.
func rfdInterfacesStub(t *testing.T, serial string, connected bool) *httptest.Server {
	t.Helper()
	return rfdInterfaceStub(t, "CCU2", serial, connected)
}

// rfdInterfaceStub answers listBidcosInterfaces with one entry of the given type: "CCU2" for a
// module behind multimacd, "USB Interface" for the HM-CFG-USB-2, which rfd names by the adapter's
// own serial.
func rfdInterfaceStub(t *testing.T, typ, serial string, connected bool) *httptest.Server {
	t.Helper()
	conn := "0"
	if connected {
		conn = "1"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param><value><array><data><value><struct>
<member><name>ADDRESS</name><value>` + serial + `</value></member>
<member><name>DESCRIPTION</name><value>` + typ + ` ` + serial + `</value></member>
<member><name>CONNECTED</name><value><boolean>` + conn + `</boolean></value></member>
<member><name>DEFAULT</name><value><boolean>1</boolean></value></member>
<member><name>TYPE</name><value>` + typ + `</value></member>
<member><name>FIRMWARE_VERSION</name><value>2.8.6</value></member>
<member><name>DUTY_CYCLE</name><value><i4>1</i4></value></member>
</struct></value></data></array></value></param></params></methodResponse>`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// openccu-lite task 275: the record of an import and what the page reads from it afterwards - the
// HmIP identity's move from hmipserver's files and the marker, the BidCos identity from rfd's entry
func TestImportRecordOutcome(t *testing.T) {
	root := t.TempDir()
	plan := radio.Plan{HmIP: &radio.Role{Hardware: "RPI-RF-MOD", Serial: "0000000A03", SGTIN: "3014f711a0001f0000000a03"}, HmRF: &radio.Role{Hardware: "RPI-RF-MOD", Serial: "0000000A03"}}
	hasPlan := true
	var lines []string
	rfd := rfdInterfacesStub(t, "1709ADFA00", true)
	rec0 := &ImportRecord{Path: filepath.Join(root, "state/devices-import.json"), Root: Root(root),
		Plan:    func() (radio.Plan, bool) { return plan, hasPlan },
		Journal: func(context.Context, time.Time) []string { return lines },
		Interfaces: func() []interfaces.Interface {
			return interfaces.FromList([]struct{ Name, URL string }{{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(rfd.URL, "http://")}, {"HmIP-RF", "xmlrpc://127.0.0.1:1"}})
		}, InterfacesTimeout: 2 * time.Second}

	// no record: nothing; a nil record: nothing either
	if rec0.Read() != nil || (*ImportRecord)(nil).Read() != nil {
		t.Fatal("a record out of nowhere")
	}
	if err := (*ImportRecord)(nil).Clear(); err != nil {
		t.Fatal(err)
	}

	// the record of a backup whose identity belongs to another module, with a non-default key,
	// onto a system with a key store of its own
	b := RadioBackup{KeyIndex: 1, Version: "3.89.11"}
	b.HmIP.IdentitySGTIN, b.HmIP.Devices, b.HmIP.LocalKey = "3014F711A0001F5F000000AF", 2, false
	b.BidCosRF.Address, b.BidCosRF.Serial, b.BidCosRF.Devices, b.BidCosRF.HasKey = "0xFF5678", "1709ADFA00", 1, true
	at := time.Date(2026, 9, 27, 18, 0, 0, 0, time.UTC)
	rec := NewImportedRadio(at, "restore-ccu.sbk", b, plan, hasPlan, true)
	if !rec.HmIP.ModuleChanged || rec.HmIP.FromSGTIN != "3014F711A0001F5F000000AF" || rec.HmIP.ToSGTIN != "3014F711A0001F0000000A03" || rec.HmIP.ToModule != "RPI-RF-MOD 0000000A03" || !rec.BidCosRF.NonDefaultKey || rec.BidCosRF.KeyIndex != 1 || !rec.BidCosRF.TargetKeyReplaced || rec.BidCosRF.Module != "RPI-RF-MOD 0000000A03" || rec.HmIP.Devices != 2 {
		t.Fatalf("record: %+v", rec)
	}
	if !strings.Contains(rec.String(), "3014F711A0001F5F000000AF -> 3014F711A0001F0000000A03") {
		t.Errorf("summary: %s", rec.String())
	}
	if err := rec0.Write(rec); err != nil {
		t.Fatal(err)
	}
	got := rec0.Read()
	if got == nil || !got.At.Equal(at) || got.HmIP.FromSGTIN != rec.HmIP.FromSGTIN || got.File != "restore-ccu.sbk" {
		t.Fatalf("read back: %+v", got)
	}
	if st, _ := os.Stat(rec0.Path); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}

	// the same module: nothing to exchange
	bSame := b
	bSame.HmIP.IdentitySGTIN = plan.HmIP.SGTIN
	same := NewImportedRadio(at, "x", bSame, plan, true, false)
	if same.HmIP.ModuleChanged {
		t.Error("the same module is a change")
	}
	if out := rec0.Outcome(context.Background(), same); out.HmIP.State != ImportNoExchange {
		t.Errorf("same module: %+v", out.HmIP)
	}
	// no HmIP identity in the backup: nothing to exchange either
	var noHmIP RadioBackup
	noHmIP.BidCosRF.Serial = "1709ADFA00"
	if out := rec0.Outcome(context.Background(), NewImportedRadio(at, "x", noHmIP, plan, true, false)); out.HmIP.State != ImportNoExchange {
		t.Errorf("no identity: %+v", out.HmIP)
	}

	// pending: the previous module's identity file is still in hmipserver's data directory
	data := filepath.Join(root, crRFDDataDir)
	_ = os.MkdirAll(data, 0o755)
	write := func(name string) { _ = os.WriteFile(filepath.Join(data, name), []byte("x"), 0o600) }
	write(rec.HmIP.FromSGTIN + ".ap")
	out := rec0.Outcome(context.Background(), rec)
	if out.HmIP.State != ImportPending || out.HmIP.ModuleNow != rec.HmIP.ToSGTIN || out.HmIP.Line != "" {
		t.Errorf("pending: %+v", out.HmIP)
	}
	// the BidCos side: rfd runs with the imported serial, connected
	if out.BidCosRF.Took == nil || !*out.BidCosRF.Took || !out.BidCosRF.Connected || out.BidCosRF.Interface != "CCU2 1709ADFA00" || out.BidCosRF.Error != "" {
		t.Errorf("bidcos: %+v", out.BidCosRF)
	}

	// done: hmipserver wrote this module's identity and removed the previous one's; its own line
	// in the journal
	_ = os.Remove(filepath.Join(data, rec.HmIP.FromSGTIN+".ap"))
	write(rec.HmIP.ToSGTIN + ".ap")
	lines = []string{"something else", "Adapter exchange successful."}
	out = rec0.Outcome(context.Background(), rec)
	if out.HmIP.State != ImportDone || out.HmIP.Line != "Adapter exchange successful." {
		t.Errorf("done: %+v", out.HmIP)
	}

	// rejected: the marker, written after the import
	f := radio.HmIPFatal{Code: "adapter-exchange-rejected", Line: "Adapter exchange was rejected by key server.", Cause: radio.CauseUnreachable, At: at.Add(time.Minute)}
	fb, _ := json.Marshal(f)
	_ = os.MkdirAll(filepath.Dir(radio.FatalPath(root)), 0o755)
	_ = os.WriteFile(radio.FatalPath(root), fb, 0o644)
	out = rec0.Outcome(context.Background(), rec)
	if out.HmIP.State != ImportRejected || out.HmIP.Cause != radio.CauseUnreachable {
		t.Errorf("rejected: %+v", out.HmIP)
	}
	// a marker older than the import is not this import's
	f.At = at.Add(-time.Hour)
	fb, _ = json.Marshal(f)
	_ = os.WriteFile(radio.FatalPath(root), fb, 0o644)
	if out = rec0.Outcome(context.Background(), rec); out.HmIP.State != ImportDone {
		t.Errorf("old marker: %+v", out.HmIP)
	}
	_ = os.Remove(radio.FatalPath(root))

	// unknown: neither identity file
	_ = os.Remove(filepath.Join(data, rec.HmIP.ToSGTIN+".ap"))
	if out = rec0.Outcome(context.Background(), rec); out.HmIP.State != ImportUnknown {
		t.Errorf("unknown: %+v", out.HmIP)
	}
	// no module now
	hasPlan = false
	if out = rec0.Outcome(context.Background(), rec); out.HmIP.State != ImportNoModule || out.HmIP.ModuleNow != "" {
		t.Errorf("no module: %+v", out.HmIP)
	}
	hasPlan = true

	// the BidCos side when rfd runs with another serial, and when it does not answer
	other := rfdInterfacesStub(t, "0000000A01", false)
	rec0.Interfaces = func() []interfaces.Interface {
		return interfaces.FromList([]struct{ Name, URL string }{{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(other.URL, "http://")}})
	}
	out = rec0.Outcome(context.Background(), rec)
	if out.BidCosRF.Took == nil || *out.BidCosRF.Took || out.BidCosRF.Interface != "CCU2 0000000A01" || out.BidCosRF.Connected {
		t.Errorf("other serial: %+v", out.BidCosRF)
	}
	other.Close()
	rec0.InterfacesTimeout = 300 * time.Millisecond
	out = rec0.Outcome(context.Background(), rec)
	if out.BidCosRF.Took != nil || out.BidCosRF.Error == "" {
		t.Errorf("rfd silent: %+v", out.BidCosRF)
	}

	// dismissed
	if err := rec0.Clear(); err != nil || rec0.Read() != nil {
		t.Fatalf("clear: %v", err)
	}
	if err := rec0.Clear(); err != nil {
		t.Fatal("a second clear fails")
	}
}

// openccu-lite task 275, the lab run onto an HmIP-RFUSB-TK and an HM-CFG-USB-2 (2026-09-27): the
// page said "unknown" for HmIP and "not the imported identity" for BidCos although both had taken.
// hmipserver's data directory is closed to occulited (B-253), so the identity files are seen
// through the helper; and rfd names the HM-CFG-USB-2's entry by the adapter's serial, so there the
// address rfd runs with (the plan's active one, from the ids file) is what says it took.
func TestImportOutcomeUSBAdapterAndClosedData(t *testing.T) {
	root := t.TempDir()
	plan := radio.Plan{HmIP: &radio.Role{Hardware: "HMIP-RFUSB-TK", Serial: "0000000B01", SGTIN: "3014F5AC940004000000B01"},
		HmRF: &radio.Role{Hardware: "HM-CFG-USB-2", Serial: "JEQ0000001"}, HmRFAddressActive: "0xFF5678"}
	b := RadioBackup{KeyIndex: 1}
	b.HmIP.IdentitySGTIN, b.HmIP.Devices = "3014F711A0001F5F000000AF", 2
	b.BidCosRF.Address, b.BidCosRF.Serial, b.BidCosRF.Devices = "0xFF5678", "5F000000AF", 1
	at := time.Date(2026, 9, 27, 19, 38, 0, 0, time.UTC)
	rec := NewImportedRadio(at, "restore-x.sbk", b, plan, true, false)

	usb := rfdInterfaceStub(t, "USB Interface", "JEQ0000001", true)
	r := &ImportRecord{Root: Root(root), Plan: func() (radio.Plan, bool) { return plan, true },
		Journal: func(context.Context, time.Time) []string { return nil },
		Interfaces: func() []interfaces.Interface {
			return interfaces.FromList([]struct{ Name, URL string }{{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(usb.URL, "http://")}})
		}, InterfacesTimeout: 2 * time.Second}

	data := filepath.Join(root, crRFDDataDir)
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	// hmipserver took the identity over: this module's file (its own name case), the previous one gone
	if err := os.WriteFile(filepath.Join(data, "3014F5AC940004000000B01.ap"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := r.Outcome(context.Background(), rec)
	if out.HmIP.State != ImportDone {
		t.Errorf("hmip: %+v", out.HmIP)
	}
	if out.BidCosRF.Took == nil || !*out.BidCosRF.Took || !out.BidCosRF.Connected || out.BidCosRF.Interface != "USB Interface JEQ0000001" {
		t.Errorf("bidcos on the USB adapter: %+v", out.BidCosRF)
	}

	// rfd runs with another address than the imported one: not taken
	plan.HmRFAddressActive = "0xABCDEF"
	if out = r.Outcome(context.Background(), rec); out.BidCosRF.Took == nil || *out.BidCosRF.Took {
		t.Errorf("another address: %+v", out.BidCosRF)
	}
	plan.HmRFAddressActive = "0xff5678"

	// an entry that is neither the module nor a CCU2 one (a LAN gateway beside it): no local entry
	other := rfdInterfaceStub(t, "Lan Interface", "KEQ0000001", true)
	r.Interfaces = func() []interfaces.Interface {
		return interfaces.FromList([]struct{ Name, URL string }{{"BidCos-RF", "xmlrpc://" + strings.TrimPrefix(other.URL, "http://")}})
	}
	if out = r.Outcome(context.Background(), rec); out.BidCosRF.Took == nil || *out.BidCosRF.Took || out.BidCosRF.Interface != "" {
		t.Errorf("no local entry: %+v", out.BidCosRF)
	}

	// the data directory closed to occulited: without the helper nothing is seen, with it the move
	if os.Geteuid() == 0 {
		return
	}
	if err := os.Chmod(data, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(data, 0o755) })
	old := Priv
	t.Cleanup(func() { Priv = old })
	Priv = priv.Local{}
	if out = r.Outcome(context.Background(), rec); out.HmIP.State != ImportUnknown {
		t.Errorf("closed without the helper: %+v", out.HmIP)
	}
	Priv = rootReader{}
	if out = r.Outcome(context.Background(), rec); out.HmIP.State != ImportDone {
		t.Errorf("closed, through the helper: %+v", out.HmIP)
	}
}
