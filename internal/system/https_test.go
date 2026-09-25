package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHTTPSSettings: the markers are the settings - none, both, a hand-edited HSTS marker.
func TestHTTPSSettings(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays}) {
		t.Fatalf("empty tree: %+v", got)
	}
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, HTTPSRedirectMarker), nil, 0o644)
	_ = os.WriteFile(filepath.Join(dir, HSTSMarker), []byte("15552000\n"), 0o644)
	if got := root.HTTPSSettings(); got != (HTTPSSettings{RedirectHTTPS: true, HSTS: true, HSTSMaxAgeDays: 180}) {
		t.Fatalf("both markers: %+v", got)
	}
	// a marker without a number reads as on with the default, as S50lighttpd treats it
	_ = os.WriteFile(filepath.Join(dir, HSTSMarker), []byte("yes\n"), 0o644)
	if got := root.HTTPSSettings(); !got.HSTS || got.HSTSMaxAgeDays != DefaultHSTSMaxAgeDays {
		t.Fatalf("hand-written marker: %+v", got)
	}
	// less than a day rounds to the default, never to zero
	_ = os.WriteFile(filepath.Join(dir, HSTSMarker), []byte("3600"), 0o644)
	if got := root.HTTPSSettings(); got.HSTSMaxAgeDays != DefaultHSTSMaxAgeDays {
		t.Fatalf("an hour: %+v", got)
	}
}

// TestHSTSDefaultIsAWeek: D-64 - seven days when HSTS is switched on without a value.
func TestHSTSDefaultIsAWeek(t *testing.T) {
	if DefaultHSTSMaxAgeDays != 7 || MaxHSTSClearingDays != 30 {
		t.Fatalf("default %d days, clearing at most %d days", DefaultHSTSMaxAgeDays, MaxHSTSClearingDays)
	}
}

// TestHTTPSSettingsClearing: the marker forms of task 96 - a 0 with the deadline beside it, a 0
// without a readable deadline (written by hand), and a deadline file beside a real max-age, which
// is not read.
func TestHTTPSSettingsClearing(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	marker, until := filepath.Join(dir, HSTSMarker), filepath.Join(dir, HSTSClearUntilFile)
	_ = os.WriteFile(marker, []byte("0\n"), 0o644)
	_ = os.WriteFile(until, []byte("1789000000\n"), 0o644)
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true, HSTSClearUntil: 1789000000}) {
		t.Fatalf("clearing with a deadline: %+v", got)
	}
	for _, content := range []string{"soon", "0", "-5", ""} {
		_ = os.WriteFile(until, []byte(content), 0o644)
		if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true}) {
			t.Fatalf("deadline %q: %+v", content, got)
		}
	}
	_ = os.Remove(until)
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true}) {
		t.Fatalf("clearing without a deadline file: %+v", got)
	}
	_ = os.WriteFile(marker, []byte("00"), 0o644)
	if got := root.HTTPSSettings(); !got.HSTSClearing || got.HSTS {
		t.Fatalf("00: %+v", got)
	}
	_ = os.WriteFile(marker, []byte("604800\n"), 0o644)
	_ = os.WriteFile(until, []byte("1789000000\n"), 0o644)
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTS: true, HSTSMaxAgeDays: 7}) {
		t.Fatalf("a deadline beside a max-age: %+v", got)
	}
}

func TestHTTPSSettingsValidate(t *testing.T) {
	for _, tc := range []struct {
		s  HTTPSSettings
		ok bool
	}{
		{HTTPSSettings{}, true},
		{HTTPSSettings{RedirectHTTPS: true}, true},
		{HTTPSSettings{HSTS: true, HSTSMaxAgeDays: 1}, true},
		{HTTPSSettings{HSTS: true, HSTSMaxAgeDays: MaxHSTSMaxAgeDays}, true},
		{HTTPSSettings{HSTS: true, HSTSMaxAgeDays: 0}, false},
		{HTTPSSettings{HSTS: true, HSTSMaxAgeDays: -5}, false},
		{HTTPSSettings{HSTS: true, HSTSMaxAgeDays: MaxHSTSMaxAgeDays + 1}, false},
		{HTTPSSettings{HSTS: false, HSTSMaxAgeDays: 99999}, true}, // not written while off
	} {
		err := tc.s.Validate()
		if (err == nil) != tc.ok || (err != nil && !errors.Is(err, ErrHTTPSInvalid)) {
			t.Errorf("%+v: %v", tc.s, err)
		}
	}
}

