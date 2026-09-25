package led

import (
	"slices"
	"sort"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// Activity is one active state: since when, and what the page names (the failed units, the version).
type Activity struct {
	Since  time.Time `json:"since"`
	Detail string    `json:"detail,omitempty"`
}

// Override is an external caller's look at its priority level (POST /led/override).
type Override struct {
	ID      string `json:"id"`
	Color   string `json:"color"`
	Pattern string `json:"pattern"`
	Color2  string `json:"color2,omitempty"`
	// OverNormal blinks over the normal colour instead of over dark (task 134).
	OverNormal bool `json:"over_normal,omitempty"`
	// Priority orders the overrides among themselves: high, normal, low; the newest within a level.
	Priority string `json:"priority"`
	// Night is respect (hidden in the night window) or ignore.
	Night string `json:"night"`
	// Until is when it ends; nil = until it is cleared.
	Until *time.Time `json:"until,omitempty"`
	By    string     `json:"by"`
	Since time.Time  `json:"since"`
}

// Look is the override's look.
func (o Override) Look() Look {
	return Look{Color: o.Color, Pattern: o.Pattern, Color2: o.Color2, OverNormal: o.OverNormal}
}

// Timed is a look that ends by itself: locate and the page's Test.
type Timed struct {
	Look
	Until time.Time `json:"until"`
}

// Inputs are everything the resolver decides on.
type Inputs struct {
	Now      time.Time
	Location *time.Location
	Config   Config
	Shutdown bool
	Booting  bool
	Preview  *Timed
	Locate   *Timed
	// Active are the states whose condition holds, by id.
	Active    map[string]Activity
	Overrides []Override
}

// The sources of what is shown.
const (
	SourceFixed    = "fixed"
	SourcePreview  = "preview"
	SourceLocate   = "locate"
	SourceState    = "state"
	SourceOverride = "override"
	SourceNormal   = "normal"
	SourceNight    = "night"
	SourceOff      = "off"
)

// Shown is what the LED shows and why.
type Shown struct {
	Look
	// Background is the colour a blink's off phase shows (task 134): the normal state's colour when
	// the look blinks over it and that is visible - empty for dark.
	Background string `json:"background,omitempty"`
	Source     string `json:"source"`
	ID         string `json:"id"`
	Detail     string `json:"detail,omitempty"`
}

// NightActive reports whether now is inside the night window, in loc.
func NightActive(n Night, now time.Time, loc *time.Location) bool {
	if !n.Enabled || !hhmmRe.MatchString(n.From) || !hhmmRe.MatchString(n.To) {
		return false
	}
	if loc != nil {
		now = now.In(loc)
	}
	m := now.Hour()*60 + now.Minute()
	from, to := minutesOf(n.From), minutesOf(n.To)
	switch {
	case from == to:
		return false
	case from < to:
		return m >= from && m < to
	default: // across midnight
		return m >= from || m < to
	}
}

func priorityRank(p string) int {
	switch p {
	case "high":
		return 2
	case "low":
		return 0
	}
	return 1
}

// bestOverride is the override shown at the external row: live, not hidden by the night, the
// highest level, the newest within it.
func bestOverride(list []Override, now time.Time, night bool) *Override {
	var live []Override
	for _, o := range list {
		if o.Until != nil && !now.Before(*o.Until) {
			continue
		}
		if night && o.Night != "ignore" {
			continue
		}
		live = append(live, o)
	}
	if len(live) == 0 {
		return nil
	}
	sort.SliceStable(live, func(i, j int) bool {
		if a, b := priorityRank(live[i].Priority), priorityRank(live[j].Priority); a != b {
			return a > b
		}
		return live[i].Since.After(live[j].Since)
	})
	return &live[0]
}

// Resolve is the whole decision, a pure function: the fixed states, then the configured list in its
// order (the first active and enabled row wins, the external row standing for the best override),
// then normal - or dark, at night or when the LED is switched off.
func Resolve(in Inputs) Shown {
	night := NightActive(in.Config.Night, in.Now, in.Location)
	switch {
	case in.Shutdown:
		return Shown{Look: LookShutdown, Source: SourceFixed, ID: StateShutdown}
	case in.Booting:
		return Shown{Look: LookBooting, Source: SourceFixed, ID: StateBooting}
	case in.Preview != nil && in.Now.Before(in.Preview.Until):
		return over(Shown{Look: in.Preview.Look, Source: SourcePreview, ID: StatePreview}, in.Config, night)
	case in.Locate != nil && in.Now.Before(in.Locate.Until):
		return Shown{Look: in.Locate.Look, Source: SourceLocate, ID: StateLocate}
	case !in.Config.Enabled:
		return Shown{Look: LookDark, Source: SourceOff, ID: SourceOff}
	}
	for _, st := range in.Config.States {
		if !st.Enabled {
			continue
		}
		if st.ID == StateExternal {
			if o := bestOverride(in.Overrides, in.Now, night); o != nil {
				return over(Shown{Look: o.Look(), Source: SourceOverride, ID: o.ID}, in.Config, night)
			}
			continue
		}
		a, ok := in.Active[st.ID]
		if !ok {
			continue
		}
		if night && (in.Config.Night.Show == "off" || !slices.Contains(ErrorStates, st.ID)) {
			continue
		}
		return over(Shown{Look: st.Look(), Source: SourceState, ID: st.ID, Detail: a.Detail}, in.Config, night)
	}
	if night {
		return Shown{Look: LookDark, Source: SourceNight, ID: SourceNight}
	}
	return Shown{Look: in.Config.Normal, Source: SourceNormal, ID: StateNormal}
}

// over sets the background of a look that blinks over the normal colour (task 134), where that is
// visible: the LED is on, it is not night, normal lights, and in another colour than the blink -
// otherwise the look blinks over dark as without the option, so a state never becomes invisible.
func over(s Shown, cfg Config, night bool) Shown {
	bg := cfg.Normal.Color
	if !s.OverNormal || !slices.Contains(OverNormalPatterns, s.Pattern) || s.Color == Off ||
		!cfg.Enabled || night || bg == "" || bg == Off || bg == s.Color {
		return s
	}
	s.Background = bg
	return s
}

// The RPI-RF-MOD's LEDs under /sys/class/leds, in channel order.
var ChannelLEDs = [3]string{"rpi_rf_mod:red", "rpi_rf_mod:green", "rpi_rf_mod:blue"}

// PWRLED is the Pi's red power LED, the error light of a box without an RGB LED.
const PWRLED = "PWR"

// doublePattern is two 150 ms flashes every 2 s in the pattern trigger's syntax: a hard step is a
// repeated brightness with a duration of 0.
const doublePattern = "1 150 1 0 0 150 0 0 1 150 1 0 0 1550 0 0"

// doubleCounter is doublePattern's complement: lit while it is dark (task 134).
const doubleCounter = "0 150 0 0 1 150 1 0 0 150 0 0 1 1550 1 0"

// timerOf is the timer trigger's delays of a blinking pattern.
func timerOf(pattern string) (on, off int) {
	switch pattern {
	case Fast:
		return 100, 100
	case Flash, Double:
		return 100, 1900
	}
	return 500, 500
}

// Frame turns a look into the helper's frame: all three channels, the ones the colour does not
// light at none. Without the pattern trigger, double is shown as flash. Alternate is the timer
// trigger on both colours with the second one started half a period later, as hss_led does.
//
// A background (task 134, Shown.Background) is the colour a blink's off phase shows: the blink
// colour's own channels blink in phase, the background's own channels in the opposite phase (the
// timer trigger with on and off swapped, started when the blink's first on phase ends, or the
// complementary pattern string), and the channels both light stay on.
func Frame(l Look, background string, patternTrigger bool) []priv.LEDWrite {
	first := channels[l.Color]
	var second, under [3]bool
	if l.Pattern == Alternate {
		second = channels[l.Color2]
	} else if l.Pattern != Solid && background != l.Color {
		under = channels[background]
	}
	blink := func(i int, counter, delayed bool) priv.LEDWrite {
		w := priv.LEDWrite{LED: ChannelLEDs[i]}
		switch l.Pattern {
		case Solid:
			w.Trigger = "default-on"
			return w
		case Double:
			if patternTrigger {
				w.Trigger, w.Pattern = "pattern", doublePattern
				if counter {
					w.Pattern = doubleCounter
				}
				return w
			}
		}
		w.Trigger = "timer"
		w.DelayOn, w.DelayOff = timerOf(l.Pattern)
		if counter {
			w.DelayOn, w.DelayOff = w.DelayOff, w.DelayOn
		}
		if delayed {
			w.StartAfterMS = w.DelayOff // when the first on phase ends
			if !counter {
				w.StartAfterMS = w.DelayOn
			}
		}
		return w
	}
	var steady, frame, later, dark []priv.LEDWrite
	for i := range ChannelLEDs {
		switch {
		case first[i] && under[i]:
			steady = append(steady, priv.LEDWrite{LED: ChannelLEDs[i], Trigger: "default-on"})
		case first[i]:
			frame = append(frame, blink(i, false, false))
		case second[i]:
			later = append(later, blink(i, false, len(later) == 0))
		case under[i]:
			later = append(later, blink(i, true, len(later) == 0 && !(l.Pattern == Double && patternTrigger)))
		default:
			dark = append(dark, priv.LEDWrite{LED: ChannelLEDs[i], Trigger: "none"})
		}
	}
	return slices.Concat(steady, frame, later, dark)
}
