package system

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The addon ingress scope's view of the addons (openccu-lite task 307, GitHub issue #3): which URL
// segments under /addons/ belong to an addon, which addon a segment belongs to, and the installed
// addons' names - what the gate's token mirror (internal/auth), occulited's own guard of the addon
// pages (internal/httpapi RequireSession) and the token and pairing APIs need. None of it runs an
// rc.d script: the ids are the rc.d entries, the segments come from the lighttpd drop-ins, the
// names from the stored manifests.

// AddonIngressSegments answers the URL segments under /addons/ that the addon's lighttpd drop-in
// proxies besides the addon's own id (RedMatic's /addons/red/ gives "red"), sorted; nil when the
// drop-in maps none or there is none.
func (r Root) AddonIngressSegments(id string) []string {
	if !addonIDRe.MatchString(id) {
		return nil
	}
	f := filepath.Join(r.join(lighttpdDropinDir), id+".conf")
	st, err := os.Stat(f)
	if err != nil || st.Size() > 256*1024 {
		return nil
	}
	raw, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range frontendPaths(string(raw)) {
		if seg := ingressSegment(p); seg != "" && seg != id && !seen[seg] {
			seen[seg] = true
			out = append(out, seg)
		}
	}
	sort.Strings(out)
	return out
}

// ingressSegment is the first segment of an /addons/… path, "" for any other path.
func ingressSegment(p string) string {
	rest, ok := strings.CutPrefix(p, "/addons/")
	if !ok {
		return ""
	}
	seg, _, _ := strings.Cut(rest, "/")
	return seg
}

// AddonForIngressSegment answers the installed addon whose pages /addons/<segment>/ are: the addon
// of that id when one is installed, else the addon whose drop-in proxies the segment; "" when none.
func (r Root) AddonForIngressSegment(seg string) string {
	if seg == "" {
		return ""
	}
	ids := rcdAddonIDs(r)
	for _, id := range ids {
		if id == seg {
			return seg
		}
	}
	for _, id := range ids {
		for _, s := range r.AddonIngressSegments(id) {
			if s == seg {
				return id
			}
		}
	}
	return ""
}

// InstalledAddonNames answers id → display name for the installed addons (an rc.d script each,
// a valid id): the stored manifest's name, else the one registered in hm_addons.cfg, else a known
// one, else the id itself.
func (r Root) InstalledAddonNames() map[string]string {
	settings := ParseHMAddonsCfg(readFile(r.join("/usr/local/etc/config/hm_addons.cfg")))
	out := map[string]string{}
	for _, id := range rcdAddonIDs(r) {
		if !addonIDRe.MatchString(id) {
			continue
		}
		name := ""
		if m := r.ReadAddonManifest(id); m != nil {
			name = m.Name.In("en") // the manifest's name; a plain string is both languages
		}
		if name == "" {
			if s, ok := settings[id]; ok {
				name = s.Name
			}
		}
		if name == "" {
			name = KnownAddonNames[id]
		}
		if name == "" {
			name = id
		}
		out[id] = name
	}
	return out
}
