package system

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// The take-over of an addon's data directories (D-52, B-62).
//
// Confinement (D-36) chowns exactly the three directories an addon is installed into and puts
// the same three on ReadWritePaths. An addon that keeps its state elsewhere on the userfs -
// homematic-manager's profile in /usr/local/hmm, root-owned 0700 from its OpenCCU days - can
// then neither write that state nor even see it read-write under ProtectSystem=strict, and
// dies with "Permission denied" in the journal while its rc.d script reports OK. Two sources say
// where such state is: the catalogue entry's runtime block (data_dirs; paths is not that - it may
// name a shared directory such as rc.d and is never chowned), and the convention /usr/local/<id>
// that CCU addons have used forever, taken automatically when the directory exists and belongs
// to no other addon. The guard rails, in one place: never outside /usr/local/, never /usr/local
// or one of its shared trees (priv.UserfsSharedDirs: addons, etc, tmp, ...), never a path that
// is another addon's directory, an ancestor of one or inside one, never a symlink, and the chown
// itself follows no symlink (the helper walks the tree with lchown). What survived the rails is
// stored on the policy and rendered into the drop-in, so the unit may write exactly what the
// addon owns. The take-over runs when the policy is written and at every start of occulited
// (RefreshDataDirs), so a box already confined and broken heals with the next update.

// userfsPrefix is the only tree a take-over may touch.
const userfsPrefix = "/usr/local/"

// dataDirShape is the static half of the guard rails: the shape of a declared data directory,
// checked when a runtime block is validated. The box-dependent half (other addons, symlinks,
// existence) is dataDirAllowed.
func dataDirShape(d string) error {
	if !pathRe.MatchString(d) || strings.Contains(d, "..") {
		return fmt.Errorf("not a plain absolute path")
	}
	clean := filepath.Clean(d)
	if !strings.HasPrefix(clean, userfsPrefix) {
		return fmt.Errorf("outside %s", userfsPrefix)
	}
	first, _, _ := strings.Cut(strings.TrimPrefix(clean, userfsPrefix), "/")
	if first == "" || strings.HasPrefix(first, ".") || slices.Contains(priv.UserfsSharedDirs, first) {
		return fmt.Errorf("a shared directory of the system")
	}
	return nil
}

// otherAddonDirs is every directory that belongs to an addon other than id: the three standard
// ones and the convention directory of every addon with an rc.d script or a policy, plus what
// their policies declare or already took over.
func (a *SystemdAddons) otherAddonDirs(id string) []string {
	root := a.Scripts.Root
	ids := map[string]bool{}
	for o := range a.rcd() {
		ids[o] = true
	}
	policies := root.AddonPolicies()
	for o := range policies {
		ids[o] = true
	}
	var out []string
	for o := range ids {
		if o == id || !addonIDRe.MatchString(o) {
			continue
		}
		out = append(out, "/usr/local/addons/"+o, "/usr/local/etc/config/addons/"+o, AddonWWW+"/"+o, userfsPrefix+o)
		if p := policies[o]; p != nil {
			out = append(out, p.DataDirs...)
			if p.Runtime != nil {
				for _, d := range p.Runtime.DataDirs {
					out = append(out, filepath.Clean(d))
				}
			}
		}
	}
	return out
}

// dataDirAllowed is the box-dependent half of the guard rails for one candidate. It returns the
// cleaned path, or the reason it was refused.
func (a *SystemdAddons) dataDirAllowed(id, d string, others []string) (string, string) {
	if err := dataDirShape(d); err != nil {
		return "", err.Error()
	}
	clean := filepath.Clean(d)
	own := []string{"/usr/local/addons/" + id, "/usr/local/etc/config/addons/" + id, AddonWWW + "/" + id}
	for _, o := range own {
		if clean == o || strings.HasPrefix(clean, o+"/") {
			return "", "inside the addon's own directory, which confinement covers anyway"
		}
	}
	for _, o := range others {
		switch {
		case clean == o:
			return "", "another addon's directory " + o
		case strings.HasPrefix(o, clean+"/"):
			return "", "a prefix of another addon's directory " + o
		case strings.HasPrefix(clean, o+"/"):
			return "", "inside another addon's directory " + o
		}
	}
	st, err := os.Lstat(a.Scripts.Root.join(clean))
	if err != nil {
		return "", "does not exist"
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return "", "a symlink"
	}
	if !st.IsDir() {
		return "", "not a directory"
	}
	return clean, ""
}

