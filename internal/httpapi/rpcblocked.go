package httpapi

import (
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/rpcstall"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 234 (the maintainer, 2026-09-25: "detect from the log, warn, offer the drop").
// A listener whose registration at hmipserver finished and that later takes an event without
// answering it holds only itself: hmipserver delivers each registered listener's events on a
// worker pool of its own (<id>_WorkerPool-1), and every other client goes on getting its events -
// so the subscriber's stall detection (B-201) never fires. But the pool never times out, a
// deregistration does not release it, and Vert.x's blocked-thread checker logs the held thread
// with a stack trace every second (about 43 journal lines a second, measured on a Pi 4) until the
// connection ends. That line names the listener's id: it is read from the journal here, the
// listener is named on the Status page (rpc-stalled, a third kind "listener") and on the
// Interfaces page, where B-201's End connection ends exactly its connection. Nothing is dropped
// by itself.

// stallListener is the kind of a stall found in hmipserver's log: one listener does not take its
// events; the interface itself delivers to everybody else.
const stallListener = "listener"

// blockedWindow is how far back a line counts as "held now". The checker writes one a second while
// the pool is held; hmipserver's unit limits its journal to 100 lines per 30 s, which lets the
// first two or three of the checker's lines (each with its ~43-line stack trace) of every 30 s
// through (measured on a Pi 4), so the newest is up to 30 s old while the pool is held.
var blockedWindow = 60 * time.Second

// blockedInterfaces are the interfaces hmipserver serves: their handlers files hold the ids its
// pools are named after.
var blockedInterfaces = []string{"HmIP-RF", "VirtualDevices"}

// blockedLines bounds the read: one line a second per held listener, over the window.
const blockedLines = 500

// blockedPool is one listener hmipserver's log names, from its newest line.
type blockedPool struct {
	id    string
	at    time.Time // the newest line
	since time.Time // when the pool began to be held: the newest line's time less its "blocked for"
	held  time.Duration
}

// hmipBlockedPools reads this boot's journal of hmipserver for the checker's lines of the window,
// newest per id, oldest first. Nil without a journal or when the read fails.
func (a *SystemAPI) hmipBlockedPools(now time.Time) []blockedPool {
	if a.Journal == nil {
		return nil
	}
	lines, err := a.Journal.Read(system.LogQuery{Unit: hmipserverUnit, Boot: "0", Since: "-" + strconv.Itoa(int(blockedWindow/time.Second)) + "s",
		Contains: rpcstall.BlockedMatch, Limit: blockedLines})
	if err != nil {
		return nil
	}
	byID := map[string]*blockedPool{}
	var order []string
	for _, l := range lines {
		id, held, ok := rpcstall.BlockedPool(l.Message)
		if !ok || strings.HasPrefix(id, rpcsub.IDPrefix) {
			continue
		}
		ts := l.Timestamp
		if ts == "" {
			ts = l.Time
		}
		at, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil || now.Sub(at) > blockedWindow {
			continue
		}
		p, seen := byID[id]
		if !seen {
			p = &blockedPool{id: id}
			byID[id] = p
			order = append(order, id)
		}
		if !at.Before(p.at) {
			p.at, p.held, p.since = at, held, at.Add(-held)
		}
	}
	out := make([]blockedPool, 0, len(order))
	a.stalls.mu.Lock()
	for _, id := range order {
		// a connection the drop ended: its lines until then are history
		if d, ok := a.stalls.dropped[id]; ok && !byID[id].at.After(d) {
			continue
		}
		out = append(out, *byID[id])
	}
	a.stalls.mu.Unlock()
	return out
}

// blockedViews turns the pools into the stall views of their interfaces: each listener with its
// handlers entry (the id's), its address and whether hmipserver holds a connection to it. A pool
// whose id is on no list any more (deregistered - which does not release it) is named by its id
// alone; there is no address to end a connection to.
func (a *SystemAPI) blockedViews(now time.Time) []stallView {
	pools := a.hmipBlockedPools(now)
	a.logBlocked(pools)
	if len(pools) == 0 {
		return nil
	}
	read := func(p string) ([]byte, error) { return os.ReadFile(a.Root.Path(p)) }
	conns := rpcstall.ReadAll(read)
	users := passwdNames(read)
	subs := map[string][]system.InterfaceSubscriber{}
	for _, name := range blockedInterfaces {
		subs[name] = a.Root.InterfaceSubscribers(name)
	}
	views := map[string]*stallView{}
	var order []string
	for _, p := range pools {
		iface, url := blockedInterfaces[0], ""
	find:
		for _, name := range blockedInterfaces {
			for _, s := range subs[name] {
				if s.ID == p.id {
					iface, url = name, s.URL
					break find
				}
			}
		}
		v, ok := views[iface]
		if !ok {
			port := urlPort(a.Root.InterfaceURL(iface))
			v = &stallView{Interface: iface, Kind: stallListener, since: p.since, at: now, port: port, Listeners: []rpcstall.Listener{}}
			views[iface] = v
			order = append(order, iface)
		}
		if p.since.Before(v.since) {
			v.since = p.since
		}
		l := rpcstall.Listener{ID: p.id, URL: url, Verdict: rpcstall.VerdictBlocked, BlockedFor: int64(p.held / time.Second)}
		if url != "" {
			l.Address = rpcstall.ListenerAddress(url)
		}
		if ap, err := netip.ParseAddrPort(l.Address); err == nil {
			if d, ok := rpcstall.DaemonOf(conns, uint16(v.port)); ok {
				for _, peer := range d.Peers {
					l.Connected = l.Connected || peer == ap
				}
			}
			if ap.Addr().IsLoopback() {
				l.Local = true
				l.Owner = users[rpcstall.ListenOwner(conns, ap.Port())]
			}
		}
		v.Listeners = append(v.Listeners, l)
	}
	out := make([]stallView, 0, len(order))
	for _, name := range order {
		v := views[name]
		v.Since = v.since.UTC().Format(time.RFC3339)
		v.CheckedAt = now.UTC().Format(time.RFC3339)
		sort.SliceStable(v.Listeners, func(i, j int) bool { return v.Listeners[i].ID < v.Listeners[j].ID })
		out = append(out, *v)
	}
	return out
}

// logBlocked writes one line to occulited's log when a listener's pool is found held, not one per
// read: the pools of the last read are remembered, and a pool that is gone ends its episode.
func (a *SystemAPI) logBlocked(pools []blockedPool) {
	a.stalls.mu.Lock()
	if a.stalls.blocked == nil {
		a.stalls.blocked = map[string]bool{}
	}
	var fresh []blockedPool
	now := map[string]bool{}
	for _, p := range pools {
		now[p.id] = true
		if !a.stalls.blocked[p.id] {
			fresh = append(fresh, p)
		}
	}
	a.stalls.blocked = now
	a.stalls.mu.Unlock()
	for _, p := range fresh {
		a.liteLog().Warn("rpc: hmipserver's worker pool of a listener is blocked - the listener took an event and does not answer; hmipserver logs it every second until that connection ends", "listener", p.id, "blocked_for", p.held.Round(time.Second).String())
	}
}

// mergeBlocked adds the views of the log to the subscriber's: a listener the log names on an
// interface that is stalled as well joins that view (as blocked, whatever the probe said).
func mergeBlocked(out []stallView, blocked []stallView) []stallView {
	for _, b := range blocked {
		merged := false
		for i := range out {
			if out[i].Interface != b.Interface {
				continue
			}
			merged = true
			// the subscriber's view shares its list with the cached result: a copy
			out[i].Listeners = append([]rpcstall.Listener(nil), out[i].Listeners...)
			for _, l := range b.Listeners {
				replaced := false
				for j := range out[i].Listeners {
					if l.Address != "" && out[i].Listeners[j].Address == l.Address {
						out[i].Listeners[j] = l
						replaced = true
					}
				}
				if !replaced {
					out[i].Listeners = append(out[i].Listeners, l)
				}
			}
			if out[i].port == 0 {
				out[i].port = b.port
			}
		}
		if !merged {
			out = append(out, b)
		}
	}
	return out
}

// noteDropped: the drop ended the connections of these ids; their lines up to now no longer count.
func (a *SystemAPI) noteDropped(ids []string, at time.Time) {
	a.stalls.mu.Lock()
	defer a.stalls.mu.Unlock()
	if a.stalls.dropped == nil {
		a.stalls.dropped = map[string]time.Time{}
	}
	for id, t := range a.stalls.dropped {
		if at.Sub(t) > time.Hour {
			delete(a.stalls.dropped, id)
		}
	}
	for _, id := range ids {
		a.stalls.dropped[id] = at
	}
}
