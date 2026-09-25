package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/pairing"
)

// task 219 through HTTP: ask, reveal, the list with the code, approve, the token once; the
// switch; a PATCH of the label.
func TestPairingRoutes(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "occulited.json")
	_ = config.Save(cfgPath, config.Default())
	a := &AuthAPI{Store: store, ConfigFile: cfgPath, Pairing: &pairing.Manager{Minter: store, Enabled: func() bool { return PairingEnabled(cfgPath) }}}
	mux := http.NewServeMux()
	a.Register(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cn := strings.Repeat("ab", 16)
	raw, _ := hex.DecodeString(cn)
	sum := sha256.Sum256(raw)
	st, out, body := do(t, srv, "POST", "/api/auth/v1/pairing/request", `{"app":"hm2mqtt.js","instance":"nas","access":{"devices":"operate","names":"read"},"commit":"`+hex.EncodeToString(sum[:])+`"}`, nil)
	if st != 202 || out["poll"] == nil {
		t.Fatalf("ask: %d %s", st, body)
	}
	id, poll := out["id"].(string), out["poll"].(string)
	hdr := map[string]string{"Authorization": "Pairing " + poll}
	if st, out, body := do(t, srv, "GET", "/api/auth/v1/pairing/request/"+id+"?client_nonce="+cn, "", hdr); st != 200 || out["state"] != "pending" {
		t.Fatalf("reveal: %d %s", st, body)
	}
	if st, _, _ := do(t, srv, "GET", "/api/auth/v1/pairing/request/"+id, "", map[string]string{"Authorization": "Pairing nope"}); st != 403 {
		t.Fatalf("another secret: %d", st)
	}
	st, out, body = do(t, srv, "GET", "/api/auth/v1/pairing", "", nil)
	reqs, _ := out["requests"].([]any)
	if st != 200 || out["enabled"] != true || len(reqs) != 1 || strings.Contains(body, poll) {
		t.Fatalf("list: %d %s", st, body)
	}
	code := reqs[0].(map[string]any)["code"].(string)
	if st, _, body := do(t, srv, "POST", "/api/auth/v1/pairing/"+id+"/approve", `{"code":"`+code+`"}`, nil); st != 200 || !strings.Contains(body, "hm2mqtt-js-nas") {
		t.Fatalf("approve: %d %s", st, body)
	}
	st, out, body = do(t, srv, "GET", "/api/auth/v1/pairing/request/"+id+"?wait=1", "", hdr)
	if st != 200 || out["state"] != "approved" || !strings.HasPrefix(out["token"].(string), "olt_") {
		t.Fatalf("token: %d %s", st, body)
	}
	if st, _, _ := do(t, srv, "GET", "/api/auth/v1/pairing/request/"+id, "", hdr); st != 404 {
		t.Fatalf("twice: %d", st)
	}
	if st, _, body := do(t, srv, "PATCH", "/api/auth/v1/tokens/hm2mqtt-js-nas", `{"label":"MQTT"}`, nil); st != 200 || !strings.Contains(body, `"label":"MQTT"`) {
		t.Fatalf("patch: %d %s", st, body)
	}
	if st, _, _ := do(t, srv, "PATCH", "/api/auth/v1/tokens/hm2mqtt-js-nas", `{"scopes":["*"]}`, nil); st != 422 {
		t.Fatalf("widen: %d", st)
	}
	// the switch
	if st, out, _ := do(t, srv, "PUT", "/api/auth/v1/pairing/settings", `{"enabled":false}`, nil); st != 200 || out["enabled"] != false {
		t.Fatalf("switch: %d", st)
	}
	if st, out, _ := do(t, srv, "POST", "/api/auth/v1/pairing/request", `{"app":"x","access":{"devices":"read"},"commit":"`+hex.EncodeToString(sum[:])+`"}`, nil); st != 403 || out["error"] != "pairing-off" {
		t.Fatalf("off: %d %v", st, out)
	}
	if st, _, _ := do(t, srv, "PUT", "/api/auth/v1/pairing/settings", `{}`, nil); st != 422 {
		t.Fatalf("empty switch: %d", st)
	}
}
