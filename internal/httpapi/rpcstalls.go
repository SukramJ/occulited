package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/rpcstall"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/system"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

// openccu-lite B-201 (the maintainer, 2026-09-25: "detect, name, then drop"). One callback listener
// that takes an interface process's call and never answers holds the process for everybody: rfd's
// whole XML-RPC server, or every event hmipserver delivers. The subscriber (rpcsub) notices the
// stall; this finds the listener behind it - every loopback address the daemon is connected to
// and every entry of its handlers file, each asked for system.listMethods - and names it on the
// Status page. The drop ends the daemon's connection to it: a deregistration alone does not
// release the daemon (measured), the end of the connection does.

// stallProber asks the listeners; a package variable, so the tests shorten its timeout.
var stallProber = &rpcstall.Prober{}

// stallRecheck is how long a check's result stands while the stall lasts.
var stallRecheck = time.Minute

// stallView is one stalled interface and what the check found.
type stallView struct {
	Interface string `json:"interface"`
	// Kind: delivery (hmipserver: calls answer, events do not come) or calls (rfd: nothing answers)
	Kind  string `json:"kind"`
	Since string `json:"since"`
	// CheckedAt is when the listeners were asked; absent while the first check runs.
	CheckedAt string              `json:"checked_at,omitempty"`
	Listeners []rpcstall.Listener `json:"listeners"`
	// Port is the daemon's, for the drop.
	port  int
	since time.Time
	at    time.Time
}

// Stuck are the listeners that took the probe's call and did not answer.
func (v stallView) Stuck() []rpcstall.Listener {
	out := []rpcstall.Listener{}
	for _, l := range v.Listeners {
		if l.Stuck() {
			out = append(out, l)
		}
	}
	return out
}

// stallState is the API's memory of the checks.
type stallState struct {
	mu      sync.Mutex
	results map[string]stallView
	// running: the checks under way, closed when done - a caller that waits waits for one another
	// caller started as well
	running map[string]chan struct{}
	// task 234: the listeners hmipserver's log named at the last read (logged once per episode),
	// and when the drop ended an id's connection (its older lines no longer count)
	blocked map[string]bool
	dropped map[string]time.Time
}

// rpcStallList is the subscriber's stalls; StallSource replaces it in the tests.
func (a *SystemAPI) rpcStallList() []rpcsub.Stall {
	if a.StallSource != nil {
		return a.StallSource()
	}
	if a.RPC == nil {
		return nil
	}
	return a.RPC.Stalls()
}

// rpcStalls answers every stalled interface with the last check's result, and starts a check where
// there is none for this stall or it is older than stallRecheck. wait holds the answer for the
// checks it started (bounded by ctx).
func (a *SystemAPI) rpcStalls(ctx context.Context, wait bool) []stallView {
	stalls := a.rpcStallList()
	st := &a.stalls
	st.mu.Lock()
	if st.results == nil {
		st.results, st.running = map[string]stallView{}, map[string]chan struct{}{}
	}
	live := map[string]bool{}
	var pending []chan struct{}
	for _, s := range stalls {
		live[s.Interface] = true
		if d, ok := st.running[s.Interface]; ok {
			pending = append(pending, d)
			continue
		}
		r, ok := st.results[s.Interface]
		if ok && r.since.Equal(s.Since) && !r.at.IsZero() && time.Since(r.at) < stallRecheck {
			continue
		}
		if !ok || !r.since.Equal(s.Since) {
			st.results[s.Interface] = stallView{Interface: s.Interface, Kind: s.Kind, Since: s.Since.UTC().Format(time.RFC3339), since: s.Since, Listeners: []rpcstall.Listener{}}
		}
		done := make(chan struct{})
		st.running[s.Interface] = done
		pending = append(pending, done)
		go func(s rpcsub.Stall) {
			defer close(done)
			v := a.checkStall(context.Background(), s)
			st.mu.Lock()
			delete(st.running, s.Interface)
			if cur, ok := st.results[s.Interface]; ok && cur.since.Equal(s.Since) {
				st.results[s.Interface] = v
			}
			st.mu.Unlock()
			if stuck := v.Stuck(); len(stuck) > 0 {
				names := make([]string, len(stuck))
				for i, l := range stuck {
					names[i] = l.Address
					if l.ID != "" {
						names[i] += " (" + l.ID + ")"
					}
				}
				a.liteLog().Warn("rpc: a callback listener takes the interface's calls and does not answer - it holds the interface for every client", "interface", s.Interface, "kind", s.Kind, "listeners", strings.Join(names, ", "))
			}
		}(s)
	}
	for name := range st.results {
		if !live[name] {
			delete(st.results, name) // the stall is over
		}
	}
	st.mu.Unlock()
	if wait {
		for _, d := range pending {
			select {
			case <-d:
			case <-ctx.Done():
			}
		}
	}
	st.mu.Lock()
	out := []stallView{}
	for _, s := range stalls {
		if r, ok := st.results[s.Interface]; ok {
			out = append(out, r)
		}
	}
	st.mu.Unlock()
	// task 234: the listeners hmipserver's log names
	return mergeBlocked(out, a.blockedViews(time.Now()))
}

