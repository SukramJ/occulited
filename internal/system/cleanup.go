package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Legacy leftovers (roadmap task 21): what an update from OpenCCU leaves on the userfs that
// openccu-lite never reads - the ReGa database above all. Removing them frees space and ends the
// confusion of two sources of names. Since task 250 they go by themselves after the first start
// (RemoveLeftoversOnce); the Backup page's panel is gone.
//
// The way back to OpenCCU is a backup taken before the migration in every case (D-35, revised
// 2026-09-08) - openccu-lite does not claim you can flash OpenCCU over lite and find your box as
// you left it. Removing these makes that worse rather than newly true: without them you cannot
// even recover the ReGa database as it stood on the day of the switch. The page says so.

// LegacyItem is one removable leftover.
type LegacyItem struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Present bool   `json:"present"`
	Why     string `json:"why"`
	// WayBack: this is ReGa state. A backup from before the migration is the way back either
	// way; removing this takes away even the frozen copy the box still carries.
	WayBack bool `json:"way_back"`
	// Also are further paths that go with Path (an addon's config directory, web tree and rc.d
	// entry beside its directory): counted in Bytes, removed together, and Present when any of
	// them is - a half-removed addon is still a leftover.
	Also []string `json:"also,omitempty"`
	// Unit is the systemd unit to stop before the paths go (an addon that may still be running).
	Unit string `json:"-"`
	// after runs once the paths are gone: what the leftover's own uninstall would do beyond them.
	after func(Root) error
}

var legacyItems = []LegacyItem{
	{ID: "regadom", Path: "/etc/config/homematic.regadom", Why: "the ReGa database: programs, system variables, and the names openccu-lite imported at its first boot", WayBack: true},
	{ID: "regadom-bak", Path: "/etc/config/homematic.regadom.bak", Why: "the ReGa's previous database", WayBack: true},
	{ID: "measurement", Path: "/etc/config/measurement", Why: "the WebUI's diagram data"},
	{ID: "userprofiles", Path: "/etc/config/userprofiles", Why: "WebUI user profiles (favourites, page settings)"},
	{ID: "rega-tmp", Path: "/etc/config/rega", Why: "ReGa working files"},
	// task 37: OpenCCU's NEO Server, which cannot work without the ReGa; the removal is what the
	// addon's own uninstall does - its four paths and the neoDisabled marker - plus the wrapper's
	// twin, its hm_addons.cfg entry and the watchdog line it left in root's crontab
	{ID: "neoserver", Path: NeoServerDir, Also: neoServerPaths[1:], Unit: "addon-" + NeoServerID + ".service",
		Why:   "mediola's NEO Server, unpacked by OpenCCU: it posts to /tclrega.exe (the ReGa) and /api/homematic.cgi (the WebUI's CGI stack), neither of which openccu-lite has",
		after: Root.removeNeoServerRemains},
}

// LegacyLeftovers lists the items with their presence and size.
func (r Root) LegacyLeftovers() []LegacyItem {
	out := make([]LegacyItem, 0, len(legacyItems))
	for _, it := range legacyItems {
		for _, path := range append([]string{it.Path}, it.Also...) {
			p := r.join(path)
			st, err := os.Lstat(p)
			if err != nil {
				continue
			}
			it.Present = true
			if st.IsDir() {
				_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
					if err == nil && !d.IsDir() {
						if i, err := d.Info(); err == nil {
							it.Bytes += i.Size()
						}
					}
					return nil
				})
			} else {
				it.Bytes += st.Size()
			}
		}
		out = append(out, it)
	}
	return out
}

// RemoveLegacy deletes the named items (all present ones when ids is empty) through the
// privilege boundary. Unknown ids are an error before anything is touched.
func (r Root) RemoveLegacy(ids []string) ([]string, error) {
	return r.RemoveLegacyWith(ids, nil, nil)
}

