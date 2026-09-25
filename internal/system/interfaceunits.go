package system

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// ---- the radio stack while the box boots (task 94, section 7) ----------------------------------
//
// After task 94 the web UI answers at about 24 s on the Charly and hmipserver's JVM is ready at
// about 65 s: for most of a minute the Status and Interfaces pages are up and HmIP is not. An
// interface process whose unit systemd is still starting is not down, and the pages must not say
// it is - the maintainer's "hmipserver does not come up" would otherwise be everyone's first
// impression of a box.

// InterfaceUnit is one unit of the radio stack as the Status and Interfaces pages show it.
type InterfaceUnit struct {
	// Unit is the unit's name without .service: the detection (RadioDetectionUnit), multimacd, rfd,
	// hmipserver, hs485d, hmlangw.
	Unit string `json:"unit"`
	// Interfaces are the InterfacesList.xml names the unit's process serves (rfd: BidCos-RF,
	// hmipserver: HmIP-RF and VirtualDevices, hs485d: BidCos-Wired); empty for the others.
	Interfaces  []string `json:"interfaces"`
	ActiveState string   `json:"active_state"`
	SubState    string   `json:"sub_state"`
	// Starting: systemd is starting the unit - it is activating, or it is inactive with a start job
	// that waits for the units it is ordered after (Queued: rfd and hmipserver while multimacd
	// starts). A unit waiting to be restarted after a failure (auto-restart) is not starting.
	Starting bool `json:"starting,omitempty"`
	Queued   bool `json:"queued,omitempty"`
	// StartingS is how long the unit has been starting, in whole seconds: InactiveExitTimestamp-
	// Monotonic against /proc/uptime (B-61: a box without an RTC has a wrong wall clock at boot, and
	// the clock may be stepped while the unit starts). Absent for a queued unit, which has not begun.
	StartingS int64 `json:"starting_s,omitempty"`
	// ActiveFor is how long the unit has been active (ActiveEnterTimestampMonotonic), 0 when it is
	// not; the API compares it with the radio sampler's last poll.
	ActiveFor time.Duration `json:"-"`
}

// RadioDetectionUnit writes /var/hm_mode: until it has run, the box knows no radio module, and the
// Interfaces page says the detection is still running rather than that there is no module.
const RadioDetectionUnit = "occu-init-rf-hardware"

// radioStackUnits are the units InterfaceUnits reads, in boot order.
var radioStackUnits = []string{RadioDetectionUnit, "multimacd", "rfd", "hmipserver", "hs485d", "hmlangw"}

// UnitOfInterface names the unit behind an InterfacesList.xml entry: by its name for the four the
// firmware writes, else by the URL's port - the firmware's own (2000, 2001, 2010, 9292), the lite
// image's internal ones (32000, 32001, 32010, 39292) and the TLS faces (42000, 42001, 42010, 49292).
// "" for an entry no unit of the radio stack serves.
func UnitOfInterface(i Interface) string {
	switch i.Name {
	case "BidCos-RF":
		return "rfd"
	case "BidCos-Wired":
		return "hs485d"
	case "HmIP-RF", "VirtualDevices":
		return "hmipserver"
	}
	switch urlPort(i.URL) {
	case 2001, 32001, 42001:
		return "rfd"
	case 2000, 32000, 42000:
		return "hs485d"
	case 2010, 32010, 42010, 9292, 39292, 49292:
		return "hmipserver"
	}
	return ""
}

// urlPort is the port of an interface URL (xmlrpc_bin://127.0.0.1:2001, xmlrpc://127.0.0.1:9292/groups); 0 without one.
func urlPort(raw string) int {
	_, rest, ok := strings.Cut(raw, "://")
	if !ok {
		return 0
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	i := strings.LastIndexByte(rest, ':')
	if i < 0 {
		return 0
	}
	n, err := strconv.Atoi(rest[i+1:])
	if err != nil {
		return 0
	}
	return n
}

// unitStarting is the verdict on one unit's state: starting when activating (but not waiting for
// an automatic restart after a failure), queued when inactive with a job - a start job waiting for
// the units it is ordered after. `systemctl show` prints Job= empty when there is none.
func unitStarting(active, sub, job string) (starting, queued bool) {
	switch {
	case active == "activating":
		return !strings.HasPrefix(sub, "auto-restart"), false
	case active == "inactive" && job != "" && job != "0":
		return true, true
	}
	return false, false
}

// monotonicAge is how long ago a *TimestampMonotonic (microseconds since boot) was, against the
// uptime; false when either is unknown (0 is systemd's "never").
func monotonicAge(v string, uptime time.Duration) (time.Duration, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || uptime <= 0 {
		return 0, false
	}
	age := uptime - time.Duration(n)*time.Microsecond
	if age < 0 {
		age = 0
	}
	return age, true
}

// InterfaceUnits reads the radio stack's units with one `systemctl show` for all of them (B-60's
// rule), for the Status and Interfaces pages; ifs is InterfacesList.xml as ReadRadio has it, read by
// the caller at the request (a list occu-init-hs485d rewrites after occulited started counts). A
// unit this box does not have (LoadState not-found) is left out; nil when systemd did not answer.
func (s SystemdServices) InterfaceUnits(ctx context.Context, ifs []Interface) []InterfaceUnit {
	units := make([]string, len(radioStackUnits))
	for i, u := range radioStackUnits {
		units[i] = u + ".service"
	}
	shown := s.showAll(ctx, units, "LoadState", "ActiveState", "SubState", "Job", "InactiveExitTimestampMonotonic", "ActiveEnterTimestampMonotonic")
	if len(shown) == 0 {
		return nil
	}
	uptime := s.uptime()
	out := []InterfaceUnit{}
	for _, name := range radioStackUnits {
		p, ok := shown[name+".service"]
		if !ok || p["LoadState"] == "" || p["LoadState"] == "not-found" {
			continue
		}
		u := InterfaceUnit{Unit: name, Interfaces: []string{}, ActiveState: p["ActiveState"], SubState: p["SubState"]}
		for _, i := range ifs {
			if UnitOfInterface(i) == name {
				u.Interfaces = append(u.Interfaces, i.Name)
			}
		}
		u.Starting, u.Queued = unitStarting(u.ActiveState, u.SubState, p["Job"])
		if u.Starting && !u.Queued {
			if age, ok := monotonicAge(p["InactiveExitTimestampMonotonic"], uptime); ok {
				u.StartingS = int64(age / time.Second)
			}
		}
		if u.ActiveState == "active" {
			if age, ok := monotonicAge(p["ActiveEnterTimestampMonotonic"], uptime); ok {
				u.ActiveFor = age
			}
		}
		out = append(out, u)
	}
	return out
}

// applyStarting sets a listed service's starting state from its list-units row and its show props:
// the Services page's "starting" beside running, stopped and failed.
func applyStarting(sv *Service, row unitRow, props map[string]string, uptime time.Duration) {
	starting, _ := unitStarting(row.Active, row.Sub, "")
	if !starting {
		return
	}
	sv.Starting = true
	if age, ok := monotonicAge(props["InactiveExitTimestampMonotonic"], uptime); ok {
		sv.StartingS = int64(age / time.Second)
	}
}
