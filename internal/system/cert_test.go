package system

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/certpem/testcert"
)

// TestCertInstaller: the live file goes through Priv's certificate operation, lighttpd is
// reloaded, and exactly the confined addons - the ones whose drop-in carries the certs group -
// are restarted; the busybox path reloads the init script and restarts nothing.
func TestCertInstaller(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	w := func(p, c string) {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(c), 0o644)
	}
	w("etc/group", "root:x:0:\ncerts:x:8101:\n")
	for _, p := range []AddonPolicy{{ID: "mosquitto", Mode: "confined", UID: 30001, User: "addon-mosquitto"}, {ID: "redmatic", Mode: "root"}, {ID: "hm2mqtt", Mode: "confined", UID: 30002, User: "addon-hm2mqtt"}} {
		b, _ := json.Marshal(p)
		w(AddonPolicyDir+"/"+p.ID+".json", string(b))
	}
	if got := root.CertsGroupAddons(); strings.Join(got, ",") != "hm2mqtt,mosquitto" {
		t.Fatalf("%v", got)
	}
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	leaf, key, ca := testcert.Issued([]string{"box.example.org"}, time.Now().Add(90*24*time.Hour))
	live, _ := certpem.AssemblePEM(append(leaf, ca...), key)
	var lines []string
	log := func(l string) { lines = append(lines, l) }

	inst := CertInstaller{Root: root, Run: rec, Systemd: true}
	restarted, err := inst.Install(context.Background(), live, "acme", log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(restarted, ",") != "hm2mqtt,mosquitto" {
		t.Fatalf("%v", restarted)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "etc/config/server.pem")); err != nil || string(b) != string(live) {
		t.Fatalf("live file: %v", err)
	}
	want := []string{"systemctl reload --no-pager -- lighttpd.service", "systemctl restart --no-pager -- addon-hm2mqtt.service", "systemctl restart --no-pager -- addon-mosquitto.service"}
	if strings.Join(cmds, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%v", cmds)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "wrote /etc/config/server.pem") || !strings.Contains(j, "restarted addon-mosquitto.service (certs group)") {
		t.Fatalf("%v", lines)
	}
	// the markers beside the file say whose the certificate is and in which mode; ReadLive
	// gives the certificate half and the managed marker's line
	if m, err := os.ReadFile(filepath.Join(dir, "etc/config/server.pem.acme")); err != nil || !strings.Contains(string(m), "Test CA") {
		t.Fatalf("marker: %v %q", err, m)
	}
	if m, err := os.ReadFile(filepath.Join(dir, "etc/config/server.pem.managed")); err != nil || !strings.HasPrefix(string(m), "acme CN=Test CA") {
		t.Fatalf("managed marker: %v %q", err, m)
	}
	got, marker, err := inst.ReadLive()
	if err != nil || strings.Contains(string(got), "PRIVATE KEY") || !strings.Contains(string(got), "BEGIN CERTIFICATE") || !strings.HasPrefix(marker, "acme CN=Test CA") {
		t.Fatalf("%v %s %q", err, got, marker)
	}
	// a bad file never reaches the disk, nor does an unknown mode
	if _, err := inst.Install(context.Background(), leaf, "acme", log); err == nil {
		t.Fatal("a certificate without its key was installed")
	}
	if _, err := inst.Install(context.Background(), live, "self-signed", log); err == nil {
		t.Fatal("an unknown mode was installed")
	}
	// task 38: a manual install names its mode
	if _, err := inst.Install(context.Background(), live, "manual", log); err != nil {
		t.Fatal(err)
	}
	if _, marker, _ := inst.ReadLive(); !strings.HasPrefix(marker, "manual CN=Test CA") {
		t.Fatalf("manual marker: %q", marker)
	}
	// the switch back removes the file and reloads; the addons follow
	cmds = nil
	restarted, err = inst.Remove(context.Background(), log)
	if err != nil || len(restarted) != 2 || len(cmds) != 3 || !strings.HasPrefix(cmds[0], "systemctl reload") {
		t.Fatalf("%v %v %v", err, restarted, cmds)
	}
	if _, err := os.Stat(filepath.Join(dir, "etc/config/server.pem")); err == nil {
		t.Fatal("the live file is still there")
	}
	// and the markers are gone before the reload, so S50lighttpd regenerates
	for _, m := range []string{"etc/config/server.pem.acme", "etc/config/server.pem.managed"} {
		if _, err := os.Stat(filepath.Join(dir, m)); err == nil {
			t.Fatalf("%s is still there", m)
		}
	}
	if _, marker, _ := inst.ReadLive(); marker != "" {
		t.Fatalf("marker after remove: %q", marker)
	}
	// no certs group on the box: nothing to restart
	w("etc/group", "root:x:0:\n")
	if got := root.CertsGroupAddons(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// busybox: the init script's reload, no restarts
	cmds = nil
	bb := CertInstaller{Root: root, Run: rec, Systemd: false}
	if restarted, err := bb.Install(context.Background(), live, "acme", log); err != nil || len(restarted) != 0 || len(cmds) != 1 || !strings.HasSuffix(cmds[0], "/etc/init.d/S50lighttpd reload") {
		t.Fatalf("%v %v %v", err, restarted, cmds)
	}
}
