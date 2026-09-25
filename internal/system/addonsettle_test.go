package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/ownwalk"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// fakeProcess puts one process into a fake /proc: its command line (NUL-separated), its cgroup
// (the cgroup v2 line) and a stat line with the parent pid and the start time in the fields the
// kernel uses, so that the readers see what they see on a box.
func fakeProcess(t *testing.T, root Root, pid int, cmd, cgroup string, ppid int, start uint64) {
	t.Helper()
	dir := root.join(fmt.Sprintf("/proc/%d", pid))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "cmdline"), []byte(strings.ReplaceAll(cmd, " ", "\x00")+"\x00"), 0o644)
	if cgroup != "" {
		_ = os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::"+cgroup+"\n"), 0o644)
	}
	name := filepath.Base(strings.Fields(cmd)[0])
	stat := fmt.Sprintf("%d (%s) S %d %d %d 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 %d 0 0\n", pid, name, ppid, pid, pid, start)
	_ = os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o644)
}

const testScope = "occulite-addon-08e4e24f.scope"

// The leftover rule (task 48): only processes of that addon, only in that scope - never a
// lighttpd an installer restarted into the scope (B-3), never the addon's processes that are in
// its unit already, never another addon whose name starts the same.
func TestAddonLeftoverSelection(t *testing.T) {
	root := Root(t.TempDir())
	scope := "/system.slice/" + testScope
	fakeProcess(t, root, 100, "node /usr/local/addons/hmm/app/dist/cli.js", scope, 1, 500)
	fakeProcess(t, root, 101, "/usr/sbin/lighttpd -f /etc/lighttpd/lighttpd.conf", scope, 1, 400)
	fakeProcess(t, root, 102, "node /usr/local/addons/hmm/app/worker.js", "/system.slice/addon-hmm.service", 1, 300)
	fakeProcess(t, root, 103, "/usr/local/addons/hmm/bin/helper", "/system.slice/lighttpd.service", 1, 600)
	fakeProcess(t, root, 104, "/usr/local/addons/hmmx/bin/daemon", scope, 1, 700)
	fakeProcess(t, root, 105, "/usr/local/addons/redmatic/bin/node", scope, 1, 800)
	fakeProcess(t, root, 106, "sh /usr/local/addons/hmm/bin/loop.sh", scope+"/child", 1, 900)
	fakeProcess(t, root, 107, "node /usr/local/addons/hmm/app/dist/cli.js", "/system.slice/addon-hmm.service/sub", 1, 950)
	s := SystemdServices{Root: root}

	pids := func(ps []proc) string {
		var out []string
		for _, p := range ps {
			out = append(out, fmt.Sprint(p.PID))
		}
		return strings.Join(out, " ")
	}
	if got := pids(s.addonLeftovers("hmm", testScope)); got != "100 106" {
		t.Errorf("in the install scope: %q, want the addon's two processes there and nothing else", got)
	}
	// the stray rule: anywhere but the unit
	if got := pids(s.addonLeftovers("hmm", "")); got != "100 103 106" {
		t.Errorf("outside the unit: %q", got)
	}
	if got := pids(s.addonLeftovers("redmatic", testScope)); got != "105" {
		t.Errorf("another addon: %q", got)
	}
	// whole path components only
	if inCgroup("/system.slice/addon-hmmx.service", "addon-hmm.service") || inCgroup("/system.slice/xaddon-hmm.service", "addon-hmm.service") || inCgroup("", "addon-hmm.service") {
		t.Error("inCgroup matched a different unit")
	}
	// cgroup v1: the name=systemd line
	_ = os.WriteFile(root.join("/proc/103/cgroup"), []byte("12:pids:/system.slice/x.service\n1:name=systemd:/system.slice/addon-hmm.service\n"), 0o644)
	if cg := procCgroup(root, 103); cg != "/system.slice/addon-hmm.service" {
		t.Errorf("v1 cgroup: %q", cg)
	}
	// a zombie is not alive, whatever /proc still shows
	_ = os.WriteFile(root.join("/proc/104/stat"), []byte("104 (daemon) Z 1 104 104 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 700\n"), 0o644)
	if procAlive(root, 104) || !procAlive(root, 100) || procAlive(root, 999) {
		t.Error("procAlive")
	}
}

