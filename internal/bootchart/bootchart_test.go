package bootchart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata holds captures of 2026-09-12 (fork 7006221d4, systemd 258.7), a quarter of an hour after
// a boot: show-units-* is `systemctl show --all -p <unitProps> '*'`, manager-* the manager's
// timestamps, and for the two Pis - the x86_64 VM has no systemd-analyze - what systemd-analyze
// critical-chain, time and blame printed a minute before.

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// spanUS reads systemd's FORMAT_TIMESPAN at millisecond accuracy: "1min 46.011s", "296ms".
func spanUS(t *testing.T, s string) int64 {
	t.Helper()
	var us float64
	for _, tok := range strings.Fields(s) {
		unit, mult := "", 0.0
		for _, u := range []struct {
			suffix string
			mult   float64
		}{{"min", 60e6}, {"ms", 1e3}, {"us", 1}, {"h", 3600e6}, {"d", 86400e6}, {"s", 1e6}} {
			if strings.HasSuffix(tok, u.suffix) {
				unit, mult = u.suffix, u.mult
				break
			}
		}
		n, err := strconv.ParseFloat(strings.TrimSuffix(tok, unit), 64)
		if unit == "" || err != nil {
			t.Fatalf("timespan %q", s)
		}
		us += n * mult
	}
	return int64(us + 0.5)
}

// truncated: systemd prints a timespan cut to the millisecond, so the exact value lies in [printed, printed + 1 ms)
func truncated(exact, printed int64) bool { return exact >= printed && exact-printed < 1000 }

var chainLine = regexp.MustCompile(`^[\s│├└─]*(\S+)(?: @(.+?))?(?: \+(\S+))?\s*$`)

func TestCriticalChainAgainstSystemdAnalyze(t *testing.T) {
	for _, box := range []string{"charly", "rpi4"} {
		t.Run(box, func(t *testing.T) {
			manager, units := read(t, "manager-"+box+".txt"), read(t, "show-units-"+box+".txt")
			tl := Build(manager, units, 0)
			all := parseUnits(units)
			userspace := int64(tl.Timestamps.Userspace*1e6 + 0.5)

			var want []string
			lines := strings.Split(strings.TrimSpace(string(read(t, "critical-chain-"+box+".txt"))), "\n")
			for _, l := range lines[3:] { // two lines of explanation and a blank one
				m := chainLine.FindStringSubmatch(l)
				if m == nil {
					t.Fatalf("line %q", l)
				}
				want = append(want, m[1])
				u := all[m[1]]
				if u == nil {
					t.Fatalf("%s is not in the capture", m[1])
				}
				// "@" is when the unit began to start where a "+" follows, else when it was up
				at := u.activated
				if m[3] != "" {
					at = u.activating
				}
				if m[2] != "" && !truncated(at-userspace, spanUS(t, m[2])) {
					t.Errorf("%s @%s: %d µs after userspace", m[1], m[2], at-userspace)
				}
				if m[3] != "" && !truncated(u.durationUS(0), spanUS(t, m[3])) {
					t.Errorf("%s +%s: took %d µs", m[1], m[3], u.durationUS(0))
				}
			}
			if strings.Join(tl.CriticalChain, " ") != strings.Join(want, " ") {
				t.Errorf("chain\n got %v\nwant %v", tl.CriticalChain, want)
			}
			if len(want) < 25 {
				t.Errorf("a chain of %d", len(want))
			}
		})
	}
}

var analyzeTime = regexp.MustCompile(`Startup finished in (.+?) \(kernel\) \+ (.+?) \(userspace\) = (.+?)\s*\n.*reached after (.+?) in userspace`)

