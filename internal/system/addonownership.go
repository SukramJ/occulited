package system

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// B-92, decided in D-64: a confined addon updated with a direct /bin/install_addon (SSH, an
// addon's own updater) keeps what the update wrote as root, and its next start as addon-<id>
// may fail on it - hmm could not replace its pid file. occulited does not repair that at every
// start (a chown over RedMatic's node_modules at every boot is the cost that was not wanted);
// it looks for root-owned files and the Status page warns, with a button that gives the addon
// its files back (the same step an install through occulited takes, ownAddonDirs).
//
// The look is a walk of the addon's directories with one stat per entry, which on a large tree
// takes seconds on a slow card. So it runs in the background, and a result stands for an hour
// unless something about the addon changed - its rc.d script, its directory, hm_addons.cfg,
// which an install touches - or the root-owned file it named is not root's any more.
//
// B-123: the walk is the helper's (task 107's owntree, a dry run), not the daemon's own. A confined
// addon's private directories are exactly where an update as root leaves its files - Mosquitto's
// var/ is drwx------ addon-mosquitto - and the daemon's user could not open them, so a root-owned
// file there was never reported. A walk that did not look everywhere is no clean result.

// ErrNotConfined is the refusal of FixOwnership for an addon that runs as root.
var ErrNotConfined = errors.New("the addon is not confined")

// rootOwnerUID is the owner the check looks for; the tests use their own uid.
var rootOwnerUID = 0

// RootOwned is a confined addon with a root-owned entry in its directories.
type RootOwned struct {
	ID string `json:"id"`
	// Path is the first root-owned entry the walk met
	Path string `json:"path"`
	// Count is how many entries root owns there: the whole walk's count, which a root-owned entry the
	// quick check finds brings (task 110); the quick check's own when the whole walk failed
	Count int `json:"count,omitempty"`
}

// AddonOwnership keeps the results of the check.
type AddonOwnership struct {
	Addons *SystemdAddons
	// Every is how long a result stands when nothing about the addon changed; 0 = an hour
	Every time.Duration
	// Limit is how many entries a walk may look at per addon; a walk past it proves nothing. 0 =
	// 2,000,000, where the helper's walk stops by itself (ownwalk's MaxEntries).
	Limit int
	Now   func() time.Time

	mu      sync.Mutex
	results map[string]ownershipResult
	running bool
}

type ownershipResult struct {
	path  string // "" = none found
	count int
	sig   string
	at    time.Time
	// incomplete: the walk did not look everywhere (refused, an error, past the limit), so path ""
	// says nothing; it is tried again after ownershipRetry, not at every look
	incomplete bool
}

// ownershipRetry is how long a walk that did not look everywhere stands before the next try.
const ownershipRetry = 10 * time.Minute

// ownershipLimit is Limit's default.
const ownershipLimit = 2_000_000

func (o *AddonOwnership) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// ownershipDirs are the directories an addon's user owns once confined: the three standard ones
// and the data directories the take-over recorded.
func ownershipDirs(p *AddonPolicy) []string {
	return append(standardAddonDirs(p.ID), p.DataDirs...)
}

// ownershipSig changes when an install touched the addon.
func (o *AddonOwnership) ownershipSig(p *AddonPolicy) string {
	root := o.Addons.Scripts.Root
	parts := []string{strconv.Itoa(p.UID), strings.Join(p.DataDirs, ",")}
	for _, f := range []string{"/usr/local/etc/config/rc.d/" + p.ID, "/usr/local/addons/" + p.ID, "/usr/local/etc/config/hm_addons.cfg"} {
		if st, err := os.Stat(root.join(f)); err == nil {
			parts = append(parts, strconv.FormatInt(st.ModTime().UnixNano(), 10)+"/"+strconv.FormatInt(st.Size(), 10))
		} else {
			parts = append(parts, "-")
		}
	}
	return strings.Join(parts, "|")
}