// checkStall asks the listeners of one stalled interface.
func (a *SystemAPI) checkStall(ctx context.Context, s rpcsub.Stall) stallView {
	v := stallView{Interface: s.Interface, Kind: s.Kind, Since: s.Since.UTC().Format(time.RFC3339), since: s.Since}
	v.port = urlPort(s.URL)
	read := func(p string) ([]byte, error) { return os.ReadFile(a.Root.Path(p)) }
	conns := rpcstall.ReadAll(read)
	daemon, _ := rpcstall.DaemonOf(conns, uint16(v.port))
	var subs []rpcstall.Subscriber
	for _, x := range a.Root.InterfaceSubscribers(s.Interface) {
		subs = append(subs, rpcstall.Subscriber{ID: x.ID, URL: x.URL})
	}
	users := passwdNames(read)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	v.Listeners = stallProber.Check(cctx, rpcstall.Input{
		Subscribers: subs,
		Daemon:      daemon,
		Skip:        func(x rpcstall.Subscriber) bool { return strings.HasPrefix(x.ID, rpcsub.IDPrefix) },
		Owner: func(port uint16) string {
			return users[rpcstall.ListenOwner(conns, port)]
		},
	})
	v.at = time.Now()
	v.CheckedAt = v.at.UTC().Format(time.RFC3339)
	return v
}

// urlPort is the port of an InterfacesList.xml URL, 0 when it has none.
func urlPort(u string) int {
	_, rest, _ := strings.Cut(u, "://")
	hostport, _, _ := strings.Cut(rest, "/")
	if i := strings.LastIndex(hostport, ":"); i >= 0 {
		if n, err := strconv.Atoi(hostport[i+1:]); err == nil && n > 0 && n < 65536 {
			return n
		}
	}
	return 0
}

// passwdNames maps user ids to names, from /etc/passwd; an addon's listener is named by its user.
func passwdNames(read func(string) ([]byte, error)) map[int]string {
	out := map[int]string{}
	b, err := read("/etc/passwd")
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Split(l, ":")
		if len(f) < 3 {
			continue
		}
		if uid, err := strconv.Atoi(f[2]); err == nil {
			if _, dup := out[uid]; !dup {
				out[uid] = f[0]
			}
		}
	}
	return out
}

// rpc-stalled (B-201): an interface process is held. The variant is the interface, so a stall of
// another one is a new warning; the listeners that do not answer are in the params. A delivery
// stall is a warning even before, or without, a named listener: no event arrives from that
// interface. A stall of the calls without a named listener is left to the interface's down state
// (the daemon may simply be restarting).
func (a *SystemAPI) rpcStallWarnings(ctx context.Context) ([]warnings.Warning, bool) {
	var out []warnings.Warning
	for _, v := range a.rpcStalls(ctx, false) {
		stuck := v.Stuck()
		if len(stuck) == 0 && v.Kind != rpcsub.StallDelivery {
			continue
		}
		out = append(out, warnings.Warning{ID: "rpc-stalled", Variant: v.Interface, Severity: warnings.SeverityError, Href: "/system/interfaces#stalls",
			Params: map[string]any{"interface": v.Interface, "kind": v.Kind, "since": v.Since, "checked": v.CheckedAt != "", "listeners": stuck}})
	}
	return out, true
}

// radioStalls is GET /radio/subscribers/stalls: the stalled interfaces and their listeners. ?check=1
// waits for a check it starts (at most 20 s).
func (a *SystemAPI) radioStalls(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	writeJSON(w, 200, map[string]any{"stalls": a.rpcStalls(ctx, r.URL.Query().Get("check") != "")})
}

// dropWatch is how long a drop watches for the process calling the listener again, and dropQuiet
// how long without a new connection ends the watch early; dropPoll the step. rfd called a listener
// whose init callback failed again at once, three times, then registered it anyway and sent it an
// event about 10 s later (measured): one press of the button covers all of it. The tests shorten
// them.
var (
	dropWatch = 15 * time.Second
	dropQuiet = 5 * time.Second
	dropPoll  = 500 * time.Millisecond
)

