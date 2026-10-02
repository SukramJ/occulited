package system

import (
	"context"
	"os"
	"strings"
	"testing"
)

const bootPasswd = "root:x:0:0::/:/bin/sh\n"

func uidRoot(t *testing.T, ids ...string) Root {
	t.Helper()
	files := map[string]string{
		"etc/passwd": bootPasswd,
		"etc/group":  "root:x:0:\n",
	}
	for _, id := range ids {
		files["usr/local/addons/"+id+"/.keep"] = ""
		files["usr/local/etc/config/rc.d/"+id] = "#!/bin/sh\n"
	}
	return rootWith(t, files)
}

func confine(t *testing.T, a *SystemdAddons, id string) int {
	t.Helper()
	p, err := a.SetPolicy(context.Background(), id, "confined", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.UID
}

// reboot is what a new boot does to the addon users: /etc/passwd is the image's again, and
// addon-users recreates only the users of the policies still stored.
func reboot(t *testing.T, r Root) {
	t.Helper()
	if err := os.WriteFile(r.join("/etc/passwd"), []byte(bootPasswd), 0o644); err != nil {
		t.Fatal(err)
	}
}

// openccu-lite B-288: an uninstall (B-283 removes every addon-policy/<id>.*) keeps the addon's uid
// reserved. Another addon installed in between never gets it, and the reinstall gets it back - even
// when the uninstalled addon held the highest uid and a reboot took its user out of /etc/passwd,
// which is where the max-of-the-policies rule handed it to the next addon.
func TestAddonUIDSurvivesUninstall(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t, "redmatic", "hm2mqtt", "mosquitto")
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if got := confine(t, a, "redmatic"); got != AddonUIDBase {
		t.Fatalf("redmatic: %d", got)
	}
	if got := confine(t, a, "hm2mqtt"); got != AddonUIDBase+1 {
		t.Fatalf("hm2mqtt: %d", got)
	}

	// uninstalled: the policy files go, the rc.d entry goes, the box reboots
	if removed := r.removeAddonPolicyFiles("hm2mqtt"); len(removed) == 0 {
		t.Fatal("nothing removed")
	}
	if err := os.Remove(r.join("/usr/local/etc/config/rc.d/hm2mqtt")); err != nil {
		t.Fatal(err)
	}
	reboot(t, r)
	if ids := a.SweepStalePolicyFiles(); len(ids) != 0 {
		t.Fatalf("swept %v", ids)
	}
	if reg := r.ReadAddonUIDs(); reg["hm2mqtt"] != AddonUIDBase+1 {
		t.Fatalf("the reservation went with the policy: %v", reg)
	}

	if got := confine(t, a, "mosquitto"); got != AddonUIDBase+2 {
		t.Fatalf("another addon got %d, want %d (hm2mqtt's is reserved)", got, AddonUIDBase+2)
	}
	if err := os.WriteFile(r.join("/usr/local/etc/config/rc.d/hm2mqtt"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := confine(t, a, "hm2mqtt"); got != AddonUIDBase+1 {
		t.Fatalf("the reinstall got %d, want its old %d", got, AddonUIDBase+1)
	}
	want := map[string]int{"redmatic": AddonUIDBase, "hm2mqtt": AddonUIDBase + 1, "mosquitto": AddonUIDBase + 2}
	if reg := r.ReadAddonUIDs(); len(reg) != 3 || reg["redmatic"] != want["redmatic"] || reg["hm2mqtt"] != want["hm2mqtt"] || reg["mosquitto"] != want["mosquitto"] {
		t.Fatalf("registry %v, want %v", reg, want)
	}
	// the registry is not a policy: the stale sweep and the policy list do not see it
	if _, ok := r.AddonPolicies()["addon-uids"]; ok {
		t.Fatal("the registry reads as a policy")
	}
	if !strings.Contains(readFile(r.join(AddonUIDsFile)), `"hm2mqtt": 30001`) {
		t.Fatalf("file: %s", readFile(r.join(AddonUIDsFile)))
	}
}

// A switch to root and back keeps the uid as before (it is in the policy), and a policy the user
// set to root never got a uid, so nothing is registered for it.
func TestAddonUIDRootModeRegistersNothing(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t, "hmm")
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if _, err := a.SetPolicy(context.Background(), "hmm", "root", "user", nil); err != nil {
		t.Fatal(err)
	}
	if reg := r.ReadAddonUIDs(); len(reg) != 0 {
		t.Fatalf("root mode registered %v", reg)
	}
	uid := confine(t, a, "hmm")
	if _, err := a.SetPolicy(context.Background(), "hmm", "root", "user", nil); err != nil {
		t.Fatal(err)
	}
	if again := confine(t, a, "hmm"); again != uid {
		t.Fatalf("confined again as %d, was %d", again, uid)
	}
}

// A registered uid that another addon's stored policy holds - a system where an uninstall under
// B-283 gave it away before the registry existed - is not shared: the addon gets a new one, above
// every uid in use, and the registry follows.
func TestAddonUIDNotSharedWithAPolicy(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t, "old", "taker")
	writeTestFile(t, r, AddonPolicyDir+"/taker.json", `{"id":"taker","mode":"confined","uid":30001,"user":"addon-taker"}`+"\n")
	writeTestFile(t, r, AddonUIDsFile, `{"uids":{"old":30001,"taker":30001,"gone":30004}}`+"\n")
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if got := confine(t, a, "old"); got != 30005 {
		t.Fatalf("old got %d, want 30005", got)
	}
	if reg := r.ReadAddonUIDs(); reg["old"] != 30005 || reg["taker"] != 30001 || reg["gone"] != 30004 {
		t.Fatalf("registry %v", reg)
	}
}

