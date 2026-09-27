package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/hbrfeth"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// task 218: the setting, the board's view, the refusal of a board another system uses, the find
// with the configured board, the warning.
func TestHBRFETHRoutes(t *testing.T) {
	root := fakeRoot(t)
	connectedTo := "192.0.2.99"
	board := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"sysInfo":{"serial":"ABCDEF1234","currentVersion":"1.3.0","rawUartRemoteAddress":"` + connectedTo + `","radioModuleType":"HM-MOD-RPI-PCB","radioModuleSerial":"MEQ9000005","radioModuleBidCosRadioMAC":"0x3D0A01","radioModuleHmIPRadioMAC":"0x000000","radioModuleSGTIN":"x"}}`))
	}))
	defer board.Close()
	kicks := 0
	watch := &radio.HBRFETHWatch{Root: string(root), Start: func(context.Context) error { kicks++; return nil }}
	api := &SystemAPI{Root: root, HBRFETH: watch, HBRFETHClient: &hbrfeth.Client{MDNSAddr: "127.0.0.1:9", BaseURL: func(string) string { return board.URL }},
		OwnAddresses: func() []string { return []string{"192.0.2.1"} }}
	mux := http.NewServeMux()
	api.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	st, out, raw := do(t, srv, "GET", "/api/system/v1/radio/hb-rf-eth", "", nil)
	if st != 200 || out["address"] != "" || out["board"] != nil || out["retrying"] != false {
		t.Fatalf("none: %d %s", st, raw)
	}
	if st, _, raw := do(t, srv, "PUT", "/api/system/v1/radio/hb-rf-eth", `{"address":"hb-rf-eth.local"}`, nil); st != 422 || !strings.Contains(raw, "IPv4 only") {
		t.Fatalf("a name: %d %s", st, raw)
	}
	// connected to another system: refused, then taken
	st, out, raw = do(t, srv, "PUT", "/api/system/v1/radio/hb-rf-eth", `{"address":"192.0.2.50"}`, nil)
	if st != 409 || out["error"] != "in-use" || out["board"].(map[string]any)["connected_to"] != "192.0.2.99" {
		t.Fatalf("in use: %d %s", st, raw)
	}
	if _, err := os.Stat(filepath.Join(string(root), radio.HBRFETHFile)); err == nil {
		t.Fatal("a refused board was written")
	}
	st, out, raw = do(t, srv, "PUT", "/api/system/v1/radio/hb-rf-eth", `{"address":"192.0.2.50","take":true}`, nil)
	if st != 200 || out["address"] != "192.0.2.50" || out["retrying"] != true || out["board"].(map[string]any)["firmware"] != "1.3.0" {
		t.Fatalf("taken: %d %s", st, raw)
	}
	if b, _ := os.ReadFile(filepath.Join(string(root), radio.HBRFETHFile)); string(b) != "192.0.2.50\n" {
		t.Fatalf("file %q", b)
	}
	// the warning while it is not connected
	if ws, ok := api.hbRFETHWarning(context.Background()); !ok || len(ws) != 1 || ws[0].ID != "hb-rf-eth" || ws[0].Variant != "192.0.2.50" {
		t.Fatalf("warning %+v", ws)
	}
	// connected to this system: the state says so, no warning
	connectedTo = "192.0.2.1"
	_ = os.MkdirAll(filepath.Join(string(root), filepath.Dir(radio.HBRFETHConnectedFile)), 0o755)
	_ = os.WriteFile(filepath.Join(string(root), radio.HBRFETHConnectedFile), []byte("1\n"), 0o644)
	st, out, raw = do(t, srv, "GET", "/api/system/v1/radio/hb-rf-eth", "", nil)
	if st != 200 || out["connected"] != true || out["board"].(map[string]any)["state"] != "this" {
		t.Fatalf("connected: %d %s", st, raw)
	}
	if ws, _ := api.hbRFETHWarning(context.Background()); len(ws) != 0 {
		t.Fatalf("warning while connected %+v", ws)
	}
	// find: nothing answers mDNS here, the configured board is listed
	st, out, raw = do(t, srv, "POST", "/api/system/v1/radio/hb-rf-eth/find", "", nil)
	if st != 200 || len(out["boards"].([]any)) != 1 || out["boards"].([]any)[0].(map[string]any)["state"] != "this" {
		t.Fatalf("find: %d %s", st, raw)
	}
	// removed
	if st, out, raw := do(t, srv, "PUT", "/api/system/v1/radio/hb-rf-eth", `{"address":""}`, nil); st != 200 || out["address"] != "" {
		t.Fatalf("remove: %d %s", st, raw)
	}
	if _, err := os.Stat(filepath.Join(string(root), radio.HBRFETHFile)); err == nil {
		t.Fatal("the file stayed")
	}
}
