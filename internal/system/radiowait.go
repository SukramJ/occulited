package system

import (
	"context"
	"strings"
	"time"
)

// Addon starts wait for the radio stack (openccu-lite B-307). During a connection change or a
// coprocessor flash the radio daemons are stopped on purpose; an addon started meanwhile comes up
// without its interfaces (and before openccu-lite B-308 its unit's Wants= pulled them in on the old
// plan's files). So occulited's own addon starts wait while one runs: the addon supervisor pauses
// (CrashLoops.Paused), and a start or restart of an addon unit through the API waits until the
// change is done.

// radioIdlePoll is WaitRadioIdle's step; a test shortens it.
var radioIdlePoll = 250 * time.Millisecond

// AnyBusy is busy while any of the given answers is (nil entries are skipped).
func AnyBusy(busy ...func() bool) func() bool {
	return func() bool {
		for _, b := range busy {
			if b != nil && b() {
				return true
			}
		}
		return false
	}
}

// WaitRadioIdle returns once busy says no more, or with ctx's error; how long it waited. A nil busy
// never waits.
func WaitRadioIdle(ctx context.Context, busy func() bool) (time.Duration, error) {
	if busy == nil || !busy() {
		return 0, nil
	}
	start := time.Now()
	t := time.NewTicker(radioIdlePoll)
	defer t.Stop()
	for busy() {
		select {
		case <-ctx.Done():
			return time.Since(start), ctx.Err()
		case <-t.C:
		}
	}
	return time.Since(start), nil
}

// AddonStartAction says whether a control of the unit starts an addon's unit: start, restart and
// their conditional forms on addon-<id>(.service).
func AddonStartAction(unit, action string) bool {
	if !strings.HasPrefix(strings.TrimSuffix(unit, ".service"), "addon-") {
		return false
	}
	switch action {
	case "start", "restart", "try-restart", "reload-or-restart", "try-reload-or-restart":
		return true
	}
	return false
}
