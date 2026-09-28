package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// B-5: `occulited version` (the flag is --version) started the daemon - the flag package stops at
// the first word and run() ignored what was left. As root on a running system that second daemon
// rewrote state files as root before it died on the bound port. Now a word that is not a command is
// refused with the usage line and exit 2, and so is a word left over after the daemon's flags.

// subcommands are the words and flag-spelled modes main dispatches on, in the usage line's order.
var subcommands = []string{
	"helper", "passwd", "token", "auth", "webauthn", "radio", "wifi", "firewall", "syslog-forward",
	addonOwnFlag, lighttpdDropinsFlag, backupFlag,
	// the helper's own child (openccu-lite B-201), not in the usage line: nobody else runs it
	"rpc-drop",
}

const usageLine = "usage: occulited [--config FILE] [--state-dir DIR] [--listen ADDR] [--log auto|journal|text] [--root DIR] [--version]\n" +
	"       occulited helper|passwd|token|auth|webauthn|radio|wifi|firewall|syslog-forward ...\n" +
	"       occulited -addon-own|-lighttpd-dropins|-backup ..."

// errUsage is an unknown command or a stray word: main prints the usage line and exits 2.
var errUsage = errors.New("usage")

// pickCommand names the mode the arguments ask for: "" for the daemon (no arguments, or a flag
// first), a subcommand, or errUsage for a first word that is none of them.
func pickCommand(args []string) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	for _, c := range subcommands {
		if args[0] == c {
			return c, nil
		}
	}
	if strings.HasPrefix(args[0], "-") {
		// a flag-spelled mode is matched above; any other flag is the daemon's (and an unknown
		// one is refused by its flag set)
		return "", nil
	}
	return "", fmt.Errorf("%w: unknown command %q", errUsage, args[0])
}

// daemonOptions are the daemon's flags.
type daemonOptions struct {
	config, listen, stateDir, log, root, sessionDir, helperSocket string
	version                                                       bool
}

// parseDaemonArgs parses the daemon's flags; a word left over is errUsage, flag.ErrHelp is -h.
func parseDaemonArgs(args []string, out io.Writer) (daemonOptions, error) {
	var o daemonOptions
	fs := flag.NewFlagSet("occulited", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.config, "config", "/usr/local/etc/occulite/occulited.json", "configuration file")
	fs.StringVar(&o.listen, "listen", "", "override the listen address (loopback only)")
	fs.StringVar(&o.stateDir, "state-dir", "", "override the state directory")
	fs.StringVar(&o.log, "log", "auto", "where to log: auto (the journal when stderr is on it, else text), journal, text")
	fs.BoolVar(&o.version, "version", false, "print the version and exit")
	fs.StringVar(&o.root, "root", "/", "filesystem root the system module reads (a fake tree for development)")
	fs.StringVar(&o.sessionDir, "session-dir", "/var/run/occulite/sessions", "tmpfs directory mirroring live sessions for the lighttpd gate")
	fs.StringVar(&o.helperSocket, "helper-socket", DefaultHelperSocket, "the privilege helper's socket, used when not running as root")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, err
		}
		return o, fmt.Errorf("%w: %v", errUsage, err)
	}
	if fs.NArg() > 0 {
		return o, fmt.Errorf("%w: unexpected argument %q", errUsage, fs.Arg(0))
	}
	return o, nil
}

// helperUnits are where the fork installs the helper's unit: where one exists, occulited's own unit
// runs the daemon as the occulite user, and a daemon started as root is a mistake.
var helperUnits = []string{
	"usr/lib/systemd/system/occulited-helper.service",
	"lib/systemd/system/occulited-helper.service",
	"etc/systemd/system/occulited-helper.service",
}

// refuseRootDaemon is the second half of B-5: as root on a system with the helper setup, the daemon
// is not started at all - the helper's socket or its unit is that setup. Without it (the eQ-3 CCU3
// firmware, a development tree) root stays allowed.
func refuseRootDaemon(isRoot bool, fsRoot, helperSocket string) error {
	if !isRoot {
		return nil
	}
	found := ""
	if _, err := os.Stat(helperSocket); err == nil {
		found = helperSocket
	}
	for _, u := range helperUnits {
		if found != "" {
			break
		}
		if _, err := os.Stat(filepath.Join(fsRoot, u)); err == nil {
			found = "/" + u
		}
	}
	if found == "" {
		return nil
	}
	return fmt.Errorf("not starting the daemon as root: this system runs it as the occulite user with the privilege helper (%s); a root daemon would leave state files root's. Use `systemctl restart occulited`, or `occulited --version` for the version", found)
}
