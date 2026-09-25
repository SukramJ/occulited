package priv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The status LED (task 95). occulited's LED controller is the only writer of the RPI-RF-MOD's RGB
// LED after the boot; what it writes is a frame - one entry per LED - and the helper applies it as
// root. One typed operation rather than a sysfs path on Paths or a shell redirect: the LED names
// are a closed list, the triggers a closed set, the numbers bounded, and a sysfs attribute cannot
// be written with WriteFile's temporary file plus rename anyway.
//
// The order is part of the operation: every LED of the frame goes to trigger "none" first, then
// the triggers are set back to back, so the channels of a mixed colour (yellow = red + green) start
// their timers together and blink without colour fringes. StartAfterMS delays one entry, which is
// how two colours alternate on the timer trigger (hss_led's own way, led.cpp:75-76).

// opLED is the operation's name on the wire.
const opLED = "led"

// LEDWrite is one LED's part of a frame.
type LEDWrite struct {
	// LED is the name under /sys/class/leds (rpi_rf_mod:red, PWR).
	LED string `json:"led"`
	// Trigger is one of LEDTriggers.
	Trigger string `json:"trigger"`
	// DelayOn and DelayOff are the timer trigger's milliseconds (LEDDelayMin to LEDDelayMax);
	// zero for every other trigger.
	DelayOn  int `json:"delay_on,omitempty"`
	DelayOff int `json:"delay_off,omitempty"`
	// Pattern is the pattern trigger's "brightness duration" pairs; empty for every other trigger.
	Pattern string `json:"pattern,omitempty"`
	// StartAfterMS waits this long before the entry's trigger is set (0 to LEDStartAfterMax).
	StartAfterMS int `json:"start_after_ms,omitempty"`
}

// LEDTriggers are the triggers a frame may set: the controller's own (none, default-on, timer,
// pattern) and the board LEDs' normal ones from /var/hm_mode, which the controller writes back
// after it used the red power LED as an error light.
var LEDTriggers = []string{"none", "default-on", "timer", "pattern", "heartbeat", "mmc0", "mmc1", "input", "actpwr"}

// The bounds of a frame.
const (
	LEDMaxFrame      = 4
	LEDDelayMin      = 10
	LEDDelayMax      = 10000
	LEDStartAfterMax = 1000
)