// TestHTTPSConfigSet: the markers are written and removed through Priv, the HSTS one carrying
// the max-age in seconds; switched off it clears (a 0 and the deadline, task 96); one reload per
// change, none when nothing changed; systemd and the init script; an invalid value writes nothing.
func TestHTTPSConfigSet(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	var lines []string
	log := func(l string) { lines = append(lines, l) }
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	h := HTTPSConfig{Root: root, Run: rec, Systemd: true, Now: clock}
	ctx := context.Background()
	marker, untilFile := filepath.Join(dir, HSTSMarker), filepath.Join(dir, HSTSClearUntilFile)

	got, err := h.Set(ctx, HTTPSSettings{RedirectHTTPS: true, HSTS: true, HSTSMaxAgeDays: 30}, log)
	if err != nil || got != (HTTPSSettings{RedirectHTTPS: true, HSTS: true, HSTSMaxAgeDays: 30}) {
		t.Fatalf("%v %+v", err, got)
	}
	if _, err := os.Stat(filepath.Join(dir, HTTPSRedirectMarker)); err != nil {
		t.Fatalf("redirect marker: %v", err)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "2592000\n" {
		t.Fatalf("hsts marker: %v %q", err, b)
	}
	if len(cmds) != 1 || cmds[0] != "systemctl reload --no-pager -- lighttpd.service" {
		t.Fatalf("%v", cmds)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "redirect on, hsts on (max-age 30 days)") || !strings.Contains(j, "lighttpd reloaded") {
		t.Fatalf("%v", lines)
	}
	// the same again: no write, no reload
	cmds = nil
	if _, err := h.Set(ctx, HTTPSSettings{RedirectHTTPS: true, HSTS: true, HSTSMaxAgeDays: 30}, log); err != nil || len(cmds) != 0 {
		t.Fatalf("unchanged: %v %v", err, cmds)
	}
	// HSTS off keeps the redirect and clears: the marker holds 0, the deadline is the previous
	// max-age from now, the days field is the default again
	lines = nil
	until := now.Add(30 * 24 * time.Hour).Unix()
	got, err = h.Set(ctx, HTTPSSettings{RedirectHTTPS: true, HSTS: false, HSTSMaxAgeDays: 30}, log)
	if err != nil || got != (HTTPSSettings{RedirectHTTPS: true, HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true, HSTSClearUntil: until}) || len(cmds) != 1 {
		t.Fatalf("%v %+v %v", err, got, cmds)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "0\n" {
		t.Fatalf("clearing marker: %v %q", err, b)
	}
	if b, err := os.ReadFile(untilFile); err != nil || string(b) != strconv.FormatInt(until, 10)+"\n" {
		t.Fatalf("deadline: %v %q", err, b)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "hsts off, max-age=0 until 2026-10-12T20:00:00Z") {
		t.Fatalf("%v", lines)
	}
	// off again while it clears: the deadline stays, nothing is written, no reload - also later
	cmds = nil
	now = now.Add(24 * time.Hour)
	if got, err := h.Set(ctx, HTTPSSettings{RedirectHTTPS: true}, log); err != nil || got.HSTSClearUntil != until || len(cmds) != 0 {
		t.Fatalf("off again: %v %+v %v", err, got, cmds)
	}
	// an invalid max-age changes nothing and names the rule
	if got, err := h.Set(ctx, HTTPSSettings{HSTS: true, HSTSMaxAgeDays: 0}, log); !errors.Is(err, ErrHTTPSInvalid) || !got.RedirectHTTPS || !got.HSTSClearing || len(cmds) != 0 {
		t.Fatalf("%v %+v %v", err, got, cmds)
	}
	// the redirect off as well: its marker goes, the clearing runs on; the busybox path reloads the
	// init script
	bb := HTTPSConfig{Root: root, Run: rec, Systemd: false, Now: clock}
	got, err = bb.Set(ctx, HTTPSSettings{}, log)
	if err != nil || got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true, HSTSClearUntil: until}) || len(cmds) != 1 || !strings.HasSuffix(cmds[0], "/etc/init.d/S50lighttpd reload") {
		t.Fatalf("%v %+v %v", err, got, cmds)
	}
	if _, err := os.Stat(filepath.Join(dir, HTTPSRedirectMarker)); err == nil {
		t.Fatal("the redirect marker is still there")
	}
	// on again ends the clearing: the seconds, no deadline file
	cmds = nil
	got, err = h.Set(ctx, HTTPSSettings{HSTS: true, HSTSMaxAgeDays: DefaultHSTSMaxAgeDays}, log)
	if err != nil || got != (HTTPSSettings{HSTS: true, HSTSMaxAgeDays: 7}) || len(cmds) != 1 {
		t.Fatalf("on again: %v %+v %v", err, got, cmds)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "604800\n" {
		t.Fatalf("a week: %v %q", err, b)
	}
	if _, err := os.Stat(untilFile); err == nil {
		t.Fatal("the deadline file is still there")
	}
	// the clearing lasts the previous max-age, at least a day and at most 30 days
	for _, tc := range []struct{ days, want int }{{1, 1}, {7, 7}, {30, 30}, {365, 30}, {730, 30}} {
		if _, err := h.Set(ctx, HTTPSSettings{HSTS: true, HSTSMaxAgeDays: tc.days}, log); err != nil {
			t.Fatal(err)
		}
		got, err := h.Set(ctx, HTTPSSettings{}, log)
		if want := now.Add(time.Duration(tc.want) * 24 * time.Hour).Unix(); err != nil || got.HSTSClearUntil != want {
			t.Fatalf("%d days: %v %+v, want until %d", tc.days, err, got, want)
		}
	}
	// a failed reload is the error, the markers stay as written
	fail := HTTPSConfig{Root: root, Run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("no such unit"), errors.New("exit 1")
	}, Systemd: true}
	if _, err := fail.Set(ctx, HTTPSSettings{RedirectHTTPS: true}, log); err == nil || !strings.Contains(err.Error(), "lighttpd reload") || !strings.Contains(err.Error(), "no such unit") {
		t.Fatalf("%v", err)
	}
	if !root.HTTPSSettings().RedirectHTTPS {
		t.Fatal("the marker was not written before the failed reload")
	}
}

