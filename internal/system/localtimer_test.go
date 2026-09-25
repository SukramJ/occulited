package system

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testTimerText   = "[Unit]\nDescription=hello\n\n[Timer]\nOnCalendar=*-*-* 03:00:00\nPersistent=true\n\n[Install]\nWantedBy=timers.target\n"
	testServiceText = "[Unit]\nDescription=hello\n\n[Service]\nType=oneshot\nExecStart=/usr/bin/logger hello\n"
)

// timerRig is a box for the own timers: the recorder runner answers systemctl show from what the
// fake helper has in /run (a written unit is loaded, anything else not found), with overrides per
// unit; systemd-analyze exists or not; a systemctl verb can be made to fail.
type timerRig struct {
	s       SystemdServices
	fp      *dropInPriv
	calls   []string
	known   map[string]string // LoadState of a unit whatever /run holds (a shipped unit)
	load    map[string]string // LoadState of a unit once it is written to /run
	loadErr map[string]string
	ufs     map[string]string // UnitFileState
	timers  string            // list-timers JSON
	analyze bool
	verify  string // non-empty: systemd-analyze verify fails with this
	fail    map[string]bool
}

func newTimerRig(t *testing.T) *timerRig {
	t.Helper()
	rig := &timerRig{fp: &dropInPriv{files: map[string]string{}}, known: map[string]string{}, load: map[string]string{}, loadErr: map[string]string{}, ufs: map[string]string{}, timers: "[]", fail: map[string]bool{}}
	old := Priv
	Priv = rig.fp
	t.Cleanup(func() { Priv = old })
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		rig.calls = append(rig.calls, name+" "+strings.Join(args, " "))
		if name == "systemd-analyze" {
			switch args[0] {
			case "verify":
				if rig.verify != "" {
					return []byte(rig.verify), errors.New("exit status 1")
				}
				return nil, nil
			case "calendar":
				if args[len(args)-1] == "bogus" {
					return []byte("Failed to parse calendar specification 'bogus': Invalid argument"), errors.New("exit status 1")
				}
				return []byte("  Original form: daily\nNormalized form: *-*-* 00:00:00\n    Next elapse: Sat 2026-09-12 00:00:00 CEST\n       (in UTC): Fri 2026-09-11 22:00:00 UTC\n       From now: 13h left\n       Iter. #2: Sun 2026-09-13 00:00:00 CEST\n       (in UTC): Sat 2026-09-12 22:00:00 UTC\n       From now: 1 day 13h left\n       Iter. #3: Mon 2026-09-14 00:00:00 CEST\n       (in UTC): Sun 2026-09-13 22:00:00 UTC\n       From now: 2 days 13h left\n"), nil
			}
		}
		if name != "systemctl" {
			return []byte("ok"), nil
		}
		if rig.fail[args[0]] {
			return []byte(args[0] + " failed"), errors.New("exit status 1")
		}
		switch args[0] {
		case "list-timers":
			return []byte(rig.timers), nil
		case "show":
			sep, props := 0, ""
			for i, a := range args {
				if a == "--" && sep == 0 {
					sep = i + 1
				}
				if a == "-p" && i+1 < len(args) {
					props = args[i+1]
				}
			}
			var blocks []string
			for _, u := range args[sep:] {
				b := "Id=" + u + "\n"
				if strings.Contains(props, "LoadState") {
					st := rig.known[u]
					if st == "" {
						if _, written := rig.fp.files[runUnitPath(u)]; written {
							st = "loaded"
							if o, ok := rig.load[u]; ok {
								st = o
							}
						} else {
							st = "not-found"
						}
					}
					b += "LoadState=" + st + "\n"
					if e := rig.loadErr[u]; e != "" {
						b += "LoadError=" + e + "\n"
					}
				}
				if strings.Contains(props, "UnitFileState") {
					b += "UnitFileState=" + rig.ufs[u] + "\n"
				}
				blocks = append(blocks, b)
			}
			return []byte(strings.Join(blocks, "\n")), nil
		}
		return []byte("ok"), nil
	}
	rig.s = SystemdServices{Run: run, SwitchFile: filepath.Join(t.TempDir(), "unit-switch.json"), LookPath: func(string) (string, error) {
		if rig.analyze {
			return "/usr/bin/systemd-analyze", nil
		}
		return "", exec.ErrNotFound
	}}
	return rig
}

func (rig *timerRig) stored(name string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(rig.s.unitsDir(), name))
	return string(b), err == nil
}

