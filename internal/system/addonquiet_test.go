package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// B-106: an addon's old processes write on their way out - Mosquitto saves its persistence when it
// is stopped - and on the Pi 4 and the x86_64 box that write came after the ownership step, so
// var/mosquitto.db was root's, mode 600, beside the confined addon's own pid file. The addon is
// stopped now until nothing of it is left, then its files change hands, then its unit starts it.

// lingering is a fake addon process that writes a file as root a moment after it was told to stop,
// and ends only then. events is what happened, in order; own is the files' owners.
type lingering struct {
	mu     sync.Mutex
	events []string
	own    *owners
	once   sync.Once
	done   chan struct{}
}

func newLingering() *lingering {
	return &lingering{own: &owners{m: map[string]int{}}, done: make(chan struct{})}
}

func (l *lingering) note(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *lingering) order() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.events, " | ")
}

func (l *lingering) owner(path string) (int, bool) {
	l.own.mu.Lock()
	defer l.own.mu.Unlock()
	uid, ok := l.own.m[path]
	return uid, ok
}

// wrap makes the rig's runner play the process: the call trigger makes it save file as root 300 ms
// later and then run gone (which ends it); a chown -R gives what exists below its path to its uid.
func (l *lingering) wrap(t *testing.T, rig *settleRig, trigger, file string, gone func()) {
	base := rig.a.Systemd.Run
	rig.a.Systemd.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name+" "+strings.Join(args, " ") == trigger {
			l.once.Do(func() {
				go func() {
					defer close(l.done)
					time.Sleep(300 * time.Millisecond)
					_ = os.MkdirAll(filepath.Dir(file), 0o755)
					_ = os.WriteFile(file, []byte("persistence"), 0o600)
					l.own.set(file, 0)
					l.note("saved as root")
					gone()
				}()
			})
		}
		return base(ctx, name, args...)
	}
	rig.onOwn = func(dir string, uid int) {
		l.own.chown(dir, uid)
		l.note("chown " + strings.TrimPrefix(dir, string(rig.root)))
	}
	t.Cleanup(func() {
		select {
		case <-l.done:
		case <-time.After(5 * time.Second):
			t.Error("the fake process never ended")
		}
	})
}

func (l *lingering) wait(t *testing.T) {
	t.Helper()
	select {
	case <-l.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the fake process never ended")
	}
}

