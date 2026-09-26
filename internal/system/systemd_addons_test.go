package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

func TestSystemdAddonsInstallUninstall(t *testing.T) {
	// a fake install_addon that "installs" an addon: creates its rc.d script
	r := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/old": "#!/bin/sh\nexit 0\n", "usr/local/tmp/.keep": ""})
	rc := r.join("/usr/local/etc/config/rc.d/new")
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\nprintf '#!/bin/sh\\necho uninstalled\\nexit 0\\n' > "+rc+"\nchmod +x "+rc+"\nrm -f "+r.join("/usr/local/tmp/new_addon.tar.gz")+"\necho installed\nexit 0\n"), 0o755)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	res, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	if res.Exit != 0 || !strings.Contains(res.Output, "installed") || !strings.Contains(res.Output, "started new in their units") {
		t.Fatalf("%+v", res)
	}
	joined := strings.Join(calls, "\n")
	// task 48: a new addon is settled like an updated one - its unit stopped, what its install
	// script left in the scope stopped by pid, the unit started - so a daemon the script already
	// started does not stay in the scope while `restart` only runs the unit's start
	if !strings.Contains(joined, "systemctl daemon-reload") || !strings.Contains(joined, "systemctl stop --no-pager -- addon-new.service\nsystemctl start --no-pager -- addon-new.service") {
		t.Errorf("calls:\n%s", joined)
	}
	// what was done to an addon, not what was asked: B-98's look at every addon's state before the
	// install names addon-old too
	var acted []string
	for _, c := range calls {
		if !strings.HasPrefix(c, "systemctl show ") {
			acted = append(acted, c)
		}
	}
	if strings.Contains(strings.Join(acted, "\n"), "addon-old.service") {
		t.Errorf("restarted an addon that was already there:\n%s", joined)
	}
	if strings.Contains(joined, "stop --no-pager -- occulite-addon-") {
		t.Errorf("the install scope must not be stopped (B-3):\n%s", joined)
	}
	calls = nil
	out, err := a.Uninstall(context.Background(), "new")
	if err != nil || out != "uninstalled" {
		t.Fatalf("%v %q", err, out)
	}
	joined = strings.Join(calls, "\n")
	if !strings.HasPrefix(calls[0], "systemctl stop --no-pager -- addon-new.service") || !strings.Contains(joined, "daemon-reload") {
		t.Errorf("calls:\n%s", joined)
	}
	if _, err := os.Stat(rc); !os.IsNotExist(err) {
		t.Error("rc.d entry kept")
	}
	// B-236: the unit's failure is forgotten once its file is gone, or it stays listed not-found
	if !strings.HasSuffix(joined, "systemctl daemon-reload\nsystemctl reset-failed --no-pager -- addon-new.service") {
		t.Errorf("no reset-failed after the reload:\n%s", joined)
	}
}

// B-236: an addon whose stop exits non-zero (RedMatic's, when Node-RED is already gone) is
// uninstalled all the same - the failed stop is logged, the uninstall runs, the failed unit is
// reset after the reload
func TestSystemdAddonsUninstallAfterAFailedStop(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/etc/config/rc.d/red": "#!/bin/sh\necho uninstalled\nexit 0\n"})
	_ = os.Chmod(r.join("/usr/local/etc/config/rc.d/red"), 0o755)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if name == "systemctl" && args[0] == "stop" {
			return []byte("Job for addon-red.service failed because the control process exited with error code."), errors.New("exit status 1")
		}
		return nil, nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	out, err := a.Uninstall(context.Background(), "red")
	if err != nil || out != "uninstalled" {
		t.Fatalf("%v %q", err, out)
	}
	want := []string{"systemctl stop --no-pager -- addon-red.service", "systemctl daemon-reload", "systemctl reset-failed --no-pager -- addon-red.service"}
	var got []string
	for _, c := range calls {
		if strings.HasPrefix(c, "systemctl ") && !strings.HasPrefix(c, "systemctl show ") && !strings.HasPrefix(c, "systemctl list-units") {
			got = append(got, c)
		}
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls:\n%s", strings.Join(calls, "\n"))
	}
	if _, err := os.Stat(r.join("/usr/local/etc/config/rc.d/red")); !os.IsNotExist(err) {
		t.Error("rc.d entry kept")
	}
}

