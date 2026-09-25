package system

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// Task 110: the marker "full walk needed" - <id>.fullwalk beside the addon's policy - is what makes
// the unit's start step and B-92's look walk an addon's whole tree. The install flow and a switch to
// the addon's own user write it; the start step's whole walk removes it (cmd/occulited's tests).

// markInstaller is hmm's update as it is (its own link over the wrapper, so hmm counts as touched),
// and it says which markers are there while it runs - where an update script's own unit start
// would look.
const markInstaller = "#!/bin/sh\nfor id in hmm calm waiting old; do [ -e ROOT/usr/local/etc/config/addon-policy/$id.fullwalk ] && echo \"marked during the install: $id\"; done\nrm -f ROOT/usr/local/etc/config/rc.d/hmm\nln -sfn ROOT/usr/local/addons/hmm/rc.d/hmm ROOT/usr/local/etc/config/rc.d/hmm\nrm -f ROOT/usr/local/tmp/new_addon.tar.gz\nexit 0\n"

func markRig(t *testing.T) *settleRig {
	t.Helper()
	rig := newSettleRig(t, markInstaller)
	old := Priv
	Priv = settlePriv{rig: rig}
	t.Cleanup(func() { Priv = old })
	for id, policy := range map[string]string{
		"hmm":     `{"id":"hmm","mode":"confined","uid":30007,"user":"addon-hmm","source":"catalog"}`,
		"calm":    `{"id":"calm","mode":"confined","uid":30008,"user":"addon-calm","source":"catalog"}`,
		"waiting": `{"id":"waiting","mode":"confined","uid":30009,"user":"addon-waiting","source":"catalog"}`,
	} {
		if err := os.WriteFile(rig.root.join(AddonPolicyDir+"/"+id+".json"), []byte(policy), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// waiting's install asked for a reboot, and it was not started since: its marker is older
	markWhole(t, rig.root, "waiting")
	return rig
}

func TestInstallMarksTheWholeWalk(t *testing.T) {
	marks := func(r Root) string {
		var out []string
		for _, id := range []string{"hmm", "calm", "waiting", "old"} {
			if r.FullWalkNeeded(id) {
				out = append(out, id)
			}
		}
		return strings.Join(out, " ")
	}

	// hmm runs and is put back into its unit (B-106). A start of its unit between the install's mark and
	// B-106's stop took the mark - the stop's leftovers may write as root, so the mark is made again
	// before the unit starts.
	t.Run("an update put back into its unit", func(t *testing.T) {
		rig := markRig(t)
		rig.active["addon-hmm.service"] = "active"
		base := rig.a.Systemd.Run
		markedAtStart := false
		rig.a.Systemd.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			line := name + " " + strings.Join(args, " ")
			switch line {
			case "systemctl stop --no-pager -- addon-hmm.service":
				_ = os.Remove(rig.root.FullWalkMarker("hmm"))
			case "systemctl start --no-pager -- addon-hmm.service":
				markedAtStart = rig.root.FullWalkNeeded("hmm")
			}
			return base(ctx, name, args...)
		}
		res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
		if err != nil {
			t.Fatal(err)
		}
		// every confined addon while the installer runs, root's old never
		for _, id := range []string{"hmm", "calm", "waiting"} {
			if !strings.Contains(res.Output, "marked during the install: "+id) {
				t.Errorf("%s not marked while the installer ran:\n%s", id, res.Output)
			}
		}
		if strings.Contains(res.Output, "marked during the install: old") {
			t.Errorf("a root addon was marked:\n%s", res.Output)
		}
		if rig.index(t, "systemctl start --no-pager -- addon-hmm.service") < 0 || !markedAtStart {
			t.Errorf("the unit's start came without the mark (%v):\n%s", markedAtStart, strings.Join(rig.calls, "\n"))
		}
		// afterwards: the touched addon marked for its start, the untouched one taken back, the older mark kept
		if got := marks(rig.root); got != "hmm waiting" {
			t.Errorf("marks after the install %q, want %q", got, "hmm waiting")
		}
	})

	// hmm is stopped and stays stopped: its next start walks the whole tree
	t.Run("an update left stopped", func(t *testing.T) {
		rig := markRig(t)
		rig.active["addon-hmm.service"] = "inactive"
		res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
		if err != nil || !strings.Contains(res.Output, "hmm is not running and was not started") {
			t.Fatalf("%v\n%+v", err, res)
		}
		if got := marks(rig.root); got != "hmm waiting" {
			t.Errorf("marks %q", got)
		}
	})

	// an archive the installer never saw: every mark the install made is taken back
	t.Run("the installer did not run", func(t *testing.T) {
		rig := markRig(t)
		if _, err := rig.a.Install(context.Background(), strings.NewReader("x")); err == nil {
			t.Fatal("an empty archive was installed")
		}
		if got := marks(rig.root); got != "waiting" {
			t.Errorf("marks %q", got)
		}
	})
}

// markWritesPriv is dataPriv with every file write recorded.
type markWritesPriv struct {
	*dataPriv
	writes []string
}

func (p *markWritesPriv) WriteFile(path string, data []byte, mode os.FileMode) error {
	p.writes = append(p.writes, fmt.Sprintf("%s %o", path, mode))
	return p.dataPriv.WriteFile(path, data, mode)
}

// A switch to the addon's own user marks the whole walk: what it wrote as root is anywhere in its
// tree, and a root daemon still running (PUT /policy without the restart) may write more. A policy
// write that keeps the addon confined - the catalogue's runtime block after its install - does not,
// and neither does a switch to root.
func TestSetPolicyMarksTheWholeWalk(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                    "root:x:0:0::/:/bin/sh\n",
		"etc/group":                     "root:x:0:\n",
		"usr/local/addons/hmm/.keep":    "",
		"usr/local/etc/config/rc.d/hmm": "#!/bin/sh\n",
	})
	mp := &markWritesPriv{dataPriv: &dataPriv{}}
	old := Priv
	Priv = mp
	t.Cleanup(func() { Priv = old })
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	ctx := context.Background()

	if _, err := a.SetPolicy(ctx, "hmm", "confined", "user", nil); err != nil {
		t.Fatal(err)
	}
	if !r.FullWalkNeeded("hmm") || !strings.Contains(strings.Join(mp.writes, "\n"), r.FullWalkMarker("hmm")+" 644") {
		t.Fatalf("the switch to its own user left no marker through the helper: %v", mp.writes)
	}
	if err := os.Remove(r.FullWalkMarker("hmm")); err != nil { // the unit's start walked the whole tree
		t.Fatal(err)
	}
	if _, err := a.SetPolicy(ctx, "hmm", "confined", "catalog", &AddonRuntime{Groups: []string{"dialout"}}); err != nil {
		t.Fatal(err)
	}
	if r.FullWalkNeeded("hmm") {
		t.Error("a policy write that keeps the addon confined marked the whole walk")
	}
	if _, err := a.SetPolicy(ctx, "hmm", "root", "user", nil); err != nil || r.FullWalkNeeded("hmm") {
		t.Errorf("the switch to root: %v, marked %v", err, r.FullWalkNeeded("hmm"))
	}
	if _, err := a.SetPolicy(ctx, "hmm", "confined", "user", nil); err != nil || !r.FullWalkNeeded("hmm") {
		t.Errorf("confined again: %v, marked %v", err, r.FullWalkNeeded("hmm"))
	}
}

