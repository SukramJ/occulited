package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// Task 93: the kernel log beside the journal, and the boots a log query can be narrowed to.

// logReader is the reader of a query and the name of its source: on a systemd box the journal for
// the log and the kernel log alike, elsewhere busybox syslog for the log and dmesg for the kernel.
func (a *SystemAPI) logReader(q system.LogQuery) (system.LogReader, string) {
	if q.Kernel && a.Journal == nil {
		return a.dmesg(), "dmesg"
	}
	return a.Log, a.logSource()
}

func (a *SystemAPI) dmesg() system.LogReader {
	if a.Dmesg != nil {
		return a.Dmesg
	}
	return system.Dmesg{Root: a.Root}
}

// kernelFallback answers /log's kernel query with the ring buffer when the journal of a systemd
// box holds no kernel line of this boot at all: in a container journald does not read the kernel's
// messages. A filter that matched nothing is not that case, so the journal is asked once more
// without the filters first; and where dmesg is not allowed either, the journal's empty answer
// stands.
func (a *SystemAPI) kernelFallback(q system.LogQuery, lines []system.LogLine) ([]system.LogLine, string, bool) {
	if !q.Kernel || a.Journal == nil || len(lines) > 0 || !a.Root.IsThisBoot(q.Boot) {
		return nil, "", false
	}
	if probe, err := a.Log.Read(system.LogQuery{Kernel: true, Limit: 1}); err != nil || len(probe) > 0 {
		return nil, "", false
	}
	ring, err := a.dmesg().Read(q)
	if err != nil {
		return nil, "", false
	}
	return ring, "dmesg", true
}

// bootName is a boot in a download's file name: a boot id's first eight digits, an offset as
// "minus1"; "" for every boot and for this one.
func bootName(b string) string {
	switch {
	case b == "" || b == "0":
		return ""
	case strings.HasPrefix(b, "-"):
		return "minus" + b[1:]
	case len(b) > 8:
		return b[:8]
	}
	return b
}

// bootsCache keeps the journal's boot list for bootsTTL: --list-boots reads the head and tail of
// every journal file, and a persistent journal on a card has many.
type bootsCache struct {
	mu    sync.Mutex
	at    time.Time
	boots []system.Boot
	// starts: an earlier boot's start by its last entry (B-114), kept while the process runs - an
	// ended boot's last entry does not change; a lookup that failed is asked again with the next list
	starts map[string]bootStart
}

type bootStart struct {
	t  time.Time
	ok bool
}

const bootsTTL = 30 * time.Second

