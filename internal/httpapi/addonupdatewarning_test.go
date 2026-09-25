package httpapi

import (
	"context"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonupdates"
	"github.com/hobbyquaker/occulited/internal/catalog"
	"github.com/hobbyquaker/occulited/internal/system"
)

// cachedCatalog is a catalogue whose view the system holds already (catalog.Service.Cached).
type cachedCatalog struct {
	fakeCatalog
	cached *catalog.View
}

func (c *cachedCatalog) Cached() *catalog.View { return c.cached }

// openccu-lite task 248: the addon-update warning - from the catalogue's latest releases as the
// system holds them, and for an addon the catalogue does not know from its own update check; the
// variant names every id@version; none when nothing is newer.
func TestAddonUpdateWarning(t *testing.T) {
	box := &updatesBox{addons: []system.Addon{
		{ID: "mosquitto", Name: "Mosquitto", Version: "2.1.0", Update: "/addons/mosquitto/update.cgi"},
		{ID: "hm2mqtt", Name: "hm2mqtt", Version: "3.6.2", Update: "/addons/hm2mqtt/update.cgi"},
		{ID: "own", Name: "Own addon", Version: "1.0", Update: "/addons/own/update.cgi"},
	}, available: map[string]bool{"own": true, "mosquitto": true}}
	upd := &addonupdates.Service{Lister: box, Checker: box}
	upd.Check(context.Background())
	cat := &cachedCatalog{cached: &catalog.View{Addons: []catalog.Item{
		item("mosquitto", &catalog.Latest{Version: "2.1.2"}),
		item("hm2mqtt", &catalog.Latest{Version: "3.6.2"}),
	}}}
	a := &SystemAPI{Addons: box, Catalog: cat, Updates: upd}
	ws, ok := a.addonUpdateWarning(context.Background())
	if !ok || len(ws) != 1 {
		t.Fatalf("%v %+v", ok, ws)
	}
	w := ws[0]
	// mosquitto from the catalogue (its own check is not asked for an addon the catalogue knows),
	// the own addon from its own check; hm2mqtt is current
	if w.ID != "addon-update" || w.Href != "/addons" || w.Variant != "mosquitto@2.1.2,own@" || w.Params["count"] != 2 {
		t.Fatalf("%+v", w)
	}
	// nothing newer: no warning
	box.set("own", false)
	upd.Check(context.Background())
	cat.cached = &catalog.View{Addons: []catalog.Item{item("mosquitto", &catalog.Latest{Version: "2.1.0"})}}
	if ws, ok := a.addonUpdateWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("%+v", ws)
	}
	// no catalogue held yet and no check: nothing, and nothing fetched
	a2 := &SystemAPI{Addons: box, Catalog: &cachedCatalog{}}
	if ws, ok := a2.addonUpdateWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("%+v", ws)
	}
}

// task 248: an enabled addon whose unit failed is addon-failed (an error, linked to /addons); a
// disabled one is not
func TestAddonFailedWarning(t *testing.T) {
	box := &updatesBox{addons: []system.Addon{
		{ID: "mosquitto", Name: "Mosquitto", Enabled: true, Failed: true},
		{ID: "off", Name: "Off", Enabled: false, Failed: true},
		{ID: "fine", Name: "Fine", Enabled: true, Running: true},
	}, available: map[string]bool{}}
	a := &SystemAPI{Addons: box}
	ws, ok := a.addonWarnings(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "addon-failed" || ws[0].Variant != "mosquitto" || ws[0].Severity != "error" || ws[0].Href != "/addons" {
		t.Fatalf("%v %+v", ok, ws)
	}
}