// settleRig is an install on a fake box: rc.d with the wrapper in front of hmm, an installer
// script the test writes, a recorder runner that can make a TERMed process go away, and a fake
// /proc the test fills.
type settleRig struct {
	root   Root
	calls  []string
	active map[string]string // unit -> ActiveState for `systemctl show`
	tasks  map[string]string // unit -> TasksCurrent
	dies   bool              // a TERMed process ends (false: it has to be KILLed)
	// chownErr makes the ownership walk fail with this error
	chownErr error
	// onOwn is told every directory the walk gives away (not the dry run)
	onOwn func(dir string, uid int)
	// dirty makes the dry run find an entry with another owner (task 107)
	dirty bool
	// since is InactiveExitTimestampMonotonic per unit for `systemctl show`
	since map[string]string
	a     *SystemdAddons
}

func newSettleRig(t *testing.T, installer string) *settleRig {
	t.Helper()
	r := rootWith(t, map[string]string{
		"usr/local/tmp/.keep":                        "",
		"etc/passwd":                                 "root:x:0:0::/:/bin/sh\n",
		"etc/group":                                  "root:x:0:\n",
		"usr/local/etc/config/rc.d/hmm":              "#!/bin/sh\n# openccu-lite addon-rc wrapper (test copy)\n",
		"usr/local/etc/config/rc.d/hmm.script":       "#!/bin/sh\nexit 0\n",
		"usr/local/etc/config/rc.d/old":              "#!/bin/sh\nexit 0\n",
		"usr/local/addons/hmm/rc.d/hmm":              "#!/bin/sh\nexit 0\n",
		"usr/local/etc/config/addon-policy/hmm.json": `{"id":"hmm","mode":"root","source":"user"}`,
		"usr/local/etc/config/addon-policy/old.json": `{"id":"old","mode":"root","source":"user"}`,
	})
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	script := strings.NewReplacer("ROOT", string(r)).Replace(installer)
	if err := os.WriteFile(r.join("/bin/install_addon"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rig := &settleRig{root: r, active: map[string]string{}, tasks: map[string]string{}, since: map[string]string{}, dies: true}
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		rig.calls = append(rig.calls, name+" "+strings.Join(args, " "))
		switch {
		case name == "kill" && rig.dies:
			for _, a := range args[1:] {
				_ = os.RemoveAll(r.join("/proc/" + a))
			}
		case name == "systemctl" && args[0] == "start":
			// a unit that started is active (B-186 looks after the start)
			rig.active[args[len(args)-1]] = "active"
		case name == "systemctl" && args[0] == "show":
			var blocks []string
			sep := 0
			for i, a := range args {
				if a == "--" {
					sep = i + 1
				}
			}
			for _, unit := range args[sep:] {
				b := "Id=" + unit + "\nActiveState=" + rig.active[unit] + "\n"
				if tc, ok := rig.tasks[unit]; ok {
					b += "TasksCurrent=" + tc + "\n"
				}
				if s, ok := rig.since[unit]; ok {
					b += "InactiveExitTimestampMonotonic=" + s + "\n"
				}
				blocks = append(blocks, b)
			}
			return []byte(strings.Join(blocks, "\n")), nil
		}
		return []byte("ok"), nil
	}
	rig.a = NewSystemdAddons(r, SystemdServices{Root: r, Run: run, StopWait: 30 * time.Millisecond})
	rig.a.scopeFn = func() string { return testScope }
	return rig
}

func (rig *settleRig) index(t *testing.T, call string) int {
	t.Helper()
	for i, c := range rig.calls {
		if c == call {
			return i
		}
	}
	return -1
}

// hmm's update script, as it is: stop (the wrapper sends that to the unit), its own link over
// the wrapper, start - which meets no wrapper and leaves the new daemon in the install scope.
const hmmUpdate = "#!/bin/sh\nrm -f ROOT/usr/local/etc/config/rc.d/hmm\nln -sfn ROOT/usr/local/addons/hmm/rc.d/hmm ROOT/usr/local/etc/config/rc.d/hmm\nrm -f ROOT/usr/local/tmp/new_addon.tar.gz\necho updated\nexit 0\n"

// An update of an existing id (task 48): adopted again, its unit stopped, the daemon the update
// script left in the scope stopped by pid - not lighttpd beside it - and the unit started.
func TestInstallSettlesAnUpdatedAddon(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	scope := "/system.slice/" + testScope
	fakeProcess(t, rig.root, 6807, "node /usr/local/addons/hmm/app/dist/cli.js", scope, 1, 1000)
	fakeProcess(t, rig.root, 980, "/usr/sbin/lighttpd -f /etc/lighttpd/lighttpd.conf", scope, 1, 900)
	rig.active["addon-hmm.service"] = "inactive" // the update's stop reached the unit

	res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rig.calls, "\n")
	adopt, stop, term, start := rig.index(t, AddonRCTool+" adopt hmm"), rig.index(t, "systemctl stop --no-pager -- addon-hmm.service"), rig.index(t, "kill -TERM 6807"), rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
	if adopt < 0 || stop < 0 || term < 0 || start < 0 || !(adopt < stop && stop < term && term < start) {
		t.Fatalf("want adopt, stop, kill, start in that order:\n%s", joined)
	}
	if !strings.Contains(res.Output, "[systemd] hmm restarted in its unit") {
		t.Errorf("output:\n%s", res.Output)
	}
	if strings.Contains(joined, "980") || strings.Contains(joined, "-KILL") {
		t.Errorf("lighttpd was signalled, or a process that ended got a KILL:\n%s", joined)
	}
	// what was done to an addon, not what was asked: B-98's look at every addon's state before the
	// install names addon-old too
	var acted []string
	for _, c := range rig.calls {
		if !strings.HasPrefix(c, "systemctl show ") {
			acted = append(acted, c)
		}
	}
	if done := strings.Join(acted, "\n"); strings.Contains(done, "addon-old") || strings.Contains(done, "adopt hmm old") {
		t.Errorf("an addon the install did not touch was touched:\n%s", joined)
	}
	if strings.Contains(joined, testScope) {
		t.Errorf("the install scope must never be stopped (B-3):\n%s", joined)
	}
}

