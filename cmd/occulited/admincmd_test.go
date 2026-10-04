package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// occulited task 14: the console's `occulited admin list` and `occulited admin reset-auth <user>` -
// root only, the usage for every wrong shape, the one-time password on stdout, the journal line.

// recordHandler keeps the lines a command writes to the journal.
type recordHandler struct{ lines *[]slog.Record }

func (h recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordHandler) Handle(_ context.Context, r slog.Record) error {
	*h.lines = append(*h.lines, r)
	return nil
}
func (h recordHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordHandler) WithGroup(string) slog.Handler      { return h }

func TestAdminCmd(t *testing.T) {
	dir := t.TempDir()
	root := true
	var journal []slog.Record
	oldRoot, oldJournal := adminIsRoot, adminJournal
	adminIsRoot = func() bool { return root }
	adminJournal = func() slog.Handler { return recordHandler{&journal} }
	t.Cleanup(func() { adminIsRoot, adminJournal = oldRoot, oldJournal })
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	run := func(args ...string) (string, string, error) {
		var out, errOut bytes.Buffer
		err := adminRun(append(args, "--state-dir", dir), &out, &errOut, now)
		return out.String(), errOut.String(), err
	}

	for _, args := range [][]string{{}, {"list", "x"}, {"reset-auth"}, {"reset-auth", "a", "b"}, {"frobnicate"}} {
		if _, _, err := run(args...); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("%v: %v, want the usage", args, err)
		}
	}
	// root only - also for the list
	root = false
	if _, _, err := run("list"); err == nil || !strings.Contains(err.Error(), "only root") {
		t.Errorf("list as another user: %v", err)
	}
	if _, _, err := run("reset-auth", "admin"); err == nil || !strings.Contains(err.Error(), "only root") {
		t.Errorf("reset as another user: %v", err)
	}
	root = true
	if _, errOut, err := run("list"); err != nil || !strings.Contains(errOut, "no accounts") {
		t.Errorf("an empty list: %v %q", err, errOut)
	}
	if err := auth.ConsoleSetPassword(dir, "admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run("list")
	if err != nil || !strings.Contains(out, "USER") || !strings.Contains(out, "admin") || !strings.Contains(out, "administer") {
		t.Errorf("list: %v %q", err, out)
	}
	if _, _, err := run("reset-auth", "nobody"); err != auth.ErrUnknownUser {
		t.Errorf("reset an unknown account: %v", err)
	}
	if len(journal) != 0 {
		t.Errorf("a refused reset reached the journal: %v", journal)
	}
	out, errOut, err := run("reset-auth", "admin")
	pw := strings.TrimSpace(out)
	if err != nil || len(pw) != 23 || !strings.Contains(errOut, "access of admin reset") {
		t.Fatalf("reset: %v %q %q", err, out, errOut)
	}
	// the one-time password signs in, must be changed, and the list says so
	s, err := auth.Open(dir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Login("admin", pw, "r", "a"); err != nil || !s.MustChangePassword("admin") {
		t.Errorf("the one-time password: %v", err)
	}
	if out, _, _ := run("list"); !strings.Contains(out, "must change") {
		t.Errorf("list after the reset: %q", out)
	}
	// one journal line, Warn, with the account and never the password
	if len(journal) != 1 || journal[0].Level != slog.LevelWarn || journal[0].Message != "auth: access reset on the console" {
		t.Fatalf("journal: %v", journal)
	}
	var user string
	journal[0].Attrs(func(a slog.Attr) bool {
		if a.Key == "user" {
			user = a.Value.String()
		}
		if strings.Contains(a.Value.String(), pw) {
			t.Errorf("the password in the journal: %v", a)
		}
		return true
	})
	if user != "admin" {
		t.Errorf("journal user %q", user)
	}
	// the Status page's notice reads the reset from users.json
	if r := s.ConsoleResets(now.Add(-time.Hour)); len(r) != 1 || r[0].User != "admin" {
		t.Errorf("resets: %v", r)
	}
}