func (a *SystemAPI) journalBoots(ctx context.Context) ([]system.Boot, error) {
	c := &a.bootList
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.boots != nil && time.Since(c.at) < bootsTTL {
		return c.boots, nil
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	boots, err := a.Journal.Boots(cctx)
	if err != nil {
		return nil, err
	}
	c.boots, c.at = boots, time.Now()
	for id, s := range c.starts {
		if !s.ok {
			delete(c.starts, id)
		}
	}
	return boots, nil
}

// resolveBoot names an earlier boot by its id for journalctl (B-114). On the Pi 4 without a
// real-time clock, with its journal kept, `journalctl -b -1` answered "No journal boot entry found"
// while --list-boots named the previous boot at -1 and its lines were there by id: the running
// boot's first entry carried the image's date, 13 March, older than the boots before it. The list
// GET /boots keeps is the one the Log page's menu shows, so an offset means the boot listed at it.
// An offset the list does not hold, or a list that cannot be read, stays as it came.
func (a *SystemAPI) resolveBoot(ctx context.Context, q *system.LogQuery) {
	if a.Journal == nil || !strings.HasPrefix(q.Boot, "-") {
		return
	}
	offset, err := strconv.Atoi(q.Boot)
	if err != nil {
		return
	}
	boots, err := a.journalBoots(ctx)
	if err != nil {
		return
	}
	for _, b := range boots {
		if b.Index == offset {
			q.Boot = b.BootID
			return
		}
	}
}

// earlierBootLookups bounds the earlier boots one GET /boots looks up for the first time.
const earlierBootLookups = 8

// earlierBootStart is an earlier boot's start by its last entry, once per boot (bootsCache.starts);
// budget counts the lookups this request may still make.
func (a *SystemAPI) earlierBootStart(ctx context.Context, id string, budget *int) (time.Time, bool) {
	c := &a.bootList
	c.mu.Lock()
	s, known := c.starts[id]
	c.mu.Unlock()
	if known {
		return s.t, s.ok
	}
	if *budget <= 0 {
		return time.Time{}, false
	}
	*budget--
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s.t, s.ok = a.Journal.BootStart(cctx, id)
	c.mu.Lock()
	if c.starts == nil {
		c.starts = map[string]bootStart{}
	}
	c.starts[id] = s
	c.mu.Unlock()
	return s.t, s.ok
}

// bootEntry is one boot of GET /boots: in the journal (its index, journal: true), kept (snapshot:
// true, with its startup's total), or both. A boot kept but no longer in the journal has no index.
type bootEntry struct {
	system.Boot
	Index    *int   `json:"index,omitempty"`
	Current  bool   `json:"current"`
	Journal  bool   `json:"journal"`
	Snapshot bool   `json:"snapshot,omitempty"`
	TotalMS  *int64 `json:"total_ms,omitempty"`
}

// withKept marks the boots that are kept and adds the kept ones the journal no longer holds, newest
// first after the journal's.
func (a *SystemAPI) withKept(entries []bootEntry) []bootEntry {
	for i := range entries {
		if entries[i].Journal {
			idx := entries[i].Boot.Index
			entries[i].Index = &idx
		}
	}
	if a.BootSnapshots == nil {
		return entries
	}
	listed := map[string]int{}
	for i, e := range entries {
		listed[e.BootID] = i
	}
	for _, k := range a.BootSnapshots.List() {
		if i, ok := listed[k.BootID]; ok {
			entries[i].Snapshot, entries[i].TotalMS = true, k.TotalMS
			continue
		}
		entries = append(entries, bootEntry{Boot: system.Boot{BootID: k.BootID, First: k.Started}, Snapshot: true, TotalMS: k.TotalMS})
	}
	return entries
}

// listBoots is GET /boots: the boots the journal holds, newest first, the running one marked.
// Without a journal - or when journalctl fails - there is this boot alone; persistent says whether
// the journal can hold earlier ones at all (in RAM it holds this boot only).
func (a *SystemAPI) listBoots(w http.ResponseWriter, r *http.Request) {
	current := a.Root.BootID()
	this := bootEntry{Boot: system.Boot{BootID: current}, Current: true, Journal: true}
	if up, ok := a.Root.Uptime(); ok {
		this.First = time.Now().Add(-up).Format(time.RFC3339)
	}
	out := map[string]any{"current": current, "source": a.logSource(), "persistent": false}
	if a.Journal == nil {
		out["boots"] = a.withKept([]bootEntry{this})
		writeJSON(w, 200, out)
		return
	}
	out["persistent"] = a.Root.JournalPersistent()
	boots, err := a.journalBoots(r.Context())
	if err != nil {
		out["boots"] = a.withKept([]bootEntry{this})
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}
	entries := make([]bootEntry, 0, len(boots)+1)
	seen := false
	budget := earlierBootLookups
	for i := len(boots) - 1; i >= 0; i-- {
		e := bootEntry{Boot: boots[i], Journal: true}
		// where /proc does not tell, the journal's newest boot is the running one
		e.Current = e.BootID == current || (current == "" && e.Boot.Index == 0)
		// a box without a clock writes its first entries with the wrong time (the Pi 4's journal
		// began "on 13 March"): the running boot began an uptime ago
		if e.Current && this.First != "" {
			e.First = this.First
		} else if !e.Current {
			// B-114: so does an earlier boot, whose span in the boot menu then came to months; it
			// began no earlier than its last entry says
			if start, ok := a.earlierBootStart(r.Context(), e.BootID, &budget); ok {
				if first, err := time.Parse(time.RFC3339, e.First); err != nil || first.Before(start.Truncate(time.Second)) {
					e.First = start.Format(time.RFC3339)
				}
			}
		}
		seen = seen || e.Current
		entries = append(entries, e)
	}
	if !seen && current != "" {
		// a journal that has not written this boot's first entry yet
		entries = append([]bootEntry{this}, entries...)
	}
	out["boots"] = a.withKept(entries)
	writeJSON(w, 200, out)
}
