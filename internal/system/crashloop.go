package system

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// openccu-lite task 283: a crash loop reported instead of hidden by the backoff. Every core unit
// restarts on failure with a backoff of 2 s doubling to 5 minutes and no start limit, so a unit
// that fails at every start is never "failed" and the Status page's failed-unit warning never
// fires; it just retries every five minutes for good. CrashLoops samples the units' restart
// counters and says which of them are in a loop - restarted CrashLoopClass.Restarts times within
// the window and not up for Stable since - for the Status page's crash-loop warning and the LED's
// service-failed state.
//
// It also supervises the addons that declare a daemon (runtime.daemon): their generated units are
// Type=oneshot around the rc.d script's start (the CCU's ABI, kept), so a daemon that dies leaves
// the unit active with an empty cgroup (B-158's Ended) and systemd restarts nothing. CrashLoops
// restarts such a unit itself - the rc.d stop, then start - with the core units' backoff, and
// counts those restarts for the same warning.

// CrashLoopClass is one class of units' thresholds.
type CrashLoopClass struct {
	Window   time.Duration // the restarts counted
	Restarts int           // that many within Window is a loop ...
	Stable   time.Duration // ... unless the unit has been active this long since
}

var (
	// CoreCrashLoop is the class of the system's own units.
	CoreCrashLoop = CrashLoopClass{Window: 10 * time.Minute, Restarts: 3, Stable: 120 * time.Second}
	// AddonCrashLoop is the class of the supervised addon units.
	AddonCrashLoop = CrashLoopClass{Window: 15 * time.Minute, Restarts: 3, Stable: 120 * time.Second}
)

// CrashLoopCoreUnits are the units watched; a unit this system does not have is skipped.
var CrashLoopCoreUnits = []string{"occulited", "occulited-helper", "lighttpd", "rfd", "hmipserver", "multimacd", "hs485d", "hmlangw", "sshd", "chrony", "chronyd", "ssdpd"}

// The addon supervisor's backoff: AddonRestartMin doubling up to AddonRestartMax (systemd's
// RestartSec=2, RestartMaxDelaySec=300 of the core units).
var (
	AddonRestartMin = 2 * time.Second
	AddonRestartMax = 300 * time.Second
)

// CrashLoopInterval is how often Run samples.
var CrashLoopInterval = 15 * time.Second

// CrashLoopUnit is one unit in a crash loop, as the warning names it.
type CrashLoopUnit struct {
	Unit     string    `json:"unit"`             // the unit's id without .service
	Restarts int       `json:"restarts"`         // restarts within the class's window
	Total    int       `json:"total"`            // systemd's NRestarts (addons: the supervisor's count)
	Since    time.Time `json:"since"`            // the first restart counted
	Result   string    `json:"result,omitempty"` // systemd's Result of the last run
	Addon    bool      `json:"addon,omitempty"`
}

// CrashLoops samples the units and supervises the addon daemons. The zero value is not usable:
// set Show (or Systemd), and Supervised and Restart for the addons.
type CrashLoops struct {
	// Systemd is what Show uses when Show is nil.
	Systemd SystemdServices
	// Show answers systemctl show's properties per unit (without .service in the key: the Id is
	// the unit's full name); nil = Systemd.showAll.
	Show func(ctx context.Context, units []string, props ...string) map[string]map[string]string
	// Uptime is the time since boot (the monotonic clock of the *TimestampMonotonic properties);
	// nil = Systemd.uptime.
	Uptime func() time.Duration
	// Now is the clock; nil = time.Now.
	Now func() time.Time
	// Supervised answers the addons whose daemon occulited restarts (runtime.daemon declared).
	Supervised func() []string
	// Procs answers whether a unit's cgroup holds a process; nil = the cgroup's file.
	Procs func(cgroup string) bool
	// Restart restarts an addon's unit (addon-<id>); nil = no supervision.
	Restart func(ctx context.Context, unit string) error
	// Paused says whether an addon install or uninstall runs (SystemdAddons.Busy, occulited B-30):
	// meanwhile the core units are still observed, the addons' units are left alone and nothing is
	// restarted, and the first sample after the job counts an ended daemon afresh (its backoff
	// from then), so the job's own stop and start are never taken for a crash. nil = never.
	Paused func() bool
	// OnChange is called after a sample that changed which units are in a loop (the Status
	// warnings are evaluated again, so the LED follows within its step, not the tracker's five
	// minutes); nil = nothing.
	OnChange func()
	Log      *slog.Logger

	mu     sync.Mutex
	units  map[string]*unitHistory
	addons map[string]*addonSupervision
	looped string // the units in a loop at the last sample, for OnChange
	paused bool   // the last sample fell into Paused
}

