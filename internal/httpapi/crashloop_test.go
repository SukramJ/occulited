package httpapi

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// openccu-lite task 283: the crash-loop warning names the looping units from the sampler; occulited's
// own loop comes from its unit's state file for a day after the last failure.
func TestCrashLoopWarnings(t *testing.T) {
	r := fakeRoot(t)
	a := &SystemAPI{Root: r}
	if ws, ok := a.crashLoopWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("without a sampler: %+v", ws)
	}
	if ws, ok := a.occulitedCrashLoopWarning(context.Background()); !ok || len(ws) != 0 {
		t.Fatalf("without a file: %+v", ws)
	}

	// a sampler that saw rfd restart three times
	boot := time.Now().Add(-time.Hour)
	now := time.Now()
	n := 0
	a.CrashLoops = &system.CrashLoops{
		Show: func(_ context.Context, units []string, _ ...string) map[string]map[string]string {
			return map[string]map[string]string{"rfd.service": {"LoadState": "loaded", "ActiveState": "activating", "SubState": "auto-restart", "Result": "exit-code", "NRestarts": strconv.Itoa(n)}}
		},
		Now:    func() time.Time { return now },
		Uptime: func() time.Duration { return now.Sub(boot) },
	}
	a.CrashLoops.Sample(context.Background())
	if ws, _ := a.crashLoopWarning(context.Background()); len(ws) != 0 {
		t.Fatalf("no restart yet: %+v", ws)
	}
	for n = 1; n <= 3; n++ {
		now = now.Add(20 * time.Second)
		a.CrashLoops.Sample(context.Background())
	}
	ws, ok := a.crashLoopWarning(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "crash-loop" || ws[0].Variant != "rfd" || ws[0].Severity != "error" || ws[0].Href != "/system/services" {
		t.Fatalf("the loop: %+v", ws)
	}
	if units, _ := ws[0].Params["units"].([]system.CrashLoopUnit); len(units) != 1 || units[0].Restarts != 3 {
		t.Errorf("params: %+v", ws[0].Params)
	}

	// occulited's own state file: three failures, the last a minute ago
	write := func(fails int, last time.Time) {
		t.Helper()
		f := filepath.Join(string(r), system.OcculitedUnitStateFile)
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		body := `{"state":"running","since":1,"started":1,"restarts":4,"fails":` + strconv.Itoa(fails) + `,"first_fail":1790000000,"last_fail":` + strconv.FormatInt(last.Unix(), 10) + `,"result":"signal","next_retry":0,"reason":""}`
		if err := os.WriteFile(f, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(3, time.Now().Add(-time.Minute))
	ws, ok = a.occulitedCrashLoopWarning(context.Background())
	if !ok || len(ws) != 1 || ws[0].ID != "occulited-crash-loop" || ws[0].Variant != "1790000000" || ws[0].Severity != "warning" || ws[0].Params["fails"] != 3 || ws[0].Params["result"] != "signal" {
		t.Fatalf("own loop: %+v", ws)
	}
	// two failures are no loop; a loop older than a day is gone
	write(2, time.Now())
	if ws, _ := a.occulitedCrashLoopWarning(context.Background()); len(ws) != 0 {
		t.Errorf("two failures: %+v", ws)
	}
	write(5, time.Now().Add(-25*time.Hour))
	if ws, _ := a.occulitedCrashLoopWarning(context.Background()); len(ws) != 0 {
		t.Errorf("a day old: %+v", ws)
	}
}