// An update like Mosquitto's: its script started the daemon as root in the install scope, and the
// daemon saves its database a moment after its TERM. The files change hands after that, and the
// unit is started once.
func TestInstallGivesTheFilesOverOnceTheOldDaemonHasEnded(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	old := Priv
	Priv = settlePriv{rig: rig}
	t.Cleanup(func() { Priv = old })
	if err := os.WriteFile(rig.root.join(AddonPolicyDir+"/hmm.json"), []byte(`{"id":"hmm","mode":"confined","uid":30007,"user":"addon-hmm","source":"catalog"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProcess(t, rig.root, 6807, "/usr/local/addons/hmm/bin/mosquitto -c /usr/local/addons/hmm/etc/mosquitto.conf", "/system.slice/"+testScope, 1, 1000)
	rig.active["addon-hmm.service"] = "inactive" // the update's stop reached the unit
	rig.dies = false                             // it ends on its own, after its last write
	rig.a.Systemd.StopWait = 5 * time.Second
	db := rig.root.join("/usr/local/addons/hmm/var/mosquitto.db")
	l := newLingering()
	l.wrap(t, rig, "kill -TERM 6807", db, func() { _ = os.RemoveAll(rig.root.join("/proc/6807")) })
	var after []string
	rig.a.AfterStart = func(ids []string) { after = ids }

	res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	l.wait(t)
	if uid, ok := l.owner(db); !ok || uid != 30007 {
		t.Errorf("mosquitto.db is uid %d after the install, want the addon's 30007 (%s)", uid, l.order())
	}
	if got := l.order(); !strings.HasPrefix(got, "saved as root | chown /usr/local/addons/hmm") {
		t.Errorf("the files changed hands before the daemon had ended: %s", got)
	}
	joined := strings.Join(rig.calls, "\n")
	term, chown, start := rig.index(t, "kill -TERM 6807"), -1, rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
	for i, c := range rig.calls {
		if strings.HasPrefix(c, "chown -R 30007:30007 ") {
			chown = i
			break
		}
	}
	if term < 0 || chown < term || start < chown || strings.Count(joined, "systemctl start --no-pager -- addon-hmm.service") != 1 {
		t.Errorf("want the TERM, the chown and one start, in that order:\n%s", joined)
	}
	if strings.Contains(joined, "-KILL") {
		t.Errorf("a process that ended in time got a KILL:\n%s", joined)
	}
	if !strings.Contains(res.Output, "[systemd] hmm restarted in its unit (1 process(es) left in the install scope stopped)") {
		t.Errorf("output:\n%s", res.Output)
	}
	if strings.Join(after, ",") != "hmm" {
		t.Errorf("the ownership check after the start was told %v", after)
	}
}

// The switch to its own user (the x86_64 box's Mosquitto): the root daemon runs in its unit and saves
// its database while the unit stops it. The unit's cgroup is watched until it is empty - by the
// cgroup, the daemon's command line does not name the addon's directory - and only then the files
// change hands and the unit starts as the new user.
func TestSwitchPolicyWaitsForTheUnitsCgroup(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	old := Priv
	Priv = settlePriv{rig: rig}
	t.Cleanup(func() { Priv = old })
	rig.a.Systemd.StopWait = 5 * time.Second
	procs := rig.root.join("/sys/fs/cgroup/system.slice/addon-hmm.service/cgroup.procs")
	if err := os.MkdirAll(filepath.Dir(procs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procs, []byte("3222\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakeProcess(t, rig.root, 3222, "mosquitto -c /etc/mosquitto.conf", "/system.slice/addon-hmm.service", 1, 1000)
	rig.active["addon-hmm.service"], rig.tasks["addon-hmm.service"] = "active", "1"
	db := rig.root.join("/usr/local/addons/hmm/var/mosquitto.db")
	l := newLingering()
	l.wrap(t, rig, "systemctl stop --no-pager -- addon-hmm.service", db, func() {
		_ = os.WriteFile(procs, nil, 0o644)
		_ = os.RemoveAll(rig.root.join("/proc/3222"))
	})
	var after []string
	rig.a.AfterStart = func(ids []string) { after = ids }

	sw, err := rig.a.SwitchPolicy(context.Background(), "hmm", "confined", "user")
	if err != nil || !sw.Restarted || sw.RestartError != "" || sw.Policy == nil || sw.Policy.Mode != "confined" || sw.Policy.UID <= 0 {
		t.Fatalf("%+v %v", sw, err)
	}
	l.wait(t)
	if uid, ok := l.owner(db); !ok || uid != sw.Policy.UID {
		t.Errorf("mosquitto.db is uid %d after the switch, want the addon's %d (%s)", uid, sw.Policy.UID, l.order())
	}
	if got := l.order(); !strings.HasPrefix(got, "saved as root | chown /usr/local/addons/hmm") {
		t.Errorf("the files changed hands before the unit's cgroup was empty: %s", got)
	}
	joined := strings.Join(rig.calls, "\n")
	stop, chown, start := rig.index(t, "systemctl stop --no-pager -- addon-hmm.service"), -1, rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
	for i, c := range rig.calls {
		if strings.HasPrefix(c, "chown -R ") {
			chown = i
			break
		}
	}
	if stop < 0 || chown < stop || start < chown || strings.Contains(joined, "systemctl restart") || strings.Contains(joined, "kill") {
		t.Errorf("want the stop, the chown and the start, nothing killed:\n%s", joined)
	}
	if strings.Join(after, ",") != "hmm" {
		t.Errorf("the ownership check after the start was told %v", after)
	}

	// back to root: stopped and started again, nothing chowned, no ownership check
	rig.calls, after = nil, nil
	sw, err = rig.a.SwitchPolicy(context.Background(), "hmm", "root", "user")
	if err != nil || !sw.Restarted || sw.Policy.Mode != "root" {
		t.Fatalf("root: %+v %v", sw, err)
	}
	joined = strings.Join(rig.calls, "\n")
	if rig.index(t, "systemctl stop --no-pager -- addon-hmm.service") < 0 || rig.index(t, "systemctl start --no-pager -- addon-hmm.service") < 0 || strings.Contains(joined, "chown") || after != nil {
		t.Errorf("root: %v\n%s", after, joined)
	}
	// a mode that is refused stops nothing
	rig.calls = nil
	if _, err := rig.a.SwitchPolicy(context.Background(), "hmm", "nobody", "user"); err == nil || len(rig.calls) != 0 {
		t.Errorf("a refused mode: %v\n%s", err, strings.Join(rig.calls, "\n"))
	}
}

// What stays in the unit's cgroup after StopWait gets a KILL by pid before anything goes on.
func TestQuietAddonKillsWhatStaysInTheCgroup(t *testing.T) {
	rig := newSettleRig(t, hmmUpdate)
	procs := rig.root.join("/sys/fs/cgroup/system.slice/addon-hmm.service/cgroup.procs")
	if err := os.MkdirAll(filepath.Dir(procs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procs, []byte("3222\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rig.active["addon-hmm.service"] = "active"
	base := rig.a.Systemd.Run
	rig.a.Systemd.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "kill" && strings.Join(args, " ") == "-KILL 3222" {
			_ = os.WriteFile(procs, nil, 0o644)
		}
		return base(ctx, name, args...)
	}
	sw, err := rig.a.SwitchPolicy(context.Background(), "hmm", "root", "user")
	if err != nil || !sw.Restarted {
		t.Fatalf("%+v %v", sw, err)
	}
	stop, kill, start := rig.index(t, "systemctl stop --no-pager -- addon-hmm.service"), rig.index(t, "kill -KILL 3222"), rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
	if stop < 0 || kill < stop || start < kill {
		t.Errorf("want the stop, the KILL and the start:\n%s", strings.Join(rig.calls, "\n"))
	}
}
