package httpapi

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/bootchart"
	"github.com/hobbyquaker/occulited/internal/system"
)

func TestBootSnapshotsRoutes(t *testing.T) {
	const (
		current = "6c97f67681ee40c78e69d3bc0c03efea"
		older   = "7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d" // in the journal and kept
		oldest  = "1e2b5b5c1d6f4b0e9a1c3f6e7d8a9b0c" // kept, no longer in the journal
	)
	r := logRoot(t)
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", "6c97f676-81ee-40c7-8e69-d3bc0c03efea\n")
	writeRootFile(t, r, "proc/uptime", "527.58 1848.78\n")
	manager, err := os.ReadFile("../bootchart/testdata/manager-charly.txt")
	if err != nil {
		t.Fatal(err)
	}
	units, err := os.ReadFile("../bootchart/testdata/show-units-charly.txt")
	if err != nil {
		t.Fatal(err)
	}
	reader := &bootchart.Reader{Root: string(r), Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case len(args) > 1 && args[1] == "--all":
			return units, nil
		case len(args) > 0 && args[0] == "show":
			return manager, nil
		}
		return nil, errors.New("unexpected systemctl call")
	}}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	store := &bootchart.Store{Dir: t.TempDir(), Now: func() time.Time { return now }}
	for _, id := range []string{oldest, older} {
		tl := bootchart.Build(manager, units, 0)
		tl.BootID, tl.Started = id, now.Add(-2*time.Minute).Format(time.RFC3339)
		if err := store.Save(&tl); err != nil {
			t.Fatal(err)
		}
		now = now.Add(24 * time.Hour)
	}
	list := `[{"index":-1,"boot_id":"` + older + `","first_entry":1789060000000000,"last_entry":1789140000000000},{"index":0,"boot_id":"` + current + `","first_entry":1789232771104733,"last_entry":1789233285739510}]`
	journal := &system.JournalLog{Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		return []byte(list), nil
	}}
	api := &SystemAPI{Root: r, Log: &recordingLog{}, Journal: journal, BootChart: reader, BootSnapshots: store}
	srv := logDownloadServer(t, api)

	// this boot: the newest kept boot is the previous one
	st, out, body := do(t, srv, "GET", "/api/system/v1/boot", "", nil)
	prev, _ := out["previous"].(map[string]any)
	if st != 200 || out["boot_id"] != current || out["snapshot"] != nil || prev == nil || prev["boot_id"] != older || prev["total_ms"] != 114730.0 {
		t.Fatalf("this boot: %d %.400s", st, body)
	}
	// a kept boot: its timeline, marked, with the boot kept before it
	st, out, body = do(t, srv, "GET", "/api/system/v1/boot?id="+older, "", nil)
	prev, _ = out["previous"].(map[string]any)
	list209, _ := out["units"].([]any)
	if st != 200 || out["boot_id"] != older || out["snapshot"] != true || prev == nil || prev["boot_id"] != oldest || len(list209) != 209 {
		t.Fatalf("kept: %d %.400s", st, body)
	}
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/boot?id="+strings.ToUpper(oldest), "", nil); st != 200 || out["previous"] != nil {
		t.Errorf("the oldest kept: %d previous %v", st, out["previous"])
	}
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/boot?id=ffffffffffffffffffffffffffffffff", "", nil); st != 404 || out["error"] != "no-timeline" {
		t.Errorf("not kept: %d %v", st, out)
	}
	// once this boot is kept, the previous one is still the one before it
	tl, _ := reader.Current(context.Background())
	now = now.Add(time.Hour)
	if err := store.Save(tl); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/boot", "", nil); out["previous"].(map[string]any)["boot_id"] != older {
		t.Errorf("after this boot was kept: %v", out["previous"])
	}

	// the boots: the journal's with their index, the kept ones marked, the kept one the journal no longer holds without
	st, out, body = do(t, srv, "GET", "/api/system/v1/boots", "", nil)
	boots, _ := out["boots"].([]any)
	if st != 200 || len(boots) != 3 {
		t.Fatalf("boots: %d %s", st, body)
	}
	byID := map[string]map[string]any{}
	for _, b := range boots {
		m := b.(map[string]any)
		byID[m["boot_id"].(string)] = m
	}
	if b := byID[current]; b["journal"] != true || b["snapshot"] != true || b["current"] != true || b["index"] != 0.0 {
		t.Errorf("current: %v", b)
	}
	if b := byID[older]; b["journal"] != true || b["snapshot"] != true || b["index"] != -1.0 || b["total_ms"] != 114730.0 {
		t.Errorf("older: %v", b)
	}
	b := byID[oldest]
	if _, hasIndex := b["index"]; b["journal"] != false || b["snapshot"] != true || hasIndex || b["first"] == nil || b["current"] != false {
		t.Errorf("oldest: %v", b)
	}
	if last := boots[2].(map[string]any); last["boot_id"] != oldest {
		t.Errorf("the kept one after the journal's: %v", boots)
	}

	// without a store: this boot only, no previous, no marks
	plain := logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}, Journal: journal, BootChart: reader})
	if _, out, _ := do(t, plain, "GET", "/api/system/v1/boot", "", nil); out["previous"] != nil {
		t.Errorf("no store: %v", out["previous"])
	}
	if st, _, _ := do(t, plain, "GET", "/api/system/v1/boot?id="+older, "", nil); st != 404 {
		t.Errorf("no store, a kept id: %d", st)
	}
	if _, out, body := do(t, plain, "GET", "/api/system/v1/boots", "", nil); strings.Contains(body, `"snapshot"`) || len(out["boots"].([]any)) != 2 {
		t.Errorf("no store: %s", body)
	}
}