// TestHTTPSSetOffWithoutHSTS: switching off what was never on writes no clearing.
func TestHTTPSSetOffWithoutHSTS(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name)
		return nil, nil
	}
	h := HTTPSConfig{Root: root, Run: rec, Systemd: true}
	got, err := h.Set(context.Background(), HTTPSSettings{RedirectHTTPS: true}, nil)
	if err != nil || got != (HTTPSSettings{RedirectHTTPS: true, HSTSMaxAgeDays: DefaultHSTSMaxAgeDays}) || len(cmds) != 1 {
		t.Fatalf("%v %+v %v", err, got, cmds)
	}
	for _, f := range []string{HSTSMarker, HSTSClearUntilFile} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("%s written", f)
		}
	}
}

// TestHSTSClearingExpires: a clearing ends at its deadline - both files go, one reload - and not
// before, nor without a deadline, nor while HSTS is on or off.
func TestHSTSClearingExpires(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name)
		return nil, nil
	}
	var lines []string
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	h := &HTTPSConfig{Root: root, Run: rec, Systemd: true, Now: func() time.Time { return now }}
	ctx := context.Background()
	log := func(l string) { lines = append(lines, l) }
	marker, untilFile := filepath.Join(dir, HSTSMarker), filepath.Join(dir, HSTSClearUntilFile)
	expire := func(label string, want bool) {
		t.Helper()
		cmds = nil
		done, err := h.ExpireHSTSClearing(ctx, log)
		if err != nil || done != want || (len(cmds) == 1) != want {
			t.Fatalf("%s: %v %v %v", label, done, err, cmds)
		}
	}
	expire("off", false)
	_ = os.WriteFile(marker, []byte("604800\n"), 0o644)
	_ = os.WriteFile(untilFile, []byte("1\n"), 0o644)
	expire("on, with a stale deadline file", false)
	_ = os.WriteFile(marker, []byte("0\n"), 0o644)
	_ = os.Remove(untilFile)
	expire("clearing without a deadline", false)
	_ = os.WriteFile(untilFile, []byte(strconv.FormatInt(now.Add(time.Hour).Unix(), 10)+"\n"), 0o644)
	expire("an hour before the deadline", false)
	now = now.Add(time.Hour)
	expire("at the deadline", true)
	for _, f := range []string{marker, untilFile} {
		if _, err := os.Stat(f); err == nil {
			t.Fatalf("%s still there", f)
		}
	}
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays}) {
		t.Fatalf("after: %+v", got)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "the HSTS clearing ended") {
		t.Fatalf("%v", lines)
	}
	expire("again", false)
	// a failed reload is the error; the files are gone already, as S50lighttpd would decide anyway
	_ = os.WriteFile(marker, []byte("0\n"), 0o644)
	_ = os.WriteFile(untilFile, []byte("1\n"), 0o644)
	h.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("exit 1") }
	if done, err := h.ExpireHSTSClearing(ctx, log); !done || err == nil {
		t.Fatalf("failed reload: %v %v", done, err)
	}
}