func TestLocalTimerNames(t *testing.T) {
	for _, ok := range []string{"backup", "a", "Nightly_Job-2", "0day", strings.Repeat("x", 32)} {
		if !ValidLocalTimerName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-x", "_x", "a.b", "a/b", "a b", "ä", strings.Repeat("x", 33), "x\n", "local-x.timer"} {
		if ValidLocalTimerName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	// the unit name is accepted wherever a name is, the service's is not a timer's name
	if localTimerName("local-backup.timer") != "backup" || localTimerName("backup") != "backup" || localTimerName("local-backup.service") != "local-backup.service" {
		t.Error("localTimerName")
	}
}

// Create, the replay after a boot, the switch, an edit, run now, delete - with the calls systemd
// gets and the files on the userfs and in /run after each step.
func TestLocalTimerLifecycle(t *testing.T) {
	rig := newTimerRig(t)
	ctx := context.Background()

	lt, err := rig.s.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText)
	if err != nil {
		t.Fatal(err)
	}
	if lt.Name != "backup" || lt.Unit != "local-backup.timer" || lt.Service != "local-backup.service" || lt.TimerFile != testTimerText || lt.ServiceFile != testServiceText || !lt.Enabled {
		t.Errorf("%+v", lt)
	}
	want := []string{
		"systemctl show --timestamp=unix -p Id,LoadState -- local-backup.timer local-backup.service",
		"systemctl daemon-reload",
		"systemctl show --timestamp=unix -p Id,LoadState,LoadError -- local-backup.timer local-backup.service",
		"systemctl enable --runtime --now --no-pager -- local-backup.timer",
	}
	if strings.Join(rig.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("create calls:\n%s", strings.Join(rig.calls, "\n"))
	}
	if txt, ok := rig.stored("local-backup.timer"); !ok || txt != testTimerText {
		t.Errorf("stored timer: %q", txt)
	}
	if txt, ok := rig.stored("local-backup.service"); !ok || txt != testServiceText {
		t.Errorf("stored service: %q", txt)
	}
	if rig.fp.files["/run/systemd/system/local-backup.timer"] != testTimerText || rig.fp.files["/run/systemd/system/local-backup.service"] != testServiceText {
		t.Errorf("/run: %v", rig.fp.files)
	}
	if l := rig.s.ListLocalTimers(); len(l) != 1 || l[0].Name != "backup" {
		t.Errorf("list: %+v", l)
	}

	// a boot: /run is empty, the replay writes both units, reloads once, enables the timer
	rig.fp.files, rig.calls = map[string]string{}, nil
	applied, problems := rig.s.ReplayLocalTimers(ctx)
	if len(applied) != 1 || applied[0] != "local-backup.timer" || len(problems) != 0 {
		t.Errorf("replay: %v %v", applied, problems)
	}
	if rig.fp.files["/run/systemd/system/local-backup.timer"] != testTimerText || strings.Join(rig.calls, "|") != "systemctl daemon-reload|systemctl enable --runtime --now --no-pager -- local-backup.timer" {
		t.Errorf("replay: %v %v", rig.fp.files, rig.calls)
	}

	// Disable through the Services page's switch: a runtime disable, never the mask (its file is
	// where a mask would go), remembered beside the files - the replay then writes but does not
	// enable it
	rig.calls = nil
	if _, err := rig.s.Control(ctx, "local-backup.timer", "disable"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rig.calls, "|") != "systemctl disable --runtime --now --no-pager -- local-backup.timer" {
		t.Errorf("disable: %v", rig.calls)
	}
	if lt, _ := rig.s.ReadLocalTimer("backup"); lt.Enabled {
		t.Error("still enabled after disable")
	}
	if sw := rig.s.readSwitch(); len(sw.Masked)+len(sw.Enabled) != 0 {
		t.Errorf("an own timer went into the mask switch: %+v", sw)
	}
	rig.fp.files, rig.calls = map[string]string{}, nil
	if applied, _ := rig.s.ReplayLocalTimers(ctx); len(applied) != 1 || strings.Join(rig.calls, "|") != "systemctl daemon-reload" {
		t.Errorf("replay of a switched-off timer: %v %v", applied, rig.calls)
	}
	rig.calls = nil
	if _, err := rig.s.Control(ctx, "local-backup.timer", "enable"); err != nil {
		t.Fatal(err)
	}
	if lt, _ := rig.s.ReadLocalTimer("backup"); !lt.Enabled || strings.Join(rig.calls, "|") != "systemctl enable --runtime --now --no-pager -- local-backup.timer" {
		t.Errorf("enable: %+v %v", lt, rig.calls)
	}

	// an edit, addressed by the unit name: stored, in /run, reloaded, checked, the timer restarted
	rig.calls = nil
	newTimer := strings.Replace(testTimerText, "03:00:00", "04:30:00", 1)
	if lt, err := rig.s.UpdateLocalTimer(ctx, "local-backup.timer", newTimer, testServiceText); err != nil || lt.TimerFile != newTimer {
		t.Fatalf("%v %+v", err, lt)
	}
	if txt, _ := rig.stored("local-backup.timer"); txt != newTimer || rig.fp.files["/run/systemd/system/local-backup.timer"] != newTimer {
		t.Errorf("update not written: %q", txt)
	}
	if strings.Join(rig.calls, "|") != "systemctl daemon-reload|systemctl show --timestamp=unix -p Id,LoadState,LoadError -- local-backup.timer local-backup.service|systemctl restart --no-pager -- local-backup.timer" {
		t.Errorf("update calls: %v", rig.calls)
	}

	// run now
	rig.calls = nil
	if _, err := rig.s.RunLocalTimer(ctx, "backup"); err != nil || strings.Join(rig.calls, "|") != "systemctl start --no-block --no-pager -- local-backup.service" {
		t.Errorf("run: %v %v", err, rig.calls)
	}

	// delete: stopped and disabled, both files gone from /run and the userfs, reloaded
	rig.calls = nil
	if err := rig.s.DeleteLocalTimer(ctx, "backup"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(rig.calls, "|") != "systemctl disable --runtime --now --no-pager -- local-backup.timer|systemctl stop --no-pager -- local-backup.service|systemctl daemon-reload" {
		t.Errorf("delete calls: %v", rig.calls)
	}
	if len(rig.fp.files) != 0 {
		t.Errorf("/run after delete: %v", rig.fp.files)
	}
	if _, ok := rig.stored("local-backup.timer"); ok {
		t.Error("stored timer kept")
	}
	if _, err := rig.s.ReadLocalTimer("backup"); !errors.Is(err, ErrLocalTimerNotFound) {
		t.Errorf("read after delete: %v", err)
	}
	if l := rig.s.ListLocalTimers(); l == nil || len(l) != 0 {
		t.Errorf("list after delete: %#v", l)
	}
}

// Refused, and nothing written: a bad name, an existing own timer, a unit systemd knows, files of
// the wrong shape, what systemd-analyze rejects, and what systemd does not load after the reload
// (taken back out). An edit systemd does not load puts the previous text back.
func TestLocalTimerRefusals(t *testing.T) {
	ctx := context.Background()
	nothingWritten := func(t *testing.T, rig *timerRig) {
		t.Helper()
		if len(rig.fp.files) != 0 {
			t.Errorf("/run: %v", rig.fp.files)
		}
		if entries, _ := os.ReadDir(rig.s.unitsDir()); len(entries) != 0 {
			t.Errorf("userfs: %v", entries)
		}
	}

	rig := newTimerRig(t)
	for _, tc := range []struct{ name, timer, service, want string }{
		{"bad name", testTimerText, testServiceText, "a timer name"},
		{"x", testServiceText, testServiceText, "no [Timer] section"},
		{"x", testTimerText, testTimerText, "no [Service] section"},
		{"x", "", testServiceText, "empty"},
		{"x", testTimerText, "[Service]\nExecStart=/bin/a\x00b\n", "NUL"},
		{"x", "[Timer]\n" + strings.Repeat("OnCalendar=daily\n", 5000), testServiceText, "64 KiB"},
	} {
		if _, err := rig.s.CreateLocalTimer(ctx, tc.name, tc.timer, tc.service); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s/%q: %v", tc.name, tc.want, err)
		}
	}
	if len(rig.calls) != 0 {
		t.Errorf("a refused shape reached systemd: %v", rig.calls)
	}
	nothingWritten(t, rig)

	// a unit systemd can load under that name already
	rig.known["local-backup.service"] = "loaded"
	if _, err := rig.s.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText); !errors.Is(err, ErrLocalTimerExists) || !strings.Contains(err.Error(), "local-backup.service exists on this system") {
		t.Errorf("collision: %v", err)
	}
	nothingWritten(t, rig)
	delete(rig.known, "local-backup.service")

	// systemd-analyze verify says no
	rig.analyze, rig.verify = true, "local-backup.service:6: Command /nonexistent is not executable: No such file or directory"
	if _, err := rig.s.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText); err == nil || !strings.Contains(err.Error(), "systemd-analyze verify: local-backup.service:6: Command /nonexistent is not executable") {
		t.Errorf("verify: %v", err)
	}
	nothingWritten(t, rig)
	rig.analyze, rig.verify = false, ""

	// systemd does not load the timer after the reload: taken back out of /run and the userfs
	rig.calls = nil
	rig.load["local-broken.timer"] = "bad-setting"
	rig.loadErr["local-broken.timer"] = `org.freedesktop.systemd1.BadSetting "Unit local-broken.timer has a bad unit file setting."`
	if _, err := rig.s.CreateLocalTimer(ctx, "broken", strings.Replace(testTimerText, "*-*-* 03:00:00", "someday", 1), testServiceText); err == nil || !strings.Contains(err.Error(), "bad-setting") || !strings.Contains(err.Error(), "has a bad unit file setting") {
		t.Errorf("load check: %v", err)
	}
	nothingWritten(t, rig)
	joined := strings.Join(rig.calls, "\n")
	if strings.Contains(joined, "enable") || strings.Count(joined, "daemon-reload") != 2 {
		t.Errorf("a refused timer was enabled, or not reloaded away:\n%s", joined)
	}

	// enable fails: taken back out as well
	rig.fail["enable"] = true
	if _, err := rig.s.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText); err == nil || !strings.Contains(err.Error(), "systemctl enable local-backup.timer") {
		t.Errorf("enable failure: %v", err)
	}
	nothingWritten(t, rig)
	delete(rig.fail, "enable")

	// the same name twice
	if _, err := rig.s.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText); err != nil {
		t.Fatal(err)
	}
	if _, err := rig.s.CreateLocalTimer(ctx, "local-backup.timer", testTimerText, testServiceText); !errors.Is(err, ErrLocalTimerExists) {
		t.Errorf("second create: %v", err)
	}

	// an edit systemd does not load: the previous text is back in both places, nothing restarted
	rig.calls = nil
	rig.load["local-backup.timer"] = "bad-setting"
	if _, err := rig.s.UpdateLocalTimer(ctx, "backup", strings.Replace(testTimerText, "*-*-* 03:00:00", "someday", 1), testServiceText); err == nil {
		t.Error("an unloadable edit was accepted")
	}
	if txt, _ := rig.stored("local-backup.timer"); txt != testTimerText || rig.fp.files["/run/systemd/system/local-backup.timer"] != testTimerText {
		t.Errorf("the previous text was not restored: %q / %q", txt, rig.fp.files["/run/systemd/system/local-backup.timer"])
	}
	if strings.Contains(strings.Join(rig.calls, "\n"), "restart") {
		t.Errorf("restarted after a refused edit: %v", rig.calls)
	}
	delete(rig.load, "local-backup.timer")

	// an edit, a run, a delete of a timer that does not exist: not found
	if _, err := rig.s.UpdateLocalTimer(ctx, "nope", testTimerText, testServiceText); !errors.Is(err, ErrLocalTimerNotFound) {
		t.Errorf("update missing: %v", err)
	}
	if _, err := rig.s.RunLocalTimer(ctx, "nope"); !errors.Is(err, ErrLocalTimerNotFound) {
		t.Errorf("run missing: %v", err)
	}
	if err := rig.s.DeleteLocalTimer(ctx, "nope"); !errors.Is(err, ErrLocalTimerNotFound) {
		t.Errorf("delete missing: %v", err)
	}
	// and without a state directory nothing can be kept
	plain := SystemdServices{Run: rig.s.Run}
	if _, err := plain.CreateLocalTimer(ctx, "backup", testTimerText, testServiceText); err == nil {
		t.Error("created without a state directory")
	}
}

