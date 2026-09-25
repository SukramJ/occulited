package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The leader of a cgroup (task 49): the process whose parent is not in it; several such, the
// oldest; nothing readable, the lowest pid; nothing at all, nothing.
func TestLeaderOf(t *testing.T) {
	root := Root(t.TempDir())
	// RedMatic's shape: a loader, node-red under it, loggers under both
	fakeProcess(t, root, 2210, "/usr/local/addons/redmatic/bin/node loader.js", "", 1, 100)
	fakeProcess(t, root, 2230, "/usr/local/addons/redmatic/bin/node red.js", "", 2210, 150)
	fakeProcess(t, root, 2231, "/usr/bin/logger -t node-red", "", 2210, 151)
	fakeProcess(t, root, 2240, "/usr/bin/logger -t red", "", 2230, 160)
	if l, rest := leaderOf(root, []int{2210, 2230, 2231, 2240}); l != 2210 || fmt.Sprint(rest) != "[2230 2231 2240]" {
		t.Errorf("one leader: %d %v", l, rest)
	}
	// one process
	if l, rest := leaderOf(root, []int{2230}); l != 2230 || rest != nil {
		t.Errorf("single: %d %v", l, rest)
	}
	// two leaders (a script that started two daemons and ended): the older one, though its pid
	// is higher
	fakeProcess(t, root, 300, "/usr/local/addons/x/a", "", 1, 900)
	fakeProcess(t, root, 400, "/usr/local/addons/x/b", "", 1, 500)
	fakeProcess(t, root, 401, "/usr/local/addons/x/c", "", 400, 600)
	if l, rest := leaderOf(root, []int{300, 400, 401}); l != 400 || fmt.Sprint(rest) != "[300 401]" {
		t.Errorf("several leaders: %d %v", l, rest)
	}
	// no stat readable at all: the lowest pid
	if l, rest := leaderOf(root, []int{10, 20, 30}); l != 10 || fmt.Sprint(rest) != "[20 30]" {
		t.Errorf("unreadable: %d %v", l, rest)
	}
	// empty
	if l, rest := leaderOf(root, nil); l != 0 || rest != nil {
		t.Errorf("empty: %d %v", l, rest)
	}
	// a stat with a command name holding ") (" is still read from the last parenthesis
	_ = os.WriteFile(root.join("/proc/401/stat"), []byte("401 (we (ird) name) S 400 401 401 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 600 0 0\n"), 0o644)
	if ppid, start, ok := procStat(root, 401); !ok || ppid != 400 || start != 600 {
		t.Errorf("procStat: %d %d %v", ppid, start, ok)
	}
	// cgroup.procs: sorted, and a path that leaves the tree is not read
	_ = os.MkdirAll(root.join("/sys/fs/cgroup/system.slice/a.service"), 0o755)
	_ = os.WriteFile(root.join("/sys/fs/cgroup/system.slice/a.service/cgroup.procs"), []byte("30\n10\n20\n"), 0o644)
	if got := cgroupProcs(root, "/system.slice/a.service"); fmt.Sprint(got) != "[10 20 30]" {
		t.Errorf("cgroup.procs: %v", got)
	}
	if cgroupProcs(root, "/system.slice/../../../etc") != nil || cgroupProcs(root, "") != nil || cgroupProcs(root, "/system.slice/gone.service") != nil {
		t.Error("cgroupProcs read what it should not")
	}
}

