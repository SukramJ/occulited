package system

import (
	"os"
	"slices"
	"strings"
	"testing"
)

// Task 37: mediola's NEO Server is switched off with the vendor's own marker and disabled like
// every other ReGa-dependent addon, once; the switch is idempotent.
func TestNeoServerDisable(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/addons/mediola/VERSION":             "2.10.1\n",
		"usr/local/addons/mediola/rc.d/97NeoServer":    "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/97NeoServer":        "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/97NeoServer.script": "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/hmm":                "#!/bin/sh\n",
	})
	for _, id := range []string{NeoServerID, "hmm"} {
		if err := os.Chmod(r.join("/usr/local/etc/config/rc.d/"+id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if r.HasReGa() {
		t.Error("a ReGa on a fake root")
	}
	if !r.NeoServerInstalled() || r.NeoServerDisabled() {
		t.Fatal("installed and enabled, before")
	}
	if dep, why := r.RegaDependence(NeoServerID); !dep || !strings.Contains(why, "/tclrega.exe") {
		t.Errorf("not known as ReGa-dependent: %v %q", dep, why)
	}
	done, err := r.DisableNeoServer()
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 2 || !r.NeoServerDisabled() || r.AddonEnabled(NeoServerID) {
		t.Errorf("done %v disabled %v enabled %v", done, r.NeoServerDisabled(), r.AddonEnabled(NeoServerID))
	}
	// idempotent: nothing left to do, nothing touched
	if done, err := r.DisableNeoServer(); err != nil || len(done) != 0 {
		t.Errorf("second run: %v %v", done, err)
	}
	// the neighbour is not touched
	if !r.AddonEnabled("hmm") {
		t.Error("hmm disabled")
	}
	// a box without the addon: nothing installed, nothing to do
	empty := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/hmm": "#!/bin/sh\n"})
	if empty.NeoServerInstalled() {
		t.Error("installed on an empty box")
	}
	if done, err := empty.DisableNeoServer(); err != nil || len(done) != 0 {
		t.Errorf("empty box: %v %v", done, err)
	}
	// the ReGa check reads the binary
	rega := rootWith(t, map[string]string{"bin/ReGaHss": ""})
	if !rega.HasReGa() {
		t.Error("ReGaHss not seen")
	}
}

// The leftover: the addon directory with its size, its companions, and a removal that does what
// the vendor's uninstall does - and stops the unit first.
func TestNeoServerLeftover(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/addons/mediola/VERSION":                  "2.10.1\n",
		"usr/local/addons/mediola/Disabled":                 "",
		"usr/local/addons/mediola/neo_server/automation.js": strings.Repeat("x", 4000),
		"usr/local/addons/mediola/rc.d/97NeoServer":         strings.Repeat("y", 100),
		"usr/local/etc/config/addons/mediola/.keep":         "",
		"usr/local/etc/config/rc.d/97NeoServer":             strings.Repeat("w", 50),
		"usr/local/etc/config/rc.d/hmm":                     "#!/bin/sh\n",
		"usr/local/crontabs/root":                           "0 3 * * * /bin/cronBackup.sh\n*/5 * * * * /usr/local/addons/mediola/bin/watchdog\n",
		"usr/local/etc/config/hm_addons.cfg":                "hmm {CONFIG_URL /addons/hmm/settings.cgi CONFIG_DESCRIPTION {de {<li>x</li>} en {<li>x</li>}} ID hmm CONFIG_NAME {Homematic Manager}}\nmediola {CONFIG_URL /addons/mediola/index.html CONFIG_DESCRIPTION {de {<li>NEO Server</li>} en {<li>NEO Server</li>}} ID mediola CONFIG_NAME NEOServer}\n",
		"opt/mediola/www/index.html":                        "<html>",
	})
	// the web tree is a symlink into a tree the removal must not follow, and the wrapper's twin
	// points at the addon's own script
	if err := os.MkdirAll(r.join(AddonWWW), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.join("/opt/mediola/www"), r.join(AddonWWW+"/mediola")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(r.join(NeoServerDir+"/rc.d/97NeoServer"), r.join("/usr/local/etc/config/rc.d/97NeoServer.script")); err != nil {
		t.Fatal(err)
	}
	var item LegacyItem
	for _, it := range r.LegacyLeftovers() {
		if it.ID == "neoserver" {
			item = it
		}
	}
	if !item.Present || item.Path != NeoServerDir || item.Bytes < 4150 || item.WayBack || !strings.Contains(item.Why, "ReGa") {
		t.Fatalf("%+v", item)
	}
	if !slices.Contains(item.Also, "/usr/local/etc/config/rc.d/97NeoServer") || !slices.Contains(item.Also, AddonWWW+"/mediola") {
		t.Errorf("companions: %v", item.Also)
	}
	var stopped []string
	reloads := 0
	removed, err := r.RemoveLegacyWith([]string{"neoserver"}, func(unit string) { stopped = append(stopped, unit) }, func() { reloads++ })
	if err != nil || !slices.Equal(removed, []string{"neoserver"}) {
		t.Fatalf("%v %v", removed, err)
	}
	if !slices.Equal(stopped, []string{"addon-97NeoServer.service"}) || reloads != 1 {
		t.Errorf("stopped %v reloads %d", stopped, reloads)
	}
	for _, p := range neoServerPaths {
		if _, err := os.Lstat(r.join(p)); !os.IsNotExist(err) {
			t.Errorf("%s still there", p)
		}
	}
	if _, err := os.Stat(r.join("/opt/mediola/www/index.html")); err != nil {
		t.Error("the symlink's target was removed")
	}
	if _, err := os.Stat(r.join(NeoServerUninstalledMarker)); err != nil {
		t.Error("neoDisabled not touched")
	}
	if b, _ := os.ReadFile(r.join("/usr/local/crontabs/root")); string(b) != "0 3 * * * /bin/cronBackup.sh\n" {
		t.Errorf("crontab:\n%s", b)
	}
	if b, _ := os.ReadFile(r.join("/usr/local/etc/config/hm_addons.cfg")); strings.Contains(string(b), "mediola") || !strings.Contains(string(b), "hmm ") {
		t.Errorf("hm_addons.cfg:\n%s", b)
	}
	if r.NeoServerInstalled() {
		t.Error("still installed")
	}
	// gone: not present, nothing to remove, no unit stopped
	for _, it := range r.LegacyLeftovers() {
		if it.ID == "neoserver" && (it.Present || it.Bytes != 0) {
			t.Errorf("after removal: %+v", it)
		}
	}
	stopped = nil
	if removed, err := r.RemoveLegacyWith([]string{"neoserver"}, func(unit string) { stopped = append(stopped, unit) }, nil); err != nil || len(removed) != 0 || len(stopped) != 0 {
		t.Errorf("second removal: %v %v %v", removed, err, stopped)
	}
	// a half-removed addon - the rc.d entry alone - is still a leftover, and RemoveLegacy(nil)
	// takes it with the rest
	half := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/97NeoServer": "#!/bin/sh\n", "etc/config/homematic.regadom": "x"})
	var present []string
	for _, it := range half.LegacyLeftovers() {
		if it.Present {
			present = append(present, it.ID)
		}
	}
	if !slices.Equal(present, []string{"regadom", "neoserver"}) {
		t.Errorf("present: %v", present)
	}
	if removed, err := half.RemoveLegacy(nil); err != nil || !slices.Equal(removed, []string{"neoserver", "regadom"}) {
		t.Errorf("%v %v", removed, err)
	}
}
