package led

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/hobbyquaker/occulited/internal/system"
)

// Why a box has no status LED the controller drives.
const (
	ReasonDetecting = "detecting" // /var/hm_mode is not written yet
	ReasonNoModule  = "no-module" // the radio module has no status LED (HmIP-RFUSB, HM-MOD-RPI-PCB)
	ReasonNoLEDs    = "no-leds"   // an RPI-RF-MOD without its LED devices
	ReasonVirtual   = "virtual"   // a virtual machine
	ReasonContainer = "container" // the LED is the host's
	ReasonHMLGW     = "hm-lgw"    // LAN gateway mode: S99SetupLEDs' blue, the controller stands down
)

// Hardware is what the probe found.
type Hardware struct {
	Available  bool     `json:"available"`
	Reason     string   `json:"reason,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	LEDs       []string `json:"leds"`
	Colors     []string `json:"colors"`
	Patterns   []string `json:"patterns"`
	Brightness bool     `json:"brightness"`
	// PatternTrigger: the kernel has the pattern trigger, built in or as a module to load.
	PatternTrigger bool `json:"-"`
	// PWR: the red power LED exists and /var/hm_mode names it; PWRNormal is its trigger after the boot.
	PWR       bool   `json:"-"`
	PWRNormal string `json:"-"`
	// Final: the detection has run; before that the probe is repeated.
	Final bool `json:"-"`
	// StandDown: HM-LGW mode, where nothing of the controller's is written.
	StandDown bool `json:"-"`
}

var virtualHostRe = regexp.MustCompile(`ova|nuc|generic|x86`)

// Probe looks at the box. An RPI-RF-MOD must be the detected module *and* its LED devices must be
// there: the rpi-rf-mod overlay is loaded on every Pi image, so a Pi with an HmIP-RFUSB has the
// three devices too, on GPIOs nothing is connected to, and they are never written.
func Probe(root system.Root) Hardware {
	hw := Hardware{LEDs: []string{}, Colors: Colors, Patterns: Patterns}
	join := func(p ...string) string { return filepath.Join(append([]string{string(root)}, p...)...) }
	exists := func(p ...string) bool {
		_, err := os.Stat(join(p...))
		return err == nil
	}
	kv := root.HMMode()
	if root.Container() != "" {
		hw.Reason, hw.Final = ReasonContainer, true
		return hw
	}
	if kv["HM_MODE"] == "" {
		hw.Reason = ReasonDetecting
		return hw
	}
	hw.Final = true
	if kv["HM_MODE"] == "HM-LGW" {
		hw.Reason, hw.StandDown = ReasonHMLGW, true
		return hw
	}
	module := kv["HM_HMIP_DEV"] == "RPI-RF-MOD" || kv["HM_HMRF_DEV"] == "RPI-RF-MOD"
	leds := true
	for _, l := range ChannelLEDs {
		leds = leds && exists("sys/class/leds", l, "trigger")
	}
	if module && leds {
		hw.Available, hw.Kind, hw.LEDs = true, "rpi-rf-mod", []string{Red, Green, Blue}
		hw.PatternTrigger = patternTrigger(root)
		if !hw.PatternTrigger {
			hw.Patterns = slices.DeleteFunc(slices.Clone(Patterns), func(p string) bool { return p == Double })
		}
		return hw
	}
	if red := kv["HM_LED_RED"]; red != "" && filepath.Base(red) == PWRLED && exists("sys/class/leds", PWRLED, "trigger") {
		hw.PWR, hw.PWRNormal = true, kv["HM_LED_RED_MODE2"]
	}
	switch {
	case module:
		hw.Reason = ReasonNoLEDs
	case virtualHostRe.MatchString(kv["HM_HOST"]):
		hw.Reason = ReasonVirtual
	default:
		hw.Reason = ReasonNoModule
	}
	return hw
}

// patternTrigger: the LED lists the trigger, the kernel has it built in, or its module is there to
// load (the images build it as a module; nothing loads it at boot).
func patternTrigger(root system.Root) bool {
	trig, _ := os.ReadFile(filepath.Join(string(root), "sys/class/leds", ChannelLEDs[0], "trigger"))
	if slices.Contains(strings.Fields(strings.NewReplacer("[", "", "]", "").Replace(string(trig))), "pattern") {
		return true
	}
	if _, err := os.Stat(filepath.Join(string(root), "sys/module/ledtrig_pattern")); err == nil {
		return true
	}
	rel, _ := os.ReadFile(filepath.Join(string(root), "proc/sys/kernel/osrelease"))
	release := strings.TrimSpace(string(rel))
	if release == "" {
		return false
	}
	found, _ := filepath.Glob(filepath.Join(string(root), "lib/modules", release, "kernel/drivers/leds/trigger/ledtrig-pattern.ko*"))
	return len(found) > 0
}
