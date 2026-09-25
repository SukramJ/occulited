package system

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/ownwalk"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// dataPriv records what the take-over asks the privilege boundary for. MkdirAll is done for
// real (the guard rails look at the result); Chown is recorded only - a test process cannot
// give a directory away.
type dataPriv struct {
	priv.Local
	chowned []string // "<path> <uid>:<gid> R"
	made    []string
}

func (d *dataPriv) Chown(path string, uid, gid int, recursive bool) error {
	r := ""
	if recursive {
		r = " R"
	}
	d.chowned = append(d.chowned, path+" "+strconv.Itoa(uid)+":"+strconv.Itoa(gid)+r)
	return nil
}

// OwnTree records the walk over the standard directories (task 107) like a recursive chown, with a T.
func (d *dataPriv) OwnTree(_ string, dirs []string, uid int, opt priv.OwnTreeOptions) (ownwalk.Result, error) {
	if !opt.DryRun {
		for _, p := range dirs {
			d.chowned = append(d.chowned, p+" "+strconv.Itoa(uid)+":"+strconv.Itoa(uid)+" T")
		}
	}
	return ownwalk.Result{}, nil
}

func (d *dataPriv) MkdirAll(path string, mode os.FileMode) error {
	d.made = append(d.made, path)
	return os.MkdirAll(path, mode)
}

func useDataPriv(t *testing.T) *dataPriv {
	t.Helper()
	dp := &dataPriv{}
	old := Priv
	Priv = dp
	t.Cleanup(func() { Priv = old })
	return dp
}

func quietRun(_ context.Context, _ string, _ ...string) ([]byte, error) { return nil, nil }

// B-62: the addon's own data directories - declared in the runtime block, or /usr/local/<id> by
// convention - are chowned with the three standard ones and go on ReadWritePaths.
func TestDataDirTakeOver(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                     "root:x:0:0::/:/bin/sh\n",
		"etc/group":                      "root:x:0:\n",
		"usr/local/addons/hmm/.keep":     "",
		"usr/local/etc/config/rc.d/hmm":  "#!/bin/sh\n",
		"usr/local/hmm/token":            "secret\n",
		"usr/local/hmm/cache/images/x":   "",
		"usr/local/hmm-data/config.json": "{}",
	})
	dp := useDataPriv(t)
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	p, err := a.SetPolicy(context.Background(), "hmm", "confined", "catalog", &AddonRuntime{DataDirs: []string{"/usr/local/hmm-data", "/usr/local/hmm-new"}})
	if err != nil {
		t.Fatal(err)
	}
	// the declared directory, the declared one that had to be created, and the convention one
	if want := []string{"/usr/local/hmm", "/usr/local/hmm-data", "/usr/local/hmm-new"}; !slices.Equal(p.DataDirs, want) {
		t.Fatalf("data dirs %v, want %v", p.DataDirs, want)
	}
	if !slices.Contains(dp.made, r.join("/usr/local/hmm-new")) {
		t.Errorf("the declared directory was not created: %v", dp.made)
	}
	for _, d := range p.DataDirs {
		want := r.join(d) + " " + strconv.Itoa(p.UID) + ":" + strconv.Itoa(p.UID) + " R"
		if !slices.Contains(dp.chowned, want) {
			t.Errorf("missing chown %q in %v", want, dp.chowned)
		}
	}
	conf, _ := os.ReadFile(r.join(AddonPolicyDir + "/hmm.conf"))
	if !strings.Contains(string(conf), "ReadWritePaths=-/usr/local/addons/hmm -/usr/local/etc/config/addons/hmm -/usr/local/etc/config/rc.d -/run -/var/log -/tmp -/var/tmp -/usr/local/hmm -/usr/local/hmm-data -/usr/local/hmm-new\n") {
		t.Errorf("drop-in lacks the data directories:\n%s", conf)
	}
	if got := r.ReadAddonPolicy("hmm"); got == nil || !slices.Equal(got.DataDirs, p.DataDirs) {
		t.Errorf("stored policy: %+v", got)
	}
	// a declared directory outside /usr/local is refused before anything is touched
	if _, err := a.SetPolicy(context.Background(), "hmm", "confined", "catalog", &AddonRuntime{DataDirs: []string{"/etc/config"}}); err == nil || !strings.Contains(err.Error(), "data_dir") {
		t.Errorf("a data_dir outside /usr/local accepted: %v", err)
	}
	for _, bad := range []string{"/usr/local", "/usr/local/", "/usr/local/addons", "/usr/local/etc/config", "/usr/local/tmp", "/usr/local/.firmwareUpdate", "/usr/local/hmm/../etc", "relative", "/usr/local/x;y"} {
		if _, err := a.SetPolicy(context.Background(), "hmm", "confined", "catalog", &AddonRuntime{DataDirs: []string{bad}}); err == nil {
			t.Errorf("data_dir %q accepted", bad)
		}
	}
}

