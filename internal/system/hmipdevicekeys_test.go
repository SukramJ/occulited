package system

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// dkSvc is hmipserver's unit as the device keys see it: running since a time, restarted on Apply.
type dkSvc struct {
	mu      sync.Mutex
	since   time.Time
	running bool
	calls   []string
	restart time.Time // the start time a restart sets
}

func (s *dkSvc) List() ([]Service, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []Service{{ID: "rfd", Running: true}, {ID: "hmipserver", Running: s.running, Since: s.since.Format(time.RFC3339)}}, nil
}

func (s *dkSvc) Control(_ context.Context, id, action string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, id+" "+action)
	if id == "hmipserver" && action == "restart" {
		s.since, s.running = s.restart, true
	}
	return "", nil
}

const (
	dkPDT  = "3014F711A0000A1B2C3D4E5F" // HmIP-PDT in fakeHmIPServer's list
	dkTRV  = "3014F711A0000B1B2C3D4E5F"
	dkNew  = "3014F711A0000E0000000A05" // not paired
	dkCode = "EQ01SG" + dkPDT + "DLK0123456789ABCDEFFEDCBA9876543210"
)

func dkRig(t *testing.T, hmip *httptest.Server) (*HmIPDeviceKeys, *dkSvc, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/config/crRFD"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/config/crRFD/hmip_user.conf"), []byte("occulite.hmip.path=direct\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	svc := &dkSvc{since: now.Add(-time.Hour), running: true, restart: now.Add(2 * time.Second)}
	d := &HmIPDeviceKeys{
		Root: Root(root), Services: svc, StateDir: filepath.Join(root, "state/hmip-device-keys"),
		Now: func() time.Time { return now },
		HmIP: func() (interfaces.Interface, bool) {
			if hmip == nil {
				return interfaces.Interface{}, false
			}
			ifs := interfaces.FromList([]struct{ Name, URL string }{{"HmIP-RF", "xmlrpc://" + strings.TrimPrefix(hmip.URL, "http://")}})
			return ifs[0], true
		},
		Name: func(ref string) string {
			if ref == "HmIP-RF.000A1B2C3D4E5F" {
				return "Dimmer Hall"
			}
			return ""
		},
	}
	return d, svc, root
}

func TestDeviceKeysAddViewApplyExport(t *testing.T) {
	ctx := context.Background()
	d, svc, root := dkRig(t, (&fakeHmIPServer{state: "ok"}).serve(t))
	v := d.View(ctx)
	// the PDT, the eTRV and the DRAP; the receiver is left out
	if !v.DevicesKnown || v.Paired != 3 || v.WithKey != 0 || v.Stored != 0 || v.Pending != 0 || len(v.Rows) != 3 || v.Unread != "" {
		t.Fatalf("empty: %+v", v)
	}
	res, err := d.Add(ctx, dkCode, "", "")
	if err != nil || !res.Paired || res.SGTIN != dkPDT || res.Address != "000A1B2C3D4E5F" || res.Replaced || res.Same {
		t.Fatalf("add: %+v %v", res, err)
	}
	st, err := os.Stat(filepath.Join(root, "etc/config/crRFD/sgtin.map"))
	if err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("the map: %v %v", st, err)
	}
	conf, _ := os.ReadFile(filepath.Join(root, "etc/config/crRFD/hmip_user.conf"))
	if !strings.HasPrefix(string(conf), "occulite.hmip.path=direct\n") || radio.MappingFile(string(conf)) != radio.DeviceKeyMapFile {
		t.Fatalf("hmip_user.conf: %q", conf)
	}
	if res, err := d.Add(ctx, dkCode, "", ""); err != nil || !res.Same {
		t.Fatalf("again: %+v %v", res, err)
	}
	// a sticker of a device not paired yet: stored all the same
	if res, err := d.Add(ctx, "", "3014-F711-A000-0E00-0000-0A05", "014E2-PG2EB-SQQZX-Q5TL1-U58CHH"); err != nil || res.Paired {
		t.Fatalf("unpaired: %+v %v", res, err)
	}
	v = d.View(ctx)
	if v.Paired != 3 || v.WithKey != 1 || v.Stored != 2 || v.Pending != 2 || len(v.Rows) != 4 {
		t.Fatalf("after adding: %+v", v)
	}
	// the paired ones without a key first, then the paired with one, then the stored others
	if v.Rows[0].HasKey || v.Rows[1].HasKey || !v.Rows[2].HasKey || !v.Rows[2].Paired || v.Rows[2].Name != "Dimmer Hall" || !v.Rows[2].Pending ||
		v.Rows[3].Paired || v.Rows[3].SGTIN != dkNew || !v.Rows[3].Pending {
		t.Fatalf("rows: %+v", v.Rows)
	}
	// no answer but the export carries a key
	if b := strings.ToUpper(strings.Join([]string{v.Rows[2].SGTIN, v.Rows[2].Address, v.Rows[2].Name}, " ")); strings.Contains(b, "01234567") {
		t.Fatal("a key in the view")
	}
	if res, err := d.Add(ctx, "", dkPDT, "0123456789abcdef0123456789abcdef"); err != nil || !res.Replaced {
		t.Fatalf("replace: %+v %v", res, err)
	}
	if err := d.Delete(dkTRV); err == nil {
		t.Fatal("removed a key that was not there")
	}
	if err := d.Delete("3014-f711-a000-0e00-0000-0a05"); err != nil {
		t.Fatal(err)
	}
	if v := d.View(ctx); v.Stored != 1 || v.Pending != 2 {
		t.Fatalf("after removing: %+v", v)
	}
	// the export: every stored key with its QR text, the device's type and name
	ex := d.Export(ctx)
	if len(ex) != 1 || ex[0].Key != "0123456789ABCDEF0123456789ABCDEF" || ex[0].Payload != "EQ01SG"+dkPDT+"DLK0123456789ABCDEF0123456789ABCDEF" || ex[0].Type != "HmIP-PDT" || ex[0].Name != "Dimmer Hall" {
		t.Fatalf("export: %+v", ex)
	}
	// Apply restarts HmIP-RF, which reads the map at its start: nothing waits any more
	if err := d.Apply(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for d.View(ctx).Applying && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if v := d.View(ctx); v.Pending != 0 || v.Applying || v.Error != "" || strings.Join(svc.calls, ",") != "hmipserver restart" {
		t.Fatalf("applied: %+v %v", v, svc.calls)
	}
}

