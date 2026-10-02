package priv

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hobbyquaker/occulited/internal/addonunit"
)

// The files root runs or obeys are outside the generic operations (openccu-lite B-293).
//
// B-238 took the named operations' own files off the write prefixes. The same prefixes
// (/etc/config/, /usr/local/etc/config/, /usr/local/addons/) still reached files a compromised
// daemon could turn into root:
//   - rc.d/<id> and rc.d/<id>.script: run as root at boot, by the addon lifecycle and by the
//     WebUI's actions (ProgramDirs);
//   - the addon web trees (CGIRoots): a CGI there may run as uid 0 (RunAs);
//   - addon-policy/<id>.conf, .needs and .start: the occu-addons generator installs the .conf as
//     the unit's drop-in (User=, ExecStartPre=+…) and its addon-users step makes an account of the
//     uid in it; .needs and .start order the unit.
//
// So every entry under rc.d and under a CGI root, at any depth, is named-only, and the policy
// files are too: the drop-in, the start order and the early start are written by the helper from
// checked data (AddonPolicyFile, addonunit), an addon is enabled or disabled by SetAddonEnabled,
// and an uninstall's rc.d entry and web link go through RemoveAddonEntry. Nothing else under rc.d
// or the web trees is the daemon's: the root install path (install_addon) and the fork's
// lite-addon-rc (the addon-rc wrapper) write them.
//
// A name is not enough (the lesson of B-238 and B-235): rc.d/<id>.script is usually a link into
// the addon's directory, and an addon's web tree is usually a link to /usr/local/addons/<id>/www -
// both on the /usr/local/addons/ write prefix until B-294 took it off (addonhome.go), and a link
// may lead anywhere else on a prefix. So the links found in those trees are followed
// here, and what they lead to is named-only as well: the file a script link names, everything
// below a directory a web link names, and the links found there in turn. The directories that
// hold any of it may not be moved, removed, given away or replaced by a link as a whole
// (holdsNamed).

// obeyedLimit bounds the walk of the trees. A tree larger than that is not looked through to the
// end, and the addons' whole tree is then named-only: what the walk did not see is refused rather
// than admitted.
const obeyedLimit = 20000

// obeyedSet is what root runs or obeys, relative to the root: trees (ending in "/") whose every
// entry below is named-only, and exact files.
type obeyedSet struct {
	trees []string
	files []string
}

// policyDirs are the addon policy directory in both spellings, ending in "/".
func (p Policy) policyDirs() []string {
	if p.AddonPolicyDir == "" {
		return nil
	}
	d := strings.TrimSuffix(p.AddonPolicyDir, "/") + "/"
	out := []string{d}
	if o := otherSpelling(d); o != "" {
		out = append(out, o)
	}
	return out
}

// addonPolicyFile splits rel into the addon id and the kind when it is one of the policy files root
// obeys: <policy dir>/<id>.conf, .needs or .start. The stored policy (.json), the stored manifest
// and the markers beside them are the daemon's own and stay generic.
func (p Policy) addonPolicyFile(rel string) (id, kind string, ok bool) {
	for _, d := range p.policyDirs() {
		name, found := strings.CutPrefix(rel, d)
		if !found || name == "" || strings.Contains(name, "/") {
			continue
		}
		for _, k := range addonunit.Kinds {
			if base, cut := strings.CutSuffix(name, k); cut {
				// an id of the addon shape; anything else with the suffix is refused as well, it is
				// nobody's file (the generator reads any <name>.conf)
				return base, k, true
			}
		}
	}
	return "", "", false
}

// obeyedRoots are the trees themselves: the addons' rc.d in both spellings and the CGI roots.
func (p Policy) obeyedRoots() []string {
	var out []string
	add := func(d string) {
		if d == "" {
			return
		}
		d = strings.TrimSuffix(d, "/") + "/"
		out = append(out, d)
		if o := otherSpelling(d); o != "" {
			out = append(out, o)
		}
	}
	add(p.AddonRCDir)
	for _, r := range p.CGIRoots {
		add(r)
	}
	return out
}

