package led

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// Task 315: colours as #rrggbb beside the seven names, and their one spelling.
func TestColors(t *testing.T) {
	if rgb, ok := RGB(Yellow); !ok || rgb != [3]uint8{255, 255, 0} {
		t.Errorf("yellow: %v %v", rgb, ok)
	}
	if rgb, ok := RGB("#FF8000"); !ok || rgb != [3]uint8{255, 128, 0} {
		t.Errorf("#FF8000: %v %v", rgb, ok)
	}
	for _, bad := range []string{"orange", "#12345", "#gg0000", "ff8000", ""} {
		if _, ok := RGB(bad); ok {
			t.Errorf("%q taken as a colour", bad)
		}
	}
	if Hex(Blue) != "#0000ff" || Hex("#FF8000") != "#ff8000" || Hex("orange") != "" {
		t.Errorf("hex: %q %q %q", Hex(Blue), Hex("#FF8000"), Hex("orange"))
	}
	for in, want := range map[string]string{"#0000FF": Blue, "#000000": Off, "#ffffff": White, "#FF8000": "#ff8000", Red: Red} {
		if got, ok := normalizeColor(in); !ok || got != want {
			t.Errorf("normalize %q: %q %v, want %q", in, got, ok, want)
		}
	}
	// a look in a free colour, breathing
	l, err := NormalizeLook(Look{Color: "#FF8000", Pattern: Breathe, OverNormal: true})
	if err != nil || l != (Look{Color: "#ff8000", Pattern: Breathe, OverNormal: true}) {
		t.Errorf("free colour: %+v %v", l, err)
	}
	if l, err := NormalizeLook(Look{Color: "#000000", Pattern: Breathe}); err != nil || l != LookDark {
		t.Errorf("black is off: %+v %v", l, err)
	}
	// alternate: the colours must not share a channel, on an on/off LED their timers run in opposite phases
	if _, err := NormalizeLook(Look{Color: "#ff8000", Pattern: Alternate, Color2: Blue}); err != nil {
		t.Errorf("orange and blue alternate: %v", err)
	}
	if _, err := NormalizeLook(Look{Color: "#ff8000", Pattern: Alternate, Color2: "#00ff00"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("orange and green share a channel: %v", err)
	}
	// a file from before task 315: no brightness, no fade, no dim - the defaults
	old := Defaults()
	old.Brightness, old.Fade, old.Night.Dim = 0, nil, 0
	v, err := Validate(old)
	if err != nil || v.Brightness != 100 || !v.Fades() || v.Night.Dim != DefaultNightDim {
		t.Errorf("old file: %+v %v", v, err)
	}
	off := false
	c := Defaults()
	c.Brightness, c.Fade, c.Night.Show, c.Night.Dim = 40, &off, "dimmed", 15
	if v, err := Validate(c); err != nil || v.Brightness != 40 || v.Fades() || v.Night.Show != "dimmed" || v.Night.Dim != 15 {
		t.Errorf("brightness, fade, dimmed night: %+v %v", v, err)
	}
	if !slices.Contains(OverNormalPatterns, Breathe) || !slices.Contains(Patterns, Breathe) {
		t.Error("breathe is a pattern that blinks over normal")
	}
}

// Task 315: the night dims what it shows; "dimmed" hides nothing.
func TestResolveNightDimmed(t *testing.T) {
	cfg := Defaults()
	cfg.Night.Enabled = true
	night := at("23:00")
	in := func(show string, dim int, active ...string) Inputs {
		c := cfg
		c.Night.Show, c.Night.Dim = show, dim
		a := map[string]Activity{}
		for _, id := range active {
			a[id] = Activity{Since: night}
		}
		return Inputs{Now: night, Location: time.UTC, Config: c, Active: a}
	}
	if s := Resolve(in("dimmed", 30)); s.Source != SourceNormal || s.Dim != 30 {
		t.Errorf("dimmed night, nothing active: %+v", s)
	}
	if s := Resolve(in("dimmed", 30, StateStatusWarning)); s.ID != StateStatusWarning || s.Dim != 30 {
		t.Errorf("dimmed night shows a warning, dimmed: %+v", s)
	}
	if s := Resolve(in("errors", 30, StateStatusWarning)); s.Source != SourceNight || s.Dim != 0 {
		t.Errorf("errors night hides a warning: %+v", s)
	}
	if s := Resolve(in("errors", 30, StateServiceFailed)); s.ID != StateServiceFailed || s.Dim != 30 {
		t.Errorf("errors night shows an error, dimmed: %+v", s)
	}
	if s := Resolve(in("dimmed", 100, StateServiceFailed)); s.Dim != 0 {
		t.Errorf("dim 100 is no dimming: %+v", s)
	}
	i := in("dimmed", 30)
	i.Locate = &Timed{Look: Look{Color: White, Pattern: Fast}, Until: night.Add(time.Minute)}
	if s := Resolve(i); s.ID != StateLocate || s.Dim != 0 {
		t.Errorf("locate is never dimmed: %+v", s)
	}
	i = in("dimmed", 30)
	i.Overrides = []Override{{ID: "ha", Color: Green, Pattern: Solid, Priority: "normal", Night: "respect", Since: night}}
	if s := Resolve(i); s.Source != SourceOverride || s.Dim != 30 {
		t.Errorf("dimmed night shows an override that respects the night, dimmed: %+v", s)
	}
	// by day nothing is dimmed
	d := in("dimmed", 30, StateStatusWarning)
	d.Now = at("12:00")
	if s := Resolve(d); s.Dim != 0 {
		t.Errorf("day: %+v", s)
	}
}

// Task 315: the frames over PWM with the pattern trigger - levels through the gamma curve, every
// moving channel one hr_pattern with the same timings, fades between looks.
func TestFramePWM(t *testing.T) {
	r, g, b := ChannelLEDs[0], ChannelLEDs[1], ChannelLEDs[2]
	pwm := Render{PatternTrigger: true, MaxBrightness: 255, Brightness: 100}
	none := func(led string, lv int) priv.LEDWrite {
		return priv.LEDWrite{LED: led, Trigger: "none", Brightness: lv}
	}
	pat := func(led, p string) priv.LEDWrite { return priv.LEDWrite{LED: led, Trigger: "pattern", Pattern: p} }
	slow := "255 500 255 0 0 500 0 0"
	for _, c := range []struct {
		name string
		look Look
		bg   string
		r    Render
		want []priv.LEDWrite
	}{
		{"yellow solid", Look{Color: Yellow, Pattern: Solid}, "", pwm, []priv.LEDWrite{none(r, 255), none(g, 255), none(b, 0)}},
		{"yellow slow: both channels one string, in step", Look{Color: Yellow, Pattern: Slow}, "", pwm, []priv.LEDWrite{pat(r, slow), pat(g, slow), none(b, 0)}},
		{"yellow slow over blue: blue the counter phase", Look{Color: Yellow, Pattern: Slow, OverNormal: true}, Blue, pwm, []priv.LEDWrite{pat(r, slow), pat(g, slow), pat(b, "0 500 0 0 255 500 255 0")}},
		{"cyan flash over blue: blue steady, green flashes", Look{Color: Cyan, Pattern: Flash, OverNormal: true}, Blue, pwm, []priv.LEDWrite{pat(g, "255 100 255 0 0 1900 0 0"), none(r, 0), none(b, 255)}},
		{"red double", Look{Color: Red, Pattern: Double}, "", pwm, []priv.LEDWrite{pat(r, "255 150 255 0 0 150 0 0 255 150 255 0 0 1550 0 0"), none(g, 0), none(b, 0)}},
		{"yellow and blue alternate: one cycle on every channel", Look{Color: Yellow, Pattern: Alternate, Color2: Blue}, "", pwm, []priv.LEDWrite{pat(r, slow), pat(g, slow), pat(b, "0 500 0 0 255 500 255 0")}},
		{"red breathe: a cosine in eight glides", Look{Color: Red, Pattern: Breathe}, "", pwm, []priv.LEDWrite{pat(r, "0 250 37 250 128 250 218 250 255 250 218 250 128 250 37 250"), none(g, 0), none(b, 0)}},
		{"red breathe over blue: a cross-fade between the two", Look{Color: Red, Pattern: Breathe, OverNormal: true}, Blue, pwm, []priv.LEDWrite{pat(r, "0 250 37 250 128 250 218 250 255 250 218 250 128 250 37 250"), pat(b, "255 250 218 250 128 250 37 250 0 250 37 250 128 250 218 250"), none(g, 0)}},
		{"orange: the gamma curve on the green channel", Look{Color: "#ff8000", Pattern: Solid}, "", pwm, []priv.LEDWrite{none(r, 255), none(g, 56), none(b, 0)}},
		{"brightness 50 %", Look{Color: Red, Pattern: Solid}, "", Render{PatternTrigger: true, MaxBrightness: 255, Brightness: 50}, []priv.LEDWrite{none(r, 55), none(g, 0), none(b, 0)}},
		{"night dim 30 %", Look{Color: Red, Pattern: Solid}, "", Render{PatternTrigger: true, MaxBrightness: 255, Brightness: 100, Dim: 30}, []priv.LEDWrite{none(r, 18), none(g, 0), none(b, 0)}},
		{"a lit channel never below the floor", Look{Color: "#010101", Pattern: Solid}, "", pwm, []priv.LEDWrite{none(r, 8), none(g, 8), none(b, 8)}},
		{"dark", LookDark, "", pwm, []priv.LEDWrite{none(r, 0), none(g, 0), none(b, 0)}},
		// PWM without the pattern trigger: the timer trigger, dimmed where the level is below the maximum
		{"orange slow without the pattern trigger", Look{Color: "#ff8000", Pattern: Slow}, "", Render{MaxBrightness: 255, Brightness: 100}, []priv.LEDWrite{{LED: r, Trigger: "timer", DelayOn: 500, DelayOff: 500}, {LED: g, Trigger: "timer", DelayOn: 500, DelayOff: 500, Brightness: 56}, {LED: b, Trigger: "none"}}},
		{"orange solid without the pattern trigger", Look{Color: "#ff8000", Pattern: Solid}, "", Render{MaxBrightness: 255, Brightness: 100}, []priv.LEDWrite{{LED: r, Trigger: "default-on"}, none(g, 56), {LED: b, Trigger: "none"}}},
		{"breathe without the pattern trigger is slow", Look{Color: Red, Pattern: Breathe}, "", Render{MaxBrightness: 255, Brightness: 100}, []priv.LEDWrite{{LED: r, Trigger: "timer", DelayOn: 500, DelayOff: 500}, {LED: g, Trigger: "none"}, {LED: b, Trigger: "none"}}},
		// an on/off LED: a free colour is the nearest of the seven, dimming is ignored
		{"orange on an on/off LED is yellow", Look{Color: "#ff8000", Pattern: Solid}, "", Render{PatternTrigger: true, MaxBrightness: 1, Brightness: 40, Dim: 30}, Frame(Look{Color: Yellow, Pattern: Solid}, "", true)},
		{"a dark orange on an on/off LED is red", Look{Color: "#ff7000", Pattern: Slow}, "", Render{PatternTrigger: true, MaxBrightness: 1}, Frame(Look{Color: Red, Pattern: Slow}, "", true)},
	} {
		got := FrameFor(c.look, c.bg, c.r)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, got, c.want)
		}
		if err := priv.ValidLEDFrame(got); err != nil {
			t.Errorf("%s: the helper refuses the frame: %v", c.name, err)
		}
	}
	// every look the page can make renders to a frame the helper takes, over PWM too
	for _, color := range append(slices.Clone(Colors), "#ff8000", "#102030") {
		for _, p := range Patterns {
			for _, bg := range []string{"", Blue, "#ff8000"} {
				l := Look{Color: color, Pattern: p, Color2: Blue, OverNormal: true}
				if p == Alternate && !Disjoint(color, Blue) {
					l.Color2 = Red
				}
				if err := priv.ValidLEDFrame(FrameFor(l, bg, pwm)); err != nil {
					t.Errorf("%s %s over %q: %v", color, p, bg, err)
				}
			}
		}
	}
	// the fade: from what each channel showed to what it shows first now
	yellow := FrameFor(Look{Color: Yellow, Pattern: Solid}, "", pwm)
	blue := FrameFor(Look{Color: Blue, Pattern: Solid}, "", pwm)
	got := WithFade(blue, yellow, pwm)
	want := []priv.LEDWrite{{LED: r, Trigger: "none", Fade: "255 300 0 0"}, {LED: g, Trigger: "none", Fade: "255 300 0 0"}, {LED: b, Trigger: "none", Brightness: 255, Fade: "0 300 255 0"}}
	if !slices.Equal(got, want) {
		t.Errorf("fade yellow -> blue:\n got %+v\nwant %+v", got, want)
	}
	if err := priv.ValidLEDFrame(got); err != nil {
		t.Errorf("the helper refuses the fading frame: %v", err)
	}
	// a channel whose first level stays does not fade; a dimmed look fades to its level
	redSlow := FrameFor(Look{Color: Red, Pattern: Slow}, "", pwm)
	redSolid := FrameFor(Look{Color: Red, Pattern: Solid}, "", pwm)
	if got := WithFade(redSlow, redSolid, pwm); got[0].Fade != "" || !strings.HasPrefix(got[0].Pattern, "255 500") {
		t.Errorf("red solid -> red slow: %+v", got)
	}
	dim := Render{PatternTrigger: true, MaxBrightness: 255, Brightness: 100, Dim: 30}
	if got := WithFade(FrameFor(Look{Color: Red, Pattern: Solid}, "", dim), redSolid, dim); got[0].Fade != "255 300 18 0" {
		t.Errorf("into the night: %+v", got)
	}
	// no fade on an on/off LED, without the pattern trigger, or without a previous frame
	if got := WithFade(Frame(Look{Color: Blue, Pattern: Solid}, "", true), Frame(Look{Color: Red, Pattern: Solid}, "", true), Render{PatternTrigger: true, MaxBrightness: 1}); got[0].Fade != "" || got[2].Fade != "" {
		t.Errorf("on/off LED fades: %+v", got)
	}
	if got := WithFade(blue, yellow, Render{MaxBrightness: 255}); got[2].Fade != "" {
		t.Errorf("without the pattern trigger: %+v", got)
	}
	if got := WithFade(blue, nil, pwm); got[2].Fade != "" {
		t.Errorf("without a previous frame: %+v", got)
	}
}

