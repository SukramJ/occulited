package system

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// cacheBox is a fake systemd box for the unit cache (B-83): the list-units rows and show blocks it
// answers, a /proc and a unified cgroup tree under a temporary root, and a record of which units
// each `systemctl show` asked for.
type cacheBox struct {
	t      *testing.T
	root   string
	rows   map[string][2]string // unit -> active, sub
	blocks map[string]string    // unit -> show block without Id
	shows  [][]string
	lists  int
}

func newCacheBox(t *testing.T, unified bool) *cacheBox {
	b := &cacheBox{t: t, root: t.TempDir(), rows: map[string][2]string{}, blocks: map[string]string{}}
	b.write("proc/uptime", "5000.00 19000.00\n")
	if unified {
		b.write("sys/fs/cgroup/cgroup.controllers", "cpu memory pids\n")
	}
	return b
}

func (b *cacheBox) write(rel, content string) {
	b.t.Helper()
	p := filepath.Join(b.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		b.t.Fatal(err)
	}
}

// process makes a live /proc entry for pid
func (b *cacheBox) process(pid int) {
	b.write(fmt.Sprintf("proc/%d/stat", pid), fmt.Sprintf("%d (d) S 1 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100 0 0", pid))
}

// cgroup gives a unit a cgroup with a CPU time in microseconds and a task count
func (b *cacheBox) cgroup(unit string, usec, tasks int) {
	b.write("sys/fs/cgroup/system.slice/"+unit+"/cpu.stat", fmt.Sprintf("usage_usec %d\nuser_usec 1\nsystem_usec 1\n", usec))
	b.write("sys/fs/cgroup/system.slice/"+unit+"/pids.current", fmt.Sprintf("%d\n", tasks))
}

func (b *cacheBox) run(_ context.Context, name string, args ...string) ([]byte, error) {
	if name != "systemctl" {
		return nil, nil
	}
	switch args[0] {
	case "list-units":
		b.lists++
		units := make([]string, 0, len(b.rows))
		for u := range b.rows {
			units = append(units, u)
		}
		sort.Strings(units)
		var rows []string
		for _, u := range units {
			rows = append(rows, fmt.Sprintf(`{"unit":%q,"load":"loaded","active":%q,"sub":%q,"description":"x"}`, u, b.rows[u][0], b.rows[u][1]))
		}
		return []byte("[" + strings.Join(rows, ",") + "]"), nil
	case "show":
		var asked, out []string
		for i, a := range args {
			if a == "--" {
				asked = append(asked, args[i+1:]...)
				break
			}
		}
		for _, u := range asked {
			out = append(out, "Id="+u+"\n"+b.blocks[u])
		}
		b.shows = append(b.shows, asked)
		return []byte(strings.Join(out, "\n")), nil
	case "list-timers":
		return []byte(`[{"next":1789000000000000,"left":0,"last":0,"unit":"a.timer","activates":"a.service"},{"next":1789000000000000,"left":0,"last":0,"unit":"b.timer","activates":"b.service"}]`), nil
	}
	return []byte("ok"), nil
}

// takeShows returns the units asked since the last call, sorted, one string per show
func (b *cacheBox) takeShows() []string {
	var out []string
	for _, s := range b.shows {
		s = append([]string{}, s...)
		sort.Strings(s)
		out = append(out, strings.Join(s, " "))
	}
	b.shows = nil
	return out
}