// The addon's user can neither create nor remove its marker: the marker is written through the helper
// into the policy directory, which only root can change, and that directory is never one the
// ownership walk or a data directory gives to an addon. A name that is not an addon id writes
// nothing.
func TestFullWalkMarkerIsOutOfTheAddonsReach(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/passwd":                  "root:x:0:0::/:/bin/sh\naddon-hmm:x:30001:30001::/usr/local/addons/hmm:/bin/false\n",
		"etc/group":                   "root:x:0:\n",
		"usr/local/addons/hmm/.keep":  "",
		"usr/local/hmm/token":         "",
		"usr/local/etc/config/.keep":  "",
		"usr/local/etc/config/rc.d/x": "",
	})
	mp := &markWritesPriv{dataPriv: &dataPriv{}}
	old := Priv
	Priv = mp
	t.Cleanup(func() { Priv = old })
	if err := r.writeAddonPolicy(&AddonPolicy{ID: "hmm", Mode: "confined", UID: 30001, User: "addon-hmm", DataDirs: []string{"/usr/local/hmm"}}); err != nil {
		t.Fatal(err)
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	marker := r.FullWalkMarker("hmm")

	mp.writes = nil
	if created := a.markFullWalk("install", "hmm", "../hmm", "hmm/../../etc", ""); len(created) != 1 || created[0] != "hmm" {
		t.Errorf("created %v", created)
	}
	if len(mp.writes) != 1 || mp.writes[0] != marker+" 644" {
		t.Errorf("the writes through the helper: %v", mp.writes)
	}
	dir := r.join(AddonPolicyDir)
	if !strings.HasPrefix(marker, dir+"/") {
		t.Fatalf("the marker %s is not in the policy directory", marker)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm()&0o022 != 0 {
		t.Errorf("the policy directory can be written by others than its owner: %v %v", st.Mode(), err)
	}

	// the walk's directories, as the root step and the helper take them: none holds the marker
	plan, err := AddonOwnPlan(r, "hmm")
	if err != nil || plan == nil {
		t.Fatalf("plan %+v %v", plan, err)
	}
	for _, d := range plan.Dirs {
		if marker == d || strings.HasPrefix(marker, d+"/") {
			t.Errorf("the walk over %s would give the marker to the addon", d)
		}
	}
	pol := priv.DefaultPolicy(string(r), "/usr/local/etc/occulite")
	for _, d := range []string{AddonPolicyDir, "/usr/local/etc/config", "/usr/local/etc", "/usr/local/etc/config/addons"} {
		if err := pol.OwnTreeAllowed("hmm", []string{r.join(d)}, 30001); err == nil {
			t.Errorf("the helper would give %s to addon-hmm", d)
		}
		if _, why := a.dataDirAllowed("hmm", d, a.otherAddonDirs("hmm")); why == "" {
			t.Errorf("%s is admitted as hmm's data directory", d)
		}
	}
}
