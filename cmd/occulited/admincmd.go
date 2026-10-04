package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/journallog"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// adminCmd is the console's way back in (occulited task 14): `occulited admin list` shows the
// accounts, `occulited admin reset-auth <user>` resets one - its passkeys removed, a one-time
// password set that must be changed at the next login, its sessions ended. Root only: whoever
// has root on the system (ssh, or keyboard and display) is its owner, as on a router or a NAS.
// Each reset leaves a line in the journal and, for a week, a notice on the Status page. A running
// occulited follows the changed users.json at the next request, as after `occulited passwd`.
func adminCmd(args []string) error {
	return adminRun(args, os.Stdout, os.Stderr, time.Now())
}

// adminIsRoot and adminJournal are the command's view of the system; the tests replace them.
var (
	adminIsRoot  = priv.IsRoot
	adminJournal = func() slog.Handler {
		// the journal's socket, never a terminal: the command prints for the operator itself
		return journallog.New(journallog.Options{Identifier: "occulited", Level: slog.LevelInfo, Fallback: io.Discard})
	}
)

const adminUsage = "usage: occulited admin list | reset-auth <user>   [--state-dir DIR]"

func adminRun(args []string, stdout, stderr io.Writer, now time.Time) error {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	stateDir := fs.String("state-dir", "/usr/local/etc/occulite", "state directory")
	var flags, words []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			words = append(words, args[i])
		}
	}
	if err := fs.Parse(flags); err != nil {
		return err
	}
	switch {
	case len(words) == 1 && words[0] == "list":
	case len(words) == 2 && words[0] == "reset-auth":
	default:
		return errors.New(adminUsage)
	}
	if !adminIsRoot() {
		return errors.New("only root may do this: run it as root on the system (ssh, or keyboard and display)")
	}
	if words[0] == "list" {
		accounts, err := auth.ConsoleAccounts(*stateDir)
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			fmt.Fprintln(stderr, "no accounts yet: the first one is made on the login page")
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "USER\tLEVEL\tPASSWORD\tPASSKEYS\tNOTE")
		for _, a := range accounts {
			pw := "set"
			switch {
			case !a.PasswordSet:
				pw = "none"
			case a.MustChangePW:
				pw = "must change"
			}
			keys := fmt.Sprint(a.WebAuthnKeys - a.WebAuthnUnusable)
			note := ""
			if a.WebAuthnUnusable > 0 {
				note = fmt.Sprintf("%d key(s) cannot sign in", a.WebAuthnUnusable)
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", a.Name, a.Level, pw, keys, note)
		}
		return tw.Flush()
	}
	name := words[1]
	pw, removed, err := auth.ConsoleResetAuth(*stateDir, name, now)
	if pw == "" {
		return err
	}
	slog.New(adminJournal()).LogAttrs(context.Background(), slog.LevelWarn, "auth: access reset on the console",
		slog.String("user", name), slog.Int("passkeys_removed", removed), slog.String("by", "occulited admin reset-auth"))
	fmt.Fprintf(stderr, "access of %s reset: %d passkey(s) removed, the account's sessions ended.\n", name, removed)
	fmt.Fprintln(stderr, "one-time password (it must be changed at the next login):")
	fmt.Fprintln(stdout, pw)
	fmt.Fprintln(stderr, "with password login switched off (only the identity provider), `occulited auth password-login on` lets it in")
	if err != nil {
		return fmt.Errorf("the password is set, but the account's stored sessions could not be ended: %w", err)
	}
	return nil
}