// Task 315: the probe reads max_brightness - 255 over PWM, 1 on an on/off LED or an older image.
func TestProbeBrightness(t *testing.T) {
	b := charly()
	hw := Probe(b.root(t), nil)
	if hw.Brightness || hw.MaxBrightness != 1 || slices.Contains(hw.Patterns, Breathe) {
		t.Errorf("on/off LED: %+v", hw)
	}
	for _, l := range ChannelLEDs {
		b["sys/class/leds/"+l+"/max_brightness"] = "255\n"
	}
	// openccu-lite task 326: 255 is not enough - gpio-leds' LED stays on/off
	if hw := Probe(b.root(t), nil); hw.Brightness || hw.MaxBrightness != 1 || !hw.PatternTrigger {
		t.Errorf("gpio-leds with 255: %+v", hw)
	}
	b = ledDevice(b, "leds_pwm", "rpi_rf_mod_leds")
	hw = Probe(b.root(t), nil)
	if !hw.Brightness || hw.MaxBrightness != 255 || !hw.PatternTrigger || hw.Kind != KindHeader || !slices.Contains(hw.Patterns, Breathe) || !slices.Contains(hw.Patterns, Double) {
		t.Errorf("PWM: %+v", hw)
	}
	// one channel on/off: the LED counts as on/off
	b["sys/class/leds/"+ChannelLEDs[2]+"/max_brightness"] = "1\n"
	if hw := Probe(b.root(t), nil); hw.Brightness || hw.MaxBrightness != 1 {
		t.Errorf("mixed: %+v", hw)
	}
}