// TestClearHSTS: the switch before the way back is staged - on becomes clearing with one reload;
// a clearing that runs keeps its deadline, and off stays off, both without a reload.
func TestClearHSTS(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name)
		return nil, nil
	}
	var lines []string
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	h := &HTTPSConfig{Root: root, Run: rec, Systemd: true, Now: func() time.Time { return now }}
	ctx := context.Background()
	log := func(l string) { lines = append(lines, l) }
	if cleared, until, err := h.ClearHSTS(ctx, log); cleared || until != 0 || err != nil || len(cmds) != 0 {
		t.Fatalf("off: %v %d %v %v", cleared, until, err, cmds)
	}
	_ = os.WriteFile(filepath.Join(dir, HSTSMarker), []byte("15552000\n"), 0o644)
	want := now.Add(30 * 24 * time.Hour).Unix()
	if cleared, until, err := h.ClearHSTS(ctx, log); !cleared || until != want || err != nil || len(cmds) != 1 {
		t.Fatalf("on: %v %d %v %v", cleared, until, err, cmds)
	}
	if got := root.HTTPSSettings(); got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, HSTSClearing: true, HSTSClearUntil: want}) {
		t.Fatalf("after: %+v", got)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "HSTS switched off: max-age=0 until 2026-10-12T20:00:00Z") {
		t.Fatalf("%v", lines)
	}
	now = now.Add(time.Hour)
	if cleared, until, err := h.ClearHSTS(ctx, log); cleared || until != want || err != nil || len(cmds) != 1 {
		t.Fatalf("clearing: %v %d %v %v", cleared, until, err, cmds)
	}
}

// TestCertRemoveClearsHSTS: the switch back to self-signed turns HSTS into the clearing state in
// the same reload and says so; without HSTS on nothing is said.
func TestCertRemoveClearsHSTS(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "etc/group"), []byte("root:x:0:\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, HSTSMarker), []byte("31536000\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, HTTPSRedirectMarker), nil, 0o644)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	var lines []string
	inst := CertInstaller{Root: root, Run: rec, Systemd: true}
	before := time.Now()
	if _, err := inst.Remove(context.Background(), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	got := root.HTTPSSettings()
	if got.HSTS || !got.HSTSClearing || !got.RedirectHTTPS {
		t.Fatalf("after remove: %+v", got)
	}
	// a year of max-age clears for 30 days
	if lo, hi := before.Add(30*24*time.Hour).Unix(), time.Now().Add(30*24*time.Hour).Unix(); got.HSTSClearUntil < lo || got.HSTSClearUntil > hi {
		t.Fatalf("deadline %d not in [%d, %d]", got.HSTSClearUntil, lo, hi)
	}
	if j := strings.Join(lines, "\n"); !strings.Contains(j, "HSTS switched off (max-age=0 until") {
		t.Fatalf("%v", lines)
	}
	if len(cmds) != 1 {
		t.Fatalf("one reload: %v", cmds)
	}
	lines = nil
	if _, err := inst.Remove(context.Background(), func(l string) { lines = append(lines, l) }); err != nil {
		t.Fatal(err)
	}
	if j := strings.Join(lines, "\n"); strings.Contains(j, "HSTS") {
		t.Fatalf("said again: %v", lines)
	}
	if again := root.HTTPSSettings(); again.HSTSClearUntil != got.HSTSClearUntil {
		t.Fatalf("the deadline moved: %+v", again)
	}
}

// TestIsWayBack: which staged file switches HSTS into the clearing state (task 96) - anything that
// is not recognisably an openccu-lite release.
func TestIsWayBack(t *testing.T) {
	for name, want := range map[string]bool{
		"OpenCCU-3.89.8.20260719-ova.zip":                                true,
		"OpenCCU-3.89.8.20260719-generic-x86_64.zip":                     true,
		"OpenCCU-3.89.8.20260719-ccu3.tgz":                               true,
		"firmwareUpdateFile":                                             true,
		"my-renamed-file.zip":                                            true,
		"OpenCCU-3.83.6.20250901-lite.3-rpi4.zip":                        false,
		"openccu-lite-x86_64-ova-1.0.0.zip":                              false,
		"openccu-lite-aarch64-rpi4-1.0.0-alpha.0-snapshot.fdcdab853.zip": false,
		"openccu-lite-aarch64-rpi3-1.0.0-ccu3.tgz":                       false,
	} {
		if got := isWayBack(name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
