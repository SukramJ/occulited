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
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/rpcstall"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/system"
)

// testListener is a callback: answer answers, hang reads the call and never answers.
func testListener(t *testing.T, hang bool) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	t.Cleanup(func() { close(done); ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_, _ = c.Read(make([]byte, 4096))
				if hang {
					<-done
					return
				}
				_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

// stallRoot is a system whose hmipserver (uid 8111, listening on 32010) is connected to the
// hanging listener, which belongs to an addon's user and is in the handlers file as well (a
// registration that finished before the listener froze), beside an answering one and occulited's.
func stallRoot(t *testing.T) (system.Root, int, int) {
	r := fakeRoot(t)
	hang, good := testListener(t, true), testListener(t, false)
	write := func(p, c string) {
		full := filepath.Join(string(r), p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/config/InterfacesList.xml", `<interfaces><ipc><name>BidCos-RF</name><url>xmlrpc_bin://127.0.0.1:32001</url></ipc><ipc><name>HmIP-RF</name><url>xmlrpc://127.0.0.1:32010</url></ipc></interfaces>`)
	write("etc/passwd", "root:x:0:0::/:/bin/sh\nhmipserver:x:8111:8111::/:/bin/false\naddon-frozen:x:30001:30001::/:/bin/false\n")
	write("var/LegacyService.handlers", fmt.Sprintf("#comment\nocculited_HmIP-RF=http\\://127.0.0.1\\:8184/cb/HmIP-RF\nfrozen_HmIP-RF=http\\://127.0.0.1\\:%d\ngood_HmIP-RF=http\\://127.0.0.1\\:%d\n", hang, good))
	line := func(n int, local string, lport int, remote string, rport, st, uid, inode int) string {
		return fmt.Sprintf("%4d: %s:%04X %s:%04X %02X 00000000:00000000 00:00000000 00000000 %5d 0 %d 1 x 20 0 0 10 -1\n", n, local, lport, remote, rport, st, uid, inode)
	}
	lo, any := "0100007F", "00000000"
	write("proc/net/tcp", "  sl  local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n"+
		line(0, lo, 32010, any, 0, 0x0A, 8111, 100)+
		line(1, lo, hang, any, 0, 0x0A, 30001, 101)+
		line(2, lo, 52568, lo, hang, 0x01, 8111, 102)+
		line(3, lo, 52570, lo, 8184, 0x01, 8111, 103))
	return r, hang, good
}

func stallAPI(t *testing.T, r system.Root, since time.Time, kind string) (*SystemAPI, *fakeInit, *http.ServeMux) {
	oldProber, oldDrop := stallProber, rpcDrop
	oldWatch, oldQuiet, oldPoll := dropWatch, dropQuiet, dropPoll
	stallProber = &rpcstall.Prober{Timeout: 200 * time.Millisecond}
	dropWatch, dropQuiet, dropPoll = time.Second, 100*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() {
		stallProber, rpcDrop = oldProber, oldDrop
		dropWatch, dropQuiet, dropPoll = oldWatch, oldQuiet, oldPoll
	})
	fake := &fakeInit{}
	a := &SystemAPI{Root: r, InitInterface: fake.init, StallSource: func() []rpcsub.Stall {
		return []rpcsub.Stall{{Interface: "HmIP-RF", URL: "xmlrpc://127.0.0.1:32010", Kind: kind, Since: since}}
	}}
	mux := http.NewServeMux()
	a.Register(mux)
	return a, fake, mux
}

func serve(mux *http.ServeMux, method, path, body string, role auth.Role) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &auth.Session{ID: "s", User: "u", Role: role, Scopes: auth.RoleScopes(role)}))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestRadioStallsNameTheListener(t *testing.T) {
	r, hang, good := stallRoot(t)
	since := time.Date(2026, 9, 25, 10, 50, 56, 0, time.UTC)
	a, _, mux := stallAPI(t, r, since, rpcsub.StallDelivery)

	// before the first check: the warning is there, without a name yet
	ws, ok := a.rpcStallWarnings(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "rpc-stalled" || ws[0].Variant != "HmIP-RF" || ws[0].Params["checked"] != false {
		t.Fatalf("warning before the check %+v", ws)
	}

	rec := serve(mux, "GET", "/api/system/v1/radio/subscribers/stalls?check=1", "", auth.RoleUser)
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var out struct {
		Stalls []struct {
			Interface string              `json:"interface"`
			Kind      string              `json:"kind"`
			Since     string              `json:"since"`
			CheckedAt string              `json:"checked_at"`
			Listeners []rpcstall.Listener `json:"listeners"`
		} `json:"stalls"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Stalls) != 1 {
		t.Fatalf("%s", rec.Body)
	}
	s := out.Stalls[0]
	if s.Interface != "HmIP-RF" || s.Kind != "delivery" || s.Since != "2026-09-25T10:50:56Z" || s.CheckedAt == "" {
		t.Errorf("stall %+v", s)
	}
	// occulited's own entry and its connection to 8184 are not probed; the frozen one comes first
	if len(s.Listeners) != 2 {
		t.Fatalf("listeners %+v", s.Listeners)
	}
	h := s.Listeners[0]
	if h.Address != fmt.Sprintf("127.0.0.1:%d", hang) || h.Verdict != "no-answer" || h.ID != "frozen_HmIP-RF" || !h.Connected || !h.Local || h.Owner != "addon-frozen" {
		t.Errorf("the frozen listener %+v", h)
	}
	if g := s.Listeners[1]; g.Address != fmt.Sprintf("127.0.0.1:%d", good) || g.Verdict != "answers" || g.Connected {
		t.Errorf("the good one %+v", g)
	}
	ws, _ = a.rpcStallWarnings(context.Background())
	if len(ws) != 1 || ws[0].Params["checked"] != true {
		t.Fatalf("warning after the check %+v", ws)
	}
	if ls, _ := ws[0].Params["listeners"].([]rpcstall.Listener); len(ls) != 1 || ls[0].ID != "frozen_HmIP-RF" || ws[0].Href != "/system/interfaces#stalls" {
		t.Errorf("warning params %+v", ws[0])
	}
}

func TestRadioStallDrop(t *testing.T) {
	r, hang, good := stallRoot(t)
	a, fake, mux := stallAPI(t, r, time.Now(), rpcsub.StallDelivery)
	a.rpcStalls(context.Background(), true)
	var dropped []string
	// the process holds one connection, and calls the listener again twice after the drop (rfd)
	again := 2
	rpcDrop = func(port int, remote string) (int, error) {
		dropped = append(dropped, fmt.Sprintf("%d %s", port, remote))
		if len(dropped) == 1 {
			return 1, nil
		}
		if again > 0 {
			again--
			return 1, nil
		}
		return 0, nil
	}
	body := func(port int) string {
		return fmt.Sprintf(`{"interface":"HmIP-RF","address":"127.0.0.1:%d"}`, port)
	}
	if rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", body(hang), auth.RoleUser); rec.Code != 403 {
		t.Errorf("a user: %d", rec.Code)
	}
	// an answering listener is not dropped, nor one of another interface, nor an address from nowhere
	for _, b := range []string{body(good), `{"interface":"BidCos-RF","address":"127.0.0.1:` + fmt.Sprint(hang) + `"}`, body(9)} {
		if rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", b, auth.RoleAdmin); rec.Code != 422 || !strings.Contains(rec.Body.String(), "not-stuck") {
			t.Errorf("%s: %d %s", b, rec.Code, rec.Body)
		}
	}
	if len(dropped) != 0 || len(fake.calls) != 0 {
		t.Fatalf("a refused drop acted: %v %v", dropped, fake.calls)
	}
	rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", body(hang), auth.RoleAdmin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"connections":3`) || !strings.Contains(rec.Body.String(), `"deregistered":1`) {
		t.Fatalf("drop: %d %s", rec.Code, rec.Body)
	}
	// the first drop, the two calls again, then quiet polls until dropQuiet
	if len(dropped) < 4 || dropped[0] != fmt.Sprintf("32010 127.0.0.1:%d", hang) || len(dropped) > 20 {
		t.Errorf("dropped %v", dropped)
	}
	// its handlers entry went first, with the url as the file has it
	if len(fake.calls) != 1 || fake.calls[0] != fmt.Sprintf("HmIP-RF http://127.0.0.1:%d ", hang) {
		t.Errorf("init calls %v", fake.calls)
	}
	// the check is done afresh at the next read
	a.stalls.mu.Lock()
	at := a.stalls.results["HmIP-RF"].at
	a.stalls.mu.Unlock()
	if !at.IsZero() {
		t.Error("the result was kept after the drop")
	}

	// an image whose helper may not do it: said as such
	a.rpcStalls(context.Background(), true)
	rpcDrop = func(int, string) (int, error) { return 0, priv.ErrDropNotAllowed }
	if rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", body(hang), auth.RoleAdmin); rec.Code != 409 || !strings.Contains(rec.Body.String(), "drop-not-allowed") {
		t.Errorf("not allowed: %d %s", rec.Code, rec.Body)
	}
}

// rfd's kind: its calls do not answer. Without a listener that holds it, it is no warning of this
// kind (the interface is down, which says enough); with one, it is.
func TestRadioStallCallsKind(t *testing.T) {
	r := fakeRoot(t)
	a, _, _ := stallAPI(t, r, time.Now(), rpcsub.StallCalls)
	a.rpcStalls(context.Background(), true)
	if ws, _ := a.rpcStallWarnings(context.Background()); len(ws) != 0 {
		t.Errorf("a stall of the calls with nobody to name: %+v", ws)
	}
	r2, _, _ := stallRoot(t)
	a2, _, _ := stallAPI(t, r2, time.Now(), rpcsub.StallCalls)
	a2.rpcStalls(context.Background(), true)
	if ws, _ := a2.rpcStallWarnings(context.Background()); len(ws) != 1 || ws[0].Params["kind"] != "calls" {
		t.Errorf("a stall of the calls with a listener: %+v", ws)
	}
	// the stall ends: the result goes with it
	a2.StallSource = func() []rpcsub.Stall { return nil }
	if v := a2.rpcStalls(context.Background(), false); len(v) != 0 {
		t.Errorf("after the end %+v", v)
	}
	a2.stalls.mu.Lock()
	n := len(a2.stalls.results)
	a2.stalls.mu.Unlock()
	if n != 0 {
		t.Errorf("%d results kept", n)
	}
}

func TestURLPortAndPasswd(t *testing.T) {
	for u, want := range map[string]int{"xmlrpc://127.0.0.1:32010": 32010, "xmlrpc://127.0.0.1:39292/groups": 39292, "xmlrpc_bin://127.0.0.1:32001": 32001, "xmlrpc://host": 0, "x": 0} {
		if got := urlPort(u); got != want {
			t.Errorf("%s: %d", u, got)
		}
	}
	names := passwdNames(func(string) ([]byte, error) { return []byte("a:x:1:1::/:\nbroken\nb:x:1:1::/:\nc:x:z:1\n"), nil })
	if len(names) != 1 || names[1] != "a" {
		t.Errorf("%v", names)
	}
}
