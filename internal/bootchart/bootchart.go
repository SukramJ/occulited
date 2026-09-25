// Package bootchart is the startup timeline of a boot (task 93): when each systemd unit began to
// start and when it was up, the manager's milestones, and the chain of units multi-user.target
// waited for - what `systemd-analyze blame`, `plot` and `critical-chain` show. It is read from
// `systemctl show`, because systemd-analyze is not on every image (the x86_64 OVA has none), and
// computed the way systemd-analyze computes it, so the two agree where both exist.
package bootchart

import (
	"sort"

	"github.com/hobbyquaker/occulited/internal/unitshow"
)

// The properties the timeline reads. Every loaded unit is asked for with --all: without it a
// oneshot that has run and exited (inactive) is not listed.
const (
	unitProps    = "Id,Description,LoadState,ActiveState,SubState,InactiveExitTimestampMonotonic,ActiveEnterTimestampMonotonic,ActiveExitTimestampMonotonic,InactiveEnterTimestampMonotonic,After"
	managerProps = "InitRDTimestampMonotonic,UserspaceTimestampMonotonic,FinishTimestampMonotonic"
)

// UnitsArgs and ManagerArgs are the two systemctl calls of a timeline.
func UnitsArgs() []string   { return []string{"show", "--all", "-p", unitProps, "*"} }
func ManagerArgs() []string { return []string{"show", "-p", managerProps} }

// Root is the unit whose critical chain the timeline carries.
const Root = "multi-user.target"

// Timeline is one boot's startup: GET /boot's answer.
type Timeline struct {
	BootID string `json:"boot_id"`
	// Started is when the kernel started on the wall clock (RFC 3339), now − /proc/uptime when read
	Started string `json:"started,omitempty"`
	// Finished: the manager has finished starting (FinishTimestamp is set). A timeline of a boot still
	// starting has no critical chain and shows the units that are starting with their time so far.
	Finished   bool       `json:"finished"`
	Timestamps Timestamps `json:"timestamps"`
	Summary    Summary    `json:"summary"`
	// Units that began to start during the boot, in the order they began
	Units []Unit `json:"units"`
	// CriticalChain is what `systemd-analyze critical-chain multi-user.target` prints, in its order:
	// the root, then at each step the units it is ordered after that became active last
	CriticalChain []string `json:"critical_chain"`
	// Later counts the units whose (last) start began after the boot finished: started later, or
	// restarted since. Their timestamps are of that start, so they are not part of the boot.
	Later int `json:"later"`
	// LaterUnits names them (B-153), sorted
	LaterUnits []string `json:"later_units,omitempty"`
	// Snapshot: an earlier boot's timeline, kept when that boot had finished (snapshot.go)
	Snapshot bool `json:"snapshot,omitempty"`
	// Previous is the boot kept before this one, which the page compares with
	Previous *Kept `json:"previous,omitempty"`
}

// Timestamps are the manager's milestones in seconds after the kernel started.
type Timestamps struct {
	Kernel    float64  `json:"kernel"` // always 0: the clock these count on starts with the kernel
	InitRD    *float64 `json:"initrd,omitempty"`
	Userspace float64  `json:"userspace"`
	Finish    *float64 `json:"finish,omitempty"`
	MultiUser *float64 `json:"multi_user,omitempty"`
}

// Summary is what `systemd-analyze time` says, in milliseconds, and the slowest unit.
type Summary struct {
	KernelMS    int64  `json:"kernel_ms"`              // until the initrd or userspace began
	InitRDMS    *int64 `json:"initrd_ms,omitempty"`    // the initrd, where there is one
	UserspaceMS *int64 `json:"userspace_ms,omitempty"` // from userspace until the manager finished
	TotalMS     *int64 `json:"total_ms,omitempty"`     // the kernel's start until the manager finished
	// MultiUserMS is when multi-user.target was reached, after the kernel started
	MultiUserMS *int64 `json:"multi_user_ms,omitempty"`
	Slowest     *Slow  `json:"slowest,omitempty"`
}

// Slow names a unit and how long it took to start.
type Slow struct {
	ID         string `json:"id"`
	DurationMS int64  `json:"duration_ms"`
}