// rootOwnedPath looks for the first entry root owns in an addon's directories, as a path on the box,
// and counts them, through the helper's dry run of the ownership walk: it reads as root what the
// daemon cannot open (B-123), and follows no link - chown -R leaves a link's own owner as it is, and
// its owner grants nothing. complete is false when the walk did not look everywhere: the helper
// refused it or failed, a directory was refused or unreadable, or the walk went past the limit. A
// root-owned entry found is a result either way.
//
// Task 110: the walk is the quick check (the top directories and their direct entries) unless the
// addon's marker asks for the whole tree - an install, an update or a switch to its own user since
// its last start. A root-owned entry the quick check finds brings the whole dry run, for the exact
// count and the first entry in the walk's order.
func (o *AddonOwnership) rootOwnedPath(p *AddonPolicy) (found string, count int, complete bool) {
	whole := o.Addons.Scripts.Root.FullWalkNeeded(p.ID)
	res, err := o.Addons.ownershipWalk(p, !whole)
	if !whole && err == nil && res.RootOwned > 0 {
		// a whole walk that failed without a finding keeps the quick check's
		if all, aerr := o.Addons.ownershipWalk(p, false); aerr == nil && (all.RootOwned > 0 || all.Problem() == "") {
			res = all
		}
	}
	if err == nil && res.FirstRootOwned != "" {
		return o.Addons.Scripts.Root.relOf(res.FirstRootOwned), res.RootOwned, true
	}
	limit := o.Limit
	if limit <= 0 {
		limit = ownershipLimit
	}
	why := ""
	switch {
	case err != nil:
		why = err.Error()
	case res.Problem() != "":
		why = res.Problem()
	case res.Checked > limit:
		why = fmt.Sprintf("more than %d entries", limit)
	}
	if why != "" {
		slog.Debug("addons: the ownership check could not look at every file", "id", p.ID, "why", why)
		return "", 0, false
	}
	return "", 0, true
}

// hiddenFromDaemon: the daemon cannot look at path because a directory above it is closed to it -
// a finding in a confined addon's private directory (B-123).
func hiddenFromDaemon(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, fs.ErrPermission)
}

// confined lists the policies the check applies to.
func (o *AddonOwnership) confined() map[string]*AddonPolicy {
	out := map[string]*AddonPolicy{}
	for id, p := range o.Addons.Scripts.Root.AddonPolicies() {
		if p.Mode == "confined" && p.UID > 0 {
			out[id] = p
		}
	}
	return out
}