func TestSystemdAddonsRebootRequired(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": ""})
	rc := r.join("/usr/local/etc/config/rc.d/x")
	_ = os.MkdirAll(filepath.Dir(rc), 0o755)
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\ntouch "+rc+"\nexit 10\n"), 0o755)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	res, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil || !res.RebootRequired {
		t.Fatalf("%v %+v", err, res)
	}
	// B-186: a new addon is started although its installer asks for a reboot (a CCU starts it at
	// that reboot); the reboot flag stays, it is the installer's word
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "systemctl start --no-pager -- addon-x.service") {
		t.Errorf("a new addon whose installer asks for a reboot was not started:\n%s", joined)
	}
	if !strings.Contains(res.Output, "started x in their units") || !strings.Contains(res.Output, "started without waiting for it") {
		t.Errorf("output:\n%s", res.Output)
	}
}

// B-186: an installer that failed starts nothing, a new addon included.
func TestSystemdAddonsFailedInstallStartsNothing(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": ""})
	rc := r.join("/usr/local/etc/config/rc.d/x")
	_ = os.MkdirAll(filepath.Dir(rc), 0o755)
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\ntouch "+rc+"\nexit 13\n"), 0o755)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	if _, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(calls, "\n"); strings.Contains(joined, "systemctl start") {
		t.Errorf("a failed installer's addon was started:\n%s", joined)
	}
}

// B-186: the install ends when the new addon's unit is active, or says what it is instead - it
// waits through activating, and names a failed unit's result.
func TestSystemdAddonsFreshUnitStateAfterStart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states []string // what `systemctl show` answers for addon-x.service, one per call
		want   string
		start  bool // counted as started
	}{
		{name: "active at once", states: []string{"active"}, start: true},
		{name: "activating, then active", states: []string{"activating", "activating", "active"}, start: true},
		{name: "failed", states: []string{"failed"}, want: "x was started in its unit and is failed (exit-code) now"},
		{name: "stuck activating", states: []string{"activating"}, want: "x was started in its unit and is activating now"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := rootWith(t, map[string]string{"usr/local/tmp/.keep": ""})
			rc := r.join("/usr/local/etc/config/rc.d/x")
			_ = os.MkdirAll(filepath.Dir(rc), 0o755)
			_ = os.MkdirAll(r.join("/bin"), 0o755)
			_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\ntouch "+rc+"\nexit 10\n"), 0o755)
			startedUnit, n := false, 0
			run := func(_ context.Context, name string, args ...string) ([]byte, error) {
				line := strings.Join(args, " ")
				if strings.HasPrefix(line, "start ") && strings.HasSuffix(line, "addon-x.service") {
					startedUnit = true
				}
				// only the look after the start is answered; the ones before see nothing
				if startedUnit && strings.HasPrefix(line, "show ") && strings.Contains(line, "Result") {
					st := tc.states[min(n, len(tc.states)-1)]
					n++
					res := "success"
					if st == "failed" {
						res = "exit-code"
					}
					return []byte("Id=addon-x.service\nActiveState=" + st + "\nResult=" + res + "\n"), nil
				}
				return nil, nil
			}
			a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run, ActiveWait: 600 * time.Millisecond})
			res, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(res.Output, "started x in their units"); got != tc.start {
				t.Errorf("counted as started = %v, want %v:\n%s", got, tc.start, res.Output)
			}
			if tc.want != "" && !strings.Contains(res.Output, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, res.Output)
			}
		})
	}
}

// The addon's running state comes from its unit, not from a pid file most addons never write
// (B-29): three of the four addons installed on the lab box were reported "not running" while
// their daemons were alive in their units' cgroups.
func TestOverlayUnitState(t *testing.T) {
	list := []Addon{{ID: "redmatic", Running: false}, {ID: "quiet", Running: true, PID: 9}, {ID: "nounit", Running: true, PID: 7}}
	units := []Service{{ID: "addon-redmatic", Running: true, PID: 4711}, {ID: "addon-quiet", Running: false}}
	got := map[string]Addon{}
	for _, a := range overlayUnitState(list, units) {
		got[a.ID] = a
	}
	if !got["redmatic"].Running || got["redmatic"].PID != 4711 {
		t.Errorf("redmatic: %+v, want running with pid 4711", got["redmatic"])
	}
	if got["quiet"].Running || got["quiet"].PID != 0 {
		t.Errorf("quiet: %+v, want not running", got["quiet"])
	}
	if !got["nounit"].Running || got["nounit"].PID != 7 {
		t.Errorf("an addon without a unit keeps the pid-file answer: %+v", got["nounit"])
	}
}