func TestDeviceKeysRefusals(t *testing.T) {
	ctx := context.Background()
	d, _, root := dkRig(t, nil)
	var dk ErrDeviceKeys
	for _, c := range [][3]string{{"WIFI:S:x;;", "", ""}, {"", "3014F711A0000A1B2C3D4E", "014E2PG2EBSQQZXQ5TL1U58CHH"}, {"", dkPDT, "014E2PG2EBSQQZXQ5TL1U58CHO"}} {
		if _, err := d.Add(ctx, c[0], c[1], c[2]); !errors.As(err, &dk) {
			t.Fatalf("%v: %v", c, err)
		}
	}
	// hmipserver silent: the stored keys are listed, not as paired devices
	if _, err := d.Add(ctx, dkCode, "", ""); err != nil {
		t.Fatal(err)
	}
	if v := d.View(ctx); v.DevicesKnown || v.Paired != 0 || v.Stored != 1 || len(v.Rows) != 1 || v.Rows[0].Paired {
		t.Fatalf("silent: %+v", v)
	}
	// KeyServer.Mode=KEYSERVER: HmIP-RF would not read the map, and the page says so
	conf := filepath.Join(root, "etc/config/crRFD/hmip_user.conf")
	b, _ := os.ReadFile(conf)
	if err := os.WriteFile(conf, []byte(string(b)+"KeyServer.Mode=KEYSERVER\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := d.View(ctx); v.Unread == "" {
		t.Fatalf("unread: %+v", v)
	}
	d.Busy = func() bool { return true }
	if err := d.Apply(); !errors.As(err, &dk) {
		t.Fatalf("busy: %v", err)
	}
	// a hand-edited line that is not a key is counted, not answered
	if err := os.WriteFile(filepath.Join(root, "etc/config/crRFD/sgtin.map"), []byte(dkPDT+"=0123456789ABCDEFFEDCBA9876543210\nnonsense\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if v := d.View(ctx); v.BadLines != 1 || v.Stored != 1 {
		t.Fatalf("bad lines: %+v", v)
	}
}

// Task 201: whether a declined device is in by now. A failed listing says nothing (ok false), so
// the warning that uses this never clears on a silent hmipserver.
func TestPairedSGTINs(t *testing.T) {
	ctx := context.Background()
	d, _, _ := dkRig(t, (&fakeHmIPServer{state: "ok"}).serve(t))
	got, ok := d.PairedSGTINs(ctx, []string{dkPDT, dkNew, strings.ToLower(dkTRV)})
	if !ok {
		t.Fatal("the listing worked, ok must be true")
	}
	if !got[dkPDT] || got[dkNew] || !got[strings.ToLower(dkTRV)] {
		t.Errorf("paired: %+v", got)
	}
	silent, _, _ := dkRig(t, nil)
	if got, ok := silent.PairedSGTINs(ctx, []string{dkPDT}); ok || got != nil {
		t.Errorf("hmipserver silent: %+v %v", got, ok)
	}
}
