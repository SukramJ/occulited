package radio

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func lgwRoot(t *testing.T, rfd, hs485d string) (string, *recorder) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"etc/config", "var", "bin"} {
		_ = os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	_ = os.WriteFile(filepath.Join(root, "var/hm_mode"), []byte("HM_MODE='NORMAL'\n"), 0o644)
	if rfd != "" {
		_ = os.WriteFile(filepath.Join(root, "etc/config/rfd.conf"), []byte(rfd), 0o600)
	}
	if hs485d != "" {
		_ = os.WriteFile(filepath.Join(root, "etc/config/hs485d.conf"), []byte(hs485d), 0o600)
	}
	return root, &recorder{probe: &fakeProbe{}, answers: map[string]string{"ip": "1.0.0.0 via 198.51.100.1 dev eth0 src 192.0.2.116 uid 0\n    cache\n"}, fails: map[string]bool{}}
}

func TestLGWFirmware(t *testing.T) {
	ctx := context.Background()
	logf := func(string, ...any) {}
	// nothing configured: nothing run
	root, rec := lgwRoot(t, rfdTemplate, "")
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err != nil || len(rec.calls) != 0 {
		t.Fatalf("no gateway: %v %v", err, rec.calls)
	}
	// an RF and a wired gateway: the network first, then the three updates as S58 ran them
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "[Interface 0]\nType = HMWLGW\nSerial Number = JEQ0000001\n")
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"ip -4 route get 1",
		"ping -q -W 5 -c 1 198.51.100.1",
		"eq3configcmd update-coprocessor -lgw -u -rfdconf /etc/config/rfd.conf -l 1",
		"eq3configcmd update-lgw-firmware -m /firmware/fwmap -c /etc/config/rfd.conf -l 1",
		"eq3configcmd update-lgw-firmware -m /firmware/fwmap -c /etc/config/hs485d.conf -l 1",
	}
	if !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("calls:\n%v", strings.Join(rec.calls, "\n"))
	}
	// a failed update fails the step; no network fails it before any update
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "")
	rec.fails["eq3configcmd"] = true
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err == nil || !strings.Contains(err.Error(), "RF LAN gateways' coprocessor") {
		t.Fatalf("failure: %v", err)
	}
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "")
	rec.fails["ping"] = true
	sleeps := 0
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run, Sleep: func(time.Duration) { sleeps++ }}, logf); err == nil || sleeps != 5 || rec.called("eq3configcmd") {
		t.Fatalf("no network: %v sleeps %d", err, sleeps)
	}
	// LAN-gateway mode: skipped
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "")
	_ = os.WriteFile(filepath.Join(root, "var/hm_mode"), []byte("HM_MODE='HM-LGW'\n"), 0o644)
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err != nil || len(rec.calls) != 0 {
		t.Fatalf("HM-LGW mode: %v %v", err, rec.calls)
	}
}

func TestLGWKeys(t *testing.T) {
	ctx := context.Background()
	logf := func(string, ...any) {}
	root, rec := lgwRoot(t, rfdTemplate+lgwSection, "")
	w := func(name, body string) {
		_ = os.WriteFile(filepath.Join(root, "etc/config", name), []byte(body), 0o600)
	}
	// as occulited's QueueGatewayKeyChange writes it: KEY and CURKEY both there
	w("KEQ0987654.keychange", "Class=RF\nSerial=KEQ0987654\nIP=192.168.0.51\nKEY=newkey\nCURKEY=oldkey\n")
	w("JEQ0000001.keychange", "Class=Wired\nSerial=JEQ0000001\nIP=\nKEY=w2\nCURKEY=w1\n")
	w("XEQ0000002.keychange", "Class=Other\nSerial=XEQ0000002\nKEY=a\nCURKEY=b\n")
	err := LGWKeys(ctx, LGWStep{Root: root, Run: rec.run}, logf)
	if err == nil || !strings.Contains(err.Error(), "XEQ0000002.keychange") {
		t.Fatalf("the unknown class is reported: %v", err)
	}
	want := []string{
		"eq3configcmd setlgwkey -s JEQ0000001 -c w1 -n w2 -f /etc/config/hs485d.conf -l 1",
		"eq3configcmd setlgwkey -s KEQ0987654 -h 192.168.0.51 -c oldkey -n newkey -f /etc/config/rfd.conf -l 1",
	}
	if !reflect.DeepEqual(rec.calls, want) {
		t.Fatalf("calls:\n%v", strings.Join(rec.calls, "\n"))
	}
	left, _ := filepath.Glob(filepath.Join(root, "etc/config/*.keychange"))
	if len(left) != 1 || filepath.Base(left[0]) != "XEQ0000002.keychange" {
		t.Fatalf("left: %v", left)
	}
	// a failed change keeps its file for the next boot
	w("KEQ0987654.keychange", "Class=RF\nSerial=KEQ0987654\nKEY=n\nCURKEY=o\n")
	_ = os.Remove(left[0])
	rec.fails["eq3configcmd"] = true
	if err := LGWKeys(ctx, LGWStep{Root: root, Run: rec.run}, logf); err == nil || !exists(filepath.Join(root, "etc/config/KEQ0987654.keychange")) {
		t.Fatalf("failed change: %v", err)
	}
}