// Unit is one unit's start, its times in seconds after the kernel started.
type Unit struct {
	ID          string   `json:"id"`
	Description string   `json:"description,omitempty"`
	Activating  float64  `json:"activating"`         // InactiveExitTimestampMonotonic: it began to start
	Active      *float64 `json:"active,omitempty"`   // ActiveEnterTimestampMonotonic: it was up
	Inactive    *float64 `json:"inactive,omitempty"` // InactiveEnterTimestampMonotonic after that: ended (a oneshot) or failed
	// DurationMS is systemd-analyze's: active − activating, else inactive − activating; for a unit
	// still starting the time so far
	DurationMS int64    `json:"duration_ms"`
	State      string   `json:"state"`
	Sub        string   `json:"sub,omitempty"`
	After      []string `json:"after,omitempty"` // the units of the timeline it is ordered after
	// Restarted is when the unit began to start again after the boot, in seconds after the kernel
	// started (B-153): the running boot's timeline keeps the boot's stamps, State and Sub are live
	Restarted *float64 `json:"restarted,omitempty"`
}

// raw is one unit's stamps in microseconds.
type raw struct {
	id, description, state, sub string
	activating, activated       int64
	deactivated                 int64
	after                       []string
}

func parseUnits(out []byte) map[string]*raw {
	units := map[string]*raw{}
	for _, b := range unitshow.Blocks(out) {
		id := b["Id"]
		if id == "" {
			continue
		}
		units[id] = &raw{
			id: id, description: b["Description"], state: b["ActiveState"], sub: b["SubState"],
			activating:  unitshow.Micros(b, "InactiveExitTimestampMonotonic"),
			activated:   unitshow.Micros(b, "ActiveEnterTimestampMonotonic"),
			deactivated: unitshow.Micros(b, "InactiveEnterTimestampMonotonic"),
			after:       unitshow.Words(b["After"]),
		}
	}
	return units
}

// durationUS is systemd-analyze's time of a unit; a unit still starting counts until now.
func (u *raw) durationUS(nowUS int64) int64 {
	switch {
	case u.activated >= u.activating:
		return u.activated - u.activating
	case u.deactivated >= u.activating:
		return u.deactivated - u.activating
	case u.state == "activating" && nowUS > u.activating:
		return nowUS - u.activating
	}
	return 0
}

func seconds(us int64) float64 { return float64(us) / 1e6 }

func secondsPtr(us int64) *float64 {
	if us <= 0 {
		return nil
	}
	s := seconds(us)
	return &s
}

func msPtr(us int64) *int64 {
	ms := us / 1000
	return &ms
}

// Build is the timeline of the running boot from `systemctl show` of the manager (ManagerArgs) and
// of every unit (UnitsArgs). nowUS is the time since the kernel started, for the units still
// starting; 0 when it is not known.
func Build(manager, units []byte, nowUS int64) Timeline {
	var m map[string]string
	if blocks := unitshow.Blocks(manager); len(blocks) > 0 {
		m = blocks[0]
	}
	initrd := unitshow.Micros(m, "InitRDTimestampMonotonic")
	userspace := unitshow.Micros(m, "UserspaceTimestampMonotonic")
	finish := unitshow.Micros(m, "FinishTimestampMonotonic")
	all := parseUnits(units)

	t := Timeline{Finished: finish > 0, Units: []Unit{}, CriticalChain: []string{}}
	t.Timestamps = Timestamps{InitRD: secondsPtr(initrd), Userspace: seconds(userspace), Finish: secondsPtr(finish)}
	kernelDone := userspace
	if initrd > 0 {
		kernelDone = initrd
		t.Summary.InitRDMS = msPtr(userspace - initrd)
	}
	t.Summary.KernelMS = kernelDone / 1000
	if finish > 0 {
		t.Summary.UserspaceMS = msPtr(finish - userspace)
		t.Summary.TotalMS = msPtr(finish)
	}
	if mu := all[Root]; mu != nil && mu.activated > 0 {
		t.Timestamps.MultiUser = secondsPtr(mu.activated)
		t.Summary.MultiUserMS = msPtr(mu.activated)
	}

	// the units of the boot: begun to start (systemd-analyze leaves out a unit without that
	// stamp), and not after the boot finished - that is a later start, as systemd-analyze plot sees it
	in := map[string]bool{}
	var list []*raw
	for id, u := range all {
		if u.activating <= 0 {
			continue
		}
		if finish > 0 && u.activating > finish {
			t.LaterUnits = append(t.LaterUnits, id)
			continue
		}
		in[id] = true
		list = append(list, u)
	}
	sort.Strings(t.LaterUnits)
	t.Later = len(t.LaterUnits)
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.activating != b.activating {
			return a.activating < b.activating
		}
		if a.activated != b.activated {
			return a.activated < b.activated
		}
		return a.id < b.id
	})
	for _, u := range list {
		d := u.durationUS(nowUS)
		unit := Unit{ID: u.id, Description: u.description, Activating: seconds(u.activating), DurationMS: d / 1000, State: u.state, Sub: u.sub}
		if u.activated >= u.activating {
			unit.Active = secondsPtr(u.activated)
		}
		if u.deactivated >= u.activating {
			unit.Inactive = secondsPtr(u.deactivated)
		}
		for _, a := range u.after {
			if in[a] {
				unit.After = append(unit.After, a)
			}
		}
		if t.Summary.Slowest == nil || unit.DurationMS > t.Summary.Slowest.DurationMS {
			t.Summary.Slowest = &Slow{ID: unit.ID, DurationMS: unit.DurationMS}
		}
		t.Units = append(t.Units, unit)
	}
	if finish > 0 {
		t.CriticalChain = criticalChain(Root, all, finish)
	}
	return t
}