// RemoveLegacyWith is RemoveLegacy with the box's hooks: stop is called with an item's unit
// before its paths go, reload once at the end when an item had a unit (the generator's unit
// disappears with the rc.d entry only at the next daemon-reload). Either may be nil.
func (r Root) RemoveLegacyWith(ids []string, stop func(unit string), reload func()) ([]string, error) {
	known := map[string]LegacyItem{}
	for _, it := range legacyItems {
		known[it.ID] = it
	}
	if len(ids) == 0 {
		for _, it := range r.LegacyLeftovers() {
			if it.Present {
				ids = append(ids, it.ID)
			}
		}
	}
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			return nil, fmt.Errorf("unknown item %q", id)
		}
	}
	sort.Strings(ids)
	var removed []string
	stopped := false
	for _, id := range ids {
		it := known[id]
		paths := append([]string{it.Path}, it.Also...)
		present := false
		for _, path := range paths {
			if _, err := os.Lstat(r.join(path)); !errors.Is(err, os.ErrNotExist) {
				present = true
			}
		}
		if !present {
			continue
		}
		if it.Unit != "" && stop != nil {
			stop(it.Unit)
			stopped = true
		}
		for _, path := range paths {
			p := r.join(path)
			if _, err := os.Lstat(p); errors.Is(err, os.ErrNotExist) {
				continue
			}
			// RemoveAll on a symlink removes the link, never what it points to
			if err := Priv.RemoveAll(p); err != nil {
				return removed, fmt.Errorf("%s: %w", path, err)
			}
		}
		if it.after != nil {
			if err := it.after(r); err != nil {
				return removed, fmt.Errorf("%s: %w", id, err)
			}
		}
		removed = append(removed, id)
	}
	if stopped && reload != nil {
		reload()
	}
	return removed, nil
}

// Task 250 (the maintainer: "remove the 'leftovers from ccu' panel from system-backup. instead just
// kill these files after first boot of openccu-lite, there is no way back except they have a backup
// from (open)ccu before migration to lite"): the leftovers go once, by themselves, after the first
// start on openccu-lite has done what it needs of them - the names imported from the ReGa database
// (or that import settled: given up, or not needed). The radio identity, the keys and the interface
// configuration are not among them and are not touched. The way back is the (Open)CCU's own backup
// from before the switch.

// LeftoversMarker is the file (in the state directory) that says the removal ran.
const LeftoversMarker = "ccu-leftovers-removed.json"

// LeftoverRun is what one run did, as the marker keeps it.
type LeftoverRun struct {
	At      string   `json:"at"`
	Removed []string `json:"removed"`
	Paths   []string `json:"paths,omitempty"`
	Freed   int64    `json:"freed_bytes"`
}

// ErrMigrationIncomplete: the first start's own migration has not finished; nothing is removed and
// the next start tries again.
var ErrMigrationIncomplete = errors.New("the switch from the CCU is not complete")

// RemoveLeftoversOnce removes the CCU's leftovers (legacyItems, nothing else) unless the marker
// says it ran. importSettled is the first-boot name import's state: false while the ReGa database
// still has to be read (ErrMigrationIncomplete then). A system with nothing left over records an
// empty run, so a later restore of a CCU backup is not swept behind the user's back. ran is false
// when the marker was there already.
func (r Root) RemoveLeftoversOnce(stateDir string, importSettled bool, now time.Time, stop func(unit string), reload func()) (run LeftoverRun, ran bool, err error) {
	marker := filepath.Join(stateDir, LeftoversMarker)
	if _, err := os.Stat(marker); err == nil {
		return LeftoverRun{}, false, nil
	}
	if r.HasReGa() {
		return LeftoverRun{}, false, fmt.Errorf("%w: the ReGa runs on this system", ErrMigrationIncomplete)
	}
	if !importSettled {
		return LeftoverRun{}, false, fmt.Errorf("%w: the names from the ReGa database are not imported yet", ErrMigrationIncomplete)
	}
	sizes := map[string]int64{}
	paths := map[string][]string{}
	for _, it := range r.LegacyLeftovers() {
		if it.Present {
			sizes[it.ID] = it.Bytes
			for _, p := range append([]string{it.Path}, it.Also...) {
				if _, err := os.Lstat(r.join(p)); err == nil {
					paths[it.ID] = append(paths[it.ID], p)
				}
			}
		}
	}
	removed, err := r.RemoveLegacyWith(nil, stop, reload)
	run = LeftoverRun{At: now.UTC().Format(time.RFC3339), Removed: removed}
	for _, id := range removed {
		run.Freed += sizes[id]
		run.Paths = append(run.Paths, paths[id]...)
	}
	if run.Removed == nil {
		run.Removed = []string{}
	}
	if err != nil {
		return run, true, err // no marker: what is left is tried again at the next start
	}
	b, _ := json.Marshal(run)
	if werr := os.WriteFile(marker, append(b, '\n'), 0o600); werr != nil {
		return run, true, fmt.Errorf("the marker: %w", werr)
	}
	return run, true, nil
}
