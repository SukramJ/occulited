package system

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ---- editing a unit on a read-only rootfs (task 27.4) -----------------------------------------
//
// The maintainer wants to edit a service or timer from the UI. / is ext4 ro on every product and
// /etc/systemd/system is on it, so nothing can be edited in place. systemd reads drop-ins from
// three places in increasing precedence:
//
//	/usr/lib/systemd/system/<unit>.d/   the shipped unit's own      (rootfs, ro)
//	/run/systemd/system/<unit>.d/       runtime                      (tmpfs, writable)
//	/etc/systemd/system/<unit>.d/       admin                        (rootfs, ro - unusable here)
//
// So an edit is a drop-in in /run, and it is the B-26 mechanism again: the text lives on the
// userfs in occulited's state directory, is written into /run and daemon-reloaded, and is replayed
// at every start because /run does not survive a boot. The shipped unit is never touched, which is
// what lets a firmware update bring its new units intact with the overrides re-applied on top.
//
// What is edited is THE OVERRIDE, not the unit file. A drop-in adds and overrides directives and
// cannot delete them, and list-valued ones - ExecStart=, ExecStartPre=, Environment=, After= -
// accumulate unless reset with an empty assignment first. A page that showed the whole unit and
// saved it back would silently produce a unit with two ExecStart= lines. This is the same reason
// `systemctl edit` edits an override, and the page shows the effective unit read-only beside it.

// UnitOverride is what the page gets: the effective unit as systemd sees it, and the override the
// user has stored (empty when none).
type UnitOverride struct {
	Unit      string `json:"unit"`
	Effective string `json:"effective"` // systemctl cat: every fragment and drop-in that applies
	Override  string `json:"override"`  // occulited's own drop-in, from the userfs
	Path      string `json:"path"`      // where it is applied: /run/systemd/system/<unit>.d/50-occulite.conf
}

const overrideDropIn = "50-occulite.conf"

// overrideDir is where the stored overrides live on the userfs: <state>/unit-overrides/<unit>.conf.
func (s SystemdServices) overrideDir() string {
	if s.SwitchFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.SwitchFile), "unit-overrides")
}

// unitFor turns a service id into the unit name the way Control does, and admits timers too - a
// timer's schedule is exactly the kind of thing a person wants to change.
func unitFor(id string) (string, error) {
	if !plausibleUnit(id) {
		return "", fmt.Errorf("not a unit name")
	}
	switch {
	case strings.HasSuffix(id, ".service"), strings.HasSuffix(id, ".timer"):
		return id, nil
	default:
		return id + ".service", nil
	}
}

func runDropIn(unit string) string {
	return filepath.Join("/run/systemd/system", unit+".d", overrideDropIn)
}

// ReadUnitOverride shows the effective unit and the stored override.
func (s SystemdServices) ReadUnitOverride(ctx context.Context, id string) (UnitOverride, error) {
	unit, err := unitFor(id)
	if err != nil {
		return UnitOverride{}, err
	}
	out, err := s.run(ctx, "cat", "--no-pager", "--", unit)
	if err != nil {
		return UnitOverride{}, fmt.Errorf("systemctl cat %s: %w: %s", unit, err, strings.TrimSpace(string(out)))
	}
	uo := UnitOverride{Unit: unit, Effective: string(out), Path: runDropIn(unit)}
	if dir := s.overrideDir(); dir != "" {
		if b, err := os.ReadFile(filepath.Join(dir, unit+".conf")); err == nil {
			uo.Override = string(b)
		}
	}
	return uo, nil
}

// SetUnitOverride stores the override on the userfs, applies it into /run and reloads. An empty
// override removes both. Nothing on the rootfs is written.
func (s SystemdServices) SetUnitOverride(ctx context.Context, id, override string) (UnitOverride, error) {
	unit, err := unitFor(id)
	if err != nil {
		return UnitOverride{}, err
	}
	// An empty override means "remove it" and is not checked; anything else must look like a unit.
	if strings.TrimSpace(override) != "" {
		if err := checkOverride(override); err != nil {
			return UnitOverride{}, err
		}
	}
	dir := s.overrideDir()
	if dir == "" {
		return UnitOverride{}, fmt.Errorf("no state directory: overrides cannot be kept")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return UnitOverride{}, err
	}
	stored := filepath.Join(dir, unit+".conf")
	if strings.TrimSpace(override) == "" {
		if err := os.Remove(stored); err != nil && !os.IsNotExist(err) {
			return UnitOverride{}, err
		}
		if err := s.removeDropIn(unit); err != nil {
			return UnitOverride{}, err
		}
	} else {
		if err := os.WriteFile(stored, []byte(override), 0o640); err != nil {
			return UnitOverride{}, err
		}
		if err := s.applyDropIn(unit, override); err != nil {
			return UnitOverride{}, err
		}
	}
	if out, err := s.run(ctx, "daemon-reload"); err != nil {
		return UnitOverride{}, fmt.Errorf("daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return s.ReadUnitOverride(ctx, id)
}

// applyDropIn writes the override into /run through the privilege helper (the daemon is
// unprivileged and /run/systemd/system is root's).
func (s SystemdServices) applyDropIn(unit, override string) error {
	if Priv == nil {
		return fmt.Errorf("no privilege helper")
	}
	path := runDropIn(unit)
	if err := Priv.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("%s: %w", filepath.Dir(path), err)
	}
	if err := Priv.WriteFile(path, []byte(override), 0o644); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (s SystemdServices) removeDropIn(unit string) error {
	if Priv == nil {
		return nil
	}
	if err := Priv.Remove(runDropIn(unit)); err != nil && !os.IsNotExist(err) {
		// the helper answers a missing file as an error string; treat "no such file" as done
		if !strings.Contains(err.Error(), "no such file") {
			return err
		}
	}
	return nil
}

// checkOverride refuses what would break systemd or is plainly not a unit fragment. It does not
// try to validate directives - systemd is the authority - but a file with no [Section] at all is
// a mistake every time, and a size cap keeps a pasted log out of /run.
func checkOverride(override string) error {
	if len(override) > 64*1024 {
		return fmt.Errorf("override is larger than 64 KiB")
	}
	if strings.ContainsRune(override, 0) {
		return fmt.Errorf("override contains a NUL byte")
	}
	hasSection := false
	for _, line := range strings.Split(override, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") && strings.HasSuffix(l, "]") {
			hasSection = true
			break
		}
	}
	if !hasSection {
		return fmt.Errorf("an override needs a [Section] header, e.g. [Service]")
	}
	return nil
}

// ReplayUnitOverrides writes every stored override into /run at start. Like the switch replay it
// is loud about what it could not do: an override that is silently missing is a box that behaves
// differently from what the page says.
func (s SystemdServices) ReplayUnitOverrides(ctx context.Context) (applied []string, problems []string) {
	dir := s.overrideDir()
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // no overrides is the normal state
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".conf") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		unit := strings.TrimSuffix(name, ".conf")
		if !plausibleUnit(unit) {
			problems = append(problems, name+": not a unit name")
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", unit, err))
			continue
		}
		if err := checkOverride(string(b)); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", unit, err))
			continue
		}
		if err := s.applyDropIn(unit, string(b)); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", unit, err))
			continue
		}
		applied = append(applied, unit)
	}
	if len(applied) > 0 {
		if out, err := s.run(ctx, "daemon-reload"); err != nil {
			problems = append(problems, fmt.Sprintf("daemon-reload: %v %s", err, strings.TrimSpace(string(out))))
		}
	}
	return applied, problems
}
