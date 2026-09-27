package httpapi

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/servicemsg"
)

// task 75: the service messages route and its stream - the store's messages with the metadata
// store's names, 501 without a store, and a change pushed to an open stream.
func TestServiceMessagesRoutes(t *testing.T) {
	mux := http.NewServeMux()
	(&SystemAPI{Root: fakeRoot(t)}).Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	if st, _, _ := do(t, srv, "GET", "/api/system/v1/service-messages", "", nil); st != 501 {
		t.Fatalf("without a store: %d", st)
	}

	store := &servicemsg.Store{OnChange: ServiceMessageWatchers.Changed}
	mux2 := http.NewServeMux()
	api := &SystemAPI{Root: fakeRoot(t), ServiceMessages: store, Names: func(ref string) (string, []string, bool) {
		if ref == "HmIP-RF.00010000000A10" {
			return "Wandtaster Flur", []string{"room/eg/flur"}, true
		}
		return "", nil, false
	}}
	api.Register(mux2)
	srv2 := httptest.NewServer(mux2)
	t.Cleanup(srv2.Close)
	store.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "00010000000A10:0", Key: "LOW_BAT", Value: true, Time: time.Unix(1000, 0)})
	st, out, _ := do(t, srv2, "GET", "/api/system/v1/service-messages", "", nil)
	if st != 200 || out["count"] != 1.0 {
		t.Fatalf("list: %d %v", st, out)
	}
	msgs := out["messages"].([]any)
	m := msgs[0].(map[string]any)
	if m["name"] != "Wandtaster Flur" || m["key"] != "LOW_BAT" || m["seen"] != "event" || m["address"] != "00010000000A10" || m["channel"] != "0" {
		t.Fatalf("message %v", m)
	}

	// the stream: the view at once, and again when the set changes
	resp, err := http.Get(srv2.URL + "/api/system/v1/service-messages/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	r := bufio.NewReader(resp.Body)
	next := func() map[string]any {
		t.Helper()
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				t.Fatalf("stream ended: %v", err)
			}
			if strings.HasPrefix(line, "data: ") {
				var v map[string]any
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
					t.Fatal(err)
				}
				return v
			}
		}
	}
	if v := next(); v["count"] != 1.0 {
		t.Fatalf("first view %v", v)
	}
	store.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "00010000000A10:0", Key: "LOW_BAT", Value: false, Time: time.Unix(2000, 0)})
	if v := next(); v["count"] != 0.0 {
		t.Fatalf("after the clear %v", v)
	}
}