// rootObeyedSet walks the trees and the links in them (see above).
func (p Policy) rootObeyedSet() obeyedSet {
	var set obeyedSet
	roots := p.obeyedRoots()
	if len(roots) == 0 {
		return set
	}
	seen := map[string]bool{}
	under := func(rel string) bool {
		for _, t := range set.trees {
			if strings.HasPrefix(rel, t) || rel+"/" == t {
				return true
			}
		}
		return false
	}
	queue := append([]string(nil), roots...)
	set.trees = append(set.trees, roots...)
	full := func(rel string) string {
		if filepath.Clean(p.Root) == "/" {
			return rel
		}
		return filepath.Join(p.Root, rel)
	}
	// target adds what a link leads to. Only a link root made in a directory only root writes is
	// followed (trustedLink, the rule the writes themselves resolve by): a link a confined addon
	// planted in its own tree leads to nothing root runs - its CGIs run as its user - and following
	// it would let the addon close any directory to the daemon.
	target := func(link string) {
		fi, err := os.Lstat(link)
		if err != nil || p.trustedLink(link, fi) != nil {
			return
		}
		t, err := p.resolve(link, true)
		if err != nil {
			// a link to nothing that resolves, or through an untrusted link: what it names first
			// is still the addon's, taken as written
			dest, rerr := os.Readlink(link)
			if rerr != nil {
				return
			}
			if !filepath.IsAbs(dest) {
				dest = filepath.Join(filepath.Dir(link), dest)
			}
			t = filepath.Clean(dest)
		}
		rel, ok := p.rel(t)
		if !ok || rel == "/" || under(rel) {
			return
		}
		fi, err = os.Stat(t)
		switch {
		case err == nil && fi.IsDir():
			set.trees = append(set.trees, strings.TrimSuffix(rel, "/")+"/")
			queue = append(queue, strings.TrimSuffix(rel, "/")+"/")
		case err == nil:
			set.files = append(set.files, rel)
		default: // not there: either it becomes
			set.files = append(set.files, rel)
			set.trees = append(set.trees, rel+"/")
		}
	}
	// the roots may be links themselves (/www/addons on the image)
	for _, r := range roots {
		if fi, err := os.Lstat(full(strings.TrimSuffix(r, "/"))); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			target(full(strings.TrimSuffix(r, "/")))
		}
	}
	entries := 0
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if seen[dir] {
			continue
		}
		seen[dir] = true
		_ = filepath.WalkDir(full(strings.TrimSuffix(dir, "/")), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			entries++
			if entries > obeyedLimit {
				return filepath.SkipAll
			}
			if d.Type()&os.ModeSymlink != 0 {
				target(path)
			}
			return nil
		})
		if entries > obeyedLimit {
			// fail closed: the addons' trees as a whole
			set.trees = append(set.trees, strings.TrimSuffix(AddonHomeDir, "/")+"/")
			break
		}
	}
	return set
}

// rootObeyed: rel is a file root runs or obeys - a policy file, an entry of one of the trees, or
// what a link there leads to. The trees' own directories are not (holdsNamed covers them: a chmod
// that only takes bits away stays possible there).
func (p Policy) rootObeyed(rel string) bool {
	if _, _, ok := p.addonPolicyFile(rel); ok {
		return true
	}
	set := p.rootObeyedSet()
	for _, f := range set.files {
		if rel == f {
			return true
		}
	}
	for _, t := range set.trees {
		if strings.HasPrefix(rel, t) {
			return true
		}
	}
	return false
}

// rootObeyedDirs are the paths holdsNamed looks below for: the policy directories, the trees and
// the files of rootObeyedSet.
func (p Policy) rootObeyedDirs() []string {
	set := p.rootObeyedSet()
	out := append([]string(nil), p.policyDirs()...)
	out = append(out, set.trees...)
	return append(out, set.files...)
}
