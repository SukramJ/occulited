// Package led is the status LED controller (task 95): occulited is the only writer of the
// RPI-RF-MOD's RGB LED after the boot. States of the box are a priority list with CCU-compatible
// defaults, the first active and enabled one wins, and what it shows is one frame of kernel
// triggers, written through the privilege helper only when it changes - a blink keeps running in
// the kernel through a pause or a restart of the daemon.
//
// The boot (S02InitRTC's yellow, eQ3StartNetwork's blue blink) and the recovery system's magenta
// stay the shell scripts'; the controller starts in booting (the same yellow) and takes over once
// the radio is known (task 158), showing interfaces-starting while the interface daemons start.
package led

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The colours: the RPI-RF-MOD's LED is three GPIO channels, on or off, so seven colours and off.
// The names are RedMatic-LED's payloads, which users already know from their flows.
const (
	Off     = "off"
	Red     = "red"
	Green   = "green"
	Blue    = "blue"
	Yellow  = "yellow"
	Cyan    = "cyan"
	Magenta = "magenta"
	White   = "white"
)

// Colors lists them in the order the page offers them.
var Colors = []string{Red, Green, Blue, Yellow, Cyan, Magenta, White, Off}

// channels is which of red, green, blue a colour lights.
var channels = map[string][3]bool{
	Off:     {},
	Red:     {true, false, false},
	Green:   {false, true, false},
	Blue:    {false, false, true},
	Yellow:  {true, true, false},
	Cyan:    {false, true, true},
	Magenta: {true, false, true},
	White:   {true, true, true},
}

// The patterns. Severity is in the pattern, the category in the colour: solid and fast are more
// urgent than slow, flash, double and alternate.
const (
	Solid     = "solid"
	Slow      = "slow"      // 500/500 ms, upstream's 499
	Fast      = "fast"      // 100/100 ms
	Flash     = "flash"     // 100/1900 ms, a sign of life
	Double    = "double"    // two 150 ms flashes every 2 s (the pattern trigger)
	Alternate = "alternate" // with a second colour on other channels, the CCU's service message look
)

// Patterns lists them in the order the page offers them.
var Patterns = []string{Solid, Slow, Fast, Flash, Double, Alternate}

// OverNormalPatterns are the patterns that can blink over the normal colour (task 134): the ones
// with an off phase and one colour.
var OverNormalPatterns = []string{Slow, Fast, Flash, Double}

// Look is what the LED shows: a colour, a pattern, and for alternate the second colour.
// OverNormal (task 134, opt-in) makes a blink's off phase show the normal state's colour instead of
// dark; the resolver decides whether it takes effect (Shown.Background).
type Look struct {
	Color      string `json:"color"`
	Pattern    string `json:"pattern"`
	Color2     string `json:"color2,omitempty"`
	OverNormal bool   `json:"over_normal,omitempty"`
}

// The state ids. The fixed ones are above every configurable one and cannot be moved.
const (
	StateShutdown       = "shutdown"
	StateBooting        = "booting"
	StatePreview        = "preview"
	StateLocate         = "locate"
	StateRadioDown      = "radio-down"
	StateNoNetwork      = "no-network"
	StateServiceFailed  = "service-failed"
	StateStorageReplace = "storage-replace"
	// StateInterfacesStarting: one of the interface daemons is starting (task 158)
	StateInterfacesStarting = "interfaces-starting"
	StateExternal           = "external"
	StateStatusWarning      = "status-warning"
	StateSystemUpdate       = "system-update"
	StateAddonUpdate        = "addon-update"
	StateNoInternet         = "no-internet"
	StateNormal             = "normal"
)

// FixedStates are above the list, in this order.
var FixedStates = []string{StateShutdown, StateBooting, StatePreview, StateLocate}

// ErrorStates are what night mode's "only errors" still shows, and what lights the red power LED
// on a box without an RGB LED.
var ErrorStates = []string{StateRadioDown, StateServiceFailed, StateStorageReplace}

// The fixed looks: the CCU3 manual's "booting" and a shutdown that never looks fine.
var (
	LookBooting  = Look{Color: Yellow, Pattern: Solid}
	LookShutdown = Look{Color: Yellow, Pattern: Fast}
	LookDark     = Look{Color: Off, Pattern: Solid}
)