// The box-dependent guard rails: another addon's directories in every relation, symlinks,
// files, the addon's own tree.
func TestDataDirGuardRails(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/rc.d/redmatic":             "#!/bin/sh\n",
		"usr/local/etc/config/rc.d/hmm":                  "#!/bin/sh\n",
		"usr/local/addons/redmatic/.keep":                "",
		"usr/local/addons/hmm/.keep":                     "",
		"usr/local/redmatic/.keep":                       "",
		"usr/local/shared/other/.keep":                   "",
		"usr/local/shared/mine/.keep":                    "",
		"usr/local/plain-file":                           "x",
		"usr/local/etc/config/addon-policy/other.json":   `{"id":"other","mode":"confined","uid":30009,"user":"addon-other","runtime":{"data_dirs":["/usr/local/shared/other"]}}`,
		"usr/local/etc/config/addon-policy/taken.json":   `{"id":"taken","mode":"confined","uid":30010,"user":"addon-taken","data_dirs":["/usr/local/hmm"]}`,
		"usr/local/hmm/.keep":                            "",
		"usr/local/addons/redmatic/var/.keep":            "",
		"usr/local/etc/config/addons/redmatic/.keep":     "",
		"usr/local/etc/config/addons/www/redmatic/.keep": "",
	})
	if err := os.Symlink("/etc", r.join("/usr/local/link")); err != nil {
		t.Fatal(err)
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	others := a.otherAddonDirs("hmm")
	for _, want := range []string{"/usr/local/addons/redmatic", "/usr/local/redmatic", "/usr/local/shared/other", "/usr/local/hmm", "/usr/local/etc/config/addons/www/redmatic"} {
		if !slices.Contains(others, want) {
			t.Errorf("other addons' directories lack %s: %v", want, others)
		}
	}
	if slices.Contains(others, "/usr/local/addons/hmm") {
		t.Error("the addon's own directory counted as another's")
	}
	cases := map[string]string{
		"/usr/local/redmatic":                "another addon's directory",
		"/usr/local/redmatic/sub":            "inside another addon's directory",
		"/usr/local/addons/redmatic/var":     "a shared directory", // the shape rule comes first
		"/usr/local/shared":                  "a prefix of another addon's directory",
		"/usr/local/shared/mine":             "",
		"/usr/local/shared/other":            "another addon's directory",
		"/usr/local/link":                    "a symlink",
		"/usr/local/plain-file":              "not a directory",
		"/usr/local/nothing":                 "does not exist",
		"/usr/local/addons/hmm/data":         "a shared directory",
		"/usr/local/etc/config/addons/hmm":   "a shared directory",
		"/usr/local/hmm":                     "another addon's directory", // taken's policy already owns it
		"/usr/local/addons":                  "a shared directory",
		"/usr/local/lost+found":              "not a plain absolute path", // the shape rule has no "+"
		"/usr/local/sdcard":                  "a shared directory",
		"/usr/local/etc/config/addon-policy": "a shared directory",
		"/usr/local/crontabs":                "a shared directory",
	}
	for d, want := range cases {
		got, why := a.dataDirAllowed("hmm", d, others)
		if want == "" && (why != "" || got != d) {
			t.Errorf("%s refused: %s", d, why)
		}
		if want != "" && !strings.HasPrefix(why, want) {
			t.Errorf("%s: got %q, want %q", d, why, want)
		}
	}
	// the convention directory belongs to another addon here: not taken, and the take-over
	// says so rather than failing
	dirs, refused := a.addonDataDirs("hmm", &AddonRuntime{DataDirs: []string{"/usr/local/shared/mine", "/usr/local/shared/mine"}}, false)
	if !slices.Equal(dirs, []string{"/usr/local/shared/mine"}) || refused["/usr/local/hmm"] == "" {
		t.Errorf("dirs %v refused %v", dirs, refused)
	}
	// an addon without a convention directory: nothing refused, nothing to say
	if dirs, refused := a.addonDataDirs("nobody", nil, false); dirs != nil || len(refused) != 0 {
		t.Errorf("nobody: %v %v", dirs, refused)
	}
	// and one whose convention directory is free is given it
	if dirs, _ := a.addonDataDirs("redmatic", nil, false); !slices.Equal(dirs, []string{"/usr/local/redmatic"}) {
		t.Errorf("redmatic: %v", dirs)
	}
}

