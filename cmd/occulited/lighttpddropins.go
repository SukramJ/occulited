package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/system"
)

// lighttpdDropinsFlag is `occulited -lighttpd-dropins`: a flag, not a word, so an older occulited
// refuses it (exit 2) instead of starting as a daemon - the addonOwnFlag lesson.
const lighttpdDropinsFlag = "-lighttpd-dropins"

// lighttpdDropinsMain is `occulited -lighttpd-dropins` (B-120): the addons' lighttpd fragments made
// into validated root-owned copies under /usr/local/etc/config/lighttpd before lighttpd starts.
// The fork's lighttpd.service runs it as root in an ExecStartPre; the daemon runs the same sync
// through the helper after an install. It prints what it did and exits 0 even when a fragment
// was refused - that is the addon's fault, written beside its drop-in, not a failure of the
// start - and 1 only when the directory could not be read or written.
func lighttpdDropinsMain(args []string) int {
	fs := flag.NewFlagSet("occulited -lighttpd-dropins", flag.ContinueOnError)
	rootDir := fs.String("root", "/", "filesystem root (development)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	system.Priv = priv.Local{}
	changed, results, err := system.SyncLighttpdDropins(system.Root(*rootDir))
	for _, r := range results {
		if r.Reason != "" {
			fmt.Fprintf(os.Stdout, "%s: %s (%s)\n", r.ID, r.Action, r.Reason)
		} else {
			fmt.Fprintf(os.Stdout, "%s: %s\n", r.ID, r.Action)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "lighttpd-dropins:", err)
		return 1
	}
	if changed {
		fmt.Fprintln(os.Stdout, "changed")
	}
	return 0
}
