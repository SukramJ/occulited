package httpapi

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/bootchart"
)

func TestBootTimelineRoute(t *testing.T) {
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
	calls := 0
	var fail error
	reader := &bootchart.Reader{Root: string(r), Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if fail != nil {
			return nil, fail
		}
		if len(args) > 1 && args[1] == "--all" {
			return units, nil
		}
		return manager, nil
	}}
	srv := logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}, BootChart: reader})

	st, out, body := do(t, srv, "GET", "/api/system/v1/boot", "", nil)
	if st != 200 || out["boot_id"] != "6c97f67681ee40c78e69d3bc0c03efea" || out["finished"] != true {
		t.Fatalf("%d %.300s", st, body)
	}
	list, _ := out["units"].([]any)
	chain, _ := out["critical_chain"].([]any)
	summary, _ := out["summary"].(map[string]any)
	if len(list) < 150 || len(chain) < 25 || chain[0] != "multi-user.target" || summary["total_ms"] != 114730.0 || summary["kernel_ms"] != 8714.0 {
		t.Errorf("%d units, chain %v, summary %v", len(list), chain, summary)
	}
	first := list[0].(map[string]any)
	if _, ok := first["activating"].(float64); !ok || first["id"] == "" || first["state"] == "" {
		t.Errorf("a unit: %v", first)
	}
	// this boot by 0 and by its id in either form: the finished boot is kept
	for _, q := range []string{"?id=0", "?id=6c97f676-81ee-40c7-8e69-d3bc0c03efea", "?id=6C97F67681EE40C78E69D3BC0C03EFEA"} {
		if st, out, _ := do(t, srv, "GET", "/api/system/v1/boot"+q, "", nil); st != 200 || out["boot_id"] != "6c97f67681ee40c78e69d3bc0c03efea" {
			t.Errorf("%s: %d", q, st)
		}
	}
	// the manager and the units once, the units' live state once within its TTL (B-153)
	if calls != 3 {
		t.Errorf("systemctl ran %d times", calls)
	}
	// an earlier boot has no timeline here; an offset or a word is no id
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/boot?id=7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d", "", nil); st != 404 || out["error"] != "no-timeline" {
		t.Errorf("earlier: %d %v", st, out)
	}
	for _, q := range []string{"?id=-1", "?id=latest", "?id=1"} {
		if st, out, _ := do(t, srv, "GET", "/api/system/v1/boot"+q, "", nil); st != 422 || out["error"] != "invalid" {
			t.Errorf("%s: %d %v", q, st, out)
		}
	}
	// systemctl failing: an error, and the next request tries again
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", "648df1b0-f925-453b-bf4e-4cf221fc0545\n")
	fail = errors.New("exit status 1")
	if st, _, body := do(t, srv, "GET", "/api/system/v1/boot", "", nil); st < 500 || !strings.Contains(body, "exit status 1") {
		t.Errorf("failure: %d %s", st, body)
	}
	// busybox: no timeline
	bb := logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}})
	if st, out, _ := do(t, bb, "GET", "/api/system/v1/boot", "", nil); st != 501 || out["error"] != "unsupported" {
		t.Errorf("busybox: %d %v", st, out)
	}
}

// B-153: the running boot after occulited restarted, with units restarted since the boot - served
// from the boot's kept snapshot with the states of now; without a snapshot the later units are named.
func TestBootTimelineRunningBootRestarted(t *testing.T) {
	const id = "6c97f67681ee40c78e69d3bc0c03efea"
	r := logRoot(t)
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", "6c97f676-81ee-40c7-8e69-d3bc0c03efea\n")
	writeRootFile(t, r, "proc/uptime", "7100.00 1.00\n")
	manager := []byte("UserspaceTimestampMonotonic=3000000\nFinishTimestampMonotonic=65300000\n")
	block := func(unit, state string, activating, activated int64) string {
		return "Id=" + unit + "\nActiveState=" + state + "\nSubState=running\nInactiveExitTimestampMonotonic=" + strconv.FormatInt(activating, 10) +
			"\nActiveEnterTimestampMonotonic=" + strconv.FormatInt(activated, 10) + "\nInactiveEnterTimestampMonotonic=0\nAfter=\n"
	}
	atBoot := strings.Join([]string{
		block("multi-user.target", "active", 65000000, 65200000),
		block("occulited.service", "active", 27300000, 27400000),
		block("rfd.service", "active", 33200000, 35700000),
		block("hmipserver.service", "active", 33200000, 59100000),
		block("sshd.service", "active", 20000000, 20100000),
	}, "\n")
	// systemd now: rfd and hmipserver restarted at 17:00, occulited at 18:39
	now := strings.Join([]string{
		block("multi-user.target", "active", 65000000, 65200000),
		block("occulited.service", "active", 7041000000, 7041100000),
		block("rfd.service", "active", 1308000000, 1309000000),
		block("hmipserver.service", "active", 1310000000, 1340000000),
		block("sshd.service", "active", 20000000, 20100000),
	}, "\n")
	reader := func() *bootchart.Reader {
		return &bootchart.Reader{Root: string(r), Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			if len(args) > 1 && args[1] == "--all" {
				return []byte(now), nil
			}
			return manager, nil
		}}
	}
	units := func(out map[string]any) map[string]map[string]any {
		m := map[string]map[string]any{}
		list, _ := out["units"].([]any)
		for _, u := range list {
			x := u.(map[string]any)
			m[x["id"].(string)] = x
		}
		return m
	}

	// no snapshot: the first read after the restart has lost the three; the note names them
	srv := logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}, BootChart: reader()})
	st, out, body := do(t, srv, "GET", "/api/system/v1/boot", "", nil)
	if st != 200 || out["later"] != 3.0 {
		t.Fatalf("%d %.400s", st, body)
	}
	if got, _ := out["later_units"].([]any); len(got) != 3 || got[0] != "hmipserver.service" || got[1] != "occulited.service" || got[2] != "rfd.service" {
		t.Errorf("later units %v", out["later_units"])
	}
	if u := units(out); len(u) != 2 || u["sshd.service"] == nil {
		t.Errorf("units %v", u)
	}

	// the boot kept while it ran: its stamps, the states and restarts of now
	store := &bootchart.Store{Dir: t.TempDir(), Now: func() time.Time { return time.Date(2026, 9, 18, 16, 43, 0, 0, time.UTC) }}
	kept := bootchart.Build(manager, []byte(atBoot), 0)
	kept.BootID = id
	if err := store.Save(&kept); err != nil {
		t.Fatal(err)
	}
	srv = logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}, BootChart: reader(), BootSnapshots: store})
	st, out, body = do(t, srv, "GET", "/api/system/v1/boot", "", nil)
	if st != 200 || out["later"] != 0.0 || out["snapshot"] != nil || out["boot_id"] != id || out["finished"] != true {
		t.Fatalf("%d %.400s", st, body)
	}
	u := units(out)
	for unit, want := range map[string][2]float64{"occulited.service": {27.3, 7041}, "rfd.service": {33.2, 1308}, "hmipserver.service": {33.2, 1310}} {
		if x := u[unit]; x == nil || x["activating"] != want[0] || x["restarted"] != want[1] || x["state"] != "active" {
			t.Errorf("%s: %v", unit, x)
		}
	}
	if x := u["sshd.service"]; x == nil || x["restarted"] != nil {
		t.Errorf("sshd: %v", x)
	}
}
