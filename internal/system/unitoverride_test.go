package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// dropInPriv records what would have been written under /run/systemd/system instead of writing it.
// Everything else falls through to Local: the tests below only ever need the three file ops.
type dropInPriv struct {
	priv.Local
	files map[string]string
	dirs  []string
}

func (p *dropInPriv) MkdirAll(path string, _ os.FileMode) error {
	p.dirs = append(p.dirs, path)
	return nil
}
func (p *dropInPriv) WriteFile(path string, data []byte, _ os.FileMode) error {
	p.files[path] = string(data)
	return nil
}
func (p *dropInPriv) Remove(path string) error {
	if _, ok := p.files[path]; !ok {
		return errors.New("remove: no such file or directory")
	}
	delete(p.files, path)
	return nil
}

func overrideFixture(t *testing.T) (SystemdServices, *dropInPriv, *[]string) {
	t.Helper()
	fp := &dropInPriv{files: map[string]string{}}
	old := Priv
	Priv = fp
	t.Cleanup(func() { Priv = old })
	var calls []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		if args[0] == "cat" {
			return []byte("# /usr/lib/systemd/system/rfd.service\n[Service]\nExecStart=/bin/rfd\n"), nil
		}
		return []byte("ok"), nil
	}
	s := SystemdServices{Run: run, SwitchFile: filepath.Join(t.TempDir(), "unit-switch.json")}
	return s, fp, &calls
}

// The override is stored on the userfs, applied into /run through the helper, and reloaded -
// and the shipped unit is never touched. Clearing it removes both and reloads again.
func TestUnitOverrideRoundTrip(t *testing.T) {
	s, fp, calls := overrideFixture(t)
	ctx := context.Background()

	uo, err := s.SetUnitOverride(ctx, "rfd", "[Service]\nEnvironment=RFD_DEBUG=1\n")
	if err != nil {
		t.Fatal(err)
	}
	if uo.Unit != "rfd.service" || uo.Override != "[Service]\nEnvironment=RFD_DEBUG=1\n" {
		t.Errorf("%+v", uo)
	}
	if !strings.Contains(uo.Effective, "ExecStart=/bin/rfd") {
		t.Errorf("effective unit not shown: %q", uo.Effective)
	}
	// into /run, one file, the right name
	want := "/run/systemd/system/rfd.service.d/50-occulite.conf"
	if got := fp.files[want]; got != "[Service]\nEnvironment=RFD_DEBUG=1\n" {
		t.Errorf("drop-in in /run: %q (files: %v)", got, fp.files)
	}
	if uo.Path != want {
		t.Errorf("path reported as %q", uo.Path)
	}
	// on the userfs, so it survives a boot and a firmware update
	stored := filepath.Join(s.overrideDir(), "rfd.service.conf")
	if b, err := os.ReadFile(stored); err != nil || string(b) != "[Service]\nEnvironment=RFD_DEBUG=1\n" {
		t.Errorf("stored copy: %q %v", b, err)
	}
	// and systemd was told
	if !hasCall(*calls, "systemctl daemon-reload") {
		t.Errorf("no daemon-reload: %v", *calls)
	}

	// clear it: both copies go, systemd is told again
	uo, err = s.SetUnitOverride(ctx, "rfd", "")
	if err != nil {
		t.Fatal(err)
	}
	if uo.Override != "" {
		t.Errorf("override not cleared: %q", uo.Override)
	}
	if _, ok := fp.files[want]; ok {
		t.Error("drop-in still in /run after clearing")
	}
	if _, err := os.Stat(stored); !os.IsNotExist(err) {
		t.Error("stored copy still on the userfs after clearing")
	}
	// clearing what is not there is fine - the helper's "no such file" is not an error here
	if _, err := s.SetUnitOverride(ctx, "rfd", ""); err != nil {
		t.Errorf("clearing twice: %v", err)
	}
}

// What is refused: a file with no section (every time a mistake), a NUL, an oversized paste, and
// anything that is not a unit name. A timer is accepted as itself.
func TestUnitOverrideRefusals(t *testing.T) {
	s, _, _ := overrideFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ id, override, why string }{
		{"rfd", "Environment=X=1\n", "no [Section]"},
		{"rfd", "[Service]\nx\x00y\n", "NUL"},
		{"rfd", "[Service]\n" + strings.Repeat("Environment=A=b\n", 6000), "over 64 KiB"},
		{"../etc/passwd", "[Service]\n", "not a unit name"},
		{"rfd; rm -rf /", "[Service]\n", "not a unit name"},
	} {
		if _, err := s.SetUnitOverride(ctx, tc.id, tc.override); err == nil {
			t.Errorf("%s: accepted", tc.why)
		}
	}
	uo, err := s.SetUnitOverride(ctx, "occu-cron-backup.timer", "[Timer]\nOnCalendar=daily\n")
	if err != nil {
		t.Fatalf("a timer must be editable: %v", err)
	}
	if uo.Unit != "occu-cron-backup.timer" {
		t.Errorf("timer renamed to %q", uo.Unit)
	}
}

// At start every stored override goes back into /run - which is empty after a boot - and one
// daemon-reload follows. A stored file that is not a unit fragment is reported, not applied.
func TestReplayUnitOverrides(t *testing.T) {
	s, fp, calls := overrideFixture(t)
	dir := s.overrideDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "rfd.service.conf"), []byte("[Service]\nEnvironment=A=1\n"), 0o640)
	os.WriteFile(filepath.Join(dir, "occu-fstrim.timer.conf"), []byte("[Timer]\nOnCalendar=weekly\n"), 0o640)
	os.WriteFile(filepath.Join(dir, "broken.service.conf"), []byte("no section here\n"), 0o640)

	applied, problems := s.ReplayUnitOverrides(context.Background())
	if len(applied) != 2 {
		t.Errorf("applied %v", applied)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "broken.service") {
		t.Errorf("problems %v", problems)
	}
	if _, ok := fp.files["/run/systemd/system/rfd.service.d/50-occulite.conf"]; !ok {
		t.Error("rfd override not replayed")
	}
	if _, ok := fp.files["/run/systemd/system/occu-fstrim.timer.d/50-occulite.conf"]; !ok {
		t.Error("timer override not replayed")
	}
	if _, ok := fp.files["/run/systemd/system/broken.service.d/50-occulite.conf"]; ok {
		t.Error("a broken override was applied")
	}
	n := 0
	for _, c := range *calls {
		if c == "systemctl daemon-reload" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("daemon-reload called %d times, want 1", n)
	}
	// nothing stored, nothing done, nothing reported
	empty := SystemdServices{Run: s.Run, SwitchFile: filepath.Join(t.TempDir(), "x.json")}
	if a, p := empty.ReplayUnitOverrides(context.Background()); len(a)+len(p) != 0 {
		t.Errorf("empty dir: %v %v", a, p)
	}
}

func hasCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}
