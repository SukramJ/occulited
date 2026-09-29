package radio

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// openccu-lite B-275: multimacd's level is held where its start lines are still logged.
func TestMultimacdLevel(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"1", "1"}, {"2", "2"}, {"3", "2"}, {"5", "2"}, {"7", "2"}, {"x", "x"}, {"", ""},
	} {
		if got := MultimacdLevel(c.in); got != c.want {
			t.Errorf("MultimacdLevel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// the render (task 297): multimacd's own level only, never rfd's - none, 1, 2 and a stale 5
	for _, c := range []struct {
		syslog map[string]string
		want   string
	}{
		{map[string]string{}, "2"},
		{map[string]string{"LOGLEVEL_RFD": "1"}, "2"},
		{map[string]string{"LOGLEVEL_RFD": "5"}, "2"},
		{map[string]string{"LOGLEVEL_MULTIMACD": "1", "LOGLEVEL_RFD": "5"}, "1"},
		{map[string]string{"LOGLEVEL_MULTIMACD": "2", "LOGLEVEL_RFD": "1"}, "2"},
		{map[string]string{"LOGLEVEL_MULTIMACD": "5", "LOGLEVEL_RFD": "1"}, "2"},
		{map[string]string{"LOGLEVEL_MULTIMACD": "0"}, "2"},
		{map[string]string{"LOGLEVEL_MULTIMACD": "debug"}, "2"},
	} {
		if l := logLevels(c.syslog); l.Multimacd != c.want {
			t.Errorf("%v: multimacd %q, want %q", c.syslog, l.Multimacd, c.want)
		}
	}
	// the Log page's write goes through the same function
	if f := LogLevelEnvFiles("5", "5", "5"); f["multimacd"] != "MULTIMACD_LOGLEVEL=2\n" || f["rfd"] != "LOGLEVEL_RFD=5\n" {
		t.Fatalf("env files: %v", f)
	}
}

// openccu-lite B-275: the ready step reads multimacd's own lines - the version line makes it
// ready, a failure line fails the start (systemd restarts the unit), neither leaves the endpoint
// check as it was.
func TestReadyMultimacdReadsTheVersionLine(t *testing.T) {
	fakeUsers(t)
	root, rec := boxRoot(t, map[string]string{"raw-uart": "GPIO@3f201000.serial"})
	d := Detector{Root: root, Run: rec.run, Sleep: func(time.Duration) {}}
	if _, err := Run(context.Background(), root, d, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	old := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = old })
	now := time.Now()
	d.Now = func() time.Time { now = now.Add(500 * time.Millisecond); return now }
	for _, rel := range []string{"var/status/multimacd.status", "dev/mmd_bidcos", "dev/mmd_hmip"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("4242\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name, journal, wantErr, wantLog string
	}{
		{"version", "Copro application running.\nVapp=040412 Vbl=01000C Vhmos=014000\nSGTIN=30 14 F7 11 A0 61 A7 00 00 00 0A 06\n", "", "module answered (Vapp=040412 Vbl=01000C Vhmos=014000)"},
		{"failed", "Copro application running.\nGetVersion finally failed.\nSGTIN=30 14 F7 11 A0 61 A7 00 00 00 0A 06\nSerial Number=OEQ9000007\n", "GetVersion finally failed.", ""},
		{"none found", "No Coprocessor detected!!! \n", "No Coprocessor detected", ""},
		{"quiet", "", "", "no version line from multimacd within 5s"},
		{"other words", "FastMacResponder::ThreadFunction() started Id=5240\n", "", "the module not checked"},
	} {
		rec.answers["journalctl"] = c.journal
		rec.calls = nil
		var lines []string
		logf := func(f string, a ...any) { lines = append(lines, strings.TrimSpace(fmt.Sprintf(f, a...))) }
		err := Ready(context.Background(), d, "multimacd", 4242, logf)
		switch {
		case c.wantErr == "" && err != nil:
			t.Errorf("%s: ready failed: %v", c.name, err)
		case c.wantErr != "" && (err == nil || !strings.Contains(err.Error(), c.wantErr)):
			t.Errorf("%s: want an error with %q, got %v", c.name, c.wantErr, err)
		case c.wantLog != "" && !strings.Contains(strings.Join(lines, "\n"), c.wantLog):
			t.Errorf("%s: want a log line with %q, got %q", c.name, c.wantLog, lines)
		}
		if !rec.called("journalctl _PID=4242") {
			t.Errorf("%s: the main process's output was not read: %v", c.name, rec.calls)
		}
	}
	// the verdict on the lines alone
	if v, l := multimacdStartLine("x\nVapp=1 Vbl=2 Vhmos=3\nGetVersion finally failed.\n"); v != multimacdStartVersion || l != "Vapp=1 Vbl=2 Vhmos=3" {
		t.Fatalf("first line decides: %v %q", v, l)
	}
	if v, _ := multimacdStartLine("GetVersion failed. Retrying.\n"); v != multimacdStartUnknown {
		t.Fatalf("a retry is not the verdict: %v", v)
	}
}
