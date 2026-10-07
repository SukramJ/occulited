package system

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// The radio units' gate (openccu-lite B-307, radio.GateFile) as occulited's changes of the radio
// stack hold it: the connection change (RadioConnections) and the coprocessor flash
// (RadioFirmware, openccu-lite B-308). The run directory is root's, so the marker is written and
// removed through the privilege helper.

// closeRadioGate writes the marker with a deadline limit from now.
func closeRadioGate(root Root, limit time.Duration) error {
	up, _ := radio.Uptime(string(root))
	return writeFileAtomic(radio.GatePath(string(root)), radio.GateContent(up, limit), 0o644)
}

// releaseRadioGate lets one unit through the closed gate (radio.GateReleased). When that write
// fails the gate is removed instead: the change's own start must not be skipped.
func releaseRadioGate(root Root, unit string, log *slog.Logger) {
	p := radio.GatePath(string(root))
	b, err := os.ReadFile(p)
	if err != nil {
		return // open, or unreadable: nothing to release
	}
	if err := writeFileAtomic(p, radio.GateReleased(b, unit), 0o644); err != nil {
		log.Warn("radio gate: a unit could not be let through; removing the gate", "unit", unit, "err", err)
		_, _ = openRadioGate(root, log)
	}
}

// openRadioGate removes the marker: whether one was there and went, and the error when it could not
// be removed (its deadline opens it then).
func openRadioGate(root Root, log *slog.Logger) (bool, error) {
	p := radio.GatePath(string(root))
	if _, err := os.Lstat(p); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	err := os.Remove(p)
	if err != nil && !errors.Is(err, fs.ErrNotExist) && Priv != nil {
		err = remove(p)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("radio gate: the marker could not be removed; its deadline opens it", "path", p, "err", err)
		return false, err
	}
	return true, nil
}

type heldBack struct {
	unit string
	err  error
}

// startHeldBack starts those of the units that the plan runs (<unit>.enabled) and that are
// inactive because their last start was skipped - by the gate, or by an earlier plan that did not
// run them. One that is up, or that someone stopped, is left as it is.
func startHeldBack(ctx context.Context, svc ServiceManager, root Root, units []string) []heldBack {
	list, err := svc.List()
	if err != nil {
		return nil
	}
	var out []heldBack
	for _, sv := range list {
		if !slices.Contains(units, sv.ID) || sv.Running || !sv.Skipped {
			continue
		}
		if _, err := os.Stat(root.join(filepath.Join(radio.RunDir, sv.ID+".enabled"))); err != nil {
			continue
		}
		_, err := svc.Control(ctx, sv.ID, "start")
		out = append(out, heldBack{unit: sv.ID, err: err})
	}
	return out
}
