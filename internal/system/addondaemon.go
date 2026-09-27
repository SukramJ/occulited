package system

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// openccu-lite B-158: an addon whose daemon died showed as Completed. The generated addon units
// are Type=oneshot with RemainAfterExit=yes: the rc.d start puts the daemon in the background and
// returns 0, so the unit stays active (exited) whether the daemon runs or ended. The 30.1 rule
// reads such a unit with an empty cgroup as finished - right for an addon that only prepares
// things, wrong for one whose daemon died (mosquitto on the Charly, "Address in use").
//
// An addon keeps a daemon when its manifest says so (runtime.daemon), or when occulited has seen
// its unit hold a process after a start (learned, kept in the state dir until the addon is
// removed). Such an addon's empty, finished unit is Ended: shown red as Exited, with Start
// offered (a start of the active unit is a restart, SystemdServices.Control), the time and the
// unit's last journal lines, and a Status warning.

// AddonDaemons is what occulited learned: the addons whose unit held a process after a start.
type AddonDaemons struct {
	// Path is the state file (<state>/addon-daemons.json); empty = memory only.
	Path string

	mu     sync.Mutex
	seen   map[string]time.Time
	loaded bool
	lines  map[string]endedLines // an ended unit's last lines, for endedLinesTTL
}

type endedLines struct {
	read time.Time
	at   string
	log  []string
}

// endedLinesTTL: the Services page polls every few seconds; the journal is asked at most this often
// per ended unit.
const endedLinesTTL = 30 * time.Second

func (d *AddonDaemons) load() {
	if d.loaded {
		return
	}
	d.loaded = true
	d.seen = map[string]time.Time{}
	if d.Path == "" {
		return
	}
	raw, err := os.ReadFile(d.Path)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, &d.seen)
	if d.seen == nil {
		d.seen = map[string]time.Time{}
	}
}

func (d *AddonDaemons) save() {
	if d.Path == "" {
		return
	}
	raw, err := json.MarshalIndent(d.seen, "", "  ")
	if err != nil {
		return
	}
	tmp := d.Path + ".tmp"
	if os.WriteFile(tmp, raw, 0o640) == nil {
		_ = os.Rename(tmp, d.Path)
	}
}

// Learned says whether the addon's unit was seen holding a process.
func (d *AddonDaemons) Learned(id string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	_, ok := d.seen[id]
	return ok
}

// Learn notes that the addon's unit holds a process; written once per addon.
func (d *AddonDaemons) Learn(id string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if _, ok := d.seen[id]; ok {
		return
	}
	d.seen[id] = time.Now().UTC().Truncate(time.Second)
	d.save()
}

// Forget drops what was learned (the addon was removed).
func (d *AddonDaemons) Forget(id string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.load()
	if _, ok := d.seen[id]; !ok {
		return
	}
	delete(d.seen, id)
	d.save()
}

// KeepsDaemon says whether the addon keeps a process running: declared by its manifest
// (runtime.daemon, the stored policy's or the declared one) or learned.
func (a *SystemdAddons) KeepsDaemon(id string) bool {
	var stored *AddonRuntime
	if p := a.Scripts.Root.ReadAddonPolicy(id); p != nil {
		stored = p.Runtime
	}
	if rt := MergeRuntime(stored, a.declared(id)); rt != nil && rt.Daemon {
		return true
	}
	return a.Daemons.Learned(id)
}

// endedLogLines is how many of the unit's last journal lines an ended addon carries.
const endedLogLines = 3

// markEnded learns the addons whose unit holds a process and marks the ended ones: a finished
// oneshot (OneShot, not failed, result success) of an addon that keeps a daemon is Ended and not
// running, so the pages offer Start. The addon units only (addon-<id>).
func (a *SystemdAddons) markEnded(ctx context.Context, list []Service) []Service {
	for i := range list {
		s := &list[i]
		if !strings.HasPrefix(s.ID, "addon-") {
			continue
		}
		id := strings.TrimPrefix(s.ID, "addon-")
		if s.Running && !s.OneShot && !s.Stray && !s.Starting {
			// the unit is active and its cgroup holds a process: a daemon
			a.Daemons.Learn(id)
			continue
		}
		if !s.OneShot || s.Failed || (s.Result != "" && s.Result != "success") {
			continue
		}
		if !a.KeepsDaemon(id) {
			continue
		}
		s.Ended, s.Running = true, false
		s.EndedAt, s.EndedLog = a.lastLines(ctx, s.Script)
	}
	return list
}

// lastLines is the unit's last journal lines of this boot and the time of the last one - where
// the reason an ended daemon gave usually is.
func (a *SystemdAddons) lastLines(ctx context.Context, unit string) (string, []string) {
	if d := a.Daemons; d != nil {
		d.mu.Lock()
		c, ok := d.lines[unit]
		d.mu.Unlock()
		if ok && time.Since(c.read) < endedLinesTTL {
			return c.at, c.log
		}
		at, log := a.readLastLines(ctx, unit)
		d.mu.Lock()
		if d.lines == nil {
			d.lines = map[string]endedLines{}
		}
		d.lines[unit] = endedLines{read: time.Now(), at: at, log: log}
		d.mu.Unlock()
		return at, log
	}
	return a.readLastLines(ctx, unit)
}

func (a *SystemdAddons) readLastLines(ctx context.Context, unit string) (string, []string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := append([]string{"-b", "-q", "--no-pager", "-o", "json", "--output-fields=MESSAGE", "-n", strconv.Itoa(endedLogLines), "-u", unit}, JournalFileArgs(a.Scripts.Root)...)
	var out []byte
	var err error
	if a.Journalctl != nil {
		out, err = a.Journalctl(ctx, args...)
	} else {
		out, err = exec.CommandContext(ctx, "journalctl", args...).Output()
	}
	if err != nil && len(out) == 0 {
		return "", nil
	}
	var lines []string
	at := ""
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var e struct {
			Message any    `json:"MESSAGE"`
			Time    string `json:"__REALTIME_TIMESTAMP"`
		}
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		msg, ok := e.Message.(string) // a binary message comes as a byte array: skipped
		if !ok {
			continue
		}
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		lines = append(lines, msg)
		if us, err := strconv.ParseInt(e.Time, 10, 64); err == nil && us > 0 {
			at = time.UnixMicro(us).UTC().Format(time.RFC3339)
		}
	}
	return at, lines
}

// SupervisedDaemons answers the installed addons whose manifest declares a daemon
// (runtime.daemon, the stored policy's or the declared one): the ones occulited restarts when
// their daemon ended (openccu-lite task 283, CrashLoops). What was only learned is not
// supervised - an addon that never said it keeps a daemon may end on purpose.
func (a *SystemdAddons) SupervisedDaemons() []string {
	var out []string
	for _, id := range rcdAddonIDs(a.Scripts.Root) {
		var stored *AddonRuntime
		if p := a.Scripts.Root.ReadAddonPolicy(id); p != nil {
			stored = p.Runtime
		}
		if rt := MergeRuntime(stored, a.declared(id)); rt != nil && rt.Daemon {
			out = append(out, id)
		}
	}
	return out
}
