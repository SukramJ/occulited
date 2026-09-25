package logctl

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, c := range []struct {
		in   Setting
		want Setting
		bad  bool
	}{
		{Setting{}, Setting{Level: "info", DebugAreas: []string{}}, false},
		{Setting{Level: "DEBUG"}, Setting{Level: "debug", DebugAreas: []string{}}, false},
		{Setting{Level: " warn "}, Setting{Level: "warn", DebugAreas: []string{}}, false},
		// the areas in the order of Areas, once each
		{Setting{Level: "info", DebugAreas: []string{"led", "acme", "LED", "http"}}, Setting{Level: "info", DebugAreas: []string{"acme", "http", "led"}}, false},
		{Setting{Level: "trace"}, Setting{}, true},
		{Setting{Level: "info", DebugAreas: []string{"everything"}}, Setting{}, true},
	} {
		got, err := Normalize(c.in)
		if (err != nil) != c.bad {
			t.Errorf("Normalize(%+v) error %v", c.in, err)
			continue
		}
		if !c.bad && (got.Level != c.want.Level || strings.Join(got.DebugAreas, ",") != strings.Join(c.want.DebugAreas, ",")) {
			t.Errorf("Normalize(%+v) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// The level changes while the loggers exist: a debug line appears after the switch to debug and is
// gone again after the switch back, with the same logger and no new handler.
func TestLiveLevel(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	c := New()
	log := slog.New(c.Handler(base))
	acme := c.Logger(base, "acme").With("names", "ccu.example")

	log.Debug("before")
	log.Info("info line")
	if strings.Contains(buf.String(), "before") || !strings.Contains(buf.String(), "info line") {
		t.Fatalf("at info: %q", buf.String())
	}
	if _, err := c.Set(Setting{Level: "debug"}); err != nil {
		t.Fatal(err)
	}
	log.Debug("while debug")
	acme.Debug("acme while debug")
	if !strings.Contains(buf.String(), `msg="while debug"`) || !strings.Contains(buf.String(), `msg="acme while debug" names=ccu.example`) {
		t.Fatalf("at debug: %q", buf.String())
	}
	buf.Reset()
	if _, err := c.Set(Setting{Level: "info"}); err != nil {
		t.Fatal(err)
	}
	log.Debug("after")
	acme.Debug("acme after")
	if buf.Len() != 0 {
		t.Fatalf("back at info: %q", buf.String())
	}
	// warn hides info
	if _, err := c.Set(Setting{Level: "warn"}); err != nil {
		t.Fatal(err)
	}
	log.Info("hidden")
	log.Warn("shown")
	if strings.Contains(buf.String(), "hidden") || !strings.Contains(buf.String(), "shown") {
		t.Fatalf("at warn: %q", buf.String())
	}
}

// Debug for one area: that area's loggers write every line from debug up, the others and the
// general logger only what the level lets through - also when the level is error.
func TestDebugAreas(t *testing.T) {
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	c := New()
	log := slog.New(c.Handler(base))
	acme := c.Logger(base, "acme")
	http := c.Logger(base, "http")
	got, err := c.Set(Setting{Level: "info", DebugAreas: []string{"acme"}})
	if err != nil || strings.Join(got.DebugAreas, ",") != "acme" {
		t.Fatalf("%+v %v", got, err)
	}
	log.Debug("general debug")
	acme.Debug("acme debug")
	http.Debug("http debug")
	acme.WithGroup("g").Debug("acme grouped")
	out := buf.String()
	if strings.Contains(out, "general debug") || strings.Contains(out, "http debug") || !strings.Contains(out, "acme debug") || !strings.Contains(out, "acme grouped") {
		t.Fatalf("areas: %q", out)
	}
	if g := c.Get(); g.Level != "info" || strings.Join(g.DebugAreas, ",") != "acme" {
		t.Errorf("Get %+v", g)
	}
	buf.Reset()
	c.Set(Setting{Level: "error", DebugAreas: []string{"acme"}})
	acme.Info("acme info")
	acme.Debug("acme debug at error")
	http.Info("http info at error")
	log.Warn("general warn at error")
	out = buf.String()
	if !strings.Contains(out, "acme info") || !strings.Contains(out, "acme debug at error") || strings.Contains(out, "http info") || strings.Contains(out, "general warn") {
		t.Fatalf("area at error: %q", out)
	}
	// a refused setting changes nothing
	if _, err := c.Set(Setting{Level: "loud"}); err == nil {
		t.Error("accepted a bad level")
	}
	if g := c.Get(); g.Level != "error" {
		t.Errorf("after a refusal %+v", g)
	}
	// Get hands out a copy
	g := c.Get()
	g.DebugAreas[0] = "led"
	if c.Get().DebugAreas[0] != "acme" {
		t.Error("Get shares its slice")
	}
}

func TestZeroControl(t *testing.T) {
	var c Control
	if g := c.Get(); g.Level != "info" {
		t.Errorf("zero Get %+v", g)
	}
	var buf bytes.Buffer
	log := slog.New(c.Handler(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.Debug("x")
	log.Info("y")
	if strings.Contains(buf.String(), "x") || !strings.Contains(buf.String(), "y") {
		t.Errorf("zero control: %q", buf.String())
	}
	for name, want := range map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError, "?": slog.LevelInfo} {
		if SlogLevel(name) != want {
			t.Errorf("SlogLevel(%q)", name)
		}
	}
}

// areaBase records the area it was asked to mark (task 186: the journal handler's OCCULITED_AREA).
type areaBase struct {
	slog.Handler
	area *string
}

func (b areaBase) WithArea(area string) slog.Handler {
	*b.area = area
	return b
}

func TestAreaLoggerMarksItsLines(t *testing.T) {
	var marked string
	c := New()
	c.Logger(areaBase{Handler: slog.NewTextHandler(io.Discard, nil), area: &marked}, "acme")
	if marked != "acme" {
		t.Errorf("the base marked %q", marked)
	}
	// a base that cannot mark is used as it is
	if l := c.Logger(slog.NewTextHandler(io.Discard, nil), "led"); l == nil {
		t.Error("no logger")
	}
}
