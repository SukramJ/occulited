package priv

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidLEDFrame(t *testing.T) {
	ok := []LEDWrite{{LED: "rpi_rf_mod:red", Trigger: "timer", DelayOn: 100, DelayOff: 100}, {LED: "rpi_rf_mod:green", Trigger: "default-on"}}
	if err := ValidLEDFrame(ok); err != nil {
		t.Fatalf("valid frame refused: %v", err)
	}
	bad := map[string][]LEDWrite{
		"empty":             {},
		"five":              {{LED: "a", Trigger: "none"}, {LED: "b", Trigger: "none"}, {LED: "c", Trigger: "none"}, {LED: "d", Trigger: "none"}, {LED: "e", Trigger: "none"}},
		"slash":             {{LED: "../etc", Trigger: "none"}},
		"dotdot":            {{LED: "..", Trigger: "none"}},
		"twice":             {{LED: "a", Trigger: "none"}, {LED: "a", Trigger: "none"}},
		"trigger":           {{LED: "a", Trigger: "oneshot"}},
		"timer short":       {{LED: "a", Trigger: "timer", DelayOn: 1, DelayOff: 100}},
		"timer long":        {{LED: "a", Trigger: "timer", DelayOn: 100, DelayOff: 20000}},
		"delay on none":     {{LED: "a", Trigger: "none", DelayOn: 100}},
		"pattern syntax":    {{LED: "a", Trigger: "pattern", Pattern: "1 150; reboot"}},
		"pattern on timer":  {{LED: "a", Trigger: "timer", DelayOn: 100, DelayOff: 100, Pattern: "1 150 0 150"}},
		"start after":       {{LED: "a", Trigger: "none", StartAfterMS: 1001}},
		"start after total": {{LED: "a", Trigger: "none", StartAfterMS: 600}, {LED: "b", Trigger: "none", StartAfterMS: 600}},
		"negative":          {{LED: "a", Trigger: "none", StartAfterMS: -1}},
	}
	for name, f := range bad {
		if err := ValidLEDFrame(f); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeLEDs builds /sys/devices/platform/leds/<name> with the attributes a gpio LED has, and the
// /sys/class/leds/<name> symlinks into it, as the kernel lays them out.
func fakeLEDs(t *testing.T, root string, names ...string) string {
	t.Helper()
	class := filepath.Join(root, "sys/class/leds")
	if err := os.MkdirAll(class, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		dev := filepath.Join(root, "sys/devices/platform/leds", n)
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		for attr, v := range map[string]string{"trigger": "[none] default-on timer heartbeat\n", "brightness": "1\n", "delay_on": "", "delay_off": "", "pattern": ""} {
			if err := os.WriteFile(filepath.Join(dev, attr), []byte(v), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Symlink(filepath.Join("../../devices/platform/leds", n), filepath.Join(class, n)); err != nil {
			t.Fatal(err)
		}
	}
	return class
}

func readAttr(t *testing.T, class, led, attr string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(class, led, attr))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLocalWriteLEDs(t *testing.T) {
	class := fakeLEDs(t, t.TempDir(), "rpi_rf_mod:red", "rpi_rf_mod:green", "rpi_rf_mod:blue")
	frame := []LEDWrite{
		{LED: "rpi_rf_mod:red", Trigger: "timer", DelayOn: 100, DelayOff: 1900},
		{LED: "rpi_rf_mod:green", Trigger: "pattern", Pattern: "1 150 1 0 0 150 0 0"},
		{LED: "rpi_rf_mod:blue", Trigger: "none"},
	}
	if err := (Local{}).WriteLEDs(class, frame); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ led, attr, want string }{
		{"rpi_rf_mod:red", "trigger", "timer"},
		{"rpi_rf_mod:red", "delay_on", "100"},
		{"rpi_rf_mod:red", "delay_off", "1900"},
		{"rpi_rf_mod:green", "trigger", "pattern"},
		{"rpi_rf_mod:green", "pattern", "1 150 1 0 0 150 0 0"},
		{"rpi_rf_mod:blue", "trigger", "none"},
		{"rpi_rf_mod:blue", "brightness", "0"},
	} {
		if got := readAttr(t, class, c.led, c.attr); got != c.want {
			t.Errorf("%s/%s = %q, want %q", c.led, c.attr, got, c.want)
		}
	}
	// an attribute is written in place, never created: an LED without delay_on fails
	if err := os.Remove(filepath.Join(class, "rpi_rf_mod:red", "delay_on")); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).WriteLEDs(class, frame[:1]); err == nil {
		t.Error("a missing attribute was created")
	}
	if _, err := os.Stat(filepath.Join(class, "rpi_rf_mod:red", "delay_on")); err == nil {
		t.Error("delay_on was created")
	}
}

func TestLocalWriteLEDsPatternModule(t *testing.T) {
	class := fakeLEDs(t, t.TempDir(), "rpi_rf_mod:green")
	frame := []LEDWrite{{LED: "rpi_rf_mod:green", Trigger: "pattern", Pattern: "1 500 1 0 0 500 0 0"}}
	oldWrite, oldLoad := writeLEDAttr, loadLEDPattern
	defer func() { writeLEDAttr, loadLEDPattern = oldWrite, oldLoad }()
	// the kernel refuses a trigger it does not know until the module is loaded
	loaded, calls := false, 0
	writeLEDAttr = func(path, value string) error {
		if filepath.Base(path) == "trigger" && value == "pattern" && !loaded {
			return errors.New("invalid argument")
		}
		return oldWrite(path, value)
	}
	loadLEDPattern = func() error { calls++; loaded = true; return nil }
	if err := (Local{}).WriteLEDs(class, frame); err != nil {
		t.Fatalf("after loading the module: %v", err)
	}
	if calls != 1 || readAttr(t, class, "rpi_rf_mod:green", "trigger") != "pattern" {
		t.Errorf("module loads %d, trigger %q", calls, readAttr(t, class, "rpi_rf_mod:green", "trigger"))
	}
	// a second frame finds the trigger and loads nothing
	if err := (Local{}).WriteLEDs(class, frame); err != nil || calls != 1 {
		t.Errorf("second frame: %v, module loads %d", err, calls)
	}
	// no module to load: ErrLEDPattern, which the controller answers with the timer trigger
	loaded = false
	loadLEDPattern = func() error { calls++; return errors.New("module not found") }
	if err := (Local{}).WriteLEDs(class, frame); !errors.Is(err, ErrLEDPattern) {
		t.Errorf("without the module: %v", err)
	}
}

func TestServerLEDOperation(t *testing.T) {
	root := t.TempDir()
	class := fakeLEDs(t, root, "rpi_rf_mod:red", "rpi_rf_mod:green", "rpi_rf_mod:blue", "ACT")
	// an entry that leads out of /sys
	outside := filepath.Join(t.TempDir(), "evil")
	_ = os.MkdirAll(outside, 0o755)
	_ = os.WriteFile(filepath.Join(outside, "trigger"), []byte("none"), 0o644)
	_ = os.Remove(filepath.Join(class, "rpi_rf_mod:blue"))
	if err := os.Symlink(outside, filepath.Join(class, "rpi_rf_mod:blue")); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var logged []string
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite"), Log: func(f string, a ...any) { logged = append(logged, f) }}
	go func() { _ = srv.Serve(ctx, l) }()
	c := Client{Socket: sock}

	if err := c.WriteLEDs(class, []LEDWrite{{LED: "rpi_rf_mod:red", Trigger: "default-on"}, {LED: "rpi_rf_mod:green", Trigger: "timer", DelayOn: 500, DelayOff: 500}}); err != nil {
		t.Fatalf("a valid frame: %v", err)
	}
	if got := readAttr(t, class, "rpi_rf_mod:red", "trigger"); got != "default-on" {
		t.Errorf("red trigger %q", got)
	}
	refused := map[string]struct {
		dir   string
		frame []LEDWrite
	}{
		"not on the list":    {class, []LEDWrite{{LED: "ACT", Trigger: "none"}}},
		"another directory":  {filepath.Join(root, "sys/devices/platform/leds"), []LEDWrite{{LED: "rpi_rf_mod:red", Trigger: "none"}}},
		"outside /sys":       {class, []LEDWrite{{LED: "rpi_rf_mod:blue", Trigger: "none"}}},
		"bad trigger":        {class, []LEDWrite{{LED: "rpi_rf_mod:red", Trigger: "oneshot"}}},
		"not under the root": {filepath.Join(t.TempDir(), "sys/class/leds"), []LEDWrite{{LED: "rpi_rf_mod:red", Trigger: "none"}}},
	}
	for name, r := range refused {
		if err := c.WriteLEDs(r.dir, r.frame); err == nil || !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "trigger")); string(b) != "none" {
		t.Errorf("the file outside /sys was written: %q", b)
	}
	if len(logged) == 0 || !strings.Contains(strings.Join(logged, "\n"), "LED frame") {
		t.Errorf("refusals not logged: %v", logged)
	}
}
