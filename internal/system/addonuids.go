package system

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
)

// The addons' uid registry (openccu-lite B-288). Every uid occulited ever handed to an addon is
// recorded here, id -> uid, and the entry outlives the addon: an uninstall and the start's stale
// sweep (B-283) remove the addon's policy files but never its line here, so a reinstall under the
// same id gets its old uid back - the files it left on the userfs keep matching its user - and no
// other addon is ever given that uid. Until B-283 the policy file itself outlived the addon to
// keep the reservation (D-47); the registry is that reservation on its own.
//
// The file sits beside the policy directory, not in it: nothing there may read as an addon's
// file (the sweep cuts names at their suffix), and the fork's boot scripts read only the policy
// directory. It is on the userfs under /usr/local/etc/config, so a backup carries it with the
// policies and the files whose owners it explains.

// AddonUIDsFile is the registry.
const AddonUIDsFile = "/usr/local/etc/config/addon-uids.json"

// addonUIDs is the file's shape.
type addonUIDs struct {
	UIDs map[string]int `json:"uids"`
}

// ReadAddonUIDs is the registry, id -> uid; empty when there is none or it does not parse.
// Entries that are not an addon id or not an addon uid are left out.
func (r Root) ReadAddonUIDs() map[string]int {
	out := map[string]int{}
	b, err := os.ReadFile(r.join(AddonUIDsFile))
	if err != nil {
		return out
	}
	var f addonUIDs
	if err := json.Unmarshal(b, &f); err != nil {
		slog.Warn("addon uids: the registry does not parse; it is rebuilt from what the system holds", "file", AddonUIDsFile, "err", err)
		return out
	}
	for id, uid := range f.UIDs {
		if addonIDRe.MatchString(id) && uid >= AddonUIDBase {
			out[id] = uid
		}
	}
	return out
}

func (r Root) writeAddonUIDs(m map[string]int) error {
	b, _ := json.MarshalIndent(addonUIDs{UIDs: m}, "", "  ") // map keys come out sorted
	return writeFileAtomic(r.join(AddonUIDsFile), append(b, '\n'), 0o644)
}

// passwdAddonUIDs are the addon users in this boot's /etc/passwd, id -> uid: addon-<id> with a
// uid from AddonUIDBase up. The file is the rootfs's (replaced by an update, rebuilt at boot from
// the policies), so a user whose policy is gone stays in it only until the next boot.
func (r Root) passwdAddonUIDs() map[string]int {
	out := map[string]int{}
	b, err := os.ReadFile(r.join("/etc/passwd"))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 3 {
			continue
		}
		id, ok := strings.CutPrefix(f[0], "addon-")
		uid, err := strconv.Atoi(f[2])
		if ok && err == nil && uid >= AddonUIDBase && addonIDRe.MatchString(id) {
			out[id] = uid
		}
	}
	return out
}

// dirOwnerUIDs are the owners of /usr/local/addons/<id> that are addon uids, id -> uid: what a
// confined addon's directory says about its user when nothing else does.
func (r Root) dirOwnerUIDs() map[string]int {
	out := map[string]int{}
	entries, err := os.ReadDir(r.join("/usr/local/addons"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || !addonIDRe.MatchString(e.Name()) {
			continue
		}
		if uid, _, ok := ownerOf(r.join("/usr/local/addons/" + e.Name())); ok && uid >= AddonUIDBase {
			out[e.Name()] = uid
		}
	}
	return out
}

// sortedIDs is the map's keys in order, so that a merge decides the same way every time.
func sortedIDs(m map[string]int) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// mergeAddonUIDs adds to reg what the sources say, the first source first: an id already in reg
// keeps its uid, and a uid reg already holds for another id is not given to a second one (that
// conflict is logged). Returns the ids added.
func mergeAddonUIDs(reg map[string]int, sources ...map[string]int) []string {
	holder := map[int]string{}
	for id, uid := range reg {
		holder[uid] = id
	}
	var added []string
	for _, src := range sources {
		for _, id := range sortedIDs(src) {
			uid := src[id]
			if _, ok := reg[id]; ok {
				continue
			}
			if other, taken := holder[uid]; taken {
				slog.Warn("addon uids: a uid seen for one addon is registered to another; left with that one", "uid", uid, "seen_for", id, "registered_to", other)
				continue
			}
			reg[id], holder[uid] = uid, id
			added = append(added, id)
		}
	}
	sort.Strings(added)
	return added
}

// SeedAddonUIDs records in the registry every addon uid the system holds and the registry does
// not: the stored policies first, then this boot's addon users, then the owners of the addon
// directories. That is the registry's first start on a system confined before it existed, and the
// repair for one where an uninstall under B-283 removed the reservation: the user stays in
// /etc/passwd until the next boot. Run at occulited's start before the stale sweep. Returns the
// ids added, sorted.
func (a *SystemdAddons) SeedAddonUIDs() []string {
	policyMu.Lock()
	defer policyMu.Unlock()
	root := a.Scripts.Root
	policies := map[string]int{}
	for id, p := range root.AddonPolicies() {
		if p.UID >= AddonUIDBase {
			policies[id] = p.UID
		}
	}
	reg := root.ReadAddonUIDs()
	added := mergeAddonUIDs(reg, policies, root.passwdAddonUIDs(), root.dirOwnerUIDs())
	if len(added) == 0 {
		return nil
	}
	if err := root.writeAddonUIDs(reg); err != nil {
		slog.Warn("addon uids: the registry could not be written", "file", AddonUIDsFile, "err", err)
		return nil
	}
	return added
}

// addonUID is the uid the addon runs as when it is confined: its registered one, else the next
// free one, recorded at once. Free means above every uid the registry, a stored policy and this
// boot's addon users hold, so a uid is never handed to a second addon. A registered uid that a
// stored policy of another addon holds (a system where B-283's uninstall gave it away) is not
// used: the addon gets a new one and the registry follows. The caller holds policyMu.
func (a *SystemdAddons) addonUID(id string) (int, error) {
	root := a.Scripts.Root
	reg := root.ReadAddonUIDs()
	inUse := map[int]string{}
	for pid, p := range root.AddonPolicies() {
		if p.UID > 0 {
			inUse[p.UID] = pid
		}
	}
	if uid, ok := reg[id]; ok {
		if other, taken := inUse[uid]; !taken || other == id {
			return uid, nil
		}
	}
	next := AddonUIDBase
	for _, m := range []map[string]int{reg, root.passwdAddonUIDs()} {
		for _, uid := range m {
			if uid >= next {
				next = uid + 1
			}
		}
	}
	for uid := range inUse {
		if uid >= next {
			next = uid + 1
		}
	}
	reg[id] = next
	if err := root.writeAddonUIDs(reg); err != nil {
		return 0, fmt.Errorf("the addon uid registry: %w", err)
	}
	return next, nil
}
