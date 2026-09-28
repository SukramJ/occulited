package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hobbyquaker/occulited/internal/meta"
)

// task 286: the open /version says what a client can use, and not which interfaces (radio
// hardware) the system runs - those are GET /api/rpc/v1/interfaces, behind rpc:read
func TestVersionCapabilities(t *testing.T) {
	c := Capabilities(true)
	if c["pairing"] != true || c["json_double"] != true || c["apis"].(map[string]int)["rpc"] != 1 {
		t.Fatalf("capabilities: %v", c)
	}
	if _, ok := c["interfaces"]; ok {
		t.Fatal("the open capabilities must not list the interfaces")
	}
	store, _ := meta.New(nil, nil)
	a := &MetaAPI{Store: store, Capabilities: func() map[string]any { return Capabilities(false) }}
	mux := http.NewServeMux()
	a.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/meta/v1/version", nil))
	var out map[string]map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != 200 || out["capabilities"] == nil || out["capabilities"]["interfaces"] != nil || out["capabilities"]["pairing"] != false {
		t.Fatalf("/version: %d %s", w.Code, w.Body.String())
	}
}
