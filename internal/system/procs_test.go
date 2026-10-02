package system

import (
	"os"
	"slices"
	"strconv"
	"testing"
)

// occulited B-30: an addon's processes are what it runs - its executable, argv[0], an
// interpreter's script, its own user - not every command line that names a file in its directory.
// On rpi4-2 an ssh session's `sh -c "md5sum /usr/local/addons/hmm/etc/hmm.env"` was signalled.
func TestAddonProcessesAreWhatTheAddonRuns(t *testing.T) {
	root := Root(t.TempDir())
	if err := os.MkdirAll(root.join("/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(root.join("/etc/passwd"), []byte("root:x:0:0::/:/bin/sh\naddon-hmm:x:30001:30001::/usr/local/addons/hmm:/bin/false\naddon-red:x:30000:30000::/x:/bin/false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uid := func(pid, n int) {
		_ = os.WriteFile(root.join("/proc/"+strconv.Itoa(pid)+"/status"), []byte("Name:\tx\nUid:\t"+strconv.Itoa(n)+"\t"+strconv.Itoa(n)+"\t"+strconv.Itoa(n)+"\t"+strconv.Itoa(n)+"\n"), 0o644)
	}
	const scope = "/system.slice/occulite-addon-1.scope"
	// the addon's own
	fakeProcess(t, root, 10, "/usr/local/addons/hmm/bin/node --max-semi-space-size=1 /usr/local/addons/hmm/app/dist/cli.js --local", scope, 1, 1)
	fakeProcess(t, root, 11, "node --optimize-for-size /usr/local/addons/hmm/app/worker.js", scope, 1, 1)
	fakeProcess(t, root, 12, "/bin/sh /usr/local/addons/hmm/bin/loop.sh", scope, 1, 1)
	fakeProcess(t, root, 13, "sh -c /usr/local/addons/hmm/bin/run.sh", scope, 1, 1)
	fakeProcess(t, root, 14, "hmm-title", scope, 1, 1) // its title alone, as its own user
	uid(14, 30001)
	fakeProcess(t, root, 15, "/opt/bin/thing", scope, 1, 1) // an executable under the directory
	if err := os.Symlink("/usr/local/addons/hmm/bin/thing", root.join("/proc/15/exe")); err != nil {
		t.Fatal(err)
	}
	fakeProcess(t, root, 16, "python3 -u /usr/local/addons/hmm/x.py", scope, 1, 1)
	// not the addon's
	fakeProcess(t, root, 20, "sh -c md5sum /usr/local/addons/hmm/etc/hmm.env", "/system.slice/sshd.service", 1, 1)
	fakeProcess(t, root, 21, "tail -f /usr/local/addons/hmm/var/log/hmm.log", "/user.slice/session-1.scope", 1, 1)
	fakeProcess(t, root, 22, "md5sum /usr/local/addons/hmm/etc/hmm.env", "/system.slice/sshd.service", 1, 1)
	fakeProcess(t, root, 23, "vi /usr/local/addons/hmm/etc/hmm.env", "/system.slice/sshd.service", 1, 1)
	fakeProcess(t, root, 24, "/usr/local/addons/hmmx/bin/daemon", scope, 1, 1)
	fakeProcess(t, root, 25, "node-red", "/system.slice/addon-red.service", 1, 1) // another addon's user
	uid(25, 30000)
	fakeProcess(t, root, 26, "/bin/tclsh /usr/local/addons/hmm/www/settings.cgi", "/system.slice/occulited-helper.service", 1, 1) // its CGI, the helper's child
	uid(26, 30001)
	fakeProcess(t, root, 27, "/usr/local/addons/hmm/bin/node /x", "/system.slice/occulited.service", 1, 1)
	fakeProcess(t, root, 28, "grep -r foo /usr/local/addons/hmm/", "/system.slice/sshd.service", 1, 1)

	var got []int
	for _, p := range addonProcesses(root, "hmm") {
		got = append(got, p.PID)
	}
	if want := []int{10, 11, 12, 13, 14, 15, 16}; !slices.Equal(got, want) {
		t.Errorf("hmm's processes %v, want %v", got, want)
	}
	// the other addon by its user, though its command line names nothing
	if got := addonProcesses(root, "red"); len(got) != 1 || got[0].PID != 25 {
		t.Errorf("red's processes %+v", got)
	}
	// no passwd entry: no uid rule, the rest as before
	_ = os.Remove(root.join("/etc/passwd"))
	got = got[:0]
	for _, p := range addonProcesses(root, "hmm") {
		got = append(got, p.PID)
	}
	if want := []int{10, 11, 12, 13, 15, 16}; !slices.Equal(got, want) {
		t.Errorf("without passwd %v, want %v", got, want)
	}
}