// openccu-lite task 326: an RPI-RF-MOD on a radio adapter - its LED is rpi_rf_mod_led's (no parent
// device, max_brightness 255 as the LED class default), on or off per channel; no levels, no
// pattern trigger, so no breathe and no double, and the kind names the adapter. The same LED names
// held by the header's leds_pwm while the module sits on an adapter (dev.39/40 before fix A) are no
// better: the header pins lead nowhere.
func TestProbeAdapterLED(t *testing.T) {
	for _, tc := range []struct{ devtype, kind string }{
		{"HB-RF-USB@usb-1-1.3", KindUSB},
		{"HB-RF-USB-2@usb-1-1.2", KindUSB2},
		{"HB-RF-ETH@192.0.2.9", KindETH},
	} {
		b := charly()
		for k := range b {
			if strings.HasSuffix(k, "/device") || strings.HasPrefix(k, "sys/devices/") || strings.HasPrefix(k, "sys/bus/") {
				delete(b, k)
			}
		}
		b["var/hm_mode"] = "HM_HOST='ova'\nHM_MODE='NORMAL'\nHM_HMIP_DEV='RPI-RF-MOD'\nHM_HMIP_DEVTYPE='" + tc.devtype + "'\nHM_HMRF_DEV='RPI-RF-MOD'\nHM_HMRF_DEVTYPE='" + tc.devtype + "'\n"
		for _, l := range ChannelLEDs {
			b["sys/class/leds/"+l+"/max_brightness"] = "255\n"
			b["sys/class/leds/"+l+"/trigger"] = "[none] default-on timer heartbeat pattern"
		}
		hw := Probe(b.root(t), nil)
		if !hw.Available || hw.Kind != tc.kind || hw.Brightness || hw.MaxBrightness != 1 || hw.PatternTrigger || slices.Contains(hw.Patterns, Breathe) || slices.Contains(hw.Patterns, Double) {
			t.Errorf("%s: %+v", tc.devtype, hw)
		}
		// the names still held by the header's leds_pwm: on/off all the same
		b = ledDevice(b, "leds_pwm", "rpi_rf_mod_leds")
		if hw := Probe(b.root(t), nil); hw.Kind != tc.kind || hw.Brightness || hw.PatternTrigger {
			t.Errorf("%s with leds_pwm: %+v", tc.devtype, hw)
		}
	}
}

// openccu-lite task 326: what the controller writes to an adapter LED - only none, default-on and
// timer: no hr_pattern, no levels, no fade, whatever the look asks for.
func TestAdapterLEDGetsTheTimerPath(t *testing.T) {
	r := Render{PatternTrigger: false, MaxBrightness: 1}
	for _, l := range []Look{
		{Color: "#ff8000", Pattern: Breathe},
		{Color: Blue, Pattern: Double},
		{Color: Cyan, Pattern: Fast},
		{Color: Magenta, Pattern: Solid},
		{Color: Red, Pattern: Alternate, Color2: Blue},
	} {
		prev := FrameFor(Look{Color: Green, Pattern: Solid}, "", r)
		fr := WithFade(FrameFor(l, "", r), prev, r)
		for i, c := range fr {
			if c.Trigger != "none" && c.Trigger != "default-on" && c.Trigger != "timer" {
				t.Errorf("%+v channel %d: trigger %q", l, i, c.Trigger)
			}
			if c.Fade != "" || c.Pattern != "" || (c.Brightness != 0 && c.Brightness != 1) {
				t.Errorf("%+v channel %d: %+v", l, i, c)
			}
		}
	}
}