// The heal at start: a policy confined before the take-over existed (hmm on the Pi, B-62) gets
// its directory chowned and rendered on the next start; a second start does nothing.
func TestRefreshDataDirs(t *testing.T) {
	r := rootWith(t, map[string]string{
		"usr/local/etc/config/addon-policy/hmm.json":  `{"id":"hmm","mode":"confined","uid":30001,"user":"addon-hmm","source":"catalog"}`,
		"usr/local/etc/config/addon-policy/root.json": `{"id":"root","mode":"root","source":"migrated"}`,
		"usr/local/etc/config/addon-policy/old.json":  `{"id":"old","mode":"confined","uid":30002,"user":"addon-old","data_dirs":["/usr/local/old"]}`,
		"usr/local/hmm/token":                         "secret\n",
		"usr/local/old/.keep":                         "",
		"usr/local/root/.keep":                        "",
	})
	dp := useDataPriv(t)
	owners := map[string]int{}
	oldOwner := ownerOf
	ownerOf = func(path string) (int, int, bool) {
		if _, err := os.Lstat(path); err != nil {
			return 0, 0, false
		}
		uid := owners[path]
		return uid, uid, true
	}
	t.Cleanup(func() { ownerOf = oldOwner })
	owners[r.join("/usr/local/old")] = 30002 // already the addon's: left alone
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if changed := a.RefreshDataDirs(); !slices.Equal(changed, []string{"hmm"}) {
		t.Fatalf("changed %v", changed)
	}
	if want := r.join("/usr/local/hmm") + " 30001:30001 R"; !slices.Equal(dp.chowned, []string{want}) {
		t.Errorf("chowned %v, want [%s]", dp.chowned, want)
	}
	if p := r.ReadAddonPolicy("hmm"); p == nil || !slices.Equal(p.DataDirs, []string{"/usr/local/hmm"}) {
		t.Errorf("policy: %+v", p)
	}
	if ids := a.RefreshPolicyDropIns(context.Background()); !slices.Contains(ids, "hmm") {
		t.Errorf("drop-ins renewed: %v", ids)
	}
	if conf, _ := os.ReadFile(r.join(AddonPolicyDir + "/hmm.conf")); !strings.Contains(string(conf), " -/usr/local/hmm\n") {
		t.Errorf("drop-in:\n%s", conf)
	}
	// the root policy's convention directory is nobody's business
	for _, c := range dp.chowned {
		if strings.Contains(c, "/usr/local/root") {
			t.Errorf("a root policy's directory chowned: %v", dp.chowned)
		}
	}
	// second start: owned now, nothing changes
	owners[r.join("/usr/local/hmm")] = 30001
	dp.chowned = nil
	if changed := a.RefreshDataDirs(); changed != nil || dp.chowned != nil {
		t.Errorf("second run: changed %v chowned %v", changed, dp.chowned)
	}
	// a subtree left root's by a later root run heals too: the check is the top directory
	owners[r.join("/usr/local/hmm")] = 0
	if a.RefreshDataDirs(); len(dp.chowned) != 1 {
		t.Errorf("ownership not repaired: %v", dp.chowned)
	}
}

// The runtime block carries data_dirs through the merge and the catalogue refresh.
func TestDataDirsInRuntimeBlock(t *testing.T) {
	m := MergeRuntime(&AddonRuntime{DataDirs: []string{"/usr/local/hmm"}}, &AddonRuntime{Ports: []int{1}})
	if !slices.Equal(m.DataDirs, []string{"/usr/local/hmm"}) {
		t.Errorf("merge dropped data_dirs: %+v", m)
	}
	if err := (&AddonRuntime{DataDirs: []string{"/usr/local/hmm"}}).validate(); err != nil {
		t.Error(err)
	}
	if err := (&AddonRuntime{DataDirs: []string{"/usr/local/etc/x"}}).validate(); err == nil {
		t.Error("a shared tree accepted")
	}
}