// B-83: the Services page's listing shows a unit only when something about it can have changed,
// and reads CPU time and tasks from the cgroup every time.
func TestUnitCache(t *testing.T) {
	box := newCacheBox(t, true)
	box.rows["rfd.service"] = [2]string{"active", "running"}
	box.blocks["rfd.service"] = "MainPID=688\nUnitFileState=enabled\nCPUUsageNSec=1000000000\nTasksCurrent=5\nMemoryCurrent=[not set]\nControlGroup=/system.slice/rfd.service\nActiveEnterTimestampMonotonic=1000000000\n"
	box.process(688)
	box.cgroup("rfd.service", 2500000, 5)
	box.rows["hs485d.service"] = [2]string{"inactive", "dead"}
	box.blocks["hs485d.service"] = "MainPID=0\nUnitFileState=disabled\n"
	// a oneshot addon unit whose daemon runs in its cgroup
	box.rows["addon-jp.service"] = [2]string{"active", "exited"}
	box.blocks["addon-jp.service"] = "MainPID=0\nUnitFileState=generated\nType=oneshot\nResult=success\nTasksCurrent=1\nControlGroup=/system.slice/addon-jp.service\n"
	box.cgroup("addon-jp.service", 700000, 1)

	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	cache := NewUnitCache()
	s := SystemdServices{Root: Root(box.root), Run: box.run, Now: func() time.Time { return now }, Cache: cache}
	all := "addon-jp.service hs485d.service rfd.service"

	steps := []struct {
		name   string
		before func()
		shows  []string // what each `systemctl show` of the listing asked for
		check  func(t *testing.T, by map[string]Service)
	}{
		{"the first listing shows every unit", nil, []string{all}, func(t *testing.T, by map[string]Service) {
			// the CPU time is the cgroup's, not the older figure of the show block
			if r := by["rfd"]; !r.Running || r.PID != 688 || r.CPUSeconds != 2.5 {
				t.Errorf("rfd: %+v", r)
			}
			if j := by["addon-jp"]; !j.Running || j.OneShot {
				t.Errorf("a oneshot with a task left runs: %+v", j)
			}
		}},
		{"nothing changed: no show, the figures from the cgroup", func() { box.cgroup("rfd.service", 4000000, 6) }, nil, func(t *testing.T, by map[string]Service) {
			if r := by["rfd"]; r.CPUSeconds != 4 {
				t.Errorf("rfd cpu: %v", r.CPUSeconds)
			}
		}},
		{"the processes of a oneshot ended: its cgroup is gone, no show", func() {
			if err := os.RemoveAll(filepath.Join(box.root, "sys/fs/cgroup/system.slice/addon-jp.service")); err != nil {
				t.Fatal(err)
			}
		}, nil, func(t *testing.T, by map[string]Service) {
			if j := by["addon-jp"]; !j.OneShot || j.Result != "success" {
				t.Errorf("a oneshot with nothing left is completed: %+v", j)
			}
		}},
		{"a unit whose state changed is shown alone", func() {
			box.rows["hs485d.service"] = [2]string{"active", "running"}
			box.blocks["hs485d.service"] = "MainPID=700\nUnitFileState=enabled\nControlGroup=/system.slice/hs485d.service\n"
			box.process(700)
			box.cgroup("hs485d.service", 1000, 3)
		}, []string{"hs485d.service"}, func(t *testing.T, by map[string]Service) {
			if h := by["hs485d"]; !h.Running || h.PID != 700 || !h.Enabled {
				t.Errorf("hs485d: %+v", h)
			}
		}},
		{"a main process that is gone: restarted between two polls", func() {
			if err := os.RemoveAll(filepath.Join(box.root, "proc/688")); err != nil {
				t.Fatal(err)
			}
			box.blocks["rfd.service"] = strings.Replace(box.blocks["rfd.service"], "MainPID=688", "MainPID=689", 1)
			box.process(689)
		}, []string{"rfd.service"}, func(t *testing.T, by map[string]Service) {
			if r := by["rfd"]; r.PID != 689 {
				t.Errorf("rfd pid: %d", r.PID)
			}
		}},
		{"a control action shows its unit again", func() {
			if _, err := s.Control(context.Background(), "rfd", "restart"); err != nil {
				t.Fatal(err)
			}
		}, []string{"rfd.service"}, nil},
		{"a switch changes the unit file state of that unit", func() {
			if _, err := s.Control(context.Background(), "hs485d", "disable"); err != nil {
				t.Fatal(err)
			}
			box.rows["hs485d.service"] = [2]string{"inactive", "dead"}
			box.blocks["hs485d.service"] = "MainPID=0\nUnitFileState=masked-runtime\n"
		}, []string{"hs485d.service"}, func(t *testing.T, by map[string]Service) {
			if h := by["hs485d"]; h.Enabled || h.UnitFileState != "masked-runtime" {
				t.Errorf("hs485d: %+v", h)
			}
		}},
		{"a daemon-reload shows every unit", func() {
			if _, err := s.run(context.Background(), "daemon-reload"); err != nil {
				t.Fatal(err)
			}
		}, []string{all}, nil},
		{"a unit that appears is shown alone", func() {
			box.rows["redmatic.service"] = [2]string{"inactive", "dead"}
			box.blocks["redmatic.service"] = "MainPID=0\nUnitFileState=enabled\n"
		}, []string{"redmatic.service"}, func(t *testing.T, by map[string]Service) {
			if _, ok := by["redmatic"]; !ok {
				t.Error("redmatic not listed")
			}
		}},
		{"a unit that is gone leaves the cache", func() { delete(box.rows, "redmatic.service") }, nil, func(t *testing.T, by map[string]Service) {
			if _, ok := cache.services.units["redmatic.service"]; ok {
				t.Error("the cache kept a unit that is no longer listed")
			}
		}},
		{"within the age nothing is shown", func() { now = now.Add(4 * time.Minute) }, nil, nil},
		{"after the age every unit is shown", func() { now = now.Add(time.Minute) }, []string{all}, nil},
		{"a clock stepped back shows every unit", func() { now = now.Add(-time.Hour) }, []string{all}, nil},
		{"a show that fails is asked again next time", func() {
			saved := box.blocks["rfd.service"]
			box.blocks["rfd.service"] = "" // systemctl printed no block for it
			if _, err := s.run(context.Background(), "restart", "--no-pager", "--", "rfd.service"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { box.blocks["rfd.service"] = saved })
		}, []string{"rfd.service"}, nil},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			if st.before != nil {
				st.before()
			}
			box.takeShows()
			lists := box.lists
			list, err := s.List()
			if err != nil {
				t.Fatal(err)
			}
			if got := box.takeShows(); strings.Join(got, "|") != strings.Join(st.shows, "|") {
				t.Errorf("shows %q, want %q", got, st.shows)
			}
			if box.lists != lists+1 {
				t.Errorf("list-units calls: %d", box.lists-lists)
			}
			if st.check != nil {
				by := map[string]Service{}
				for _, sv := range list {
					by[sv.ID] = sv
				}
				st.check(t, by)
			}
		})
	}
}