// rpcDrop is the helper's operation; a package variable so the tests fake it.
var rpcDrop = func(port int, remote string) (int, error) {
	p, ok := system.Priv.(priv.RPCDropOps)
	if !ok {
		return 0, errors.New("the privilege helper has no rpc-drop operation")
	}
	return p.DropRPCConnections(port, remote)
}

// radioStallDrop is POST /radio/subscribers/drop {interface, address}: the listener must be one the
// last check of that stall found not answering - the address is never taken from the request
// alone. Its handlers entries are removed (init(url, "") - so the daemon does not call it again),
// and the daemon's connections to it are ended through the helper.
func (a *SystemAPI) radioStallDrop(w http.ResponseWriter, r *http.Request) {
	s := SessionFrom(r)
	if s == nil || !s.Has(auth.ScopeSystemWrite) {
		forbiddenScope(w, auth.ScopeSystemWrite)
		return
	}
	var body struct {
		Interface string `json:"interface"`
		Address   string `json:"address"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		badBody(w, err)
		return
	}
	var found *rpcstall.Listener
	var view stallView
	for _, v := range a.rpcStalls(r.Context(), false) {
		if v.Interface != body.Interface {
			continue
		}
		view = v
		for _, l := range v.Stuck() {
			if l.Address == body.Address {
				found = &l
			}
		}
	}
	if found == nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "not-stuck", Message: "the last check of " + body.Interface + " did not find " + body.Address + " holding it"})
		return
	}
	if _, err := netip.ParseAddrPort(found.Address); err != nil || view.port == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "not-stuck", Message: "no address to end a connection to"})
		return
	}
	// the handlers entries of that address first: hmipserver answers init while it is held, and
	// a listener it still has on its list would hold it again with the next event. rfd does not
	// answer while it is held - its entry is removed after the drop.
	var entries []system.InterfaceSubscriber
	for _, x := range a.Root.InterfaceSubscribers(body.Interface) {
		if addr := listenerAddress(x.URL); addr == found.Address && !x.Own {
			entries = append(entries, x)
		}
	}
	deregister := func(limit time.Duration) (left []system.InterfaceSubscriber) {
		for _, x := range entries {
			if a.InitInterface == nil {
				return entries
			}
			ctx, cancel := context.WithTimeout(r.Context(), limit)
			err := a.InitInterface(ctx, body.Interface, x.URL, "")
			cancel()
			if err != nil {
				left = append(left, x)
			}
		}
		return left
	}
	left := deregister(3 * time.Second)
	n, err := rpcDrop(view.port, found.Address)
	if err == nil {
		// the process may call the listener again at once: end those connections too, until it
		// has left it alone for dropQuiet
		start, last := time.Now(), time.Now()
		for time.Since(start) < dropWatch && time.Since(last) < dropQuiet {
			select {
			case <-r.Context().Done():
				start = time.Time{}
			case <-time.After(dropPoll):
			}
			if start.IsZero() {
				break
			}
			more, err2 := rpcDrop(view.port, found.Address)
			if err2 != nil {
				break
			}
			if more > 0 {
				n += more
				last = time.Now()
			}
		}
	}
	if err != nil {
		slog.Warn("rpc: ending the connection to a listener that does not answer failed", "user", s.User, "interface", body.Interface, "listener", found.Address, "err", err)
		status := http.StatusBadGateway
		code := "drop-failed"
		if errors.Is(err, priv.ErrDropNotAllowed) || strings.Contains(err.Error(), priv.ErrDropNotAllowed.Error()) {
			status, code = http.StatusConflict, "drop-not-allowed"
		}
		writeJSON(w, status, apiError{Error: code, Message: err.Error()})
		return
	}
	if len(left) > 0 {
		entries = left
		left = deregister(10 * time.Second)
	}
	slog.Info("rpc: connection to a listener that does not answer ended", "user", s.User, "interface", body.Interface, "listener", found.Address, "id", found.ID, "connections", n, "deregistered", len(entries)-len(left))
	// the next read checks afresh
	a.stalls.mu.Lock()
	if v, ok := a.stalls.results[body.Interface]; ok {
		v.at = time.Time{}
		a.stalls.results[body.Interface] = v
	}
	a.stalls.mu.Unlock()
	// task 234: the log's lines of this listener's pool until now are history
	var ids []string
	if found.ID != "" {
		ids = append(ids, found.ID)
	}
	for _, x := range entries {
		ids = append(ids, x.ID)
	}
	a.noteDropped(ids, time.Now())
	writeJSON(w, 200, map[string]any{"connections": n, "deregistered": len(entries) - len(left), "entries": len(entries)})
}

// listenerAddress is a callback URL's host:port as the checks write it.
func listenerAddress(u string) string { return rpcstall.ListenerAddress(u) }