// An update that leaves the addon stopped (the user had stopped it, and its script does not
// start it) is adopted and left stopped.
func TestInstallLeavesAStoppedAddonStopped(t *testing.T) {
	rig := newSettleRig(t, "#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > ROOT/usr/local/etc/config/rc.d/hmm\nrm -f ROOT/usr/local/tmp/new_addon.tar.gz\nexit 0\n")
	rig.active["addon-hmm.service"] = "inactive"
	res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rig.calls, "\n")
	if rig.index(t, AddonRCTool+" adopt hmm") < 0 {
		t.Errorf("the replaced script was not adopted:\n%s", joined)
	}
	if strings.Contains(joined, "start --no-pager -- addon-hmm") || strings.Contains(joined, "restart") || strings.Contains(joined, "kill") {
		t.Errorf("a stopped addon was started:\n%s", joined)
	}
	if !strings.Contains(res.Output, "hmm is not running and was not started") {
		t.Errorf("output:\n%s", res.Output)
	}
}

// settlePriv puts the helper's recursive chown (the data directory take-over) into the rig's call
// list, so its place among the systemctl calls can be checked; nothing is chowned for real.
type settlePriv struct {
	priv.Local
	rig *settleRig
}

func (p settlePriv) Chown(path string, uid, gid int, recursive bool) error {
	if recursive {
		p.rig.calls = append(p.rig.calls, fmt.Sprintf("lchown -R %d:%d %s", uid, gid, path))
	}
	return nil
}