// The calendar check: the next three times from systemd-analyze, its complaint for what it cannot
// parse, and "cannot tell" without it.
func TestCheckCalendar(t *testing.T) {
	rig := newTimerRig(t)
	ctx := context.Background()
	if res, err := rig.s.CheckCalendar(ctx, "daily"); err != nil || res.Available || len(rig.calls) != 0 {
		t.Errorf("without systemd-analyze: %+v %v %v", res, err, rig.calls)
	}
	rig.analyze = true
	res, err := rig.s.CheckCalendar(ctx, "daily")
	if err != nil || !res.Available || res.Normalized != "*-*-* 00:00:00" || len(res.Next) != 3 || res.Next[0] != "Sat 2026-09-12 00:00:00 CEST" || res.Next[2] != "Mon 2026-09-14 00:00:00 CEST" {
		t.Errorf("%+v %v", res, err)
	}
	if rig.calls[0] != "systemd-analyze calendar --iterations=3 -- daily" {
		t.Errorf("calls: %v", rig.calls)
	}
	if _, err := rig.s.CheckCalendar(ctx, "bogus"); err == nil || !strings.Contains(err.Error(), "Failed to parse calendar specification") {
		t.Errorf("bogus: %v", err)
	}
	rig.calls = nil
	for _, bad := range []string{"", "  ", "-h", "daily\nhourly", strings.Repeat("x", 257)} {
		if _, err := rig.s.CheckCalendar(ctx, bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if len(rig.calls) != 0 {
		t.Errorf("a refused expression reached systemd-analyze: %v", rig.calls)
	}
}

// GET /timers: own timers are marked, a stored one systemd does not list comes after the rest,
// and every timer carries its enabled state from one systemctl show. The switch on a shipped timer
// is the runtime mask; an own timer's service is not a switch of its own.
func TestTimersWithOwnTimers(t *testing.T) {
	rig := newTimerRig(t)
	ctx := context.Background()
	dir := rig.s.unitsDir()
	_ = os.MkdirAll(dir, 0o750)
	for _, name := range []string{"backup", "off"} {
		_ = os.WriteFile(filepath.Join(dir, "local-"+name+".timer"), []byte(testTimerText), 0o640)
		_ = os.WriteFile(filepath.Join(dir, "local-"+name+".service"), []byte(testServiceText), 0o640)
	}
	_ = os.WriteFile(filepath.Join(dir, "local-off.disabled"), []byte("x\n"), 0o640)
	rig.timers = `[{"next":1789000000000000,"left":1789000000000000,"last":null,"unit":"occu-fstrim.timer","activates":"occu-fstrim.service"},
{"next":1789100000000000,"left":1789100000000000,"last":null,"unit":"local-backup.timer","activates":"local-backup.service"}]`
	rig.ufs = map[string]string{"occu-fstrim.timer": "enabled", "local-backup.timer": "enabled-runtime", "local-off.timer": "disabled"}

	tm, err := rig.s.Timers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tm) != 3 {
		t.Fatalf("%+v", tm)
	}
	if f := tm[0]; f.Unit != "occu-fstrim.timer" || f.Own || !f.Enabled || f.UnitFileState != "enabled" || !f.Active {
		t.Errorf("shipped: %+v", f)
	}
	if b := tm[1]; b.Unit != "local-backup.timer" || !b.Own || !b.Enabled || b.UnitFileState != "enabled-runtime" {
		t.Errorf("own: %+v", b)
	}
	if o := tm[2]; o.Unit != "local-off.timer" || o.Activates != "local-off.service" || !o.Own || o.Enabled || o.Active || o.UnitFileState != "disabled" {
		t.Errorf("own, not loaded: %+v", o)
	}
	if len(rig.calls) != 2 || rig.calls[1] != "systemctl show --timestamp=unix -p Id,UnitFileState -- occu-fstrim.timer local-backup.timer local-off.timer" {
		t.Errorf("one list-timers and one show: %v", rig.calls)
	}

	// a shipped timer's switch is the runtime mask, like any unit's; its override editor takes it
	rig.calls = nil
	if _, err := rig.s.Control(ctx, "occu-fstrim.timer", "disable"); err != nil || rig.calls[0] != "systemctl mask --runtime --now --no-pager -- occu-fstrim.timer" {
		t.Errorf("shipped timer disable: %v %v", err, rig.calls)
	}
	if u, err := unitFor("occu-fstrim.timer"); err != nil || u != "occu-fstrim.timer" {
		t.Errorf("unitFor: %q %v", u, err)
	}
	// the service of an own timer is switched through its timer
	rig.calls = nil
	if _, err := rig.s.Control(ctx, "local-backup.service", "disable"); err == nil || len(rig.calls) != 0 {
		t.Errorf("own timer's service switched on its own: %v %v", err, rig.calls)
	}
	// but it can be started and stopped like any unit
	if _, err := rig.s.Control(ctx, "local-backup.service", "stop"); err != nil || rig.calls[0] != "systemctl stop --no-pager -- local-backup.service" {
		t.Errorf("own service stop: %v %v", err, rig.calls)
	}
}
