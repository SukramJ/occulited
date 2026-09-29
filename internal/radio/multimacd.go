package radio

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// multimacd's start (openccu-lite B-275). The multiplexer opens the radio module, asks it for its
// state and then for its version - the one request the module takes ~100 ms to answer, where every
// other one takes 3-4 ms. A radio frame the never-reset module forwards in that window is read as a
// wrong answer, the retries fail on the rest of the burst, and multimacd logs `GetVersion finally
// failed.` and runs on without a version: its endpoints exist, the status file carries its pid, the
// unit is active - and rfd reads empty versions and no serial at every one of its restarts while
// hmipserver never finishes its adapter init, until someone reboots (seen once, dev.31's first boot
// on a lab system). multimacd itself neither exits nor restarts for it.
//
// So the ready step reads what multimacd logged: the version line makes it ready, a failure line
// fails the start, and systemd restarts the unit with its backoff (Restart=on-failure, 2 s doubling
// to 5 min; a module that never answers ends in task 283's crash-loop warning). Neither line within
// multimacdStartWait - a level that hides them, a multimacd with other words - leaves the endpoint
// check as it was, with a line saying so.

// MultimacdMaxLevel is the quietest level multimacd runs with. Its log level is eQ-3's scale, 1 the
// most verbose: at 2 it logs its start - `Copro application running.`, the version line, SGTIN,
// address, serial - and the failure lines; at 3 and above it logs nothing at its start (measured
// level by level on a lab system, 2026-09-29). The ready check needs those lines, so multimacd's
// level offers only 1 and 2 (openccu-lite task 297: LOGLEVEL_MULTIMACD, its own, no fallback to
// rfd's; anything but 1 reads as 2), and MultimacdLevel stays as the guard that both the boot's
// render and the Log page's write pass through. A more verbose level (1) stays.
const MultimacdMaxLevel = "2"

// MultimacdLevel is the level multimacd is started with for a configured one: the configured
// level when it is MultimacdMaxLevel or more verbose, MultimacdMaxLevel otherwise. What is not a
// number is returned as it is.
func MultimacdLevel(configured string) string {
	n, err := strconv.Atoi(configured)
	limit, _ := strconv.Atoi(MultimacdMaxLevel)
	if err != nil || n <= limit {
		return configured
	}
	return MultimacdMaxLevel
}

// multimacdStartWait is how long the ready step waits for one of the lines. The version line comes
// ~100 ms after the start on a lab system, the failure line 8 ms after; the wait is for journald's
// lag, not the module's.
const multimacdStartWait = 5 * time.Second

// multimacdStart is the ready step's verdict on multimacd's own lines.
type multimacdStart int

const (
	multimacdStartUnknown multimacdStart = iota // neither line (yet)
	multimacdStartVersion                       // Vapp=... : the module answered
	multimacdStartFailed                        // the module did not answer, or none was found
)

// multimacdVersionNeedle is the line multimacd logs with the module's versions
// (`Vapp=040412 Vbl=01000C Vhmos=014000`: application, bootloader, HmIP OS).
const multimacdVersionNeedle = "Vapp="

// multimacdFailureNeedles are the lines after which multimacd is of no use to its clients.
var multimacdFailureNeedles = []string{
	"GetVersion finally failed.", // the version request got no usable answer (the burst)
	"No Coprocessor detected",    // nothing answered at all
}

// multimacdStartLine looks through multimacd's lines for the version or a failure; the first of
// either decides, and the line is returned for the log.
func multimacdStartLine(text string) (multimacdStart, string) {
	for _, l := range strings.Split(text, "\n") {
		if strings.Contains(l, multimacdVersionNeedle) {
			return multimacdStartVersion, strings.TrimSpace(l)
		}
		for _, n := range multimacdFailureNeedles {
			if strings.Contains(l, n) {
				return multimacdStartFailed, strings.TrimSpace(l)
			}
		}
	}
	return multimacdStartUnknown, ""
}

// multimacdStarted polls the main process's journal for up to multimacdStartWait and returns the
// verdict and its line.
func multimacdStarted(ctx context.Context, d Detector, mainPID int) (multimacdStart, string) {
	verdict, line := multimacdStartUnknown, ""
	waitFor(d, multimacdStartWait, func() bool {
		verdict, line = multimacdStartLine(daemonOutput(ctx, d, mainPID))
		return verdict != multimacdStartUnknown
	})
	return verdict, line
}