// OwnTree records the helper's ownership walk (task 107) as "chown -R" was recorded before it, one
// line per directory that exists (the walk skips a missing one), and plays it on the rig: onOwn,
// chownErr, and dirty for the dry run (task 110's quick one is "owntree --dry-run --quick").
func (p settlePriv) OwnTree(_ string, dirs []string, uid int, opt priv.OwnTreeOptions) (ownwalk.Result, error) {
	if opt.Quick && !opt.DryRun {
		return ownwalk.Result{}, fmt.Errorf("a quick walk that changes entries is not asked for")
	}
	if opt.DryRun {
		quick := ""
		if opt.Quick {
			quick = "--quick "
		}
		p.rig.calls = append(p.rig.calls, fmt.Sprintf("owntree --dry-run %s%d %s", quick, uid, strings.Join(dirs, " ")))
		if p.rig.dirty {
			return ownwalk.Result{Checked: 3, Wrong: 1, FirstWrong: dirs[0] + "/var/start.lock"}, nil
		}
		return ownwalk.Result{Checked: 3}, nil
	}
	for _, d := range dirs {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		p.rig.calls = append(p.rig.calls, fmt.Sprintf("chown -R %d:%d %s", uid, uid, d))
		if p.rig.chownErr != nil {
			return ownwalk.Result{}, p.rig.chownErr
		}
		if p.rig.onOwn != nil {
			p.rig.onOwn(d, uid)
		}
	}
	return ownwalk.Result{}, nil
}

