package system

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UnitCache keeps what `systemctl show` said about each unit between two listings (B-83).
//
// The Services page polls, and one `systemctl show` for every unit (B-60's single call) still took
// about a second on a Pi 3-class box - the Charly, 61 units: 0.98 s for the full property list,
// 0.94 s for UnitFileState alone. The cost is per unit, in systemd answering for each one, not per
// property; asking for fewer properties saves nothing. So the listing shows a unit only when
// something about it can have changed:
//
//   - its state in `list-units` (one call, 37 ms on the Charly) differs from the cached one;
//   - its main process is gone (a restart between two polls leaves the state as it was);
//   - occulited itself ran a systemctl command that changes something: start, stop, restart,
//     mask, enable, ... on named units shows those again, a daemon-reload every unit;
//   - the cache is older than MaxAge - what someone did with systemctl by hand;
//   - the box has no unified cgroup tree to read the figures from.
//
// The figures that change all the time - CPU time, memory, tasks - are read from the unit's
// cgroup on every listing (cpu.stat, memory.current, pids.current: the files systemd reads for
// CPUUsageNSec, MemoryCurrent and TasksCurrent, world-readable, no helper call).
type UnitCache struct {
	MaxAge time.Duration // every unit is shown again after this; 0 = five minutes

	refresh sync.Mutex // one refresh at a time; a second caller waits and gets the fresh table
	// guarded by refresh
	services unitTable
	timers   unitTable
	unified  *bool

	mu      sync.Mutex // guards the generations below
	counter uint64
	allGen  uint64            // the counter at the last invalidation of every unit
	unitGen map[string]uint64 // the counter at the last invalidation of one unit
}

// NewUnitCache returns a cache with the default age.
func NewUnitCache() *UnitCache { return &UnitCache{} }

type unitTable struct {
	units map[string]cachedUnit
	gen   uint64    // the counter before the last full show
	at    time.Time // when that was
}

type cachedUnit struct {
	props       map[string]string
	active, sub string
	gen         uint64 // the counter before the show that produced props
	// live: the last listing could vouch for the figures from the cgroup - cpu.stat was read, or
	// the cgroup is gone and nothing of the unit runs that could change them
	live bool
}

// readOnlyVerbs are the systemctl commands that change nothing; every other one invalidates.
var readOnlyVerbs = map[string]bool{"show": true, "list-units": true, "list-timers": true, "cat": true, "is-enabled": true, "is-active": true, "is-failed": true, "status": true}

// noteCommand invalidates what a systemctl command can have changed: the units after "--", or
// every unit when it names none (daemon-reload).
func (c *UnitCache) noteCommand(args []string) {
	if len(args) == 0 || readOnlyVerbs[args[0]] {
		return
	}
	var units []string
	for i, a := range args {
		if a == "--" {
			units = args[i+1:]
			break
		}
	}
	c.Invalidate(units...)
}

// Invalidate makes the next listing show the named units again, or every unit when none is named.
func (c *UnitCache) Invalidate(units ...string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counter++
	if len(units) == 0 {
		c.allGen = c.counter
		return
	}
	if c.unitGen == nil {
		c.unitGen = map[string]uint64{}
	}
	for _, u := range units {
		c.unitGen[u] = c.counter
	}
}

func (c *UnitCache) generation() (counter, all uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counter, c.allGen
}

// stale reports whether the unit was invalidated after gen was taken.
func (c *UnitCache) stale(unit string, gen uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return gen < c.allGen || gen < c.unitGen[unit]
}

func (c *UnitCache) maxAge() time.Duration {
	if c.MaxAge > 0 {
		return c.MaxAge
	}
	return 5 * time.Minute
}

// refreshTable shows the units of t that need it - every unit when the table is due as a whole,
// otherwise the ones recheck or an invalidation names - and returns the table's entries for units.
// A unit systemctl did not answer for has no entry and is asked again next time. The caller holds
// c.refresh.
func (c *UnitCache) refreshTable(ctx context.Context, s SystemdServices, t *unitTable, units []string, props []string, rows map[string]unitRow, recheck func(unit string, cu cachedUnit) bool) {
	counter, all := c.generation()
	now := s.now()
	full := t.units == nil || all > t.gen || now.Sub(t.at) >= c.maxAge() || now.Before(t.at)
	var ask []string
	for _, u := range units {
		cu, ok := t.units[u]
		if full || !ok || c.stale(u, cu.gen) || (recheck != nil && recheck(u, cu)) {
			ask = append(ask, u)
		}
	}
	if full {
		t.units, t.gen, t.at = map[string]cachedUnit{}, counter, now
	}
	if len(ask) > 0 {
		shown := s.showAll(ctx, ask, props...)
		for _, u := range ask {
			if block, ok := shown[u]; ok {
				t.units[u] = cachedUnit{props: block, active: rows[u].Active, sub: rows[u].Sub, gen: counter}
			} else {
				delete(t.units, u)
			}
		}
	}
	// a unit that left the list leaves the cache
	listed := make(map[string]bool, len(units))
	for _, u := range units {
		listed[u] = true
	}
	for u := range t.units {
		if !listed[u] {
			delete(t.units, u)
		}
	}
}

