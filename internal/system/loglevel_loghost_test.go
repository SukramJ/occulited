package system

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-96: LOGHOST is what occu-syslog-forward sends to - a host with an optional port, or an IPv6
// address - and a change of it restarts the forwarder alone: hmipserver logs to the journal.
func TestLogHostValues(t *testing.T) {
	for _, ok := range []string{"log.example.net", "192.0.2.7", "192.0.2.7:5514", "log:1", "[fd00::1]", "[fd00::1]:514", "fd00::1", "fe80::1:2"} {
		if !validLogHost(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"a b", "host:0", "host:65536", "host:", "$(reboot)", "h;x", "[fd00::1", "fd00::zz", "[x]:514", `"q"`, "host:514:1"} {
		if validLogHost(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestLogHostRestartsOnlyTheForwarder(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/config"), 0o755)
	r := Root(root)
	set, _, err := r.SetLogLevels(LogLevels{RFD: 5, HS485D: 5, HmIP: "WARN"})
	if err != nil {
		t.Fatal(err)
	}
	set.LogHost = "[fd00::7]:5514"
	got, restart, err := r.SetLogLevels(set)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(restart, " ") != "occu-syslog-forward" {
		t.Errorf("restart %v", restart)
	}
	if got.LogHost != "[fd00::7]:5514" {
		t.Errorf("read back %q", got.LogHost)
	}
	if _, _, err := r.SetLogLevels(LogLevels{RFD: 5, HS485D: 5, HmIP: "WARN", LogHost: "log host"}); err == nil || !strings.Contains(err.Error(), ":port") {
		t.Errorf("a bad LOGHOST: %v", err)
	}
}