// The uids of this boot's addon users count as taken too: a user whose policy is gone keeps its
// uid until the next boot, and a new addon must not be given it meanwhile.
func TestAddonUIDAvoidsPasswdUsers(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t, "new")
	writeTestFile(t, r, "/etc/passwd", bootPasswd+"addon-ghost:x:30007:30007::/usr/local/addons/ghost:/bin/false\nnobody:x:65534:65534::/:/bin/false\n")
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if got := confine(t, a, "new"); got != 30008 {
		t.Fatalf("got %d, want 30008", got)
	}
}

// SeedAddonUIDs fills the registry from what the system holds: the stored policies first, then
// the addon users of this boot, then the owners of the addon directories. What is registered is
// kept, a uid already registered to one addon is not given to a second, and a second run adds
// nothing.
func TestSeedAddonUIDs(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t)
	writeTestFile(t, r, AddonPolicyDir+"/hmm.json", `{"id":"hmm","mode":"confined","uid":30001}`+"\n")
	writeTestFile(t, r, AddonPolicyDir+"/rootie.json", `{"id":"rootie","mode":"root"}`+"\n")
	writeTestFile(t, r, AddonPolicyDir+"/redmatic.json", `{"id":"redmatic","mode":"confined","uid":30000}`+"\n")
	writeTestFile(t, r, "/etc/passwd", bootPasswd+
		"addon-hmm:x:30001:30001::/:/bin/false\n"+
		"addon-hm2mqtt:x:30003:30003::/:/bin/false\n"+ // uninstalled under B-283, before the reboot
		"addon-clash:x:30000:30000::/:/bin/false\n"+ // a uid redmatic's policy holds
		"addon-low:x:1000:1000::/:/bin/false\n"+
		"daemon:x:30009:30009::/:/bin/false\n")
	writeTestFile(t, r, AddonUIDsFile, `{"uids":{"keep":30002}}`+"\n")
	for _, d := range []string{"mosquitto", "rootowned", "keep", "bad id"} {
		if err := os.MkdirAll(r.join("/usr/local/addons/"+d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	owners := map[string]int{"mosquitto": 30005, "rootowned": 0, "keep": 30006, "bad id": 30010}
	old := ownerOf
	ownerOf = func(path string) (int, int, bool) {
		for d, uid := range owners {
			if strings.HasSuffix(path, "/usr/local/addons/"+d) {
				return uid, uid, true
			}
		}
		return 0, 0, false
	}
	t.Cleanup(func() { ownerOf = old })
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})

	if got := strings.Join(a.SeedAddonUIDs(), " "); got != "hm2mqtt hmm mosquitto redmatic" {
		t.Fatalf("added %q", got)
	}
	want := map[string]int{"keep": 30002, "hmm": 30001, "redmatic": 30000, "hm2mqtt": 30003, "mosquitto": 30005}
	reg := r.ReadAddonUIDs()
	if len(reg) != len(want) {
		t.Fatalf("registry %v, want %v", reg, want)
	}
	for id, uid := range want {
		if reg[id] != uid {
			t.Fatalf("registry %v, want %v", reg, want)
		}
	}
	if again := a.SeedAddonUIDs(); again != nil {
		t.Fatalf("second run added %v", again)
	}
}

// A registry that does not parse is rebuilt by the seed rather than trusted, and one that names a
// non-id or a uid below the base keeps those out.
func TestReadAddonUIDsRefusesJunk(t *testing.T) {
	r := rootWith(t, map[string]string{"usr/local/etc/config/addon-uids.json": "{not json"})
	if reg := r.ReadAddonUIDs(); len(reg) != 0 {
		t.Fatalf("broken file: %v", reg)
	}
	r = rootWith(t, map[string]string{"usr/local/etc/config/addon-uids.json": `{"uids":{"ok":30000,"../x":30001,"low":500}}`})
	if reg := r.ReadAddonUIDs(); len(reg) != 1 || reg["ok"] != 30000 {
		t.Fatalf("junk kept: %v", reg)
	}
	if reg := rootWith(t, nil).ReadAddonUIDs(); len(reg) != 0 {
		t.Fatalf("no file: %v", reg)
	}
}

// The registry cannot be written: the addon is not confined without its reservation.
func TestAddonUIDWriteFailure(t *testing.T) {
	useDataPriv(t)
	r := uidRoot(t, "hmm")
	// a directory where the file goes: the write fails
	if err := os.MkdirAll(r.join(AddonUIDsFile+"/x"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := NewSystemdAddons(r, SystemdServices{Root: r, Run: quietRun})
	if _, err := a.SetPolicy(context.Background(), "hmm", "confined", "user", nil); err == nil || !strings.Contains(err.Error(), "uid registry") {
		t.Fatalf("err %v", err)
	}
	if p := r.ReadAddonPolicy("hmm"); p != nil {
		t.Fatalf("a policy was stored: %+v", p)
	}
}

func writeTestFile(t *testing.T, r Root, path, content string) {
	t.Helper()
	full := r.join(path)
	if err := os.MkdirAll(full[:strings.LastIndex(full, "/")], 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
