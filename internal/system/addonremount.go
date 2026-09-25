package system

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// A refused remount of the system partition (D-66). No root addon unit has CAP_SYS_ADMIN, so an
// addon script written for a CCU - `mount -o remount,rw /`, its writes, `mount -o remount,ro /` -
// gets "mount: /: permission denied." (util-linux; busybox says "mount: permission denied (are you
// root?)") in the unit's output and goes on; what it writes into /firmware/rftypes lands in the
// writable extension directory. The Services and Addons pages say so beside the addon: a hint that
// the addon still writes the CCU way and works, not an error.
//
// One journalctl call for every addon: the current boot, grepped by journalctl itself for the
// message, the unit name as the only field. journalctl exits 1 when nothing matches (B-168), so
// that exit with no output is "none". Cached for remountCacheTTL: the two pages poll.

const remountCacheTTL = 30 * time.Second

// remountPattern is journalctl's -g (PCRE2, case-insensitive because all lower case): both
// mount implementations' messages.
const remountPattern = "^mount: .*permission denied"

type remountScan struct {
	mu  sync.Mutex
	at  time.Time
	ids map[string]bool
}

// RemountRefused answers the ids of the addons whose unit output in this boot holds a refused
// remount. Empty on a busybox box or when journalctl is not there.
func (a *SystemdAddons) RemountRefused(ctx context.Context) map[string]bool {
	a.remount.mu.Lock()
	defer a.remount.mu.Unlock()
	if a.remount.ids != nil && time.Since(a.remount.at) < remountCacheTTL {
		return a.remount.ids
	}
	ids := map[string]bool{}
	args := append([]string{"-b", "-q", "--no-pager", "-o", "json", "--output-fields=_SYSTEMD_UNIT", "-g", remountPattern}, JournalFileArgs(a.Scripts.Root)...)
	var out []byte
	var err error
	if a.Journalctl != nil {
		out, err = a.Journalctl(ctx, args...)
	} else {
		out, err = exec.CommandContext(ctx, "journalctl", args...).Output()
	}
	if err != nil && len(out) == 0 {
		// nothing matched (exit 1), or no journalctl: nothing to mark; try again next time
		a.remount.at, a.remount.ids = time.Now(), ids
		return ids
	}
	for _, id := range remountUnits(out) {
		ids[id] = true
	}
	a.remount.at, a.remount.ids = time.Now(), ids
	return ids
}

// remountUnits picks the addon ids out of journalctl's JSON lines: _SYSTEMD_UNIT
// "addon-<id>.service" and nothing else (a remount refused to some other unit is not an addon's).
func remountUnits(out []byte) []string {
	var ids []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e struct {
			Unit string `json:"_SYSTEMD_UNIT"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		id, ok := strings.CutPrefix(e.Unit, "addon-")
		if !ok {
			continue
		}
		id, ok = strings.CutSuffix(id, ".service")
		if !ok || id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}
