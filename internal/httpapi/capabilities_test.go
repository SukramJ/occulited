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
	c := Capabilities(true, 0)
	if c["pairing"] != true || c["json_double"] != true || c["apis"].(map[string]int)["rpc"] != 1 {
		t.Fatalf("capabilities: %v", c)
	}
	if _, ok := c["interfaces"]; ok {
		t.Fatal("the open capabilities must not list the interfaces")
	}
	// occulited task 19: the per-token stream limit is the one in force - 3 by default, or
	// occulited.json's rpc.streams_per_session
	if n := c["limits"].(map[string]int)["streams_per_token"]; n != 3 {
		t.Fatalf("default streams per token: %d", n)
	}
	if n := Capabilities(true, 5)["limits"].(map[string]int)["streams_per_token"]; n != 5 {
		t.Fatalf("configured streams per token: %d", n)
	}
	store, _ := meta.New(nil, nil)
	a := &MetaAPI{Store: store, Capabilities: func() map[string]any { return Capabilities(false, 0) }}
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
