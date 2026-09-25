package system

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// proc is one process as the Services page and the addon settle step (task 48) see it: its pid,
// its command line and the cgroup it runs in. Everything here is read from /proc, which needs no
// privilege: /proc/<pid>/cmdline and /proc/<pid>/cgroup are readable for every process, which
// /proc/<pid>/exe is not for an unprivileged occulited.
type proc struct {
	PID    int
	Cmd    string
	Cgroup string // the cgroup v2 path ("/system.slice/addon-hmm.service"); empty when unreadable
}

// addonProcesses lists the processes whose command line names a file under the addon's
// directory, /usr/local/addons/<id>/ - node-red under RedMatic's, mosquitto under its own, the
// manager's node. It is the one signal that survives a daemon started outside its unit (an
// addon's own restart button before 28.8's wrapper, or an update script in the install scope,
// task 48): the unit's cgroup is then empty and systemd says "exited" while the daemon runs on -
// which is exactly the case the Services page must not report as Completed or Stopped. The list
// is in pid order.
func addonProcesses(root Root, id string) []proc {
	needle := "/usr/local/addons/" + id + "/"
	entries, err := os.ReadDir(root.join("/proc"))
	if err != nil {
		return nil
	}
	var out []proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		cmd := procCmdline(root, pid)
		if cmd == "" || !strings.Contains(cmd, needle) {
			continue
		}
		out = append(out, proc{PID: pid, Cmd: cmd, Cgroup: procCgroup(root, pid)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

// procCmdline is the process's command line with the NULs turned into spaces, "" when the
// process is gone or a kernel thread.
func procCmdline(root Root, pid int) string {
	b, err := os.ReadFile(filepath.Join(root.join("/proc"), strconv.Itoa(pid), "cmdline"))
	if err != nil || len(b) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
}

// procCgroup is the process's cgroup path from /proc/<pid>/cgroup: the "0::" line on cgroup v2
// (every product boots with the unified hierarchy), or the name=systemd controller's line on v1.
// "" when the file cannot be read - a process that ended between the directory listing and this.
func procCgroup(root Root, pid int) string {
	b, err := os.ReadFile(filepath.Join(root.join("/proc"), strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	v1 := ""
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), ":", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			return parts[2]
		}
		if parts[1] == "name=systemd" {
			v1 = parts[2]
		}
	}
	return v1
}

// inCgroup reports whether path is the cgroup of unit (a unit name such as addon-hmm.service or
// occulite-addon-1234.scope) or lies below it. systemd puts a unit's processes into
// /system.slice/<unit>, and a unit with Delegate= may make children below that. The unit name
// has to be a whole path component: addon-hm.service is not addon-hmm.service.
func inCgroup(path, unit string) bool {
	return path != "" && unit != "" && (strings.HasSuffix(path, "/"+unit) || strings.Contains(path, "/"+unit+"/"))
}

// procAlive reports whether the process still exists as something that can be signalled: a
// zombie has a /proc entry until its parent reaps it, but it has already ended, and waiting for
// it to go would only wait for the parent.
func procAlive(root Root, pid int) bool {
	dir := filepath.Join(root.join("/proc"), strconv.Itoa(pid))
	b, err := os.ReadFile(filepath.Join(dir, "stat"))
	if err != nil {
		// no stat file: gone - unless the directory is still there (a stat that could not be
		// read for another reason, or a fake /proc in a test), which counts as alive
		_, serr := os.Stat(dir)
		return serr == nil
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return true
	}
	f := strings.Fields(s[i+1:])
	return len(f) == 0 || (f[0] != "Z" && f[0] != "X")
}

// ---- a pid for a unit without a main process (task 49) ----------------------------------------

// procStat reads the parent pid and the start time (in clock ticks since boot) of a process
// from /proc/<pid>/stat. The command name in field 2 is in parentheses and may itself contain
// spaces and parentheses, so the fields are counted from the last ")".
func procStat(root Root, pid int) (ppid int, start uint64, ok bool) {
	b, err := os.ReadFile(filepath.Join(root.join("/proc"), strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, 0, false
	}
	s := string(b)
	i := strings.LastIndex(s, ")")
	if i < 0 {
		return 0, 0, false
	}
	// after ")": state ppid pgrp session tty tpgid flags minflt cminflt majflt cmajflt utime
	// stime cutime cstime priority nice num_threads itrealvalue starttime ... (fields 3 to 22)
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0, 0, false
	}
	ppid, err = strconv.Atoi(f[1])
	if err != nil {
		return 0, 0, false
	}
	start, err = strconv.ParseUint(f[19], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ppid, start, true
}

// cgroupProcs lists the pids in a cgroup, read from /sys/fs/cgroup<path>/cgroup.procs (mode
// 0644, readable unprivileged - checked on the Pi) in pid order. nil when the cgroup is gone.
// The path comes from systemctl's ControlGroup; one that tries to leave the cgroup tree is not
// followed.
func cgroupProcs(root Root, cgroup string) []int {
	if cgroup == "" || !strings.HasPrefix(cgroup, "/") || strings.Contains(cgroup, "..") {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(root.join("/sys/fs/cgroup"), cgroup, "cgroup.procs"))
	if err != nil {
		return nil
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if pid, err := strconv.Atoi(f); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

// leaderOf picks the process the Services page shows as a unit's PID when systemd has no
// MainPID for it (every generated addon unit is Type=oneshot): the process whose parent is not
// in the same set - RedMatic's loader rather than the node-red it started. Several such
// processes (a script that started two daemons and ended): the oldest by start time, and the
// lowest pid when the start times cannot be read or are equal. The rest come back in pid order.
// pids must be sorted.
func leaderOf(root Root, pids []int) (leader int, rest []int) {
	if len(pids) == 0 {
		return 0, nil
	}
	set := make(map[int]bool, len(pids))
	for _, p := range pids {
		set[p] = true
	}
	type cand struct {
		pid   int
		start uint64
	}
	var leaders []cand
	for _, p := range pids {
		ppid, start, ok := procStat(root, p)
		if !ok {
			// unreadable: it may have ended a moment ago; it can still be the leader when
			// nothing readable qualifies, and then the lowest pid decides
			leaders = append(leaders, cand{p, ^uint64(0)})
			continue
		}
		if !set[ppid] {
			leaders = append(leaders, cand{p, start})
		}
	}
	if len(leaders) == 0 {
		leaders = []cand{{pids[0], 0}} // every parent is inside the set: the lowest pid
	}
	sort.SliceStable(leaders, func(i, j int) bool {
		if leaders[i].start != leaders[j].start {
			return leaders[i].start < leaders[j].start
		}
		return leaders[i].pid < leaders[j].pid
	})
	leader = leaders[0].pid
	for _, p := range pids {
		if p != leader {
			rest = append(rest, p)
		}
	}
	return leader, rest
}

// maxProcs caps the process list a service carries: enough for every addon seen so far (RedMatic
// has six), and a bound for a runaway forker - pids_more still counts them all.
const maxProcs = 32

// ServiceProc is one process of a service as the page's popup lists it (task 49).
type ServiceProc struct {
	PID int    `json:"pid"`
	Cmd string `json:"cmd"`
}

// procList turns pids into the page's list, the leader first, capped at maxProcs.
func procList(root Root, leader int, rest []int) []ServiceProc {
	out := []ServiceProc{{PID: leader, Cmd: procCmdline(root, leader)}}
	for _, p := range rest {
		if len(out) >= maxProcs {
			break
		}
		out = append(out, ServiceProc{PID: p, Cmd: procCmdline(root, p)})
	}
	return out
}
