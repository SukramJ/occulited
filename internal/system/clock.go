package system

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ClockStateFile is what the fork's occu-clock-valid.service writes when its gate opens (task 94,
// section 5): "rtc" (occu-init-rtc set a plausible time from a real-time clock), "ntp" (chrony
// reported a sync) or "timeout" (neither within its time - a box without an RTC and without a time
// server at boot - so the radio stack started with whatever clock the box had). No file: an image
// without the gate, and nothing to say.
const ClockStateFile = "/run/occulite/clock-state"

// ClockRTCImplausibleFile is what the gate writes when the real-time clock's time was outside the
// window it trusts (openccu-lite task 299): the time it read, RFC 3339, so the page can name it.
const ClockRTCImplausibleFile = "/run/occulite/clock-rtc-implausible"

// ClockStatus is the Status page's clock notice.
type ClockStatus struct {
	// State is the file's word: rtc, ntp, manual (set by hand), or an untrusted one - timeout,
	// rtc-implausible (the real-time clock's time was outside the window and no time server
	// answered), ntp-implausible (the time server's time was outside it).
	State string `json:"state"`
	// Synchronised is false only after an untrusted state while chrony still reports no
	// synchronisation; then the page says the clock is not synchronised.
	Synchronised bool `json:"synchronised"`
	// RTCImplausible is the time the real-time clock gave and the gate refused, when it did.
	RTCImplausible string `json:"rtc_implausible,omitempty"`
}

// ClockCheck answers the clock's state for the Status page. After a timeout it asks chrony
// (`chronyc -n tracking`, "Leap status : Normal") at most every clockRecheck, and once chrony has
// said Normal it does not ask again this run: the notice is about the boot's gate, and a clock that
// synchronised after it is fine.
type ClockCheck struct {
	Root Root
	// Run runs chronyc; nil = exec directly as occulited's own user, not through the helper:
	// tracking is a monitoring command chronyd answers for any local user (measured as the occulite
	// user on the x86_64 lab box, 2026-09-12), and the helper has no reason to run it as root.
	Run Runner
	Now func() time.Time // nil = time.Now

	mu      sync.Mutex
	synced  bool
	checked time.Time
}

// clockRecheck is how often chrony is asked while the clock is not synchronised.
const clockRecheck = 30 * time.Second

// Status is nil without the state file, else the file's state and the verdict.
func (c *ClockCheck) Status(ctx context.Context) *ClockStatus {
	state := strings.TrimSpace(readFile(c.Root.join(ClockStateFile)))
	if state == "" {
		return nil
	}
	st := &ClockStatus{State: state, Synchronised: true}
	switch state {
	case "rtc", "ntp", "manual":
	default:
		st.Synchronised = c.chronySynced(ctx)
	}
	st.RTCImplausible = strings.TrimSpace(readFile(c.Root.join(ClockRTCImplausibleFile)))
	return st
}

func (c *ClockCheck) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *ClockCheck) chronySynced(ctx context.Context) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.synced {
		return true
	}
	now := c.now()
	// a clock stepped backwards (now before the last check) asks again at once
	if !c.checked.IsZero() && !now.Before(c.checked) && now.Sub(c.checked) < clockRecheck {
		return false
	}
	c.checked = now
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out []byte
	var err error
	if c.Run != nil {
		out, err = c.Run(ctx, "chronyc", "-n", "tracking")
	} else {
		out, err = exec.CommandContext(ctx, "chronyc", "-n", "tracking").Output()
	}
	if err != nil {
		return false // no chrony to ask: the gate's timeout stands
	}
	c.synced = leapNormal(string(out))
	return c.synced
}

// leapNormal reads chronyc tracking's "Leap status : Normal"; "Not synchronised" and anything
// else is not.
func leapNormal(tracking string) bool {
	for _, line := range strings.Split(tracking, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) == "Leap status" {
			return strings.TrimSpace(v) == "Normal"
		}
	}
	return false
}