// addonDataDirs is the list the take-over acts on for an addon: the runtime block's data_dirs
// and the convention /usr/local/<id>, each through the guard rails, sorted, each once. Refused
// candidates are returned by reason for the log. A *declared* directory that does not exist is
// created when create is set (a fresh install: the confined addon cannot mkdir below /usr/local
// itself, so the state directory its entry names would never come to be); the convention
// directory is a guess and is never created.
func (a *SystemdAddons) addonDataDirs(id string, rt *AddonRuntime, create bool) (dirs []string, refused map[string]string) {
	var candidates []string
	if rt != nil {
		candidates = append(candidates, rt.DataDirs...)
	}
	convention := userfsPrefix + id
	candidates = append(candidates, convention)
	others := a.otherAddonDirs(id)
	refused = map[string]string{}
	for _, c := range candidates {
		clean, why := a.dataDirAllowed(id, c, others)
		if why == "does not exist" && c != convention && create {
			if err := Priv.MkdirAll(a.Scripts.Root.join(filepath.Clean(c)), 0o755); err != nil {
				why = "could not be created: " + err.Error()
			} else {
				clean, why = a.dataDirAllowed(id, c, others)
			}
		}
		if why != "" {
			// the convention directory is a guess, its absence is not worth a word
			if !(c == convention && why == "does not exist") {
				refused[c] = why
			}
			continue
		}
		if !slices.Contains(dirs, clean) {
			dirs = append(dirs, clean)
		}
	}
	sort.Strings(dirs)
	return dirs, refused
}

// ownerOf reports a path's owner; tests substitute it. ok is false when the path is gone.
var ownerOf = func(path string) (uid, gid int, ok bool) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, 0, false
	}
	sys, isStat := st.Sys().(*syscall.Stat_t)
	if !isStat {
		return 0, 0, false
	}
	return int(sys.Uid), int(sys.Gid), true
}

// takeOverDataDirs chowns the addon's data directories to its user and records them on the
// policy (not written here - the caller writes the policy). always chowns every directory; false
// chowns only one whose top is not the addon's yet, which is what the start-time refresh wants
// (a big cache tree is walked at every boot otherwise). A chown that fails is an error: the
// addon would start and die, as B-62 did, and the caller must not pretend it is confined.
func (a *SystemdAddons) takeOverDataDirs(p *AddonPolicy, always bool) error {
	root := a.Scripts.Root
	dirs, refused := a.addonDataDirs(p.ID, p.Runtime, true)
	for c, why := range refused {
		slog.Warn("addon policy: data directory not taken over", "addon", p.ID, "dir", c, "why", why)
	}
	for _, d := range dirs {
		full := root.join(d)
		if !always {
			if uid, gid, ok := ownerOf(full); !ok || (uid == p.UID && gid == p.UID) {
				continue
			}
		}
		if err := Priv.Chown(full, p.UID, p.UID, true); err != nil {
			return fmt.Errorf("chown %s: %w", d, err)
		}
	}
	p.DataDirs = dirs
	return nil
}

// RefreshDataDirs runs the take-over again for every confined policy: a directory that
// appeared, a declaration its manifest gained (RefreshManifestRuntimes runs before this), or
// ownership a previous occulited never fixed (B-62 on the Pi: hmm confined, /usr/local/hmm still
// root's). Run at start, before RefreshPolicyDropIns renders the result. Returns the ids whose
// policy changed - a chown alone is not a change of the policy.
func (a *SystemdAddons) RefreshDataDirs() []string {
	root := a.Scripts.Root
	var changed []string
	for id, p := range root.AddonPolicies() {
		if p.Mode != "confined" || p.UID == 0 {
			continue
		}
		before := append([]string(nil), p.DataDirs...)
		if err := a.takeOverDataDirs(p, false); err != nil {
			slog.Warn("addon policy: data directories not taken over", "addon", id, "err", err)
			continue
		}
		if slices.Equal(before, p.DataDirs) {
			continue
		}
		if err := root.writeAddonPolicy(p); err == nil {
			changed = append(changed, id)
		}
	}
	sort.Strings(changed)
	return changed
}
