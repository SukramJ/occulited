package radio

import (
	"context"
	"fmt"
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
	// a failed update fails the step
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "")
	rec.fails["eq3configcmd"] = true
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err == nil || !strings.Contains(err.Error(), "RF LAN gateways' coprocessor") {
		t.Fatalf("failure: %v", err)
	}
	// openccu-lite B-229: no network at all - the default gateway silent, or no default route - is
	// a skip with a journal line after five tries, not a failed unit, and no eq3configcmd runs
	for _, c := range []struct{ name, fail string }{{"silent default gateway", "ping"}, {"no default route", "ip"}} {
		root, rec = lgwRoot(t, rfdTemplate+lgwSection, "[Interface 0]\nType = HMWLGW\nSerial Number = JEQ0000001\n")
		rec.fails[c.fail] = true
		sleeps := 0
		var lines []string
		err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run, Sleep: func(time.Duration) { sleeps++ }}, func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) })
		if err != nil || sleeps != 5 || rec.called("eq3configcmd") || len(lines) != 1 || !strings.Contains(lines[0], "no network") || !strings.Contains(lines[0], "skipped until the next start") {
			t.Fatalf("%s: %v sleeps %d lines %q calls %v", c.name, err, sleeps, lines, rec.calls)
		}
	}
	// openccu-lite B-229: a gateway with an address that does not answer a ping is not an update
	// that failed - the check is skipped (two pings, no eq3configcmd, the unit a success), as the
	// QEMU test's boot 2 seeds one at a documentation address
	addressed := rfdTemplate + "\n[Interface 1]\nType = HMLGW2\nSerial Number = KEQ0123456\nEncryption Key = 00000000000000000000000000000000\nIP Address = 192.0.2.1\n"
	root, rec = lgwRoot(t, addressed, "[Interface 0]\nType = HMWLGW\nSerial Number = JEQ0000001\nIP Address = 192.0.2.2\n")
	rec.fails["ping -q -W 2 -c 1 192.0.2.1"] = true
	rec.fails["ping -q -W 2 -c 1 192.0.2.2"] = true
	var lines []string
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }); err != nil || rec.called("eq3configcmd") {
		t.Fatalf("silent gateways: %v %v", err, rec.calls)
	}
	want = []string{"ip -4 route get 1", "ping -q -W 5 -c 1 198.51.100.1", "ping -q -W 2 -c 1 192.0.2.1", "ping -q -W 2 -c 1 192.0.2.1", "ping -q -W 2 -c 1 192.0.2.2", "ping -q -W 2 -c 1 192.0.2.2"}
	if !reflect.DeepEqual(rec.calls, want) || len(lines) != 2 || !strings.Contains(lines[0], "no RF LAN gateway answers (192.0.2.1)") || !strings.Contains(lines[1], "no wired LAN gateway answers (192.0.2.2)") {
		t.Fatalf("silent gateways:\n%v\n%v", strings.Join(rec.calls, "\n"), lines)
	}
	// one of two RF gateways answers: the RF steps run; the wired one stays silent and is skipped
	two := addressed + "\n[Interface 2]\nType = Lan Interface\nSerial Number = LEQ0000001\nIP Address = 192.0.2.3\n"
	root, rec = lgwRoot(t, two, "[Interface 0]\nType = HMWLGW\nSerial Number = JEQ0000001\nIP Address = 192.0.2.2\n")
	rec.fails["ping -q -W 2 -c 1 192.0.2.1"] = true
	rec.fails["ping -q -W 2 -c 1 192.0.2.2"] = true
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err != nil || !rec.called("eq3configcmd update-coprocessor") || !rec.called("eq3configcmd update-lgw-firmware -m /firmware/fwmap -c /etc/config/rfd.conf") || rec.called("eq3configcmd update-lgw-firmware -m /firmware/fwmap -c /etc/config/hs485d.conf") {
		t.Fatalf("one answers: %v\n%v", err, strings.Join(rec.calls, "\n"))
	}
	// a gateway without an address (found by its serial) is not pinged and the steps run
	root, rec = lgwRoot(t, rfdTemplate+lgwSection, "")
	if err := LGWFirmware(ctx, LGWStep{Root: root, Run: rec.run}, logf); err != nil || rec.called("ping -q -W 2") || !rec.called("eq3configcmd") {
		t.Fatalf("unaddressed: %v\n%v", err, strings.Join(rec.calls, "\n"))
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
