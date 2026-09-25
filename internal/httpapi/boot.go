package httpapi

import (
	"errors"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/bootchart"
	"github.com/hobbyquaker/occulited/internal/system"
)

// bootTimeline is GET /boot[?id=]: the startup timeline of a boot (task 93) - its units' starts, the
// manager's milestones, the critical chain. This boot's is read through systemctl and kept once the
// boot has finished.
func (a *SystemAPI) bootTimeline(w http.ResponseWriter, r *http.Request) {
	if a.BootChart == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: bootchart.ErrUnsupported.Error()})
		return
	}
	id := system.NormalizeBoot(r.URL.Query().Get("id"))
	if !system.ValidBoot(id) || (id != "" && id[0] == '-') {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "id is a boot id (32 hex digits), or 0 for this boot"})
		return
	}
	if !a.Root.IsThisBoot(id) {
		// an earlier boot: its snapshot, when it was kept
		var kept *bootchart.Timeline
		err := bootchart.ErrNoSnapshot
		if a.BootSnapshots != nil {
			kept, err = a.BootSnapshots.Load(id)
		}
		switch {
		case errors.Is(err, bootchart.ErrNoSnapshot):
			writeJSON(w, http.StatusNotFound, apiError{Error: "no-timeline", Message: "there is no timeline of that boot"})
		case err != nil:
			writeErr(w, err)
		default:
			kept.Previous = a.BootSnapshots.Previous(id)
			writeJSON(w, http.StatusOK, kept)
		}
		return
	}
	// B-153: this boot as it happened - its kept snapshot once there is one, else the first read of
	// this process - with every unit's state as it is now. The live read of the stamps alone would
	// lose every unit restarted since the boot once occulited itself restarts.
	var base *bootchart.Timeline
	if a.BootSnapshots != nil {
		if kept, err := a.BootSnapshots.Load(a.BootChart.BootID()); err == nil {
			kept.Snapshot = false
			base = kept
		}
	}
	if base == nil {
		t, err := a.BootChart.Current(r.Context())
		switch {
		case errors.Is(err, bootchart.ErrUnsupported):
			writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: err.Error()})
			return
		case err != nil:
			writeErr(w, err)
			return
		}
		base = t
	}
	// a copy: the reader keeps the timeline, and the previous boot changes when this one is kept
	answer := *base
	if live, err := a.BootChart.Live(r.Context()); err == nil {
		answer = bootchart.Overlay(answer, live)
	}
	if a.BootSnapshots != nil {
		answer.Previous = a.BootSnapshots.Previous(answer.BootID)
	}
	writeJSON(w, http.StatusOK, answer)
}
