package led

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
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
	Available bool     `json:"available"`
	Reason    string   `json:"reason,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	LEDs      []string `json:"leds"`
	Colors    []string `json:"colors"`
	Patterns  []string `json:"patterns"`
	// Brightness: the LED has levels (task 315: the RPI-RF-MOD over PWM, max_brightness 255),
	// so colours are mixed and dimmed and breathe is offered; MaxBrightness is the LEDs'
	// max_brightness (1 on an on/off LED).
	Brightness    bool `json:"brightness"`
	MaxBrightness int  `json:"max_brightness,omitempty"`
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
//
// load loads a kernel module by name - the privilege helper's LoadLEDModule, which answers
// whether the pattern trigger can be had (openccu-lite B-299); nil = look for the module file
// under the root instead, which only works where /lib/modules is readable.
func Probe(root system.Root, load func(name string) error) Hardware {
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
		hw.PatternTrigger = patternTrigger(root, load)
		hw.MaxBrightness = maxBrightness(root)
		hw.Brightness = hw.MaxBrightness > 1
		hw.Patterns = slices.DeleteFunc(slices.Clone(Patterns), func(p string) bool {
			return (p == Double && !hw.PatternTrigger) || (p == Breathe && !hw.Brightness)
		})
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

// maxBrightness is the three channels' max_brightness - the smallest of them, 1 when it cannot be
// read (an on/off LED, as every image before task 315 had).
func maxBrightness(root system.Root) int {
	m := 0
	for _, l := range ChannelLEDs {
		b, err := os.ReadFile(filepath.Join(string(root), "sys/class/leds", l, "max_brightness"))
		if err != nil {
			return 1
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil || n < 1 {
			return 1
		}
		if m == 0 || n < m {
			m = n
		}
	}
	return max(m, 1)
}

// patternTrigger: the LED lists the trigger (built in, or the module loaded already), or the
// module is loaded (/sys/module), or it can be loaded. The loading is the helper's: the daemon's
// unit has ProtectKernelModules=, which hides /lib/modules from it (openccu-lite B-299) - a glob
// there found nothing on every image and said no. Without a loader the module file under the
// root decides, as before.
func patternTrigger(root system.Root, load func(name string) error) bool {
	trig, _ := os.ReadFile(filepath.Join(string(root), "sys/class/leds", ChannelLEDs[0], "trigger"))
	if slices.Contains(strings.Fields(strings.NewReplacer("[", "", "]", "").Replace(string(trig))), "pattern") {
		return true
	}
	if _, err := os.Stat(filepath.Join(string(root), "sys/module/ledtrig_pattern")); err == nil {
		return true
	}
	if load != nil {
		return load(PatternModule) == nil
	}
	rel, _ := os.ReadFile(filepath.Join(string(root), "proc/sys/kernel/osrelease"))
	release := strings.TrimSpace(string(rel))
	if release == "" {
		return false
	}
	found, _ := filepath.Glob(filepath.Join(string(root), "lib/modules", release, "kernel/drivers/leds/trigger/ledtrig-pattern.ko*"))
	return len(found) > 0
}

// PatternModule is the pattern trigger's module name, one of priv.LEDModules.
const PatternModule = "ledtrig-pattern"
