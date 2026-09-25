// Package logctl is occulited's own log level, changed while it runs (task 101).
//
// One level for the whole daemon - debug, info (the default), warn, error - held in a
// slog.LevelVar, so a change applies to the next line without a restart. Beside it, debug can be
// switched on for a few areas only (acme, radio-firmware, addons, metadata, http, auth, led): a
// debug hunt in one corner then does not flood the journal with every request line of the others,
// which matters on a box whose journal lives in RAM or is copied to an SD card (task 85).
//
// Every logger the daemon hands out wraps one base handler, and the base handler itself lets
// everything through (debug): the decision is the wrapper's, taken per line against the current
// setting. A logger made for an area also writes that area's debug lines while the area is on.
package logctl

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Levels are the names the setting takes, lowest first.
var Levels = []string{"debug", "info", "warn", "error"}

// Areas are the parts of occulited whose debug lines can be switched on on their own.
var Areas = []string{"acme", "radio-firmware", "addons", "metadata", "http", "auth", "led", "rpc"}

// DefaultLevel is the level of a box that never set one.
const DefaultLevel = "info"

// Setting is what the Log settings show and store: the level, and the areas that log at debug
// while the level is above it. At debug the areas say nothing more and are kept as they were.
type Setting struct {
	Level      string   `json:"level"`
	DebugAreas []string `json:"debug_areas"`
}

// Normalize checks a setting and gives it its canonical shape: the level in lower case (empty is
// the default), the areas known, once each and in the order of Areas.
func Normalize(s Setting) (Setting, error) {
	lvl := strings.ToLower(strings.TrimSpace(s.Level))
	if lvl == "" {
		lvl = DefaultLevel
	}
	if !contains(Levels, lvl) {
		return Setting{}, fmt.Errorf("occulited's level is one of %s", strings.Join(Levels, ", "))
	}
	seen := map[string]bool{}
	for _, a := range s.DebugAreas {
		a = strings.ToLower(strings.TrimSpace(a))
		if !contains(Areas, a) {
			return Setting{}, fmt.Errorf("unknown debug area %q; the areas are %s", a, strings.Join(Areas, ", "))
		}
		seen[a] = true
	}
	out := Setting{Level: lvl, DebugAreas: []string{}}
	for _, a := range Areas {
		if seen[a] {
			out.DebugAreas = append(out.DebugAreas, a)
		}
	}
	return out, nil
}

// SlogLevel is the slog level of a normalised level name; info for anything else.
func SlogLevel(name string) slog.Level {
	switch strings.ToLower(name) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

// Control holds the setting in effect. The zero value logs at info with no area at debug.
type Control struct {
	level slog.LevelVar

	mu    sync.RWMutex
	set   Setting
	areas map[string]bool
}

// New is a Control at the default level.
func New() *Control {
	c := &Control{}
	c.set = Setting{Level: DefaultLevel, DebugAreas: []string{}}
	return c
}

// Set applies a setting at once; the next line of every logger follows it.
func (c *Control) Set(s Setting) (Setting, error) {
	n, err := Normalize(s)
	if err != nil {
		return Setting{}, err
	}
	areas := map[string]bool{}
	for _, a := range n.DebugAreas {
		areas[a] = true
	}
	c.mu.Lock()
	c.set, c.areas = n, areas
	c.mu.Unlock()
	c.level.Set(SlogLevel(n.Level))
	return n, nil
}

// Get is the setting in effect.
func (c *Control) Get() Setting {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.set
	if s.Level == "" {
		s.Level = DefaultLevel
	}
	s.DebugAreas = append([]string{}, s.DebugAreas...)
	return s
}

func (c *Control) areaOn(area string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.areas[area]
}

// Handler wraps base, which should let every level through, for the daemon's general lines.
func (c *Control) Handler(base slog.Handler) slog.Handler {
	return &handler{c: c, inner: base}
}

// AreaHandler is a base handler that marks an area's lines (task 186: the journal handler
// writes the area as OCCULITED_AREA, which the Log page filters on).
type AreaHandler interface {
	WithArea(area string) slog.Handler
}

// Logger is a logger over base for one of Areas: it writes debug lines while that area is on, and
// its lines carry the area where base can mark them (AreaHandler).
func (c *Control) Logger(base slog.Handler, area string) *slog.Logger {
	inner := base
	if a, ok := base.(AreaHandler); ok {
		inner = a.WithArea(area)
	}
	return slog.New(&handler{c: c, inner: inner, area: area})
}

type handler struct {
	c     *Control
	inner slog.Handler
	area  string
}

func (h *handler) Enabled(ctx context.Context, l slog.Level) bool {
	if l >= h.c.level.Level() || (h.area != "" && l >= slog.LevelDebug && h.c.areaOn(h.area)) {
		return h.inner.Enabled(ctx, l)
	}
	return false
}

func (h *handler) Handle(ctx context.Context, r slog.Record) error { return h.inner.Handle(ctx, r) }

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{c: h.c, inner: h.inner.WithAttrs(attrs), area: h.area}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{c: h.c, inner: h.inner.WithGroup(name), area: h.area}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