// serviceDetails is List's `systemctl show` through the cache: the props of every listed unit,
// with the figures of the cgroup laid over the cached ones.
func (c *UnitCache) serviceDetails(ctx context.Context, s SystemdServices, units []string, rows map[string]unitRow) map[string]map[string]string {
	c.refresh.Lock()
	defer c.refresh.Unlock()
	unified := c.cgroupUnified(s.Root)
	c.refreshTable(ctx, s, &c.services, units, serviceProps, rows, func(u string, cu cachedUnit) bool {
		row := rows[u]
		switch {
		case cu.active != row.Active || cu.sub != row.Sub:
			return true
		case row.Active != "active":
			return false
		case !unified || !cu.live:
			return true // no cgroup figures to read: systemd is the only source
		}
		pid, err := strconv.Atoi(cu.props["MainPID"])
		return err == nil && pid > 0 && !procAlive(s.Root, pid)
	})
	out := make(map[string]map[string]string, len(units))
	for _, u := range units {
		cu, ok := c.services.units[u]
		if !ok {
			continue
		}
		props := maps.Clone(cu.props)
		if unified {
			cu.live = overlayCgroup(s.Root, props)
			c.services.units[u] = cu
		}
		out[u] = props
	}
	return out
}

// timerStates is Timers' `systemctl show -p UnitFileState` through the cache: a timer's state
// changes only through a command (invalidated) or by hand (MaxAge).
func (c *UnitCache) timerStates(ctx context.Context, s SystemdServices, units []string) map[string]map[string]string {
	c.refresh.Lock()
	defer c.refresh.Unlock()
	c.refreshTable(ctx, s, &c.timers, units, []string{"UnitFileState"}, nil, nil)
	out := make(map[string]map[string]string, len(units))
	for _, u := range units {
		if cu, ok := c.timers.units[u]; ok {
			out[u] = cu.props
		}
	}
	return out
}

// cgroupUnified reports whether the box runs the unified cgroup hierarchy, where a unit's figures
// are files under /sys/fs/cgroup<ControlGroup>. Asked once.
func (c *UnitCache) cgroupUnified(root Root) bool {
	if c.unified == nil {
		_, err := os.Stat(root.join("/sys/fs/cgroup/cgroup.controllers"))
		ok := err == nil
		c.unified = &ok
	}
	return *c.unified
}

// overlayCgroup replaces CPUUsageNSec, MemoryCurrent and TasksCurrent in props with what the
// unit's cgroup says now, as systemd itself would: a file the kernel does not offer (no memory
// controller for the unit - both lab boxes) is "[not set]". A cgroup that is gone holds nothing,
// so no process runs that could change the CPU time: that stays, and memory and tasks are
// "[not set]". It reports whether the figures could be vouched for; false sends the unit to
// systemctl at the next listing.
func overlayCgroup(root Root, props map[string]string) bool {
	cg := props["ControlGroup"]
	if cg == "" {
		return true
	}
	if !strings.HasPrefix(cg, "/") || strings.Contains(cg, "..") {
		return false
	}
	dir := filepath.Join(root.join("/sys/fs/cgroup"), cg)
	if _, err := os.Stat(dir); err != nil {
		props["MemoryCurrent"], props["TasksCurrent"] = "[not set]", "[not set]"
		return true
	}
	stat, err := os.ReadFile(filepath.Join(dir, "cpu.stat"))
	if err != nil {
		return false
	}
	usec := ""
	for _, line := range strings.Split(string(stat), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			usec = strings.TrimSpace(v)
			break
		}
	}
	n, err := strconv.ParseUint(usec, 10, 64)
	if err != nil {
		return false
	}
	props["CPUUsageNSec"] = strconv.FormatUint(n*1000, 10)
	props["MemoryCurrent"] = cgroupValue(dir, "memory.current")
	props["TasksCurrent"] = cgroupValue(dir, "pids.current")
	return true
}

func cgroupValue(dir, file string) string {
	b, err := os.ReadFile(filepath.Join(dir, file))
	if err != nil {
		return "[not set]"
	}
	v := strings.TrimSpace(string(b))
	if _, err := strconv.ParseUint(v, 10, 64); err != nil {
		return "[not set]"
	}
	return v
}