// criticalChain follows systemd-analyze critical-chain (src/analyze/analyze-critical-chain.c) with
// its default fuzz of 0: from the root, among the units it is ordered after (After=) that became
// active by the time the boot finished, the ones that became active last - more than one on a tie,
// latest first - and on from each of them, once per unit. The result is the units in the order the
// tool prints them.
func criticalChain(root string, all map[string]*raw, finish int64) []string {
	if all[root] == nil {
		return []string{}
	}
	chain := []string{root}
	seen := map[string]bool{}
	inRange := func(u *raw) bool { return u != nil && u.activating > 0 && u.activated > 0 && u.activated <= finish }
	var walk func(name string)
	walk = func(name string) {
		seen[name] = true
		u := all[name]
		if u == nil {
			return
		}
		deps := append([]string(nil), u.after...)
		sort.SliceStable(deps, func(i, j int) bool {
			return activatedOf(all[deps[i]]) > activatedOf(all[deps[j]])
		})
		var longest int64
		for _, d := range deps {
			if du := all[d]; inRange(du) && du.activated >= longest {
				longest = du.activated
			}
		}
		if longest == 0 {
			return
		}
		for _, d := range deps {
			if du := all[d]; !inRange(du) || du.activated != longest {
				continue
			}
			chain = append(chain, d)
			if seen[d] {
				continue
			}
			walk(d)
		}
	}
	walk(root)
	return chain
}

func activatedOf(u *raw) int64 {
	if u == nil {
		return 0
	}
	return u.activated
}

// Live is one unit's state now and when its last start began (microseconds after the kernel
// started): what the running boot's timeline takes from systemd at every read (B-153).
type Live struct {
	State, Sub string
	Activating int64
}

// LiveProps is the systemctl show of Live.
const LiveProps = "Id,ActiveState,SubState,InactiveExitTimestampMonotonic"

// LiveArgs is the systemctl call of Live.
func LiveArgs() []string { return []string{"show", "--all", "-p", LiveProps, "*"} }

// ParseLive reads LiveArgs' output.
func ParseLive(out []byte) map[string]Live {
	live := map[string]Live{}
	for _, b := range unitshow.Blocks(out) {
		if id := b["Id"]; id != "" {
			live[id] = Live{State: b["ActiveState"], Sub: b["SubState"], Activating: unitshow.Micros(b, "InactiveExitTimestampMonotonic")}
		}
	}
	return live
}

// Overlay is the running boot's timeline with the units as they are now (B-153): the bars stay the
// boot's - a kept snapshot's, or the first read's - while each unit's state and sub state are live,
// and a unit whose last start began after the boot finished is marked Restarted. The units that
// started after the boot and are not in the timeline are Later and LaterUnits. t is not changed.
func Overlay(t Timeline, live map[string]Live) Timeline {
	out := t
	out.Units = make([]Unit, len(t.Units))
	var finishUS int64
	if t.Timestamps.Finish != nil {
		finishUS = int64(*t.Timestamps.Finish*1e6 + 0.5)
	}
	in := make(map[string]bool, len(t.Units))
	for i, u := range t.Units {
		in[u.ID] = true
		if l, ok := live[u.ID]; ok {
			u.State, u.Sub = l.State, l.Sub
			u.Restarted = nil
			if finishUS > 0 && l.Activating > finishUS {
				u.Restarted = secondsPtr(l.Activating)
			}
		}
		out.Units[i] = u
	}
	if finishUS > 0 {
		out.LaterUnits = nil
		for id, l := range live {
			if l.Activating > finishUS && !in[id] {
				out.LaterUnits = append(out.LaterUnits, id)
			}
		}
		sort.Strings(out.LaterUnits)
		out.Later = len(out.LaterUnits)
	}
	return out
}