func TestSummaryAndBlameAgainstSystemdAnalyze(t *testing.T) {
	for _, box := range []string{"charly", "rpi4"} {
		t.Run(box, func(t *testing.T) {
			tl := Build(read(t, "manager-"+box+".txt"), read(t, "show-units-"+box+".txt"), 0)
			m := analyzeTime.FindStringSubmatch(string(read(t, "analyze-time-"+box+".txt")))
			if m == nil {
				t.Fatal("analyze time")
			}
			s := tl.Summary
			if s.InitRDMS != nil || s.UserspaceMS == nil || s.TotalMS == nil || s.MultiUserMS == nil || !tl.Finished {
				t.Fatalf("%+v", s)
			}
			userspaceMS := int64(tl.Timestamps.Userspace * 1000)
			for _, c := range []struct {
				name   string
				ms     int64
				source string
			}{{"kernel", s.KernelMS, m[1]}, {"userspace", *s.UserspaceMS, m[2]}, {"total", *s.TotalMS, m[3]}, {"multi-user", *s.MultiUserMS - userspaceMS, m[4]}} {
				// milliseconds against a printed millisecond value: one apart at most, from the two cuts
				if d := c.ms - spanUS(t, c.source)/1000; d < -1 || d > 1 {
					t.Errorf("%s: %d ms, systemd-analyze %s", c.name, c.ms, c.source)
				}
			}

			byID := map[string]Unit{}
			for _, u := range tl.Units {
				byID[u.ID] = u
			}
			matched := 0
			for _, l := range strings.Split(strings.TrimSpace(string(read(t, "blame-head-"+box+".txt"))), "\n") {
				f := strings.Fields(l)
				id, span := f[len(f)-1], strings.Join(f[:len(f)-1], " ")
				u, ok := byID[id]
				if !ok {
					continue // started after the boot finished (a timer's oneshot)
				}
				matched++
				if d := u.DurationMS - spanUS(t, span)/1000; d < 0 || d > 1 {
					t.Errorf("%s: %d ms, blame %s", id, u.DurationMS, span)
				}
			}
			if matched < 12 {
				t.Errorf("%d of the blame's units found", matched)
			}
			// blame's first line is the slowest unit of the boot
			if first := strings.Fields(strings.Split(string(read(t, "blame-head-"+box+".txt")), "\n")[0]); s.Slowest == nil || s.Slowest.ID != first[len(first)-1] {
				t.Errorf("slowest %+v, blame %v", s.Slowest, first)
			}
		})
	}
}

func TestBuildVM(t *testing.T) {
	// the x86_64 VM: no initrd, no systemd-analyze - the manager's own figures and the shape
	tl := Build(read(t, "manager-119.txt"), read(t, "show-units-119.txt"), 1_030_000_000)
	if !tl.Finished || tl.Timestamps.Userspace != 6.380673 || *tl.Timestamps.Finish != 73.643085 || tl.Timestamps.InitRD != nil || *tl.Timestamps.MultiUser != 73.639375 {
		t.Errorf("timestamps %+v", tl.Timestamps)
	}
	if tl.Summary.KernelMS != 6380 || *tl.Summary.UserspaceMS != 67262 || *tl.Summary.TotalMS != 73643 {
		t.Errorf("summary %+v", tl.Summary)
	}
	if len(tl.Units) < 150 || tl.CriticalChain[0] != Root || len(tl.CriticalChain) < 10 {
		t.Errorf("%d units, chain %v", len(tl.Units), tl.CriticalChain)
	}
	in := map[string]bool{}
	for i, u := range tl.Units {
		if i > 0 && u.Activating < tl.Units[i-1].Activating {
			t.Errorf("order at %s", u.ID)
		}
		if u.Activating <= 0 || u.Activating > *tl.Timestamps.Finish {
			t.Errorf("%s began at %v", u.ID, u.Activating)
		}
		in[u.ID] = true
	}
	for _, u := range tl.Units {
		for _, a := range u.After {
			if !in[a] {
				t.Errorf("%s after %s, which is not in the timeline", u.ID, a)
			}
		}
	}
	for _, c := range tl.CriticalChain {
		if !in[c] {
			t.Errorf("chain unit %s not in the timeline", c)
		}
	}
	// the interfaces and occulited are there, each with its start and a duration
	for _, id := range []string{"rfd.service", "hmipserver.service", "occulited.service", "lighttpd.service"} {
		if !in[id] {
			t.Errorf("%s missing", id)
		}
	}
}

