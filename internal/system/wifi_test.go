package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/wifi"
)

type wifiRec struct {
	mu    sync.Mutex
	calls []string
}

func (r *wifiRec) run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil, nil
}

func (r *wifiRec) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return ""
	}
	return r.calls[len(r.calls)-1]
}

func wifiRoot(t *testing.T, onboard bool) string {
	t.Helper()
	root := t.TempDir()
	if onboard {
		p := filepath.Join(root, "/sys/bus/sdio/devices/mmc1:0001:1/uevent")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("OF_NAME=wifi\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestWiFiViewStates(t *testing.T) {
	if v := (&WiFi{Root: Root(wifiRoot(t, false))}).View(); v.State != "no-chip" || len(v.Chips) != 0 {
		t.Fatalf("no chip: %+v", v)
	}
	w := &WiFi{Root: Root(wifiRoot(t, true))}
	v := w.View()
	if v.State != "off" || len(v.Chips) != 1 || v.Chips[0].Iface != "wlan0" || v.Chips[0].Present || v.Chips[0].Kind != "onboard" {
		t.Fatalf("off: %+v", v)
	}
	// on, but no supplicant socket yet: starting
	s := wifi.Defaults()
	s.Enabled = true
	if err := os.MkdirAll(filepath.Join(string(w.Root), "etc/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(string(w.Root), wifi.SettingsFile), []byte(s.Format()), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := w.View(); v.State != "starting" {
		t.Fatalf("starting: %+v", v)
	}
}

func TestWiFiSettingsConnectForgetRevert(t *testing.T) {
	root := wifiRoot(t, true)
	if err := os.MkdirAll(filepath.Join(root, "etc/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &wifiRec{}
	w := &WiFi{Root: Root(root), Run: rec.run, RevertAfter: 200 * time.Millisecond, LocalDir: t.TempDir()}
	ctx := context.Background()

	s := wifi.Defaults()
	s.Enabled = true
	if err := w.PutSettings(ctx, s, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.last(), "systemctl restart") {
		t.Fatalf("the switch restarts: %v", rec.calls)
	}
	s.Preferred = "wlan"
	if err := w.PutSettings(ctx, s, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.last(), "systemctl reload") {
		t.Fatalf("the preference reloads: %v", rec.calls)
	}
	bad := s
	bad.Country = "Germany"
	if err := w.PutSettings(ctx, bad, false); err == nil {
		t.Fatal("a bad country was taken")
	}

	// connect: saved, no passphrase in clear, the supplicant not reachable, so occu-wifi restarts
	if err := w.Connect(ctx, "Lab", wifi.SecWPA2, "secret password", false, false); err != nil {
		t.Fatal(err)
	}
	conf, _ := os.ReadFile(filepath.Join(root, wifi.ConfFile))
	if !strings.Contains(string(conf), `ssid="Lab"`) || strings.Contains(string(conf), "secret password") {
		t.Fatalf("conf:\n%s", conf)
	}
	if st, _ := os.Stat(filepath.Join(root, wifi.ConfFile)); st.Mode().Perm() != 0o600 {
		t.Fatalf("conf mode %v", st.Mode())
	}
	if err := w.Connect(ctx, "Other", wifi.SecWPA2, "short", false, false); err == nil {
		t.Fatal("a short password was taken")
	}
	v := w.View()
	if len(v.Networks) != 1 || v.Networks[0].SSID != "Lab" || v.Networks[0].PSK != "" {
		t.Fatalf("view: %+v", v.Networks)
	}

	// a change over the Wi-Fi reverts without a confirmation
	if err := w.Connect(ctx, "Cafe", wifi.SecOpen, "", false, true); err != nil {
		t.Fatal(err)
	}
	if w.View().Confirm == nil {
		t.Fatal("no confirmation deadline")
	}
	time.Sleep(500 * time.Millisecond)
	conf, _ = os.ReadFile(filepath.Join(root, wifi.ConfFile))
	if strings.Contains(string(conf), "Cafe") || !strings.Contains(string(conf), "Lab") {
		t.Fatalf("not reverted:\n%s", conf)
	}
	// and stays with one
	if err := w.Forget(ctx, "Lab", true); err != nil {
		t.Fatal(err)
	}
	if !w.Confirm() {
		t.Fatal("nothing to confirm")
	}
	time.Sleep(400 * time.Millisecond)
	if _, nets := w.networks(); len(nets) != 0 {
		t.Fatalf("the confirmed forget came back: %+v", nets)
	}
	if err := w.Forget(ctx, "Nope", false); err == nil {
		t.Fatal("forgot a network that is not there")
	}
}
