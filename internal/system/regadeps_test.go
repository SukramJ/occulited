package system

import (
	"os"
	"strings"
	"testing"
)

func TestRegaDependenceAndDisable(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/rc.d/cuxd":         "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/mosquitto":    "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/hb-foo":       "#!/bin/sh\n",
		"usr/local/addons/mosquitto/rc.d":        "start)\n",
		"usr/local/addons/hb-foo/www/index.cgi":  "set x [rega_script dom.GetObject(\"x\")]\n",
		"usr/local/addons/hb-foo/node_modules/a": "dom.GetObject\n",
	})
	for _, id := range []string{"cuxd", "mosquitto", "hb-foo"} {
		_ = os.Chmod(r.join("/usr/local/etc/config/rc.d/"+id), 0o755)
	}
	if dep, why := r.RegaDependence("cuxd"); !dep || why == "" {
		t.Error("cuxd")
	}
	if dep, _ := r.RegaDependence("mosquitto"); dep {
		t.Error("mosquitto flagged")
	}
	if dep, why := r.RegaDependence("hb-foo"); !dep || !strings.Contains(why, "dom.GetObject") || !strings.Contains(why, "index.cgi") {
		t.Errorf("hb-foo: %v %s", dep, why)
	}
	// a ported addon keeps its ReGa path: the catalogue or its marker file exempt it
	regaScanCache.Delete("hb-foo")
	RegaCompatible["hb-foo"] = true
	if dep, _ := r.RegaDependence("hb-foo"); dep {
		t.Error("catalogue-listed addon flagged")
	}
	delete(RegaCompatible, "hb-foo")
	regaScanCache.Delete("hb-foo")
	_ = os.WriteFile(r.join("/usr/local/addons/hb-foo/"+RegaCompatibleMarker), []byte(""), 0o644)
	if dep, _ := r.RegaDependence("hb-foo"); dep {
		t.Error("marker file ignored")
	}
	_ = os.Remove(r.join("/usr/local/addons/hb-foo/" + RegaCompatibleMarker))
	regaScanCache.Delete("hb-foo")
	disabled := r.DisableRegaDependentAddons()
	if len(disabled) != 2 || disabled[0] != "cuxd" || disabled[1] != "hb-foo" {
		t.Fatalf("%v", disabled)
	}
	if r.AddonEnabled("cuxd") || !r.AddonEnabled("mosquitto") {
		t.Error("enabled state")
	}
	if err := r.SetAddonEnabled("cuxd", true); err != nil || !r.AddonEnabled("cuxd") {
		t.Errorf("re-enable: %v", err)
	}
	if err := r.SetAddonEnabled("nope", true); err == nil {
		t.Error("unknown addon")
	}
	// the second run disables nothing new for the already-disabled one
	if again := r.DisableRegaDependentAddons(); len(again) != 1 || again[0] != "cuxd" {
		t.Errorf("%v", again)
	}
}

func TestLegacyLeftovers(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/config/homematic.regadom":     strings.Repeat("x", 1000),
		"etc/config/homematic.regadom.bak": strings.Repeat("y", 500),
		"etc/config/measurement/a/b":       "12345",
	})
	items := r.LegacyLeftovers()
	by := map[string]LegacyItem{}
	for _, it := range items {
		by[it.ID] = it
	}
	if !by["regadom"].Present || by["regadom"].Bytes != 1000 || !by["regadom"].WayBack || by["measurement"].Bytes != 5 || by["userprofiles"].Present {
		t.Fatalf("%+v", items)
	}
	if _, err := r.RemoveLegacy([]string{"regadom", "bogus"}); err == nil {
		t.Error("unknown id accepted")
	}
	removed, err := r.RemoveLegacy([]string{"regadom-bak", "measurement"})
	if err != nil || len(removed) != 2 {
		t.Fatalf("%v %v", err, removed)
	}
	if _, err := os.Stat(r.join("/etc/config/measurement")); !os.IsNotExist(err) {
		t.Error("measurement kept")
	}
	if !r.LegacyLeftovers()[0].Present {
		t.Error("regadom removed although not asked")
	}
	removed, _ = r.RemoveLegacy(nil)
	if len(removed) != 1 || removed[0] != "regadom" {
		t.Errorf("%v", removed)
	}
}
