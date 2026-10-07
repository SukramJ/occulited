package radio

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"time"
)

// The change gate (openccu-lite B-307). A change of the radio stack - a connection change, the
// hotplug's re-plan - stops the daemons, renders a new plan and starts them again. Between the stop
// and the render the run directory still holds the old plan: its <daemon>.env and its markers. A
// start that is not the change's own (an addon unit's Wants= until openccu-lite B-308, a user's
// systemctl, systemd's Restart=) used that old plan - on the way back from HmIP only,
// hmipserver started on the raw UART while the new plan put multimacd there, and hung for five
// minutes. The gate closes that window: the change writes GateFile before it stops the daemons,
// and every radio unit's ExecCondition= (/usr/libexec/occu/lite-radio-gate) refuses a start while
// it is there - a skip, not a failure, and the end of a Restart= loop. Once the new plan is
// written the change releases the units one by one, each right before its own start of it (a line
// with the unit's name in the marker: in boot order, so an outside start of rfd cannot come before
// multimacd is up), and removes the marker after its last start.
//
// The marker lives under /run, so a reboot clears it; the change removes it on every way out (a
// defer), occulited's start removes one an interrupted change left; and it carries its own
// deadline - whole seconds of /proc/uptime, the monotonic clock (a box without an RTC steps its
// wall clock) - after which the units' check lets a start through again, should every one of those
// have failed.

// GateFile is the marker, under RunDir.
const GateFile = "changing"

// HotplugGateLimit is how long the hotplug's re-plan may hold the gate: its stop, write and start
// take seconds (the detection ran before the gate closed).
const HotplugGateLimit = 3 * time.Minute

// GatePath is the marker's path under root.
func GatePath(root string) string { return shadowPath(root, GateFile) }

// GateContent is the marker's content: the deadline, in whole seconds since boot, after which the
// gate no longer holds. uptime is the time since boot now, limit how long the change may take.
func GateContent(uptime, limit time.Duration) []byte {
	return []byte(strconv.FormatInt(int64((uptime+limit+time.Second-1)/time.Second), 10) + "\n")
}

// GateReleased is the marker's content with the unit released: its name on a line of its own
// after the deadline (once).
func GateReleased(content []byte, unit string) []byte {
	lines := strings.Split(strings.TrimRight(string(content), "\n"), "\n")
	for _, l := range lines[1:] {
		if l == unit {
			return content
		}
	}
	return []byte(strings.Join(append(lines, unit), "\n") + "\n")
}

// Uptime reads the time since boot from <root>/proc/uptime; false when it cannot be read.
func Uptime(root string) (time.Duration, bool) {
	p := "/proc/uptime"
	if root != "" && root != "/" {
		p = strings.TrimSuffix(root, "/") + p
	}
	f := strings.Fields(readFile(p))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil || s < 0 {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// CloseGate writes the marker as root (the hotplug's own process; occulited goes through the
// privilege helper, see system.RadioConnections), world-readable whatever the umask.
func CloseGate(root string, limit time.Duration) error {
	up, _ := Uptime(root)
	p := GatePath(root)
	if err := os.WriteFile(p, GateContent(up, limit), 0o644); err != nil {
		return err
	}
	return os.Chmod(p, 0o644)
}

// ReleaseGate lets one unit through a closed gate (the change is about to start it); nothing when
// the gate is open.
func ReleaseGate(root, unit string) error {
	p := GatePath(root)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, GateReleased(b, unit), 0o644); err != nil {
		return err
	}
	_ = os.Chmod(tmp, 0o644)
	return os.Rename(tmp, p)
}

// OpenGate removes the marker; a missing one is no error.
func OpenGate(root string) error {
	if err := os.Remove(GatePath(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
