package system

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// ReGa dependence of installed addons (maintainer, 2026-09-06): addons that need the ReGa are
// not in the catalogue at all; after an update from OpenCCU the ones already installed are
// shown as "disabled, incompatible" and disabled. Disabled means the rc.d script is not
// executable: busybox run-parts and the systemd generator both skip it, and re-enabling is one
// chmod - nothing of the addon is touched.

// KnownRegaDependent lists addon ids (rc.d names) that live in the ReGa DOM by design.
var KnownRegaDependent = map[string]string{
	"cuxd":        "CUxD keeps its devices and their values in the ReGa DOM",
	"xmlapi":      "the XML-API reads the ReGa DOM (devices, variables, programs)",
	"hm-pd":       "Programmdrucker prints ReGa programs",
	"email":       "the E-Mail addon is driven by ReGa scripts and system variables",
	"sonos":       "the Sonos addon is driven by ReGa scripts",
	"hmm-scripts": "runs HM-Scripts in the ReGa",
	// task 37: OpenCCU's own extra, left on the userfs by the switch; its code sits under
	// node_modules, which the scan below skips, so it is named here
	NeoServerID: "NEO Server posts to /tclrega.exe (the ReGa) and /api/homematic.cgi (the WebUI's CGI stack), neither of which openccu-lite has",
}

// KnownAddonNames name the addons that declare no name where ListAddons looks for one. OpenCCU's
// NEO Server prints no Name: in its rc.d info and has no hm_addons.cfg entry under its rc.d id, so
// its id stood where a person reads a name: "97NeoServer (disabled)" in the Status warning, and in
// the addon menu with the letter tile 9. The others are the ReGa addons above, for the box whose
// copy of one of them lost its own Name: line.
var KnownAddonNames = map[string]string{
	NeoServerID: "NEO Server",
	"cuxd":      "CUxD",
	"xmlapi":    "XML-API",
	"hm-pd":     "Programmdrucker",
	"email":     "E-Mail",
	"sonos":     "Sonos",
}

// regaTokens are what an addon's code uses when it talks to the ReGa beyond the session check
// the tclrega shim answers.
var regaTokens = []string{"dom.GetObject", "dom.CreateObject", "dom.DeleteObject", ":8181/", "/rega.exe", "hmscript", "ivtype"}

var regaScanCache sync.Map // id -> regaVerdict

// RegaCompatible are the addons known to work here despite ReGa idioms in their code: a ported
// addon keeps its ReGa path beside the openccu-lite one (the porting kit requires it), so the
// scan alone would punish exactly the addons that did it right. main fills it from the ids of the
// adapter manifests the image carries (addons whose authors ship no manifest); every other addon
// says it itself - with the manifest it ships (D-119), or the older marker file below.
var RegaCompatible = map[string]bool{}

// RegaCompatibleMarker in the addon's directory says "runs without ReGa" (porting kit, before the
// manifest). Still accepted for an addon without a manifest.
const RegaCompatibleMarker = "openccu-lite.ok"

// regaFromManifest is the verdict a manifest gives: its stored copy (the install's), else the
// file in the addon's own directory - read for this one flag only, which widens nothing
// (requires.rega is about the addon's needs, not its rights). ok is false without a manifest.
func (r Root) regaFromManifest(id string) (dependent bool, ok bool) {
	if m := r.ReadAddonManifest(id); m != nil {
		return m.NeedsRega(), true
	}
	if m, err := manifest.ParseFile(r.join("/usr/local/addons/" + id + "/" + manifest.FileName)); err == nil && m.ID == id {
		return m.NeedsRega(), true
	}
	return false, false
}

type regaVerdict struct {
	dependent bool
	reason    string
}

// RegaDependence says whether an installed addon needs the ReGa, and why. The known list wins;
// otherwise the addon's code is scanned once per process for ReGa idioms.
func (r Root) RegaDependence(id string) (bool, string) {
	if RegaCompatible[id] {
		return false, ""
	}
	if dep, ok := r.regaFromManifest(id); ok {
		if dep {
			return true, "its manifest says it needs the ReGa (requires.rega)"
		}
		return false, ""
	}
	if _, err := os.Stat(r.join("/usr/local/addons/" + id + "/" + RegaCompatibleMarker)); err == nil {
		return false, ""
	}
	if why, ok := KnownRegaDependent[id]; ok {
		return true, why
	}
	if v, ok := regaScanCache.Load(id); ok {
		vv := v.(regaVerdict)
		return vv.dependent, vv.reason
	}
	v := regaVerdict{}
	hits := map[string]int{}
	var where []string
	files := 0
	for _, dir := range []string{"/usr/local/addons/" + id, AddonWWW + "/" + id} {
		_ = filepath.WalkDir(r.join(dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git" || d.Name() == "vendor") {
					return filepath.SkipDir
				}
				return nil
			}
			files++
			if files > 4000 {
				return filepath.SkipAll
			}
			st, err := d.Info()
			if err != nil || st.Size() > 2<<20 || !isCodeFile(path) {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil || bytes.IndexByte(b[:min(len(b), 512)], 0) >= 0 {
				return nil
			}
			for _, t := range regaTokens {
				if bytes.Contains(b, []byte(t)) {
					if hits[t] == 0 && len(where) < 3 {
						where = append(where, strings.TrimPrefix(path, r.join("/")))
					}
					hits[t]++
				}
			}
			return nil
		})
	}
	if len(hits) > 0 {
		var toks []string
		for t := range hits {
			toks = append(toks, t)
		}
		sort.Strings(toks)
		v = regaVerdict{dependent: true, reason: fmt.Sprintf("its code talks to the ReGa (%s in %s)", strings.Join(toks, ", "), strings.Join(where, ", "))}
	}
	regaScanCache.Store(id, v)
	return v.dependent, v.reason
}

func isCodeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tcl", ".cgi", ".sh", ".js", ".py", ".lua", ".pl", ".php", ".html", ".htm", ".json", ".conf", ".cfg", ".txt", "":
		return true
	}
	return false
}

// AddonEnabled reports whether the rc.d script is executable (run by run-parts / generated).
func (r Root) AddonEnabled(id string) bool {
	st, err := os.Stat(r.join("/usr/local/etc/config/rc.d/" + id))
	return err == nil && st.Mode()&0o111 != 0
}

// SetAddonEnabled flips the executable bits of the rc.d script through the privilege helper's own
// operation (openccu-lite B-293): rc.d is root's to run, and no generic operation reaches it.
func (r Root) SetAddonEnabled(id string, enabled bool) error {
	if !addonIDRe.MatchString(id) {
		return fmt.Errorf("invalid addon id")
	}
	path := r.join("/usr/local/etc/config/rc.d/" + id)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("unknown addon")
	}
	return Priv.SetAddonEnabled(path, enabled)
}

// DisableRegaDependentAddons is the first-boot step after an update from OpenCCU: every
// installed addon that needs the ReGa is disabled. Returns the ids it disabled.
func (r Root) DisableRegaDependentAddons() []string {
	var out []string
	entries, _ := os.ReadDir(r.join("/usr/local/etc/config/rc.d"))
	for _, e := range entries {
		id := e.Name()
		if e.IsDir() || !addonIDRe.MatchString(id) || !r.AddonEnabled(id) {
			continue
		}
		if dep, _ := r.RegaDependence(id); dep {
			if r.SetAddonEnabled(id, false) == nil {
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}