// SYNTHETIC: a boot still starting, a oneshot that ended, a unit that failed, a restart after the
// boot, a tie in the chain and an ordering cycle - the shapes the captures do not show.
func TestBuildShapes(t *testing.T) {
	manager := "InitRDTimestampMonotonic=1000000\nUserspaceTimestampMonotonic=3000000\nFinishTimestampMonotonic=20000000\n"
	block := func(id, state string, activating, activated, deactivated int64, after string) string {
		return "Id=" + id + "\nActiveState=" + state + "\nInactiveExitTimestampMonotonic=" + strconv.FormatInt(activating, 10) +
			"\nActiveEnterTimestampMonotonic=" + strconv.FormatInt(activated, 10) + "\nInactiveEnterTimestampMonotonic=" + strconv.FormatInt(deactivated, 10) +
			"\nAfter=" + after + "\n"
	}
	units := strings.Join([]string{
		block("multi-user.target", "active", 19000000, 19500000, 0, "a.service b.service c.service"),
		block("a.service", "active", 10000000, 15000000, 0, "base.target"),
		block("b.service", "active", 12000000, 15000000, 0, "base.target a.service"),
		block("c.service", "inactive", 4000000, 0, 4500000, ""),
		block("base.target", "active", 5000000, 5000000, 0, "b.service"), // a cycle back to b
		block("failed.service", "failed", 6000000, 0, 9000000, ""),
		block("restarted.service", "active", 500000000, 500100000, 0, ""),
		block("never.service", "inactive", 0, 0, 0, ""),
	}, "\n")
	tl := Build([]byte(manager), []byte(units), 600000000)
	if tl.Summary.KernelMS != 1000 || *tl.Summary.InitRDMS != 2000 || *tl.Summary.UserspaceMS != 17000 || tl.Later != 1 || len(tl.Units) != 6 {
		t.Fatalf("%+v later %d units %d", tl.Summary, tl.Later, len(tl.Units))
	}
	if strings.Join(tl.LaterUnits, " ") != "restarted.service" {
		t.Errorf("later units %v", tl.LaterUnits)
	}
	got := map[string]Unit{}
	for _, u := range tl.Units {
		got[u.ID] = u
	}
	if c := got["c.service"]; c.DurationMS != 500 || c.Active != nil || *c.Inactive != 4.5 {
		t.Errorf("oneshot %+v", c)
	}
	if f := got["failed.service"]; f.DurationMS != 3000 || f.State != "failed" {
		t.Errorf("failed %+v", f)
	}
	if b := got["b.service"]; strings.Join(b.After, " ") != "base.target a.service" {
		t.Errorf("after %v", b.After)
	}
	// a and b became active at the same moment: both, latest first in a stable order. From b the
	// unit up last is a again: named, not followed a second time - as systemd-analyze prints "..."
	if strings.Join(tl.CriticalChain, " ") != "multi-user.target a.service base.target b.service a.service b.service" {
		t.Errorf("chain %v", tl.CriticalChain)
	}
	if tl.Summary.Slowest.ID != "a.service" && tl.Summary.Slowest.ID != "b.service" {
		t.Errorf("slowest %+v", tl.Summary.Slowest)
	}

	// still starting: no finish, no chain, the starting unit counts until now
	starting := block("hmipserver.service", "activating", 40000000, 0, 0, "rfd.service")
	tl = Build([]byte("UserspaceTimestampMonotonic=3000000\nFinishTimestampMonotonic=0\n"), []byte(starting), 55000000)
	if tl.Finished || len(tl.CriticalChain) != 0 || tl.Summary.UserspaceMS != nil || len(tl.Units) != 1 || tl.Units[0].DurationMS != 15000 || tl.Units[0].Active != nil {
		t.Errorf("starting: %+v", tl)
	}
	// nothing at all
	if tl := Build(nil, nil, 0); tl.Units == nil || tl.CriticalChain == nil || tl.Finished {
		t.Errorf("empty: %+v", tl)
	}
}

func TestReader(t *testing.T) {
	dir := t.TempDir()
	write := func(p, c string) {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/sys/kernel/random/boot_id", "6c97f676-81ee-40c7-8e69-d3bc0c03efea\n")
	write("proc/uptime", "527.58 1848.78\n")
	now := time.Date(2026, 9, 12, 19, 14, 59, 0, time.UTC)
	manager := read(t, "manager-charly.txt")
	calls := 0
	var fail error
	r := &Reader{Root: dir, Now: func() time.Time { return now }, Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if fail != nil {
			return nil, fail
		}
		if strings.Join(args, " ") == strings.Join(ManagerArgs(), " ") {
			return manager, nil
		}
		if strings.Join(args, " ") != "show --all -p "+unitProps+" *" {
			t.Errorf("args %v", args)
		}
		return read(t, "show-units-charly.txt"), nil
	}}
	tl, err := r.Current(context.Background())
	if err != nil || tl.BootID != "6c97f67681ee40c78e69d3bc0c03efea" || tl.Started != "2026-09-12T19:06:11Z" || !tl.Finished || calls != 2 {
		t.Fatalf("%v %+v %d", err, tl, calls)
	}
	// a finished boot is kept, however long
	now = now.Add(time.Hour)
	if _, err := r.Current(context.Background()); err != nil || calls != 2 {
		t.Errorf("kept: %v %d", err, calls)
	}
	// another boot is read again
	write("proc/sys/kernel/random/boot_id", "648df1b0-f925-453b-bf4e-4cf221fc0545\n")
	if tl, _ := r.Current(context.Background()); calls != 4 || tl.BootID != "648df1b0f925453bbf4e4cf221fc0545" {
		t.Errorf("new boot: %d", calls)
	}
	// a boot still starting is kept for a few seconds only
	manager = []byte("UserspaceTimestampMonotonic=8714083\nFinishTimestampMonotonic=0\n")
	r.cached = nil
	r.Current(context.Background())
	now = now.Add(2 * time.Second)
	r.Current(context.Background())
	if calls != 6 {
		t.Errorf("within the few seconds: %d", calls)
	}
	now = now.Add(4 * time.Second)
	r.Current(context.Background())
	if calls != 8 {
		t.Errorf("after them: %d", calls)
	}
	// systemctl fails: an error, nothing kept
	r.cached = nil
	fail = errors.New("exit status 1")
	if _, err := r.Current(context.Background()); err == nil || !strings.Contains(err.Error(), "systemctl show") {
		t.Errorf("failure: %v", err)
	}
	if _, err := (&Reader{Root: dir}).Current(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("no systemd: %v", err)
	}
}

