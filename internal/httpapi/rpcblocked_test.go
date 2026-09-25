package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/rpcstall"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/system"
)

// blockedJournal is hmipserver's journal as journalctl -o json gives it: the checker's lines, each
// at its time. The runner seam records the arguments.
type blockedJournal struct {
	mu    sync.Mutex
	lines []string
	args  []string
	err   error
}

func (j *blockedJournal) add(t *testing.T, at time.Time, id string, held time.Duration) {
	t.Helper()
	msg := fmt.Sprintf("io.vertx.core.impl.BlockedThreadChecker [vertx-blocked-thread-checker] Thread %s_WorkerPool-1 has been blocked for %d ms, time limit is 60000 ms", id, held.Milliseconds())
	b, err := json.Marshal(map[string]any{"__REALTIME_TIMESTAMP": strconv.FormatInt(at.UnixMicro(), 10), "MESSAGE": msg, "_SYSTEMD_UNIT": "hmipserver.service", "PRIORITY": "4"})
	if err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	j.lines = append(j.lines, string(b))
	j.mu.Unlock()
}

func (j *blockedJournal) run(_ context.Context, name string, args ...string) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.args = args
	return []byte(strings.Join(j.lines, "\n") + "\n"), j.err
}

// blockedAPI is stallRoot's system (hmipserver connected to the frozen listener, which is in its
// handlers file as frozen_HmIP-RF) with no stall the subscriber sees - task 234's case - and a
// journal.
func blockedAPI(t *testing.T) (*SystemAPI, *blockedJournal, *fakeInit, int, int) {
	r, hang, good := stallRoot(t)
	a, fake, _ := stallAPI(t, r, time.Now(), rpcsub.StallDelivery)
	a.StallSource = func() []rpcsub.Stall { return nil }
	j := &blockedJournal{}
	a.Journal = &system.JournalLog{Run: j.run}
	return a, j, fake, hang, good
}

func TestBlockedListenerFromTheLog(t *testing.T) {
	a, j, _, hang, _ := blockedAPI(t)
	mux := httpMux(a)

	// a quiet log: nothing, and the read is the cheap one - this boot, the unit, the window, and
	// journalctl's own grep on the fixed part of the line
	if ws, ok := a.rpcStallWarnings(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("quiet: %+v", ws)
	}
	args := strings.Join(j.args, " ")
	for _, want := range []string{"-u hmipserver.service", "--boot=0", "--since -60s", "-g " + rpcstall.BlockedMatch} {
		if !strings.Contains(args, want) {
			t.Errorf("journalctl args %q miss %q", args, want)
		}
	}

	now := time.Now()
	j.add(t, now.Add(-2*time.Second), "frozen_HmIP-RF", 90*time.Second)
	j.add(t, now.Add(-time.Second), "frozen_HmIP-RF", 91*time.Second)
	// occulited's own id is never named, and a line older than the window does not count
	j.add(t, now.Add(-time.Second), rpcsub.IDPrefix+"HmIP-RF", 70*time.Second)
	j.add(t, now.Add(-2*blockedWindow), "gone_HmIP-RF", 70*time.Second)

	ws, ok := a.rpcStallWarnings(context.Background())
	if !ok || len(ws) != 1 {
		t.Fatalf("warnings %+v", ws)
	}
	w := ws[0]
	if w.ID != "rpc-stalled" || w.Variant != "HmIP-RF" || w.Params["kind"] != "listener" || w.Params["checked"] != true || w.Href != "/system/interfaces#stalls" {
		t.Errorf("warning %+v", w)
	}
	ls, _ := w.Params["listeners"].([]rpcstall.Listener)
	if len(ls) != 1 {
		t.Fatalf("listeners %+v", ls)
	}
	l := ls[0]
	if l.ID != "frozen_HmIP-RF" || l.Address != fmt.Sprintf("127.0.0.1:%d", hang) || l.Verdict != "blocked" || l.BlockedFor != 91 || !l.Connected || !l.Local || l.Owner != "addon-frozen" {
		t.Errorf("listener %+v", l)
	}
	// since: the newest line's time less its "blocked for"
	if since, _ := time.Parse(time.RFC3339, w.Params["since"].(string)); since.Sub(now.Add(-92*time.Second)).Abs() > 2*time.Second {
		t.Errorf("since %v", w.Params["since"])
	}

	// the Interfaces page's list says the same
	rec := serve(mux, "GET", "/api/system/v1/radio/subscribers/stalls", "", auth.RoleUser)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"listener"`) || !strings.Contains(rec.Body.String(), `"verdict":"blocked"`) || !strings.Contains(rec.Body.String(), `"blocked_for":91`) {
		t.Errorf("stalls: %d %s", rec.Code, rec.Body)
	}

	// a journal that cannot be read names nobody
	j.err = errors.New("no journal")
	if ws, _ := a.rpcStallWarnings(context.Background()); len(ws) != 0 {
		t.Errorf("failed read: %+v", ws)
	}
	j.err = nil
	// without a journal (busybox) the source is quiet
	a.Journal = nil
	if ws, _ := a.rpcStallWarnings(context.Background()); len(ws) != 0 {
		t.Errorf("no journal: %+v", ws)
	}
}

// A listener deregistered after its pool was held (the deregistration does not release it) is
// named by its id alone: there is no address, so there is nothing to end.
func TestBlockedListenerNotOnTheList(t *testing.T) {
	a, j, _, _, _ := blockedAPI(t)
	j.add(t, time.Now(), "gone", 65*time.Second)
	v := a.rpcStalls(context.Background(), false)
	if len(v) != 1 || v[0].Interface != "HmIP-RF" || len(v[0].Listeners) != 1 || v[0].Listeners[0].ID != "gone" || v[0].Listeners[0].Address != "" || v[0].Listeners[0].Connected {
		t.Fatalf("%+v", v)
	}
	rec := serve(httpMux(a), "POST", "/api/system/v1/radio/subscribers/drop", `{"interface":"HmIP-RF","address":""}`, auth.RoleAdmin)
	if rec.Code != 422 {
		t.Errorf("a drop without an address: %d %s", rec.Code, rec.Body)
	}
}

// End connection for the listener the log names: its entry goes, the helper ends the connection,
// and the lines of the pool until then no longer count - the warning goes at once, not a window
// later. A new line after the drop (it registered again and holds again) brings it back.
func TestBlockedListenerDrop(t *testing.T) {
	a, j, fake, hang, good := blockedAPI(t)
	mux := httpMux(a)
	j.add(t, time.Now().Add(-time.Second), "frozen_HmIP-RF", 75*time.Second)
	var dropped []string
	rpcDrop = func(port int, remote string) (int, error) {
		dropped = append(dropped, fmt.Sprintf("%d %s", port, remote))
		if len(dropped) == 1 {
			return 1, nil
		}
		return 0, nil
	}
	// only the listener the log names
	if rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", fmt.Sprintf(`{"interface":"HmIP-RF","address":"127.0.0.1:%d"}`, good), auth.RoleAdmin); rec.Code != 422 {
		t.Errorf("an answering listener: %d", rec.Code)
	}
	rec := serve(mux, "POST", "/api/system/v1/radio/subscribers/drop", fmt.Sprintf(`{"interface":"HmIP-RF","address":"127.0.0.1:%d"}`, hang), auth.RoleAdmin)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"connections":1`) {
		t.Fatalf("drop: %d %s", rec.Code, rec.Body)
	}
	if len(dropped) == 0 || dropped[0] != fmt.Sprintf("32010 127.0.0.1:%d", hang) {
		t.Errorf("dropped %v", dropped)
	}
	if len(fake.calls) != 1 || fake.calls[0] != fmt.Sprintf("HmIP-RF http://127.0.0.1:%d ", hang) {
		t.Errorf("init calls %v", fake.calls)
	}
	if v := a.rpcStalls(context.Background(), false); len(v) != 0 {
		t.Errorf("after the drop: %+v", v)
	}
	time.Sleep(5 * time.Millisecond)
	j.add(t, time.Now(), "frozen_HmIP-RF", 61*time.Second)
	if v := a.rpcStalls(context.Background(), false); len(v) != 1 {
		t.Errorf("held again: %+v", v)
	}
}

