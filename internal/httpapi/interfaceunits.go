package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/health"
	"github.com/hobbyquaker/occulited/internal/system"
)

// InterfaceUnitReader is what a systemd box's service manager answers about the radio stack's
// units (task 94, section 7): system.SystemdServices. A busybox box has none, and GET /radio/health
// carries no units there.
type InterfaceUnitReader interface {
	InterfaceUnits(ctx context.Context, ifs []system.Interface) []system.InterfaceUnit
}

// unitsCache keeps the last reading of the radio stack's units for unitsTTL. The Status page polls
// /radio/health every 10 s and the Interfaces page every 5 s while something starts; two open tabs
// must not double what systemd is asked (B-83). A reading in flight is shared: the lock is held
// across it.
type unitsCache struct {
	mu    sync.Mutex
	at    time.Time
	units []system.InterfaceUnit
}

const unitsTTL = 3 * time.Second

// radioUnits reads the units, or hands out the cached reading with the starting ages moved on by
// the time since it was taken, so a page's counter never runs backwards between two answers.
func (a *SystemAPI) radioUnits(ctx context.Context) []system.InterfaceUnit {
	rd, ok := a.Services.(InterfaceUnitReader)
	if !ok {
		return nil
	}
	a.units.mu.Lock()
	defer a.units.mu.Unlock()
	if a.units.at.IsZero() || time.Since(a.units.at) >= unitsTTL {
		// InterfacesList.xml at this request: occu-init-hs485d may have rewritten it since the start
		a.units.units, a.units.at = rd.InterfaceUnits(ctx, a.Root.ReadRadio().Interfaces), time.Now()
	}
	if a.units.units == nil {
		return nil
	}
	aged := time.Since(a.units.at)
	out := make([]system.InterfaceUnit, len(a.units.units))
	for i, u := range a.units.units {
		if u.Starting && !u.Queued && u.StartingS > 0 {
			u.StartingS += int64(aged / time.Second)
		}
		if u.ActiveFor > 0 {
			u.ActiveFor += aged
		}
		out[i] = u
	}
	return out
}

// staleErrors drops from a sample's errors the interfaces whose unit became active after that
// sample was taken - the error was the unit starting, not the interface failing - and says whether
// it dropped one, so the caller can have the sampler poll again (task 94: hmipserver is ready some
// 40 s after the web UI at boot, and the sampler's minute would keep "not answering" on the pages
// for up to a minute after it answers).
func staleErrors(st *health.Status, units []system.InterfaceUnit) bool {
	if st.Polled.IsZero() {
		return false
	}
	since := time.Since(st.Polled)
	dropped := false
	for _, u := range units {
		if u.ActiveState != "active" || u.ActiveFor <= 0 || u.ActiveFor >= since {
			continue
		}
		for _, name := range u.Interfaces {
			if _, ok := st.Errors[name]; ok {
				delete(st.Errors, name)
				dropped = true
			}
		}
	}
	return dropped
}
