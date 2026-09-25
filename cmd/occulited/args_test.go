package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-5: which mode the arguments ask for. A word that is no command, or a word left over after the
// daemon's flags, is errUsage - never the daemon.
func TestPickCommandAndDaemonArgs(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		command string // "" is the daemon
		usage   bool   // errUsage from pickCommand or parseDaemonArgs
		help    bool
		version bool
	}{
		{args: nil},
		{args: []string{"--version"}, version: true},
		{args: []string{"-version"}, version: true},
		{args: []string{"--config", "/tmp/x.json", "--state-dir", "/tmp/s"}},
		{args: []string{"-h"}, help: true},
		{args: []string{"version"}, usage: true},
		{args: []string{"status"}, usage: true},
		{args: []string{"Helper"}, usage: true},
		{args: []string{"--config", "/tmp/x.json", "version"}, usage: true},
		{args: []string{"--version", "now"}, usage: true},
		{args: []string{"--no-such-flag"}, usage: true},
		{args: []string{"-backupx", "create"}, usage: true},
		{args: []string{"helper", "--socket", "/tmp/h.sock"}, command: "helper"},
		{args: []string{"rpc-drop", "1234", "7"}, command: "rpc-drop"},
		{args: []string{"passwd", "admin"}, command: "passwd"},
		{args: []string{"token", "x"}, command: "token"},
		{args: []string{"auth", "password", "on"}, command: "auth"},
		{args: []string{"radio", "check"}, command: "radio"},
		{args: []string{"wifi", "up"}, command: "wifi"},
		{args: []string{"firewall", "load"}, command: "firewall"},
		{args: []string{"syslog-forward"}, command: "syslog-forward"},
		{args: []string{"-addon-own", "hmm"}, command: addonOwnFlag},
		{args: []string{"-lighttpd-dropins"}, command: lighttpdDropinsFlag},
		{args: []string{"-backup", "create", "nightly"}, command: backupFlag},
	} {
		name := strings.Join(tc.args, " ")
		cmd, err := pickCommand(tc.args)
		if err != nil {
			if !tc.usage || !errors.Is(err, errUsage) {
				t.Errorf("%q: pickCommand %v", name, err)
			}
			continue
		}
		if cmd != tc.command {
			t.Errorf("%q: command %q, want %q", name, cmd, tc.command)
			continue
		}
		if cmd != "" {
			continue
		}
		opts, err := parseDaemonArgs(tc.args, io.Discard)
		switch {
		case tc.help:
			if !errors.Is(err, flag.ErrHelp) {
				t.Errorf("%q: %v, want the help", name, err)
			}
		case tc.usage:
			if !errors.Is(err, errUsage) {
				t.Errorf("%q: %v, want errUsage", name, err)
			}
		case err != nil:
			t.Errorf("%q: %v", name, err)
		case opts.version != tc.version:
			t.Errorf("%q: version %v", name, opts.version)
		}
	}
	opts, err := parseDaemonArgs([]string{"--config", "/tmp/x.json", "--root", "/tmp/r"}, io.Discard)
	if err != nil || opts.config != "/tmp/x.json" || opts.root != "/tmp/r" || opts.helperSocket != DefaultHelperSocket || opts.log != "auto" {
		t.Errorf("options %+v %v", opts, err)
	}
}

// B-5: root is refused where the helper setup is (its socket or its unit), allowed without it.
func TestRefuseRootDaemon(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "helper.sock")
	withUnit := t.TempDir()
	unit := filepath.Join(withUnit, "usr/lib/systemd/system/occulited-helper.service")
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	bare := t.TempDir()
	for _, tc := range []struct {
		name   string
		root   bool
		fsRoot string
		socket bool
		refuse bool
	}{
		{"not root, helper unit", false, withUnit, true, false},
		{"root, nothing", true, bare, false, false},
		{"root, the unit", true, withUnit, false, true},
		{"root, the socket", true, bare, true, true},
	} {
		os.Remove(socket)
		if tc.socket {
			if err := os.WriteFile(socket, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		err := refuseRootDaemon(tc.root, tc.fsRoot, socket)
		if (err != nil) != tc.refuse {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err != nil && !strings.Contains(err.Error(), "--version") {
			t.Errorf("%s: the message does not name --version: %v", tc.name, err)
		}
	}
}