// The Services list (task 49): a PID for every unit that has processes, from its cgroup when
// systemd has no MainPID; the stray addon's pid from outside; nothing for a completed oneshot;
// the raw UnitFileState beside Enabled - and still one `systemctl show` for all units (B-60).
func TestServicesPIDFromCgroupAndUnitFileState(t *testing.T) {
	root := Root(t.TempDir())
	cg := func(unit, procs string) {
		dir := root.join("/sys/fs/cgroup/system.slice/" + unit)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(dir+"/cgroup.procs", []byte(procs), 0o644)
	}
	red := "/system.slice/addon-redmatic.service"
	cg("addon-redmatic.service", "2230\n2210\n2231\n2232\n2233\n2240\n")
	fakeProcess(t, root, 2210, "/usr/local/addons/redmatic/bin/node /usr/local/addons/redmatic/lib/loader.js", red, 1, 100)
	fakeProcess(t, root, 2230, "/usr/local/addons/redmatic/bin/node red.js", red, 2210, 150)
	for _, p := range []int{2231, 2232, 2233} {
		fakeProcess(t, root, p, "/usr/bin/logger -t node-red", red, 2210, 151)
	}
	fakeProcess(t, root, 2240, "/usr/bin/logger -t x", red, 2230, 160)
	cg("addon-mosquitto.service", "2074\n")
	fakeProcess(t, root, 2074, "/usr/local/addons/mosquitto/bin/mosquitto -c x", "/system.slice/addon-mosquitto.service", 1, 90)
	// hmm: its unit is empty, the daemon and a child of it run in an old install scope
	scope := "/system.slice/occulite-addon-08e4e24f.scope"
	cg("addon-hmm.service", "")
	fakeProcess(t, root, 6807, "node /usr/local/addons/hmm/app/dist/cli.js", scope, 1, 1000)
	fakeProcess(t, root, 6810, "/usr/local/addons/hmm/bin/helper", scope, 6807, 1001)
	fakeProcess(t, root, 980, "/usr/sbin/lighttpd -f /etc/lighttpd/lighttpd.conf", scope, 1, 50)
	cg("rfd.service", "688\n")

	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		switch args[0] {
		case "list-units":
			return []byte(`[{"unit":"rfd.service","load":"loaded","active":"active","sub":"running","description":"rfd"},
{"unit":"addon-redmatic.service","load":"loaded","active":"active","sub":"exited","description":"Addon redmatic"},
{"unit":"addon-mosquitto.service","load":"loaded","active":"active","sub":"exited","description":"Addon mosquitto"},
{"unit":"addon-hmm.service","load":"loaded","active":"active","sub":"exited","description":"Addon hmm"},
{"unit":"addon-jp.service","load":"loaded","active":"active","sub":"exited","description":"Addon jp"},
{"unit":"occu-init-rf-hardware.service","load":"loaded","active":"active","sub":"exited","description":"init rf"},
{"unit":"ssdpd.service","load":"masked","active":"inactive","sub":"dead","description":"ssdpd"}]`), nil
		case "show":
			return []byte(strings.Join([]string{
				"Id=rfd.service\nMainPID=688\nUnitFileState=enabled\nType=forking\nControlGroup=/system.slice/rfd.service\n",
				"Id=addon-redmatic.service\nMainPID=0\nUnitFileState=generated\nType=oneshot\nTasksCurrent=24\nControlGroup=" + red + "\nSourcePath=/usr/local/etc/config/rc.d/redmatic\n",
				"Id=addon-mosquitto.service\nMainPID=0\nUnitFileState=generated\nType=oneshot\nTasksCurrent=1\nControlGroup=/system.slice/addon-mosquitto.service\n",
				"Id=addon-hmm.service\nMainPID=0\nUnitFileState=generated\nType=oneshot\nTasksCurrent=0\nControlGroup=/system.slice/addon-hmm.service\n",
				"Id=addon-jp.service\nMainPID=0\nUnitFileState=generated\nType=oneshot\nResult=success\nTasksCurrent=[not set]\nControlGroup=\n",
				"Id=occu-init-rf-hardware.service\nMainPID=0\nUnitFileState=static\nType=oneshot\nResult=success\nTasksCurrent=0\nControlGroup=\n",
				"Id=ssdpd.service\nMainPID=0\nUnitFileState=masked-runtime\nType=simple\nControlGroup=\n",
			}, "\n")), nil
		}
		return nil, nil
	}
	s := SystemdServices{Root: root, Run: run}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], ",TasksCurrent,ControlGroup,") {
		t.Errorf("one list-units and one show with ControlGroup (B-60): %v", calls)
	}
	by := map[string]Service{}
	for _, sv := range list {
		by[sv.ID] = sv
	}
	if r := by["addon-redmatic"]; r.PID != 2210 || r.PIDsMore != 5 || len(r.Procs) != 6 || r.Procs[0].PID != 2210 || !strings.Contains(r.Procs[0].Cmd, "loader.js") || r.Stray || r.UnitFileState != "generated" || !r.Enabled {
		t.Errorf("redmatic: %+v", r)
	}
	if m := by["addon-mosquitto"]; m.PID != 2074 || m.PIDsMore != 0 || len(m.Procs) != 1 {
		t.Errorf("mosquitto: %+v", m)
	}
	// the stray addon: the pid found outside, never lighttpd beside it, and stray says so
	if h := by["addon-hmm"]; !h.Stray || !h.Running || h.PID != 6807 || h.PIDsMore != 1 || len(h.Procs) != 2 || h.Procs[1].PID != 6810 {
		t.Errorf("stray hmm: %+v", h)
	}
	// completed oneshots show no pid, as before
	if j := by["addon-jp"]; !j.OneShot || j.PID != 0 || j.Procs != nil {
		t.Errorf("completed: %+v", j)
	}
	// MainPID wins where there is one; no process list for it
	if r := by["rfd"]; r.PID != 688 || r.PIDsMore != 0 || r.Procs != nil || r.UnitFileState != "enabled" {
		t.Errorf("rfd: %+v", r)
	}
	// the raw state passes through; Enabled keeps its verdict
	if st := by["occu-init-rf-hardware"]; st.UnitFileState != "static" || !st.Enabled {
		t.Errorf("static: %+v", st)
	}
	if st := by["ssdpd"]; st.UnitFileState != "masked-runtime" || st.Enabled {
		t.Errorf("masked-runtime: %+v", st)
	}
	b, _ := json.Marshal(by["addon-redmatic"])
	for _, key := range []string{`"pid":2210`, `"pids_more":5`, `"procs":[{"pid":2210,"cmd":`, `"unit_file_state":"generated"`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON lacks %s: %s", key, b)
		}
	}
}