// StateConfig is one configurable row: whether it shows, and how. The external row has no look of
// its own - each override brings one - and its position is where overrides rank.
type StateConfig struct {
	ID         string `json:"id"`
	Enabled    bool   `json:"enabled"`
	Color      string `json:"color,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	Color2     string `json:"color2,omitempty"`
	OverNormal bool   `json:"over_normal,omitempty"`
}

// Look is the row's look.
func (s StateConfig) Look() Look {
	return Look{Color: s.Color, Pattern: s.Pattern, Color2: s.Color2, OverNormal: s.OverNormal}
}

// Night is the night window, in the box's time zone; To before From crosses midnight.
type Night struct {
	Enabled bool   `json:"enabled"`
	From    string `json:"from"`
	To      string `json:"to"`
	// Show is errors (only the error states and the fixed ones) or off (only the fixed ones).
	Show string `json:"show"`
}

// Locate is the locate action's look and default duration.
type Locate struct {
	Color     string `json:"color"`
	Pattern   string `json:"pattern"`
	DurationS int    `json:"duration_s"`
}

// External bounds the overrides.
type External struct {
	MaxDurationS int `json:"max_duration_s"`
	// AllowUntilCleared lets an override with duration 0 stay until it is cleared.
	AllowUntilCleared bool `json:"allow_until_cleared"`
}

// Config is led.json.
type Config struct {
	Enabled bool          `json:"enabled"`
	Normal  Look          `json:"normal"`
	Night   Night         `json:"night"`
	States  []StateConfig `json:"states"`
	// WarningsOff are the Status page warning ids the status-warning state ignores.
	WarningsOff []string `json:"warnings_off"`
	// AddonUnits: a failed addon unit counts as service-failed too.
	AddonUnits bool     `json:"addon_units"`
	Locate     Locate   `json:"locate"`
	External   External `json:"external"`
	// PWRErrorLight: on a box without an RGB LED, the red power LED blinks while an error state is
	// active.
	PWRErrorLight bool `json:"pwr_error_light"`
}

// Defaults is the configuration of a box that never saved one: the CCU's meanings where the CCU
// has one.
func Defaults() Config {
	return Config{
		Enabled: true,
		Normal:  Look{Color: Blue, Pattern: Solid},
		Night:   Night{From: "22:00", To: "06:30", Show: "errors"},
		States: []StateConfig{
			{ID: StateRadioDown, Enabled: true, Color: Red, Pattern: Solid},
			{ID: StateNoNetwork, Enabled: true, Color: Yellow, Pattern: Fast},
			{ID: StateServiceFailed, Enabled: true, Color: Red, Pattern: Slow},
			{ID: StateStorageReplace, Enabled: true, Color: Red, Pattern: Double},
			// task 158 (maintainer, 2026-09-18): a double magenta blink over the normal colour - not the
			// recovery system's magenta, which is slow, solid or fast over dark; below the errors
			{ID: StateInterfacesStarting, Enabled: true, Color: Magenta, Pattern: Double, OverNormal: true},
			{ID: StateExternal, Enabled: true},
			{ID: StateStatusWarning, Enabled: true, Color: Yellow, Pattern: Slow},
			{ID: StateSystemUpdate, Enabled: true, Color: Cyan, Pattern: Slow},
			{ID: StateAddonUpdate, Enabled: false, Color: Cyan, Pattern: Double},
			{ID: StateNoInternet, Enabled: false, Color: Blue, Pattern: Fast},
		},
		WarningsOff: []string{},
		Locate:      Locate{Color: White, Pattern: Fast, DurationS: 300},
		External:    External{MaxDurationS: 3600, AllowUntilCleared: true},
	}
}

// ErrInvalid wraps every refusal of a configuration or a look; the API answers 422.
var ErrInvalid = errors.New("invalid")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Disjoint reports whether two colours share no channel - the pairs alternate can blink.
func Disjoint(a, b string) bool {
	ca, cb := channels[a], channels[b]
	for i := range ca {
		if ca[i] && cb[i] {
			return false
		}
	}
	return true
}

// NormalizeLook checks a look and returns it in its one spelling: off is always solid, a second
// colour only with alternate, alternate only with two disjoint colours that both light, and
// over_normal only with one of OverNormalPatterns (dropped, not refused, elsewhere - an older page
// or a flow that keeps the flag while changing the pattern is not an error).
func NormalizeLook(l Look) (Look, error) {
	if _, ok := channels[l.Color]; !ok {
		return l, invalid("colour %q", l.Color)
	}
	if l.Pattern == "" {
		l.Pattern = Solid
	}
	if !slices.Contains(Patterns, l.Pattern) {
		return l, invalid("pattern %q", l.Pattern)
	}
	if l.Color == Off {
		return LookDark, nil
	}
	if !slices.Contains(OverNormalPatterns, l.Pattern) {
		l.OverNormal = false
	}
	if l.Pattern != Alternate {
		l.Color2 = ""
		return l, nil
	}
	if _, ok := channels[l.Color2]; !ok || l.Color2 == Off {
		return l, invalid("alternate needs a second colour")
	}
	if !Disjoint(l.Color, l.Color2) {
		return l, invalid("%s and %s share a channel and cannot alternate", l.Color, l.Color2)
	}
	return l, nil
}

var (
	hhmmRe      = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	warningIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
)

// minutesOf is an HH:MM in minutes after midnight.
func minutesOf(hhmm string) int {
	h, _ := strconv.Atoi(hhmm[:2])
	m, _ := strconv.Atoi(hhmm[3:])
	return h*60 + m
}

// Validate checks a configuration and returns it normalised. A row the stored or sent list lacks -
// a state a later version added - is put in at its default position, after the default row before
// it; an unknown or repeated id is refused.
func Validate(c Config) (Config, error) {
	var err error
	if c.Normal, err = NormalizeLook(c.Normal); err != nil {
		return c, fmt.Errorf("normal: %w", err)
	}
	c.Normal.OverNormal = false // normal is the background itself
	if !hhmmRe.MatchString(c.Night.From) || !hhmmRe.MatchString(c.Night.To) {
		return c, invalid("night: from and to are HH:MM")
	}
	if c.Night.Show != "errors" && c.Night.Show != "off" {
		return c, invalid("night: show is errors or off")
	}
	if l, err := NormalizeLook(Look{Color: c.Locate.Color, Pattern: c.Locate.Pattern}); err != nil {
		return c, fmt.Errorf("locate: %w", err)
	} else if l.Color == Off || l.Pattern == Alternate {
		return c, invalid("locate: a colour that lights and a pattern of one colour")
	} else {
		c.Locate.Color, c.Locate.Pattern = l.Color, l.Pattern
	}
	if c.Locate.DurationS < 10 || c.Locate.DurationS > 3600 {
		return c, invalid("locate: duration_s is 10 to 3600")
	}
	if c.External.MaxDurationS < 60 || c.External.MaxDurationS > 86400 {
		return c, invalid("external: max_duration_s is 60 to 86400")
	}
	defaults := Defaults().States
	known := map[string]StateConfig{}
	for _, d := range defaults {
		known[d.ID] = d
	}
	seen := map[string]bool{}
	states := make([]StateConfig, 0, len(defaults))
	for _, s := range c.States {
		d, ok := known[s.ID]
		if !ok {
			return c, invalid("unknown state %q", s.ID)
		}
		if seen[s.ID] {
			return c, invalid("state %q twice", s.ID)
		}
		seen[s.ID] = true
		if s.ID == StateExternal {
			states = append(states, StateConfig{ID: s.ID, Enabled: s.Enabled})
			continue
		}
		if s.Color == "" {
			s.Color, s.Pattern, s.Color2, s.OverNormal = d.Color, d.Pattern, d.Color2, d.OverNormal
		}
		l, err := NormalizeLook(s.Look())
		if err != nil {
			return c, fmt.Errorf("%s: %w", s.ID, err)
		}
		s.Color, s.Pattern, s.Color2, s.OverNormal = l.Color, l.Pattern, l.Color2, l.OverNormal
		states = append(states, s)
	}
	for i, d := range defaults {
		if seen[d.ID] {
			continue
		}
		at := 0
		for j := i - 1; j >= 0; j-- {
			if k := slices.IndexFunc(states, func(s StateConfig) bool { return s.ID == defaults[j].ID }); k >= 0 {
				at = k + 1
				break
			}
		}
		states = slices.Insert(states, at, d)
		seen[d.ID] = true
	}
	c.States = states
	off := []string{}
	for _, id := range c.WarningsOff {
		id = strings.TrimSpace(id)
		if !warningIDRe.MatchString(id) {
			return c, invalid("warning id %q", id)
		}
		if !slices.Contains(off, id) {
			off = append(off, id)
		}
	}
	c.WarningsOff = off
	return c, nil
}

// State returns the row of an id.
func (c Config) State(id string) (StateConfig, bool) {
	for _, s := range c.States {
		if s.ID == id {
			return s, true
		}
	}
	return StateConfig{}, false
}
