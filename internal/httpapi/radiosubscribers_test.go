package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/system"
)

// a fake interface process for the remove route: it records the init calls and, when it drops,
// rewrites the handlers file without the url, as rfd does
type fakeInit struct {
	mu    sync.Mutex
	calls []string
	drop  bool
	err   error
	file  string
}

func (f *fakeInit) init(_ context.Context, iface, url, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, iface+" "+url+" "+id)
	if f.err != nil {
		return f.err
	}
	if f.drop {
		raw, _ := os.ReadFile(f.file)
		var keep []string
		for _, line := range strings.Split(string(raw), "\n") {
			if line != "" && !strings.HasPrefix(line, url+"\t") {
				keep = append(keep, line)
			}
		}
		_ = os.WriteFile(f.file, []byte(strings.Join(keep, "\n")+"\n"), 0o644)
	}
	return nil
}

func TestRadioSubscriberRemove(t *testing.T) {
	oldSettle, oldPoll := subscriberSettle, subscriberPoll
	subscriberSettle, subscriberPoll = 150*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { subscriberSettle, subscriberPoll = oldSettle, oldPoll })

	const handlers = "http://127.0.0.1:59999\tolt_test\nhttp://127.0.0.1:39292/bidcos\tBidCos-RF_java\n"
	tests := []struct {
		name        string
		role        auth.Role
		body        string
		drop        bool
		err         error
		wantStatus  int
		wantCode    string
		wantRemoved bool
		wantCalls   []string
		wantLog     string
	}{
		{name: "removed", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`, drop: true,
			wantStatus: 200, wantRemoved: true, wantCalls: []string{"BidCos-RF http://127.0.0.1:59999 "}, wantLog: `msg="radio: subscription removed" user=u interface=BidCos-RF id=olt_test url=http://127.0.0.1:59999 removed=true`},
		{name: "the entry stays", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`,
			wantStatus: 200, wantRemoved: false, wantCalls: []string{"BidCos-RF http://127.0.0.1:59999 "}, wantLog: "removed=false"},
		{name: "a user", role: auth.RoleUser, body: `{"interface":"BidCos-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`, drop: true,
			wantStatus: 403, wantCode: "forbidden"},
		{name: "an url that is not in the file", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"olt_test","url":"http://10.0.0.1:1"}`, drop: true,
			wantStatus: 422, wantCode: "not-subscribed"},
		{name: "the id of another entry", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"BidCos-RF_java","url":"http://127.0.0.1:59999"}`, drop: true,
			wantStatus: 422, wantCode: "not-subscribed"},
		{name: "the pair under another interface", role: auth.RoleAdmin, body: `{"interface":"HmIP-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`, drop: true,
			wantStatus: 422, wantCode: "not-subscribed"},
		{name: "an interface that is not in the list", role: auth.RoleAdmin, body: `{"interface":"BidCos-Wired","id":"olt_test","url":"http://127.0.0.1:59999"}`, drop: true,
			wantStatus: 422, wantCode: "unknown-interface"},
		{name: "a field missing", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"olt_test"}`,
			wantStatus: 400, wantCode: "bad-request"},
		{name: "the process does not answer", role: auth.RoleAdmin, body: `{"interface":"BidCos-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`, err: errors.New("connection refused"),
			wantStatus: 502, wantCode: "interface-call", wantCalls: []string{"BidCos-RF http://127.0.0.1:59999 "}, wantLog: "removing a subscription failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := fakeRoot(t)
			write := func(p, c string) {
				full := filepath.Join(string(r), p)
				_ = os.MkdirAll(filepath.Dir(full), 0o755)
				if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("etc/config/InterfacesList.xml", `<interfaces><ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url><info>BidCos-RF</info></ipc><ipc><name>HmIP-RF</name><url>xmlrpc://127.0.0.1:32010</url><info>HmIP-RF</info></ipc></interfaces>`)
			write("var/RFD.handlers", handlers)
			fake := &fakeInit{drop: tc.drop, err: tc.err, file: filepath.Join(string(r), "var/RFD.handlers")}

			var logs bytes.Buffer
			old := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(old) })

			mux := http.NewServeMux()
			(&SystemAPI{Root: r, InitInterface: fake.init}).Register(mux)
			req := httptest.NewRequest("POST", "/api/system/v1/radio/subscribers/remove", strings.NewReader(tc.body))
			req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "u", Role: tc.role, Scopes: auth.RoleScopes(tc.role)}))
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body)
			}
			var out struct {
				Error       string                       `json:"error"`
				Removed     bool                         `json:"removed"`
				Subscribers []system.InterfaceSubscriber `json:"subscribers"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if tc.wantCode != "" && out.Error != tc.wantCode {
				t.Errorf("error %q, want %q", out.Error, tc.wantCode)
			}
			fake.mu.Lock()
			calls := fake.calls
			fake.mu.Unlock()
			if strings.Join(calls, "|") != strings.Join(tc.wantCalls, "|") {
				t.Errorf("init calls %q, want %q", calls, tc.wantCalls)
			}
			if tc.wantStatus == 200 {
				if out.Removed != tc.wantRemoved {
					t.Errorf("removed %v, want %v", out.Removed, tc.wantRemoved)
				}
				// the answer is the file read again
				found := false
				for _, s := range out.Subscribers {
					found = found || s.ID == "olt_test"
				}
				if found == tc.wantRemoved || len(out.Subscribers) == 0 {
					t.Errorf("subscribers %+v after removed=%v", out.Subscribers, out.Removed)
				}
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("journal %q does not carry %q", logs.String(), tc.wantLog)
			}
			if tc.wantLog == "" && logs.Len() > 0 {
				t.Errorf("a refused request logged %q", logs.String())
			}
		})
	}
}

// Without the interface hook (a development root) the route answers 501 for a pair that exists.
func TestRadioSubscriberRemoveUnsupported(t *testing.T) {
	r := fakeRoot(t)
	_ = os.MkdirAll(filepath.Join(string(r), "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(string(r), "etc/config/InterfacesList.xml"), []byte(`<ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url></ipc>`), 0o644)
	_ = os.WriteFile(filepath.Join(string(r), "var/RFD.handlers"), []byte("http://127.0.0.1:59999\tolt_test\n"), 0o644)
	mux := http.NewServeMux()
	(&SystemAPI{Root: r}).Register(mux)
	req := httptest.NewRequest("POST", "/api/system/v1/radio/subscribers/remove", strings.NewReader(`{"interface":"BidCos-RF","id":"olt_test","url":"http://127.0.0.1:59999"}`))
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{Role: auth.RoleAdmin, Scopes: auth.RoleScopes(auth.RoleAdmin)}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}