type unitHistory struct {
	seen     bool
	last     int         // NRestarts at the last sample
	restarts []time.Time // restarts within the window, oldest first
	active   bool
	stableAt time.Time // when the unit counts as stable (active since + Stable); zero = not active
	result   string
	addon    bool
}

type addonSupervision struct {
	attempts  int       // restarts since the daemon last stayed up Stable
	due       time.Time // the next restart; zero = none scheduled
	runningAt time.Time // when the unit was last seen holding a process after a restart
}

func (c *CrashLoops) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CrashLoops) log() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

// Run samples at once and then every CrashLoopInterval until ctx ends - sooner when an addon's
// restart is due before that, so the backoff's first seconds are kept.
func (c *CrashLoops) Run(ctx context.Context) {
	for {
		c.Sample(ctx)
		t := time.NewTimer(c.wait())
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

// wait is the time until the next sample: CrashLoopInterval, or less until the earliest restart
// that is due (at least a second).
func (c *CrashLoops) wait() time.Duration {
	w := CrashLoopInterval
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.addons {
		if !s.due.IsZero() {
			w = min(w, max(s.due.Sub(now), time.Second))
		}
	}
	return w
}

var crashLoopProps = []string{"LoadState", "ActiveState", "SubState", "Result", "NRestarts", "ActiveEnterTimestampMonotonic", "ControlGroup"}

// Sample reads the units once, updates the histories and restarts the ended addon daemons whose
// backoff ran out.
func (c *CrashLoops) Sample(ctx context.Context) {
	var addons []string
	if c.Supervised != nil && c.Restart != nil {
		addons = c.Supervised()
	}
	units := make([]string, 0, len(CrashLoopCoreUnits)+len(addons))
	for _, u := range CrashLoopCoreUnits {
		units = append(units, u+".service")
	}
	for _, id := range addons {
		units = append(units, "addon-"+id+".service")
	}
	show := c.Show
	if show == nil {
		show = c.Systemd.showAll
	}
	ctx2, cancel := context.WithTimeout(ctx, 10*time.Second)
	props := show(ctx2, units, crashLoopProps...)
	cancel()
	if len(props) == 0 {
		return // systemctl did not answer: nothing learned, nothing forgotten
	}
	var up time.Duration
	if c.Uptime != nil {
		up = c.Uptime()
	} else {
		up = c.Systemd.uptime()
	}
	now := c.now()

	c.mu.Lock()
	if c.units == nil {
		c.units = map[string]*unitHistory{}
	}
	if c.addons == nil {
		c.addons = map[string]*addonSupervision{}
	}
	var restart []string
	for _, u := range CrashLoopCoreUnits {
		p := props[u+".service"]
		if p == nil || p["LoadState"] != "loaded" {
			delete(c.units, u)
			continue
		}
		c.observeCore(u, p, up, now)
	}
	keep := map[string]bool{}
	paused := c.Paused != nil && c.Paused()
	resumed := c.paused && !paused
	c.paused = paused
	for _, id := range addons {
		keep[id] = true
		p := props["addon-"+id+".service"]
		if p == nil || p["LoadState"] != "loaded" {
			continue
		}
		if paused {
			// B-30: an install or uninstall runs; what the job does to the units is not a crash
			if s := c.addons[id]; s != nil {
				s.due, s.runningAt = time.Time{}, time.Time{}
			}
			continue
		}
		if resumed {
			// the first sample after the job: an empty unit now starts its backoff, it is not
			// restarted at once - the job's settle step may still be starting it
			if s := c.addons[id]; s != nil {
				s.due = now.Add(addonBackoff(s.attempts))
			}
		}
		if c.observeAddon(id, p, up, now) {
			restart = append(restart, id)
		}
	}
	for id := range c.addons {
		if !keep[id] {
			delete(c.addons, id)
			delete(c.units, "addon-"+id)
		}
	}
	c.mu.Unlock()

	if c.OnChange != nil {
		key := ""
		for _, l := range c.Loops() {
			key += l.Unit + ","
		}
		c.mu.Lock()
		changed := key != c.looped
		c.looped = key
		c.mu.Unlock()
		if changed {
			c.OnChange()
		}
	}
	for _, id := range restart {
		unit := "addon-" + id
		c.log().Warn("addon supervisor: the addon's daemon ended, restarting its unit", "addon", id, "unit", unit+".service", "attempt", c.attempts(id))
		if err := c.Restart(ctx, unit); err != nil {
			c.log().Error("addon supervisor: the restart failed", "addon", id, "err", err)
		}
	}
}

// activeSince answers when the unit became active, from the monotonic stamp; zero when unknown.
func activeSince(p map[string]string, up time.Duration, now time.Time) time.Time {
	n, err := strconv.ParseInt(p["ActiveEnterTimestampMonotonic"], 10, 64)
	if err != nil || n <= 0 || up <= 0 {
		return time.Time{}
	}
	age := up - time.Duration(n)*time.Microsecond
	if age < 0 {
		age = 0
	}
	return now.Add(-age)
}

// observeCore takes one sample of a core unit: the restarts since the last sample into its
// history (NRestarts grows by one per automatic restart; a start by hand resets it), and whether
// it is active and since when. The caller holds mu.
func (c *CrashLoops) observeCore(u string, p map[string]string, up time.Duration, now time.Time) {
	h := c.units[u]
	if h == nil {
		h = &unitHistory{}
		c.units[u] = h
	}
	n, _ := strconv.Atoi(p["NRestarts"])
	h.result = p["Result"]
	h.active = p["ActiveState"] == "active"
	h.stableAt = time.Time{}
	if since := activeSince(p, up, now); h.active && !since.IsZero() {
		h.stableAt = since.Add(CoreCrashLoop.Stable)
	}
	switch {
	case !h.seen:
		// the first sample: restarts before it happened at an unknown time; they count as recent
		// only while the unit is not up for good (so a loop at occulited's start is seen at once,
		// and a unit that failed three times last week and runs since is not)
		if n > 0 && !(h.active && !h.stableAt.IsZero() && !now.Before(h.stableAt)) {
			for i := 0; i < min(n, CoreCrashLoop.Restarts); i++ {
				h.restarts = append(h.restarts, now)
			}
		}
	case n > h.last:
		for i := 0; i < min(n-h.last, CoreCrashLoop.Restarts); i++ {
			h.restarts = append(h.restarts, now)
		}
	}
	h.seen, h.last = true, n
	h.restarts = pruneBefore(h.restarts, now.Add(-CoreCrashLoop.Window))
}

// observeAddon takes one sample of a supervised addon's unit and says whether the supervisor
// restarts it now. The caller holds mu.
func (c *CrashLoops) observeAddon(id string, p map[string]string, up time.Duration, now time.Time) bool {
	key := "addon-" + id
	h := c.units[key]
	if h == nil {
		h = &unitHistory{addon: true, seen: true}
		c.units[key] = h
	}
	s := c.addons[id]
	if s == nil {
		s = &addonSupervision{}
		c.addons[id] = s
	}
	h.restarts = pruneBefore(h.restarts, now.Add(-AddonCrashLoop.Window))
	h.result = p["Result"]
	active := p["ActiveState"] == "active"
	h.active = active
	h.stableAt = time.Time{}
	if !active {
		// stopped (by the user, an update, the shutdown) or failed at its start: not the
		// supervisor's - a failed start is systemd's failed state, the addon-failed warning
		s.attempts, s.due = 0, time.Time{}
		return false
	}
	procs := c.Procs
	if procs == nil {
		procs = func(cg string) bool { return len(cgroupProcs(c.Systemd.Root, cg)) > 0 }
	}
	if procs(p["ControlGroup"]) {
		// the daemon runs: once it has for Stable, the backoff starts from the beginning again
		if s.runningAt.IsZero() {
			s.runningAt = now
			if since := activeSince(p, up, now); !since.IsZero() && since.Before(now) {
				s.runningAt = since
			}
		}
		h.stableAt = s.runningAt.Add(AddonCrashLoop.Stable)
		if !now.Before(h.stableAt) {
			s.attempts = 0
		}
		s.due = time.Time{}
		return false
	}
	// active and empty: the daemon ended (B-158), unless it is a oneshot that ran to success and
	// never kept one - the caller only supervises addons that declare a daemon
	if p["SubState"] != "exited" || (h.result != "" && h.result != "success") {
		return false
	}
	s.runningAt = time.Time{}
	if s.due.IsZero() {
		s.due = now.Add(addonBackoff(s.attempts))
	}
	if now.Before(s.due) {
		return false
	}
	s.attempts++
	s.due = time.Time{}
	h.last++
	h.restarts = append(h.restarts, now)
	return true
}

// addonBackoff is the wait before the n-th restart (from 0): AddonRestartMin doubling up to
// AddonRestartMax.
func addonBackoff(n int) time.Duration {
	d := AddonRestartMin
	for i := 0; i < n && d < AddonRestartMax; i++ {
		d *= 2
	}
	return min(d, AddonRestartMax)
}

func (c *CrashLoops) attempts(id string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.addons[id]; s != nil {
		return s.attempts
	}
	return 0
}

func pruneBefore(ts []time.Time, cut time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	return ts[i:]
}

// Loops answers the units in a crash loop now, sorted by unit.
func (c *CrashLoops) Loops() []CrashLoopUnit {
	if c == nil {
		return nil
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []CrashLoopUnit
	for u, h := range c.units {
		cl := CoreCrashLoop
		if h.addon {
			cl = AddonCrashLoop
		}
		rs := pruneBefore(h.restarts, now.Add(-cl.Window))
		if len(rs) < cl.Restarts {
			continue
		}
		if h.active && !h.stableAt.IsZero() && !now.Before(h.stableAt) {
			continue // up for Stable since: the loop is over
		}
		out = append(out, CrashLoopUnit{Unit: u, Restarts: len(rs), Total: h.last, Since: rs[0].UTC().Truncate(time.Second), Result: h.result, Addon: h.addon})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Unit < out[j].Unit })
	return out
}

// OcculitedUnitStateFile is the waiting page's state file, written by occulited's unit
// (deploy/systemd/occulited-unit-state) and read here for occulited's own crash loop.
const OcculitedUnitStateFile = "/run/occulite/occulited-state.json"

// OcculitedUnitState is that file.
type OcculitedUnitState struct {
	State     string `json:"state"`
	Since     int64  `json:"since"`
	Started   int64  `json:"started"`
	Restarts  int    `json:"restarts"`
	Fails     int    `json:"fails"`
	FirstFail int64  `json:"first_fail"`
	LastFail  int64  `json:"last_fail"`
	Result    string `json:"result"`
	NextRetry int64  `json:"next_retry"`
	Reason    string `json:"reason"`
}

// OcculitedCrashLoopFails is the file's fails count that is a crash loop (the script's CRASH_LOOP).
const OcculitedCrashLoopFails = 3

// ReadOcculitedUnitState answers the state file; nil without one.
func (r Root) ReadOcculitedUnitState() *OcculitedUnitState {
	b, err := os.ReadFile(r.join(OcculitedUnitStateFile))
	if err != nil {
		return nil
	}
	var s OcculitedUnitState
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// String is the unit's line for a log.
func (u CrashLoopUnit) String() string {
	return fmt.Sprintf("%s: %d restarts since %s (%s)", u.Unit, u.Restarts, u.Since.Format(time.RFC3339), strings.TrimSpace(u.Result))
}
