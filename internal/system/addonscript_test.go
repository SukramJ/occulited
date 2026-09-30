package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// B-119: a confined addon's rc.d script is its own code (rc.d/<id>.script links into its tree), so
// its `info` runs as the addon's user through RunAs, with the addon's environment; an addon without
// a credential runs as root as the rc.d ABI always did.
func TestAddonsInfoRunsAsTheAddonUser(t *testing.T) {
	r := fakeRoot(t)
	if err := os.WriteFile(r.join("usr/local/etc/config/rc.d/rootaddon"), []byte("#!/bin/sh\ncase $1 in info) echo 'Name: Root Addon';; esac\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fp := &fakePriv{out: priv.Result{Stdout: []byte("Name: Confined Mosquitto\nVersion: 9.9\nOperations: restart uninstall\n")}}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	cred := priv.Credential{UID: 30002, GID: 30002, Groups: []int{30002}}
	b := AddonScripts{Root: r, Credential: func(id string) *priv.Credential {
		if id == "mosquitto" {
			return &cred
		}
		return nil
	}}
	addons, err := b.ListAddons(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Addon{}
	for _, a := range addons {
		got[a.ID] = a
	}
	// the confined one: what RunAs answered, run as its user with its own environment
	if got["mosquitto"].Name != "Confined Mosquitto" || got["mosquitto"].Version != "9.9" {
		t.Errorf("the confined addon's info was not RunAs's answer: %+v", got["mosquitto"])
	}
	if fp.last.cred.UID != cred.UID || fp.last.cred.GID != cred.GID || len(fp.last.cred.Groups) != 1 || fp.last.cred.Groups[0] != cred.GID || fp.last.name != r.join("/usr/local/etc/config/rc.d/mosquitto") || strings.Join(fp.last.args, " ") != "info" || fp.last.dir != "/" {
		t.Errorf("RunAs got cred=%+v name=%s args=%v dir=%s", fp.last.cred, fp.last.name, fp.last.args, fp.last.dir)
	}
	env := strings.Join(fp.last.env, "\n")
	if !strings.Contains(env, "HOME=/usr/local/addons/mosquitto\n") || !strings.Contains(env, "PATH=/bin:/sbin:/usr/bin:/usr/sbin") || !strings.Contains(env, "USER=addon-mosquitto") {
		t.Errorf("env: %v", fp.last.env)
	}
	// the root one ran for real (fakePriv's Run is Local's)
	if got["rootaddon"].Name != "Root Addon" {
		t.Errorf("the root addon's info: %+v", got["rootaddon"])
	}
}

// B-119: the uninstall of a confined addon runs as its user (RunAs, not the install scope, which
// cannot drop root's groups); afterwards root removes what the script could not - the rc.d entry
// and its .script, the www link and the standard directories the script emptied - and leaves a
// directory with anything still in it alone.
func TestUninstallConfinedRunsAsTheAddonUser(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/rc.d/new":                    "#!/bin/sh\n# openccu-lite addon-rc wrapper\nexit 0\n",
		"usr/local/etc/config/rc.d/new.script":             "#!/bin/sh\nexit 0\n",
		"usr/local/etc/config/addon-policy/new.json":       `{"id":"new","mode":"confined","uid":30000,"user":"addon-new"}`,
		"usr/local/etc/config/addons/new/.keep":            "", // not emptied by the script: stays
		"usr/local/addons/new/.emptied":                    "",
		"usr/local/etc/config/hm_addons.cfg":               "new {CONFIG_URL /addons/new/index.html ID new CONFIG_NAME New}\n",
		"usr/local/etc/config/addons/www/other/index.html": "",
	})
	// what the script "did" as its user: emptied its own tree
	_ = os.Remove(r.join("/usr/local/addons/new/.emptied"))
	_ = os.Symlink("/usr/local/addons/new/www", r.join("/usr/local/etc/config/addons/www/new"))
	fp := &fakePriv{out: priv.Result{Stdout: []byte("uninstalled as user\n")}}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte("ok"), nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run})
	out, err := a.Uninstall(context.Background(), "new")
	if err != nil || out.Output != "uninstalled as user" {
		t.Fatalf("%v %+v", err, out)
	}
	if fp.last.cred.UID != 30000 || fp.last.cred.GID != 30000 || len(fp.last.cred.Groups) != 1 || fp.last.cred.Groups[0] != 30000 {
		t.Errorf("the credential must be the addon's uid, gid and its one group: %+v", fp.last.cred)
	}
	if fp.last.name != r.join("/usr/local/etc/config/rc.d/new") || strings.Join(fp.last.args, " ") != "uninstall" {
		t.Errorf("RunAs: %s %v", fp.last.name, fp.last.args)
	}
	joined := strings.Join(calls, "\n")
	if strings.Contains(joined, "uninstall") {
		t.Errorf("the script must not run as root through the scope:\n%s", joined)
	}
	if !strings.HasPrefix(calls[0], "systemctl stop --no-pager -- addon-new.service") {
		t.Errorf("the unit is stopped first:\n%s", joined)
	}
	for _, gone := range []string{"/usr/local/etc/config/rc.d/new", "/usr/local/etc/config/rc.d/new.script", "/usr/local/etc/config/addons/www/new", "/usr/local/addons/new"} {
		if _, err := os.Lstat(r.join(gone)); !os.IsNotExist(err) {
			t.Errorf("%s must be gone after the uninstall", gone)
		}
	}
	if _, err := os.Stat(r.join("/usr/local/etc/config/addons/new/.keep")); err != nil {
		t.Error("a directory the script did not empty must stay")
	}
	if _, err := os.Stat(r.join("/usr/local/etc/config/addons/www/other/index.html")); err != nil {
		t.Error("another addon's www entry must stay")
	}
	if b, _ := os.ReadFile(r.join("/usr/local/etc/config/hm_addons.cfg")); strings.Contains(string(b), "new") {
		t.Errorf("the hm_addons.cfg entry stays: %s", b)
	}
	// a root addon's uninstall still runs as root through the scope
	_ = os.WriteFile(r.join("/usr/local/etc/config/rc.d/rooty"), []byte("#!/bin/sh\necho root uninstall\n"), 0o755)
	calls = nil
	fp.last.name = ""
	// (with a recorder Runner the scope is left out and the script runs plainly, as root)
	out, err = a.Uninstall(context.Background(), "rooty")
	if err != nil || out.Output != "root uninstall" || fp.last.name != "" {
		t.Errorf("a root addon's uninstall: out=%+v err=%v RunAs=%q", out, err, fp.last.name)
	}
}

