package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/catalog"
)

// failingCatalog is a catalogue whose last check failed (catalog.Service.CheckError).
type failingCatalog struct {
	fakeCatalog
	f *catalog.CheckFailure
}

func (c *failingCatalog) CheckError() *catalog.CheckFailure { return c.f }

// occulited B-52: the catalog-check warning only while the check keeps failing - three in a row,
// or over a day - never for one failed check, which the Addons page says alone.
func TestCatalogCheckWarning(t *testing.T) {
	now := time.Now()
	cat := &failingCatalog{}
	a := &SystemAPI{Catalog: cat}
	if ws, ok := a.catalogCheckWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("no failure: %+v", ws)
	}
	cat.f = &catalog.CheckFailure{At: now, Since: now, Failures: 2, Message: "raw.githubusercontent.com: no such host", Host: "raw.githubusercontent.com"}
	if ws, _ := a.catalogCheckWarning(context.Background()); len(ws) != 0 {
		t.Fatalf("two failures within minutes: %+v", ws)
	}
	cat.f.Failures = 3
	ws, ok := a.catalogCheckWarning(context.Background())
	if !ok || len(ws) != 1 {
		t.Fatalf("%v %+v", ok, ws)
	}
	w := ws[0]
	if w.ID != "catalog-check" || w.Variant != "failing" || w.Href != "/addons" || w.Params["failures"] != 3 || w.Params["host"] != "raw.githubusercontent.com" || w.Params["detail"] != cat.f.Message {
		t.Fatalf("%+v", w)
	}
	cat.f = &catalog.CheckFailure{At: now, Since: now.Add(-25 * time.Hour), Failures: 2}
	if ws, _ := a.catalogCheckWarning(context.Background()); len(ws) != 1 {
		t.Fatalf("two daily failures: %+v", ws)
	}
	// no catalogue on this system
	if ws, ok := (&SystemAPI{}).catalogCheckWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("%+v", ws)
	}
}