// Findings answers the confined addons with a root-owned entry, from the stored results, and
// whether every confined addon has a result. Stale and missing ones are checked again in the
// background.
func (o *AddonOwnership) Findings() ([]RootOwned, bool) {
	if o == nil || o.Addons == nil {
		return nil, true
	}
	policies := o.confined()
	now := o.now()
	every := o.Every
	if every <= 0 {
		every = time.Hour
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.results == nil {
		o.results = map[string]ownershipResult{}
	}
	for id := range o.results {
		if _, ok := policies[id]; !ok {
			delete(o.results, id)
		}
	}
	var out []RootOwned
	complete := true
	var stale []string
	for id, p := range policies {
		res, ok := o.results[id]
		if !ok || res.incomplete {
			complete = false
		}
		if !ok {
			stale = append(stale, id)
			continue
		}
		stands := every
		if res.incomplete && ownershipRetry < stands {
			stands = ownershipRetry
		}
		fresh := res.sig == o.ownershipSig(p) && now.Sub(res.at) < stands
		if res.path != "" {
			// the file named is still root's: one lstat, so a chown by hand clears it at once; in a
			// directory closed to the daemon (B-123) the walk's word stands until the next walk
			full := o.Addons.Scripts.Root.join(res.path)
			uid, _, ok := ownerOf(full)
			if (ok && uid == rootOwnerUID) || (!ok && hiddenFromDaemon(full)) {
				out = append(out, RootOwned{ID: id, Path: res.path, Count: res.count})
			} else {
				fresh = false
				res.path = ""
			}
		}
		if !fresh {
			stale = append(stale, id)
		}
	}
	if len(stale) > 0 && !o.running {
		o.running = true
		go func() {
			o.check(stale)
			o.mu.Lock()
			o.running = false
			o.mu.Unlock()
		}()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, complete
}

// Check looks at the named addons now (all confined ones without ids) and waits for it.
func (o *AddonOwnership) Check(ids ...string) {
	if len(ids) == 0 {
		for id := range o.confined() {
			ids = append(ids, id)
		}
	}
	o.check(ids)
}

func (o *AddonOwnership) check(ids []string) {
	root := o.Addons.Scripts.Root
	for _, id := range ids {
		p := root.ReadAddonPolicy(id)
		if p == nil || p.Mode != "confined" || p.UID <= 0 {
			continue
		}
		// B-131: at boot occulited runs before occu-addons puts the addons' users back into the
		// boot-time passwd copy, and the helper refuses a walk for a user it cannot find - a WARN line
		// per addon at every boot, and a result that stood incomplete for ownershipRetry. Until the
		// user is there the addon has no result, so the next look tries again.
		if !addonUserKnown(root, p) {
			slog.Debug("addons: the ownership check waits for the addon's user", "id", id)
			continue
		}
		before := o.ownershipSig(p)
		found, count, complete := o.rootOwnedPath(p)
		// an install that ran during the walk: its result says nothing, the next round looks again
		if o.ownershipSig(p) != before {
			continue
		}
		o.mu.Lock()
		if o.results == nil {
			o.results = map[string]ownershipResult{}
		}
		o.results[id] = ownershipResult{path: found, count: count, sig: before, at: o.now(), incomplete: !complete}
		o.mu.Unlock()
	}
}

// addonUserKnown: the passwd file has the addon's user with its uid, as the helper checks before a
// walk. A passwd file the daemon cannot read leaves the answer to the helper.
func addonUserKnown(root Root, p *AddonPolicy) bool {
	passwd, err := os.ReadFile(root.join("/etc/passwd"))
	if err != nil {
		return true
	}
	name := p.User
	if name == "" {
		name = "addon-" + p.ID
	}
	uid := strconv.Itoa(p.UID)
	for _, line := range strings.Split(string(passwd), "\n") {
		if f := strings.Split(line, ":"); len(f) >= 3 && f[0] == name && f[2] == uid {
			return true
		}
	}
	return false
}

// OwnershipFix is what FixOwnership did.
type OwnershipFix struct {
	// RootOwned is a root-owned entry that is still there, "" when none is
	RootOwned string
	// Started: the addon's unit had failed, and it was started once (task 81, D-67)
	Started bool
	// StartError is the failed start of a failed unit
	StartError string
}

// FixOwnership gives a confined addon its directories back and checks it again. An addon whose unit
// had failed - most likely on those very files - is started once afterwards (task 81, decided in
// D-67); a running addon, and one that is stopped on purpose, are left alone.
func (o *AddonOwnership) FixOwnership(ctx context.Context, id string) (OwnershipFix, error) {
	p := o.Addons.Scripts.Root.ReadAddonPolicy(id)
	if p == nil || p.Mode != "confined" || p.UID <= 0 {
		return OwnershipFix{}, ErrNotConfined
	}
	if err := o.Addons.ownAddonDirs(ctx, p); err != nil {
		return OwnershipFix{}, fmt.Errorf("%s: %w", id, err)
	}
	o.check([]string{id})
	o.mu.Lock()
	fix := OwnershipFix{RootOwned: o.results[id].path}
	o.mu.Unlock()
	if o.unitFailed(ctx, id) {
		if err := o.Addons.Systemd.startAddonUnit(ctx, id); err != nil {
			fix.StartError = err.Error()
		} else {
			fix.Started = true
		}
	}
	return fix, nil
}

// unitFailed: the addon's unit is in state failed and no process of the addon runs anywhere else.
func (o *AddonOwnership) unitFailed(ctx context.Context, id string) bool {
	unit := "addon-" + id + ".service"
	states := o.Addons.Systemd.showAll(ctx, []string{unit}, "ActiveState")
	return states[unit]["ActiveState"] == "failed" && len(addonProcesses(o.Addons.Scripts.Root, id)) == 0
}