// D-36's default is confined (maintainer, 2026-09-07): a freshly installed addon gets its own
// user without anyone asking for it, and root is what a box has to say explicitly.
func TestSystemdAddonsDefaultConfined(t *testing.T) {
	newRoot := func(t *testing.T) (Root, *[]string, *SystemdAddons) {
		t.Helper()
		r := rootWith(t, map[string]string{"usr/local/tmp/.keep": "", "etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n", "usr/local/etc/config/rc.d/old": "#!/bin/sh\n"})
		rc := r.join("/usr/local/etc/config/rc.d/new")
		_ = os.MkdirAll(r.join("/bin"), 0o755)
		_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > "+rc+"\nchmod +x "+rc+"\nrm -f "+r.join("/usr/local/tmp/new_addon.tar.gz")+"\nexit 0\n"), 0o755)
		var calls []string
		run := func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			return []byte("ok"), nil
		}
		return r, &calls, NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	}

	// nothing configured: confined, its own user, and the drop-in that makes it true
	r, calls, a := newRoot(t)
	if a.defaultMode() != "confined" || AddonDefaultMode != "confined" {
		t.Fatalf("default mode %q", a.defaultMode())
	}
	if _, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	p := r.ReadAddonPolicy("new")
	if p == nil || p.Mode != "confined" || p.User != "addon-new" || p.UID < AddonUIDBase || p.Source != "default" {
		t.Fatalf("policy after install: %+v", p)
	}
	// the user and its group are lines appended by the helper's operation, not busybox calls (B-54)
	if b, _ := os.ReadFile(r.join("/etc/passwd")); !strings.Contains(string(b), "\naddon-new:x:30000:30000::/usr/local/addons/new:/bin/false\n") {
		t.Errorf("the addon's user was not created:\n%s", b)
	}
	if b, _ := os.ReadFile(r.join("/etc/group")); !strings.Contains(string(b), "\naddon-new:x:30000:\n") {
		t.Errorf("the addon's group was not created:\n%s", b)
	}
	if joined := strings.Join(*calls, "\n"); strings.Contains(joined, "adduser") || strings.Contains(joined, "addgroup") {
		t.Errorf("busybox account tools ran: %v", *calls)
	}
	conf, _ := os.ReadFile(r.join(AddonPolicyDir + "/new.conf"))
	if !strings.Contains(string(conf), "[Service]\nUser=addon-new\n") || !strings.Contains(string(conf), "ProtectSystem=strict") {
		t.Errorf("drop-in:\n%s", conf)
	}
	// nobody declared anything for it, and the pages have to say so
	if v := a.PolicyView("new"); !v.Undeclared || v.Mode != "confined" || v.Source != "default" {
		t.Errorf("view: %+v", v)
	}
	// an addon with a runtime block has declared what it needs: not undeclared
	if _, err := a.SetPolicy(context.Background(), "new", "confined", "catalog", &AddonRuntime{Groups: []string{"dialout"}}); err != nil {
		t.Fatal(err)
	}
	if v := a.PolicyView("new"); v.Undeclared || v.Source != "catalog" {
		t.Errorf("declared: %+v", v)
	}
	// and an addon nobody ever wrote a policy for is root, undeclared - the generator writes no
	// drop-in for it, whatever the box default says
	if v := a.PolicyView("absent"); v.Mode != "root" || !v.Undeclared || v.Source != "" {
		t.Errorf("no policy: %+v", v)
	}

	// the box-wide opt-out: addons.default_mode = root, and nothing is confined
	r, _, a = newRoot(t)
	a.DefaultMode = "root"
	if _, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100))); err != nil {
		t.Fatal(err)
	}
	if p := r.ReadAddonPolicy("new"); p == nil || p.Mode != "root" {
		t.Fatalf("default_mode=root: %+v", p)
	}
	if b, _ := os.ReadFile(r.join("/etc/passwd")); strings.Contains(string(b), "addon-") {
		t.Errorf("a user was created for a root addon:\n%s", b)
	}

	// a misspelt value is not an open box: anything but "root" confines
	_, _, a = newRoot(t)
	a.DefaultMode = "Root"
	if a.defaultMode() != "confined" {
		t.Errorf("a value that is not exactly \"root\" must confine, got %q", a.defaultMode())
	}
}

// readOnlyEtcPriv is the helper on an image without occu-etc-writable.service: the append to
// /etc/group fails with the rootfs's error.
type readOnlyEtcPriv struct{ priv.Local }

func (readOnlyEtcPriv) AddAddonUser(_, group, _ string, _ int) error {
	return errors.New("group: open " + group + ": read-only file system")
}