// B-153: the running boot's timeline keeps the boot's stamps and takes the units' state from now.
func TestOverlay(t *testing.T) {
	manager := "UserspaceTimestampMonotonic=3000000\nFinishTimestampMonotonic=60000000\n"
	block := func(id, state string, activating, activated int64) string {
		return "Id=" + id + "\nActiveState=" + state + "\nSubState=" + map[string]string{"active": "running", "failed": "failed", "activating": "start"}[state] +
			"\nInactiveExitTimestampMonotonic=" + strconv.FormatInt(activating, 10) + "\nActiveEnterTimestampMonotonic=" + strconv.FormatInt(activated, 10) +
			"\nInactiveEnterTimestampMonotonic=0\nAfter=\n"
	}
	// the boot: rfd, hmipserver and occulited started during it
	boot := strings.Join([]string{
		block("rfd.service", "active", 33200000, 35700000),
		block("hmipserver.service", "active", 33200000, 59100000),
		block("occulited.service", "active", 27300000, 27400000),
		block("sshd.service", "active", 20000000, 20100000),
	}, "\n")
	kept := Build([]byte(manager), []byte(boot), 0)
	if len(kept.Units) != 4 || kept.Later != 0 {
		t.Fatalf("the boot: %+v", kept)
	}
	// now: rfd and hmipserver restarted (the module flashed), occulited restarted, sshd failed, a
	// timer's service ran later
	live := ParseLive([]byte(strings.Join([]string{
		block("rfd.service", "active", 1308000000, 1309000000),
		block("hmipserver.service", "activating", 1310000000, 0),
		block("occulited.service", "active", 7041000000, 7041100000),
		block("sshd.service", "failed", 20000000, 20100000),
		block("fstrim.service", "inactive", 3600000000, 0),
		block("never.service", "inactive", 0, 0),
	}, "\n")))
	got := Overlay(kept, live)
	units := map[string]Unit{}
	for _, u := range got.Units {
		units[u.ID] = u
	}
	for id, want := range map[string]struct {
		activating float64
		state      string
		restarted  float64
	}{
		"rfd.service":        {33.2, "active", 1308},
		"hmipserver.service": {33.2, "activating", 1310},
		"occulited.service":  {27.3, "active", 7041},
		"sshd.service":       {20, "failed", 0},
	} {
		u := units[id]
		restarted := 0.0
		if u.Restarted != nil {
			restarted = *u.Restarted
		}
		if u.Activating != want.activating || u.State != want.state || restarted != want.restarted {
			t.Errorf("%s: %+v restarted %v, want %+v", id, u, restarted, want)
		}
	}
	if got.Later != 1 || strings.Join(got.LaterUnits, " ") != "fstrim.service" {
		t.Errorf("later %d %v", got.Later, got.LaterUnits)
	}
	if kept.Units[0].Restarted != nil || kept.Units[0].State != "active" {
		t.Errorf("the timeline given was changed: %+v", kept.Units[0])
	}
	// a boot still starting: states only, nothing counts as later
	starting := Build([]byte("UserspaceTimestampMonotonic=3000000\nFinishTimestampMonotonic=0\n"), []byte(boot), 40000000)
	if o := Overlay(starting, live); o.Later != 0 || o.LaterUnits != nil || o.Units[0].Restarted != nil {
		t.Errorf("starting: %+v", o)
	}
}

func TestReaderLive(t *testing.T) {
	now := time.Date(2026, 9, 23, 16, 0, 0, 0, time.UTC)
	calls := 0
	r := &Reader{Root: t.TempDir(), Now: func() time.Time { return now }, Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if strings.Join(args, " ") != "show --all -p "+LiveProps+" *" {
			t.Errorf("args %v", args)
		}
		return []byte("Id=rfd.service\nActiveState=active\nSubState=running\nInactiveExitTimestampMonotonic=5\n"), nil
	}}
	for range 2 {
		if l, err := r.Live(context.Background()); err != nil || l["rfd.service"].State != "active" || l["rfd.service"].Activating != 5 {
			t.Fatalf("%v %v", l, err)
		}
	}
	if calls != 1 {
		t.Errorf("within the TTL: %d calls", calls)
	}
	now = now.Add(liveTTL)
	_, _ = r.Live(context.Background())
	if calls != 2 {
		t.Errorf("after the TTL: %d calls", calls)
	}
	if _, err := (&Reader{}).Live(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("no systemd: %v", err)
	}
}
