package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The probe route (task 76's follow-up): a verdict for every subscriber of the interfaces in
// InterfacesList.xml - reachable on an open port, refused on a closed one, none for a URL without an
// address - and nothing for a handlers file whose interface is not in the list. A user may ask.
func TestRadioSubscriberReach(t *testing.T) {
	old := subscriberProbe
	subscriberProbe = &system.SubscriberProbe{Timeout: 2 * time.Second}
	t.Cleanup(func() { subscriberProbe = old })

	open, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer open.Close()
	go func() {
		for {
			c, err := open.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := gone.Addr().String()
	_ = gone.Close()

	r := fakeRoot(t)
	write := func(p, c string) {
		full := filepath.Join(string(r), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/config/InterfacesList.xml", `<interfaces><ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url></ipc><ipc><name>VirtualDevices</name><url>xmlrpc://127.0.0.1:39292/groups</url></ipc></interfaces>`)
	write("var/RFD.handlers", fmt.Sprintf("http://%s\tnr_live_BidCos-RF\nhttp://%s\tmb_gone\n", open.Addr(), closed))
	write("var/HMSERVER.handlers", "xmlrpc://127.0.0.1\thmm_VirtualDevices\n")
	// HmIP-RF is not in the list: its file is not read, its callback not connected to
	write("var/LegacyService.handlers", "HmIP-RF_java=http\\://127.0.0.1\\:1\n")

	mux := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux)
	req := httptest.NewRequest("GET", "/api/system/v1/radio/subscribers/reachability", nil)
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "monitor", Role: auth.RoleUser, Scopes: auth.RoleScopes(auth.RoleUser)}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Subscribers []struct {
			Interface string `json:"interface"`
			ID        string `json:"id"`
			URL       string `json:"url"`
			Reachable *bool  `json:"reachable"`
			Reason    string `json:"reason"`
		} `json:"subscribers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range out.Subscribers {
		v := "none"
		if s.Reachable != nil {
			v = fmt.Sprint(*s.Reachable) + " " + s.Reason
		}
		got[s.Interface+" "+s.ID] = v
	}
	want := map[string]string{
		"BidCos-RF nr_live_BidCos-RF":       "true ",
		"BidCos-RF mb_gone":                 "false refused",
		"VirtualDevices hmm_VirtualDevices": "none",
	}
	if len(got) != len(want) {
		t.Fatalf("verdicts %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
}
