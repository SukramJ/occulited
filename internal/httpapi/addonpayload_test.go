package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/system"
)

// payloadLister is an AddonLister with a fixed list, for the payload record's routes. It hands out
// a copy each time, as the real lister builds a fresh list: the routes mark the entries in place.
type payloadLister struct{ list []system.Addon }

func (l payloadLister) ListAddons(context.Context) ([]system.Addon, error) {
	out := make([]system.Addon, len(l.list))
	copy(out, l.list)
	return out, nil
}

// openccu-lite task 146: GET /addons marks an addon a restore brought back without its .nobackup
// directories, the Status warning names it, and the dismiss route hides it at this version.
func TestAddonsPayloadMissingAndDismiss(t *testing.T) {
	g := newBootRig(t, false)
	root := string(g.root)
	addons := root + "/usr/local/addons"
	for _, p := range []string{"hmm/app/.nobackup", "hmm/app/index.js", "hmm/bin/.nobackup", "hmm/bin/node", "hmm/etc/config.json", "plain/etc/x"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(addons, p)), 0o755)
		_ = os.WriteFile(filepath.Join(addons, p), nil, 0o644)
	}
	g.api.Addons = payloadLister{list: []system.Addon{{ID: "hmm", Name: "Homematic Manager", Version: "3.0.0", Enabled: true}, {ID: "plain", Name: "Plain", Version: "1", Enabled: true}}}
	g.api.AddonPayload = &system.PayloadRecord{Root: g.root, Path: filepath.Join(g.state, system.PayloadFile)}

	// before any restore: nothing is missing
	st, out := g.do(t, "GET", "/addons", "")
	list := out["addons"].([]any)
	if st != 200 || len(list) != 2 || list[0].(map[string]any)["payload_missing"] != nil {
		t.Fatalf("%d %v", st, out)
	}
	if st, out := g.do(t, "POST", "/addons/hmm/reinstall-dismiss", ""); st != 409 || out["error"] != "not-missing" {
		t.Errorf("dismiss while present: %d %v", st, out)
	}
	if ws, ok := g.api.addonWarnings(context.Background()); !ok || len(ws) != 0 {
		t.Errorf("warnings before: %v %v", ws, ok)
	}

	// the restore: the tagged directories hold nothing but their tag (tar --exclude-tag)
	_ = os.Remove(filepath.Join(addons, "hmm/app/index.js"))
	_ = os.Remove(filepath.Join(addons, "hmm/bin/node"))
	_, out = g.do(t, "GET", "/addons", "")
	hmm := out["addons"].([]any)[0].(map[string]any)
	dirs, _ := hmm["payload_missing_dirs"].([]any)
	if hmm["payload_missing"] != true || len(dirs) != 2 || dirs[0] != "app" || dirs[1] != "bin" || hmm["reinstall_dismissed"] != nil {
		t.Fatalf("after the restore: %v", hmm)
	}
	ws, _ := g.api.addonWarnings(context.Background())
	if len(ws) != 1 || ws[0].ID != "addon-payload" || ws[0].Variant != "hmm" || ws[0].Href != "/addons#reinstall" {
		t.Fatalf("warnings after: %+v", ws)
	}

	// dismissed at this version: the flag, no warning; a new version shows it again
	if st, out := g.do(t, "POST", "/addons/hmm/reinstall-dismiss", ""); st != 200 || out["version"] != "3.0.0" {
		t.Fatalf("dismiss: %d %v", st, out)
	}
	_, out = g.do(t, "GET", "/addons", "")
	hmm = out["addons"].([]any)[0].(map[string]any)
	if hmm["payload_missing"] != true || hmm["reinstall_dismissed"] != true {
		t.Errorf("dismissed: %v", hmm)
	}
	if ws, _ := g.api.addonWarnings(context.Background()); len(ws) != 0 {
		t.Errorf("warning although dismissed: %+v", ws)
	}
	if st, out := g.do(t, "POST", "/addons/nope/reinstall-dismiss", ""); st != 404 {
		t.Errorf("unknown: %d %v", st, out)
	}

	// the reinstall: the payload back, nothing missing, the dismissal gone
	for _, p := range []string{"hmm/app/index.js", "hmm/bin/node", "hmm/www/.nobackup", "hmm/www/index.html"} {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(addons, p)), 0o755)
		_ = os.WriteFile(filepath.Join(addons, p), nil, 0o644)
	}
	_, out = g.do(t, "GET", "/addons", "")
	hmm = out["addons"].([]any)[0].(map[string]any)
	if hmm["payload_missing"] != nil || hmm["reinstall_dismissed"] != nil {
		t.Errorf("after the reinstall: %v", hmm)
	}
	// without the record: never missing, the dismiss route says so
	g.api.AddonPayload = nil
	if st, _ := g.do(t, "POST", "/addons/hmm/reinstall-dismiss", ""); st != 501 {
		t.Errorf("without the record: %d", st)
	}
}

// openccu-lite B-267: an addon whose unit the fork's program check skipped after a restore is
// missing its program even where this daemon cannot look into the tagged directories: GET /addons
// marks it, the Status warning names it, and it is not also an ended addon.
func TestAddonsPayloadMissingUnitSkipped(t *testing.T) {
	g := newBootRig(t, false)
	g.api.Addons = payloadLister{list: []system.Addon{
		{ID: "mosquitto", Name: "Mosquitto", Version: "2.1.2", Enabled: true, Skipped: true},
		{ID: "hmm", Name: "Homematic Manager", Version: "3.0.0", Enabled: true, Running: true},
	}}
	g.api.AddonPayload = &system.PayloadRecord{Root: g.root, Path: filepath.Join(g.state, system.PayloadFile)}
	_, out := g.do(t, "GET", "/addons", "")
	list := out["addons"].([]any)
	m, h := list[0].(map[string]any), list[1].(map[string]any)
	if m["payload_missing"] != true || m["skipped"] != true || h["payload_missing"] != nil {
		t.Fatalf("%v / %v", m, h)
	}
	ws, _ := g.api.addonWarnings(context.Background())
	if len(ws) != 1 || ws[0].ID != "addon-payload" || ws[0].Variant != "mosquitto" {
		t.Fatalf("warnings: %+v", ws)
	}
}

// openccu-lite B-158: an addon whose daemon ended is a Status warning pointing at the Addons page.
func TestAddonEndedWarning(t *testing.T) {
	a := &SystemAPI{Root: fakeRoot(t), Addons: payloadLister{list: []system.Addon{{ID: "mosquitto", Name: "Mosquitto", Enabled: true, Ended: true}, {ID: "hmm", Name: "Homematic Manager", Enabled: true, Running: true}}}}
	ws, ok := a.addonWarnings(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "addon-ended" || ws[0].Variant != "mosquitto" || ws[0].Href != "/addons" {
		t.Fatalf("%+v %v", ws, ok)
	}
}