var (
	ledNameRe = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,32}$`)
	// ledPatternRe is the kernel's pattern syntax, bounded: up to 32 pairs of a brightness and a
	// duration. A malformed string would silently deactivate the trigger, so it never gets there.
	ledPatternRe = regexp.MustCompile(`^[0-9]{1,3} [0-9]{1,5}( [0-9]{1,3} [0-9]{1,5}){0,31}$`)
)

// ValidLEDFrame checks a frame's shape: the helper checks it at its boundary, the operation again.
func ValidLEDFrame(frame []LEDWrite) error {
	if len(frame) == 0 || len(frame) > LEDMaxFrame {
		return fmt.Errorf("a frame has 1 to %d entries", LEDMaxFrame)
	}
	seen := map[string]bool{}
	wait := 0
	for _, w := range frame {
		if !ledNameRe.MatchString(w.LED) || w.LED == "." || w.LED == ".." {
			return fmt.Errorf("led name %q", w.LED)
		}
		if seen[w.LED] {
			return fmt.Errorf("led %s twice in one frame", w.LED)
		}
		seen[w.LED] = true
		if !slices.Contains(LEDTriggers, w.Trigger) {
			return fmt.Errorf("led %s: trigger %q", w.LED, w.Trigger)
		}
		if w.Trigger == "timer" {
			if w.DelayOn < LEDDelayMin || w.DelayOn > LEDDelayMax || w.DelayOff < LEDDelayMin || w.DelayOff > LEDDelayMax {
				return fmt.Errorf("led %s: timer delays must be %d to %d ms", w.LED, LEDDelayMin, LEDDelayMax)
			}
		} else if w.DelayOn != 0 || w.DelayOff != 0 {
			return fmt.Errorf("led %s: delays belong to the timer trigger", w.LED)
		}
		if w.Trigger == "pattern" {
			if !ledPatternRe.MatchString(w.Pattern) {
				return fmt.Errorf("led %s: pattern %q", w.LED, w.Pattern)
			}
		} else if w.Pattern != "" {
			return fmt.Errorf("led %s: a pattern belongs to the pattern trigger", w.LED)
		}
		if w.StartAfterMS < 0 || w.StartAfterMS > LEDStartAfterMax {
			return fmt.Errorf("led %s: start_after_ms must be 0 to %d", w.LED, LEDStartAfterMax)
		}
		wait += w.StartAfterMS
	}
	if wait > LEDStartAfterMax {
		return fmt.Errorf("a frame waits %d ms at most", LEDStartAfterMax)
	}
	return nil
}

// ledAllowed: the directory is exactly the policy's LED class directory and every LED of a valid
// frame is on the policy's list.
func (p Policy) ledAllowed(dir string, frame []LEDWrite) error {
	rel, ok := p.rel(dir)
	if !ok || p.LEDDir == "" || rel != filepath.Clean(p.LEDDir) {
		return fmt.Errorf("led directory %s", dir)
	}
	if err := ValidLEDFrame(frame); err != nil {
		return err
	}
	for _, w := range frame {
		if !slices.Contains(p.LEDNames, w.LED) {
			return fmt.Errorf("led %s is not on the list", w.LED)
		}
	}
	return nil
}

// ledUnderSys: the entries of /sys/class/leds are symlinks into /sys/devices, so a symlink is not
// the refusal - an LED directory that resolves anywhere outside the root's /sys is.
func (p Policy) ledUnderSys(dir string, frame []LEDWrite) error {
	sys, err := filepath.EvalSymlinks(filepath.Join(p.Root, "sys"))
	if err != nil {
		return err
	}
	for _, w := range frame {
		real, err := filepath.EvalSymlinks(filepath.Join(dir, w.LED))
		if err != nil {
			return err
		}
		if !strings.HasPrefix(real, filepath.Clean(sys)+"/") {
			return fmt.Errorf("led %s resolves to %s, outside /sys", w.LED, real)
		}
	}
	return nil
}

// loadLEDPattern loads the pattern trigger's module, which the images build as a module and nothing
// loads at boot. A variable so a test can count the calls.
var loadLEDPattern = func() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	prog := "/sbin/modprobe"
	if _, err := os.Stat(prog); err != nil {
		if p, lerr := exec.LookPath("modprobe"); lerr == nil {
			prog = p
		}
	}
	out, err := exec.CommandContext(ctx, prog, "ledtrig-pattern").CombinedOutput()
	if err != nil {
		return fmt.Errorf("modprobe ledtrig-pattern: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ErrLEDPattern is WriteLEDs' answer when the pattern trigger cannot be had; the controller then
// renders its patterns with the timer trigger instead.
var ErrLEDPattern = errors.New("the pattern trigger is not available")

// writeLEDAttr writes one sysfs attribute in place (O_TRUNC, never created). A variable so a test
// can play a kernel that refuses a trigger.
var writeLEDAttr = func(path, value string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(value)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// WriteLEDs applies a frame under dir, the LED class directory: every entry's trigger to none, then
// each entry's trigger and its attributes.
func (Local) WriteLEDs(dir string, frame []LEDWrite) error {
	if err := ValidLEDFrame(frame); err != nil {
		return err
	}
	write := func(led, attr, value string) error {
		if err := writeLEDAttr(filepath.Join(dir, led, attr), value); err != nil {
			return fmt.Errorf("%s/%s: %w", led, attr, err)
		}
		return nil
	}
	for _, w := range frame {
		if err := write(w.LED, "trigger", "none"); err != nil {
			return err
		}
	}
	for _, w := range frame {
		if w.StartAfterMS > 0 {
			time.Sleep(time.Duration(w.StartAfterMS) * time.Millisecond)
		}
		switch w.Trigger {
		case "none":
			if err := write(w.LED, "brightness", "0"); err != nil {
				return err
			}
		case "timer":
			if err := write(w.LED, "trigger", "timer"); err != nil {
				return err
			}
			if err := write(w.LED, "delay_on", strconv.Itoa(w.DelayOn)); err != nil {
				return err
			}
			if err := write(w.LED, "delay_off", strconv.Itoa(w.DelayOff)); err != nil {
				return err
			}
		case "pattern":
			if err := write(w.LED, "trigger", "pattern"); err != nil {
				// the kernel refuses a trigger it does not know: load the module once and try again
				if lerr := loadLEDPattern(); lerr != nil {
					return fmt.Errorf("%w: %v", ErrLEDPattern, lerr)
				}
				if err := write(w.LED, "trigger", "pattern"); err != nil {
					return fmt.Errorf("%w: %v", ErrLEDPattern, err)
				}
			}
			if err := write(w.LED, "pattern", w.Pattern); err != nil {
				return err
			}
		default:
			if err := write(w.LED, "trigger", w.Trigger); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteLEDs asks the helper to apply a frame. A frame that alternates waits up to a second inside
// the helper, so the deadline is generous.
func (c Client) WriteLEDs(dir string, frame []LEDWrite) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.call(ctx, request{Op: opLED, Path: dir, LEDs: frame})
	if err != nil && strings.Contains(err.Error(), ErrLEDPattern.Error()) && !errors.Is(err, ErrLEDPattern) {
		return fmt.Errorf("%w: %v", ErrLEDPattern, err)
	}
	return err
}