// A listener the log names on an interface the subscriber sees stalled too joins that stall's
// view, as blocked, whatever the probe found - and the cached probe result is not changed.
func TestBlockedListenerJoinsAStall(t *testing.T) {
	r, hang, _ := stallRoot(t)
	a, _, _ := stallAPI(t, r, time.Now(), rpcsub.StallDelivery)
	j := &blockedJournal{}
	a.Journal = &system.JournalLog{Run: j.run}
	a.rpcStalls(context.Background(), true)
	j.add(t, time.Now(), "frozen_HmIP-RF", 65*time.Second)
	v := a.rpcStalls(context.Background(), false)
	if len(v) != 1 || v[0].Kind != rpcsub.StallDelivery {
		t.Fatalf("%+v", v)
	}
	var got *rpcstall.Listener
	for i, l := range v[0].Listeners {
		if l.Address == fmt.Sprintf("127.0.0.1:%d", hang) {
			if got != nil {
				t.Error("named twice")
			}
			got = &v[0].Listeners[i]
		}
	}
	if got == nil || got.Verdict != rpcstall.VerdictBlocked {
		t.Errorf("listeners %+v", v[0].Listeners)
	}
	a.stalls.mu.Lock()
	cached := a.stalls.results["HmIP-RF"].Listeners
	a.stalls.mu.Unlock()
	for _, l := range cached {
		if l.Verdict == rpcstall.VerdictBlocked {
			t.Error("the cached result was changed")
		}
	}
}

// the log line is written once per episode, not once per read
func TestBlockedLoggedOnce(t *testing.T) {
	a := &SystemAPI{}
	p := []blockedPool{{id: "x", held: time.Minute}}
	a.logBlocked(p)
	a.stalls.mu.Lock()
	if !a.stalls.blocked["x"] {
		t.Error("not remembered")
	}
	a.stalls.mu.Unlock()
	a.logBlocked(nil)
	a.stalls.mu.Lock()
	if len(a.stalls.blocked) != 0 {
		t.Error("the episode did not end")
	}
	a.stalls.mu.Unlock()
}

func httpMux(a *SystemAPI) *http.ServeMux {
	m := http.NewServeMux()
	a.Register(m)
	return m
}