// B-92: an updated confined addon gets its directories chowned to its user again before its unit
// restarts it - what the update script wrote as root would otherwise stop the start - and also
// when it is not running, so its next start works. A root addon is left alone, a new addon is
// chowned once by its policy as before, a failing chown is said and the restart still runs, and
// the stored policy of an updated addon is not rewritten.
func TestInstallGivesAnUpdatedConfinedAddonItsFiles(t *testing.T) {
	const confined = `{"id":"hmm","mode":"confined","uid":30007,"user":"addon-hmm","source":"catalog"}`
	// an update that replaces the rc.d script and leaves the addon stopped
	const stoppedUpdate = "#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > ROOT/usr/local/etc/config/rc.d/hmm\nrm -f ROOT/usr/local/tmp/new_addon.tar.gz\nexit 0\n"
	const freshInstall = "#!/bin/sh\nmkdir -p ROOT/usr/local/addons/new\nprintf '#!/bin/sh\\nexit 0\\n' > ROOT/usr/local/etc/config/rc.d/new\nchmod +x ROOT/usr/local/etc/config/rc.d/new\nrm -f ROOT/usr/local/tmp/new_addon.tar.gz\nexit 0\n"
	cases := []struct {
		name      string
		id        string
		policy    string // hmm's stored policy; empty keeps the rig's root one
		installer string
		active    string // ActiveState of addon-<id>.service after the install
		chownErr  error
		uid       int  // the uid the chowns must name; 0 = no chown at all
		chowns    int  // every chown and helper chown -R of the install
		dataChown bool // the take-over of /usr/local/hmm runs
		wantStart bool
		want      []string // in the install output
		unchanged bool     // hmm's policy file is byte for byte what it was
	}{
		{name: "updated confined running", id: "hmm", policy: confined, installer: hmmUpdate, active: "active", uid: 30007, chowns: 2, dataChown: true, wantStart: true,
			want: []string{"[systemd] hmm restarted in its unit"}, unchanged: true},
		{name: "updated root running", id: "hmm", installer: hmmUpdate, active: "active", wantStart: true,
			want: []string{"[systemd] hmm restarted in its unit"}, unchanged: true},
		// the policy creates /usr/local/etc/config/addons/new and chowns it with the addon's tree
		{name: "new addon", id: "new", installer: freshInstall, active: "inactive", uid: 30000, chowns: 2, wantStart: true,
			want: []string{"[systemd] started new in their units"}},
		// the first chown fails, so the take-over after it does not run
		{name: "chown fails, restart still runs", id: "hmm", policy: confined, installer: hmmUpdate, active: "active", chownErr: errors.New("exit status 1"), uid: 30007, chowns: 1, wantStart: true,
			want: []string{"[systemd] hmm: its files could not be given to addon-hmm, it may fail to start: the directories of hmm: exit status 1", "[systemd] hmm restarted in its unit"}, unchanged: true},
		{name: "updated confined not running", id: "hmm", policy: confined, installer: stoppedUpdate, active: "inactive", uid: 30007, chowns: 2, dataChown: true,
			want: []string{"[systemd] hmm is not running and was not started"}, unchanged: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newSettleRig(t, tc.installer)
			old := Priv
			Priv = settlePriv{rig: rig}
			t.Cleanup(func() { Priv = old })
			rig.chownErr = tc.chownErr
			rig.active["addon-"+tc.id+".service"] = tc.active
			policyFile := rig.root.join(AddonPolicyDir + "/hmm.json")
			if tc.policy != "" {
				if err := os.WriteFile(policyFile, []byte(tc.policy), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			// hmm's own data directory, with what an update wrote as root
			if err := os.MkdirAll(rig.root.join("/usr/local/hmm/var"), 0o755); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(policyFile)

			res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(rig.calls, "\n")
			stop := rig.index(t, "systemctl stop --no-pager -- addon-"+tc.id+".service")
			start := rig.index(t, "systemctl start --no-pager -- addon-"+tc.id+".service")
			if (start >= 0) != tc.wantStart || (stop >= 0) != tc.wantStart {
				t.Fatalf("started: %v, want %v:\n%s", start >= 0, tc.wantStart, joined)
			}

			chowns := 0
			for _, c := range rig.calls {
				if strings.HasPrefix(c, "chown ") || strings.HasPrefix(c, "lchown ") {
					chowns++
				}
			}
			if tc.uid == 0 {
				if chowns != 0 {
					t.Fatalf("a root addon was chowned:\n%s", joined)
				}
			} else {
				own := fmt.Sprintf("chown -R %d:%d %s", tc.uid, tc.uid, rig.root.join("/usr/local/addons/"+tc.id))
				steps := []string{own}
				if tc.dataChown {
					steps = append(steps, fmt.Sprintf("lchown -R %d:%d %s", tc.uid, tc.uid, rig.root.join("/usr/local/hmm")))
				}
				n := 0
				for _, c := range rig.calls {
					if c == own {
						n++
					}
				}
				if n != 1 {
					t.Errorf("%q ran %d times, want once:\n%s", own, n, joined)
				}
				if chowns != tc.chowns {
					t.Errorf("%d chowns, want %d:\n%s", chowns, tc.chowns, joined)
				}
				for _, s := range steps {
					i := rig.index(t, s)
					switch {
					case i < 0:
						t.Errorf("missing %q:\n%s", s, joined)
					case tc.wantStart && tc.id == "new" && i > stop:
						// a new addon's policy gives it its files before its unit first starts it
						t.Errorf("%q after the unit was stopped for its start:\n%s", s, joined)
					case tc.wantStart && tc.id != "new" && (i < stop || i > start):
						// B-106: an updated addon's files change hands once its old processes are
						// gone - after the unit's stop and the scope's leftovers, before the start
						t.Errorf("%q not between the unit's stop and its start:\n%s", s, joined)
					}
				}
			}
			for _, w := range tc.want {
				if !strings.Contains(res.Output, w) {
					t.Errorf("output lacks %q:\n%s", w, res.Output)
				}
			}
			if after, _ := os.ReadFile(policyFile); tc.unchanged && string(after) != string(before) {
				t.Errorf("the stored policy was rewritten:\n%s\nwas\n%s", after, before)
			}
			if tc.id == "new" {
				if p := rig.root.ReadAddonPolicy("new"); p == nil || p.Mode != "confined" || p.Source != "default" || p.UID != tc.uid {
					t.Errorf("new addon's policy: %+v", p)
				}
			}
		})
	}
}

// A process that does not end on TERM gets a KILL after StopWait - still only that pid.
func TestInstallKillsALeftoverThatIgnoresTerm(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	rig.dies = false
	fakeProcess(t, rig.root, 6807, "node /usr/local/addons/hmm/app/dist/cli.js", "/system.slice/"+testScope, 1, 1000)
	rig.active["addon-hmm.service"] = "inactive"
	res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	if rig.index(t, "kill -TERM 6807") < 0 || rig.index(t, "kill -KILL 6807") < 0 {
		t.Errorf("calls:\n%s", strings.Join(rig.calls, "\n"))
	}
	if !strings.Contains(res.Output, "hmm restarted in its unit (1 process(es) left in the install scope stopped)") {
		t.Errorf("output:\n%s", res.Output)
	}
}

// Restart on the Services page for an addon that runs outside its unit (task 48): the same
// sequence as after an install, with "outside" meaning any cgroup but the unit's. An addon
// whose unit has its daemon inside gets a plain restart even when a process of it runs
// elsewhere (a CGI, a cron job): that is not a stray daemon, and it is not signalled.
func TestRestartPutsAStrayAddonBackIntoItsUnit(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	fakeProcess(t, rig.root, 4242, "node /usr/local/addons/hmm/app/dist/cli.js", "/system.slice/lighttpd.service", 1, 1000)
	fakeProcess(t, rig.root, 980, "/usr/sbin/lighttpd -f /etc/lighttpd/lighttpd.conf", "/system.slice/lighttpd.service", 1, 900)
	s := rig.a.Systemd
	rig.active["addon-hmm.service"], rig.tasks["addon-hmm.service"] = "active", "0"

	out, err := s.Control(context.Background(), "addon-hmm", "restart")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(rig.calls, "\n")
	stop, term, start := rig.index(t, "systemctl stop --no-pager -- addon-hmm.service"), rig.index(t, "kill -TERM 4242"), rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
	if stop < 0 || term < 0 || start < 0 || !(stop < term && term < start) || strings.Contains(joined, "systemctl restart") || strings.Contains(joined, "980") {
		t.Fatalf("calls:\n%s", joined)
	}
	if !strings.Contains(out, "started in its unit") {
		t.Errorf("output %q", out)
	}

	// the daemon is in the unit; a process of the addon elsewhere is left alone
	rig.calls = nil
	fakeProcess(t, rig.root, 4243, "sh /usr/local/addons/hmm/www/check.sh", "/system.slice/occulited.service", 1, 2000)
	rig.tasks["addon-hmm.service"] = "3"
	if _, err := s.Control(context.Background(), "addon-hmm", "restart"); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(rig.calls, "\n"); !strings.Contains(joined, "systemctl restart --no-pager -- addon-hmm.service") || strings.Contains(joined, "kill") {
		t.Errorf("a running unit got the stray treatment:\n%s", joined)
	}

	// nothing of the addon outside: a plain restart, and no systemctl show on the way
	rig.calls = nil
	_ = os.RemoveAll(rig.root.join("/proc/4243"))
	if _, err := s.Control(context.Background(), "addon-hmm", "restart"); err != nil {
		t.Fatal(err)
	}
	if len(rig.calls) != 1 || rig.calls[0] != "systemctl restart --no-pager -- addon-hmm.service" {
		t.Errorf("calls: %v", rig.calls)
	}
}
