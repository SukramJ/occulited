package system

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// Task 107: RedMatic's update script stops its unit, copies the new files as root and starts the
// unit itself; the unit's ownership step gives the files to addon-redmatic before the start, so the
// start works. occulited then does not stop and start the addon a second time (B-106's restart) -
// unless something speaks for it: a process of the addon outside its unit (the install scope's
// leftover, B-106's case), a file with another owner, a unit that was not started during the install.

func TestInstallLeavesAnAddonItsUpdateStartedInItsUnit(t *testing.T) {
	const confined = `{"id":"hmm","mode":"confined","uid":30007,"user":"addon-hmm","source":"catalog"}`
	cases := []struct {
		name     string
		policy   string
		after    string // ActiveState after the installer
		since    string // InactiveExitTimestampMonotonic; the install began at 1000
		leftover bool   // a process of the addon in the install scope
		dirty    bool   // the dry run finds an entry with another owner
		leave    bool
		want     string
		wantLog  string
	}{
		{name: "started by its update, its files its user's", policy: confined, after: "active", since: "5000", leave: true,
			want:    "[systemd] hmm was started in its unit by its update and its files are its user's: left running",
			wantLog: `msg="addons: an addon its update started in its unit was left running" id=hmm`},
		{name: "a file with another owner", policy: confined, after: "active", since: "5000", dirty: true,
			want:    "[systemd] hmm restarted in its unit",
			wantLog: `why="1 entries with another owner, first `},
		{name: "a leftover in the install scope", policy: confined, after: "active", since: "5000", leftover: true,
			want: "[systemd] hmm restarted in its unit (1 process(es) left in the install scope stopped)"},
		{name: "started before the install", policy: confined, after: "active", since: "500",
			want: "[systemd] hmm restarted in its unit"},
		{name: "no timestamp from systemd", policy: confined, after: "active",
			want: "[systemd] hmm restarted in its unit"},
		{name: "still starting", policy: confined, after: "activating", since: "5000",
			want: "[systemd] hmm restarted in its unit"},
		{name: "a root addon", after: "active", since: "5000",
			want: "[systemd] hmm restarted in its unit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newSettleRig(t, failedStartUpdate)
			old := Priv
			Priv = settlePriv{rig: rig}
			t.Cleanup(func() { Priv = old })
			if tc.policy != "" {
				if err := os.WriteFile(rig.root.join(AddonPolicyDir+"/hmm.json"), []byte(tc.policy), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			rig.a.monoNow = func() uint64 { return 1000 }
			if tc.since != "" {
				rig.since["addon-hmm.service"] = tc.since
			}
			rig.dirty = tc.dirty
			if tc.leftover {
				fakeProcess(t, rig.root, 6807, "node /usr/local/addons/hmm/bin/node-red", "/system.slice/"+testScope, 1, 1000)
			}
			withStates(rig, "active", tc.after, false)
			var after []string
			rig.a.AfterStart = func(ids []string) { after = ids }
			var logs bytes.Buffer
			oldLog := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(oldLog) })

			res, err := rig.a.Install(context.Background(), strings.NewReader(strings.Repeat("x", 100)))
			if err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(rig.calls, "\n")
			stop := rig.index(t, "systemctl stop --no-pager -- addon-hmm.service")
			start := rig.index(t, "systemctl start --no-pager -- addon-hmm.service")
			if tc.leave {
				if stop >= 0 || start >= 0 || strings.Contains(joined, "chown -R") || strings.Contains(joined, "kill") {
					t.Errorf("an addon its update started was stopped, started or chowned again:\n%s", joined)
				}
				if !strings.Contains(joined, "owntree --dry-run 30007 "+rig.root.join("/usr/local/addons/hmm")) {
					t.Errorf("no look at its files:\n%s", joined)
				}
				if strings.Join(after, ",") != "hmm" {
					t.Errorf("B-92's check after the start was told %v", after)
				}
			} else if stop < 0 || start < stop {
				t.Errorf("want the stop and the start of B-106:\n%s", joined)
			}
			if tc.policy == "" && strings.Contains(joined, "owntree --dry-run") {
				t.Errorf("a root addon's files were looked at:\n%s", joined)
			}
			if !strings.Contains(res.Output, tc.want) {
				t.Errorf("output lacks %q:\n%s", tc.want, res.Output)
			}
			if !tc.leave && strings.Contains(res.Output, "left running") {
				t.Errorf("output says left running:\n%s", res.Output)
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("log %q lacks %q", logs.String(), tc.wantLog)
			}
			// task 110: the next start walks the whole tree - except for an addon left running, whose
			// whole dry run just found its files right, and a root addon
			if got, want := rig.root.FullWalkNeeded("hmm"), tc.policy != "" && !tc.leave; got != want {
				t.Errorf("marked for the whole walk %v, want %v", got, want)
			}
		})
	}
}

// The clock the install compares systemd's InactiveExitTimestampMonotonic with.
func TestInstallClockIsMonotonic(t *testing.T) {
	a := &SystemdAddons{}
	first := a.monotonic()
	second := a.monotonic()
	if first == 0 || first == ^uint64(0) || second < first {
		t.Errorf("CLOCK_MONOTONIC in microseconds: %d then %d", first, second)
	}
}
