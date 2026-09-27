package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// task 94: occulited starts right after the network now - before the radio detection writes
// /var/hm_mode (seconds later on a Pi 3, up to about 20 s on a Pi 4 with an HmIP-RFUSB) and before
// occu-init-hs485d rewrites /etc/config/InterfacesList.xml. Nothing may read those once and keep
// it: a file that appears or changes after the API was built shows at the very next request.
func TestRadioFollowsLateHMMode(t *testing.T) {
	r := fakeRoot(t)
	hmMode := filepath.Join(string(r), "var/hm_mode")
	ifList := filepath.Join(string(r), "etc/config/InterfacesList.xml")
	if err := os.Remove(hmMode); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	radio := func() map[string]any {
		t.Helper()
		st, out, _ := do(t, srv, "GET", "/api/system/v1/radio", "", nil)
		if st != 200 {
			t.Fatalf("radio: %d %v", st, out)
		}
		return out
	}
	statusMode := func() any {
		t.Helper()
		st, out, _ := do(t, srv, "GET", "/api/system/v1/status", "", nil)
		if st != 200 {
			t.Fatalf("status: %d %v", st, out)
		}
		return out["hm_mode"]
	}
	names := func(out map[string]any) []string {
		var n []string
		for _, i := range out["interfaces"].([]any) {
			n = append(n, i.(map[string]any)["name"].(string))
		}
		return n
	}

	// the API is up, the detection has not run yet
	if out := radio(); out["mode"] != "" || len(out["modules"].([]any)) != 0 || len(out["interfaces"].([]any)) != 0 {
		t.Fatalf("before the detection: %v", out)
	}
	if m := statusMode(); m != "" {
		t.Fatalf("status before the detection: %v", m)
	}

	// the detection writes /var/hm_mode, the boot's InterfacesList.xml is there with BidCos-RF only
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(hmMode, "HM_MODE='NORMAL'\nHM_HOST='rpi4'\nHM_HMRF_DEV='HMIP-RFUSB'\nHM_HMRF_DEVNODE='/dev/raw-uart'\nHM_HMIP_DEV='HMIP-RFUSB'\nHM_HMIP_DEVNODE='/dev/raw-uart'\nHM_HMIP_SGTIN='3014F711A000040000000A02'\n")
	ipc := func(name, url string) string {
		return "<ipc><name>" + name + "</name><url>" + url + "</url><info>" + name + "</info></ipc>"
	}
	write(ifList, "<interfaces>"+ipc("BidCos-RF", "xmlrpc_bin://127.0.0.1:32001")+"</interfaces>")
	out := radio()
	if out["mode"] != "NORMAL" || out["host"] != "rpi4" || len(out["modules"].([]any)) != 2 {
		t.Errorf("after the detection: %v", out)
	}
	if n := names(out); len(n) != 1 || n[0] != "BidCos-RF" {
		t.Errorf("interfaces %v", n)
	}
	if m := statusMode(); m != "NORMAL" {
		t.Errorf("status after the detection: %v", m)
	}

	// occu-init-hs485d rewrites the list (HmIP and wired now), and a second detection - a flash,
	// a module plugged in - rewrites /var/hm_mode: both show at once
	write(ifList, "<interfaces>"+ipc("BidCos-RF", "xmlrpc_bin://127.0.0.1:32001")+ipc("HmIP-RF", "xmlrpc://127.0.0.1:32010")+ipc("BidCos-Wired", "xmlrpc_bin://127.0.0.1:32000")+"</interfaces>")
	write(hmMode, "HM_MODE='HmIP-RFUSB'\nHM_HMIP_DEV='HMIP-RFUSB'\n")
	out = radio()
	if n := names(out); len(n) != 3 || n[1] != "HmIP-RF" || n[2] != "BidCos-Wired" {
		t.Errorf("the rewritten list: %v", n)
	}
	if out["mode"] != "HmIP-RFUSB" || len(out["modules"].([]any)) != 1 {
		t.Errorf("the rewritten hm_mode: %v", out)
	}
	if m := statusMode(); m != "HmIP-RFUSB" {
		t.Errorf("status after the rewrite: %v", m)
	}
}