// B-119 (seen again 2026-09-23): a fresh install whose update_script started the daemon as root in
// the install scope - before the wrapper existed - and then asked for a reboot (exit 10) left that
// root daemon running until the reboot, and the unit failed on its port. The leftovers of a fresh
// addon are stopped whatever the installer's exit - before its unit starts it (B-186: a new addon is
// started after an exit 10 too).
func TestInstallStopsAFreshAddonsScopeLeftoversWhateverTheExit(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/tmp/.keep": ""})
	rc := r.join("/usr/local/etc/config/rc.d/x")
	_ = os.MkdirAll(filepath.Dir(rc), 0o755)
	_ = os.MkdirAll(r.join("/bin"), 0o755)
	_ = os.WriteFile(r.join("/bin/install_addon"), []byte("#!/bin/sh\ntouch "+rc+"\nchmod +x "+rc+"\nexit 10\n"), 0o755)
	fakeProcess(t, r, 4242, "node /usr/local/addons/x/app/server.js", "/system.slice/"+testScope, 1, 500)
	fakeProcess(t, r, 4243, "node /usr/local/addons/other/app.js", "/system.slice/"+testScope, 1, 500)
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: run, StopWait: 10 * time.Millisecond})
	a.scopeFn = func() string { return testScope }
	res, err := a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
	if err != nil || !res.RebootRequired {
		t.Fatalf("%v %+v", err, res)
	}
	joined := strings.Join(calls, "\n")
	if !strings.Contains(joined, "kill -TERM 4242") {
		t.Errorf("the fresh addon's root daemon in the scope was not stopped:\n%s", joined)
	}
	if strings.Contains(joined, "4243") {
		t.Errorf("another addon's process was signalled:\n%s", joined)
	}
	kill, start := strings.Index(joined, "kill -TERM 4242"), strings.Index(joined, "systemctl start --no-pager -- addon-x.service")
	if start < 0 || kill > start {
		t.Errorf("the new addon's unit must start after its leftovers were stopped (B-186):\n%s", joined)
	}
	if !strings.Contains(res.Output, "x: 1 process(es) its installer started outside a unit were stopped") {
		t.Errorf("the result must say what was stopped:\n%s", res.Output)
	}
}