// a box without the unified cgroup tree has no files to read the figures from: the running units
// are shown at every listing, the stopped ones are not
func TestUnitCacheWithoutUnifiedCgroups(t *testing.T) {
	box := newCacheBox(t, false)
	box.rows["rfd.service"] = [2]string{"active", "running"}
	box.blocks["rfd.service"] = "MainPID=688\nCPUUsageNSec=1000000000\nControlGroup=/system.slice/rfd.service\n"
	box.process(688)
	box.rows["hs485d.service"] = [2]string{"inactive", "dead"}
	box.blocks["hs485d.service"] = "MainPID=0\n"
	s := SystemdServices{Root: Root(box.root), Run: box.run, Cache: NewUnitCache()}
	for i, want := range []string{"hs485d.service rfd.service", "rfd.service", "rfd.service"} {
		list, err := s.List()
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(box.takeShows(), "|"); got != want {
			t.Errorf("listing %d: shows %q, want %q", i, got, want)
		}
		for _, sv := range list {
			if sv.ID == "rfd" && sv.CPUSeconds != 1 {
				t.Errorf("rfd cpu from systemd: %v", sv.CPUSeconds)
			}
		}
	}
}

// the timers' enabled state is cached too: asked again for the timer a command switched, and for
// every timer after a daemon-reload
func TestUnitCacheTimers(t *testing.T) {
	box := newCacheBox(t, true)
	box.blocks["a.timer"] = "UnitFileState=enabled\n"
	box.blocks["b.timer"] = "UnitFileState=enabled\n"
	s := SystemdServices{Root: Root(box.root), Run: box.run, Cache: NewUnitCache()}
	timers := func() []Timer {
		t.Helper()
		tm, err := s.Timers(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return tm
	}
	steps := []struct {
		name   string
		before func()
		shows  string
	}{
		{"first", nil, "a.timer b.timer"},
		{"again", nil, ""},
		{"a switched timer", func() {
			box.blocks["b.timer"] = "UnitFileState=masked-runtime\n"
			if _, err := s.Control(context.Background(), "b.timer", "disable"); err != nil {
				t.Fatal(err)
			}
		}, "b.timer"},
		{"a daemon-reload", func() { _, _ = s.run(context.Background(), "daemon-reload") }, "a.timer b.timer"},
	}
	for _, st := range steps {
		if st.before != nil {
			st.before()
		}
		box.takeShows()
		tm := timers()
		if got := strings.Join(box.takeShows(), "|"); got != st.shows {
			t.Errorf("%s: shows %q, want %q", st.name, got, st.shows)
		}
		if st.name == "a switched timer" && (tm[1].Enabled || tm[1].UnitFileState != "masked-runtime") {
			t.Errorf("%s: %+v", st.name, tm[1])
		}
	}
}

// the read-only commands leave the cache alone; the others invalidate what they name
func TestUnitCacheNoteCommand(t *testing.T) {
	cases := []struct {
		args        []string
		all, rfdOld bool // every unit stale / rfd.service stale afterwards
	}{
		{[]string{"show", "-p", "Id", "--", "rfd.service"}, false, false},
		{[]string{"list-units", "--all"}, false, false},
		{[]string{"is-enabled", "--", "rfd.service"}, false, false},
		{[]string{"cat", "--no-pager", "--", "rfd.service"}, false, false},
		{[]string{"restart", "--no-pager", "--", "rfd.service"}, false, true},
		{[]string{"mask", "--runtime", "--now", "--no-pager", "--", "hs485d.service"}, false, false},
		{[]string{"daemon-reload"}, true, true},
	}
	for _, c := range cases {
		cache := NewUnitCache()
		gen, _ := cache.generation()
		cache.noteCommand(c.args)
		if got := cache.stale("other.service", gen); got != c.all {
			t.Errorf("%v: every unit stale = %v", c.args, got)
		}
		if got := cache.stale("rfd.service", gen); got != c.rfdOld {
			t.Errorf("%v: rfd.service stale = %v", c.args, got)
		}
	}
}
