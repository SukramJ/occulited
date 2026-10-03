package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/radio"
	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite B-289: the refusal of an HmIP move onto a module whose firmware cannot take the
// network key is 422 hmip-firmware with the module, its version and the minimum - wrapped or not.
func TestRadioConnErrorHmIPFirmware(t *testing.T) {
	refused := system.HmIPFirmwareRefusal("3014f711a000040000000a01", "1.8.3", false)
	for _, err := range []error{refused, fmt.Errorf("change: %w", refused)} {
		w := httptest.NewRecorder()
		radioConnError(w, err)
		var e apiError
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != 422 || e.Error != "hmip-firmware" || e.Detail["module"] != "3014F711A000040000000A01" || e.Detail["version"] != "1.8.3" || e.Detail["minimum"] != "2.8.0" {
			t.Errorf("%v: %d %+v", err, w.Code, e)
		}
	}
}

// openccu-lite B-289: the newest exchange in the local record moved the network onto the module in
// use without the network key - an error on the Status page until HmIP-RF runs on another module.
func TestHmIPLocalSwapWarning(t *testing.T) {
	root := t.TempDir()
	api := &SystemAPI{Root: system.Root(root)}
	const stick, mod = "3014F711A000040000000A01", "3014F711A0001F0000000A03"
	if ws, ok := api.hmipLocalSwapWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("no record: %v %v", ws, ok)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(sgtin string) {
		b, _ := json.Marshal(radio.Plan{HmIP: &radio.Role{SGTIN: sgtin}})
		write("run/occulite/radio/plan.json", string(b))
	}
	swap := `{"at":"2026-10-01T16:36:00Z","from":"` + mod + `","to":"` + stick + `","to_version":"1.8.3","mode":"local-swap","outcome":"rejected","cause":"adapter-version"}` + "\n"
	write(radio.ExchangeRecordFile, swap)
	plan(stick)
	ws, ok := api.hmipLocalSwapWarning(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "hmip-local-swap" || ws[0].Variant != stick+"@2026-10-01T16:36:00Z" || ws[0].Params["version"] != "1.8.3" || ws[0].Params["from"] != mod || ws[0].Params["minimum"] != "2.8.0" {
		t.Fatalf("local swap: %+v %v", ws, ok)
	}
	// back on the previous module (its kept identity): gone
	plan(mod)
	if ws, _ := api.hmipLocalSwapWarning(context.Background()); len(ws) != 0 {
		t.Fatalf("on the other module: %+v", ws)
	}
	// a later accepted exchange is the newest word
	plan(stick)
	write(radio.ExchangeRecordFile, swap+`{"at":"2026-10-02T10:00:00Z","from":"`+mod+`","to":"`+stick+`","mode":"local-key","outcome":"accepted"}`+"\n")
	if ws, _ := api.hmipLocalSwapWarning(context.Background()); len(ws) != 0 {
		t.Fatalf("after an accepted exchange: %+v", ws)
	}
}

// openccu-lite task 318 (D-120): HmIP-RF is kept on the module holding the network, and that module
// is missing - an error on the Status page until it is back or HmIP-RF is moved by choice.
func TestHmIPModuleMissingWarning(t *testing.T) {
	root := t.TempDir()
	api := &SystemAPI{Root: system.Root(root)}
	const mod = "3014F711A0001F0000000A03"
	plan := func(p radio.Plan) {
		t.Helper()
		b, _ := json.Marshal(p)
		_ = os.MkdirAll(filepath.Join(root, "run/occulite/radio"), 0o755)
		if err := os.WriteFile(filepath.Join(root, "run/occulite/radio/plan.json"), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if ws, ok := api.hmipModuleMissingWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("no plan: %v %v", ws, ok)
	}
	plan(radio.Plan{HmIPPin: mod, MissingHmIP: mod})
	ws, ok := api.hmipModuleMissingWarning(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "hmip-module-missing" || ws[0].Variant != mod || ws[0].Params["module"] != mod || ws[0].Href != "/system/interfaces#connections" {
		t.Fatalf("missing: %+v %v", ws, ok)
	}
	for name, p := range map[string]radio.Plan{
		"back":                      {HmIPPin: mod, HmIP: &radio.Role{SGTIN: mod}},
		"a chosen module missing":   {MissingHmIP: "0000000A01"},
		"moved by choice elsewhere": {HmIP: &radio.Role{SGTIN: "3014F711A000040000000A01"}},
	} {
		plan(p)
		if ws, _ := api.hmipModuleMissingWarning(context.Background()); len(ws) != 0 {
			t.Errorf("%s: %+v", name, ws)
		}
	}
}

// openccu-lite B-301: a move blocked by a kept local-key snapshot is 409 snapshot-blocked with the
// module, wrapped or not.
func TestRadioConnErrorSnapshotBlocked(t *testing.T) {
	for _, err := range []error{&system.SnapshotBlocked{SGTIN: "3014F711A0001F0000000A03"}, fmt.Errorf("change: %w", &system.SnapshotBlocked{SGTIN: "3014F711A0001F0000000A03"})} {
		w := httptest.NewRecorder()
		radioConnError(w, err)
		var e apiError
		_ = json.Unmarshal(w.Body.Bytes(), &e)
		if w.Code != 409 || e.Error != "snapshot-blocked" || e.Detail["sgtin"] != "3014F711A0001F0000000A03" {
			t.Errorf("%v: %d %+v", err, w.Code, e)
		}
	}
}