// B-28: on an image without occu-etc-writable.service the user cannot be created. The addon must
// still run, and the page must be able to say why it is root.
func TestSystemdAddonsConfineFallsBackToRoot(t *testing.T) {
	old := Priv
	Priv = readOnlyEtcPriv{}
	t.Cleanup(func() { Priv = old })
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": "", "etc/passwd": "root:x:0:0::/:/bin/sh\n", "etc/group": "root:x:0:\n", "usr/local/etc/config/rc.d/old": "#!/bin/sh\n"})
	rc := r.join("/usr/local/etc/config/rc.d/new")
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\nprintf '#!/bin/sh\\nexit 0\\n' > "+rc+"\nchmod +x "+rc+"\nrm -f "+r.join("/usr/local/tmp/new_addon.tar.gz")+"\nexit 0\n"), 0o755)
	run := func(_ context.Context, _ string, _ ...string) ([]byte, error) { return []byte("ok"), nil }
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	res, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Output, "occu-etc-writable.service") {
		t.Errorf("the install output must name the cause (B-28):\n%s", res.Output)
	}
	if !strings.Contains(res.Output, "new runs as root") {
		t.Errorf("the fallback must be said out loud:\n%s", res.Output)
	}
	v := a.PolicyView("new")
	if v.Mode != "root" || v.Source != "fallback" {
		t.Fatalf("view: %+v", v)
	}
}

// The flipped default must not confine what is already installed and working: those addons are
// pinned to what they run as today, once per box, and shown as root + undeclared.
func TestAdoptInstalledAddons(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                                 "root:x:0:0::/:/bin/sh\n",
		"usr/local/etc/config/rc.d/redmatic":         "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/hm2mqtt":          "#!/bin/sh\n",
		"usr/local/etc/config/addon-policy/mos.json": `{"id":"mos","mode":"confined","uid":30003,"user":"addon-mos"}`,
		"usr/local/etc/config/rc.d/mos":              "#!/bin/sh\n",
	})
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	ids, err := a.AdoptInstalledAddons(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "hm2mqtt" || ids[1] != "redmatic" {
		t.Fatalf("pinned %v", ids)
	}
	for _, id := range ids {
		v := a.PolicyView(id)
		if v.Mode != "root" || v.Source != "migrated" || !v.Undeclared {
			t.Errorf("%s: %+v", id, v)
		}
		// root, so no user of its own - the root drop-in's [Service] holds the bounding set alone
		conf, _ := os.ReadFile(r.join(AddonPolicyDir + "/" + id + ".conf"))
		if strings.Contains(string(conf), "User=") || strings.Contains(string(conf), "ProtectSystem") || !strings.Contains(string(conf), "mode=root") {
			t.Errorf("%s got a confining drop-in:\n%s", id, conf)
		}
	}
	// the addon that already had a policy is untouched, and no user was created for anyone
	if p := r.ReadAddonPolicy("mos"); p == nil || p.Mode != "confined" || p.Source != "" {
		t.Errorf("an addon with a policy was overwritten: %+v", p)
	}
	if strings.Contains(strings.Join(calls, "\n"), "adduser") {
		t.Errorf("adopting must not create users: %v", calls)
	}
	// idempotent: a second run has nothing left to do (the caller's marker is the real guard)
	if ids, err := a.AdoptInstalledAddons(context.Background()); err != nil || len(ids) != 0 {
		t.Errorf("second run: %v %v", ids, err)
	}
}

// What the two pages read: the addon list and the service list both carry the policy.
func TestOverlayPolicy(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/addon-policy/a.json": `{"id":"a","mode":"confined","uid":30001,"user":"addon-a","source":"catalog","runtime":{"groups":["dialout"]}}`,
		"usr/local/etc/config/addon-policy/b.json": `{"id":"b","mode":"root","source":"user"}`,
	})
	a := NewSystemdAddons(r, SystemdServices{Root: r})
	got := map[string]Addon{}
	for _, x := range a.overlayAddonPolicy([]Addon{{ID: "a"}, {ID: "b"}, {ID: "c"}}) {
		got[x.ID] = x
	}
	if got["a"].PolicyMode != "confined" || got["a"].PolicySource != "catalog" || got["a"].Undeclared {
		t.Errorf("a: %+v", got["a"])
	}
	if got["b"].PolicyMode != "root" || got["b"].PolicySource != "user" || !got["b"].Undeclared {
		t.Errorf("b: %+v", got["b"])
	}
	if got["c"].PolicyMode != "root" || !got["c"].Undeclared {
		t.Errorf("c: %+v", got["c"])
	}
	list := a.OverlayServicePolicy([]Service{
		{ID: "addon-a", Kind: "addon"},
		{ID: "redmatic", Kind: "addon"}, // a unit the addon shipped itself: not one of ours
		{ID: "rfd", Kind: "system"},
	})
	if list[0].PolicyMode != "confined" || list[0].Undeclared {
		t.Errorf("addon-a: %+v", list[0])
	}
	if list[1].PolicyMode != "" || list[2].PolicyMode != "" || list[2].Undeclared {
		t.Errorf("only generated addon units carry a policy: %+v %+v", list[1], list[2])
	}
}
