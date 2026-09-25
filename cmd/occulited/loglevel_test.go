package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/logctl"
)

// task 101: occulited's level is stored in occulited.json and comes back at the next start; the
// store keeps what another route wrote to the file, and a setting that cannot be stored is not
// applied.
func TestOwnLogLevelSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occulited.json")
	cfg := config.Default()
	cfg.Auth.Mode = "off" // written by the auth route, must stay
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	base := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	ctl := logctl.New()
	var inMemory logctl.Setting
	own := &ownLogLevel{ctl: ctl, cfgPath: path, log: slog.New(ctl.Handler(base)), stored: func(s logctl.Setting) { inMemory = s }}

	got, err := own.Set(context.Background(), logctl.Setting{Level: "info", DebugAreas: []string{"led", "acme"}})
	if err != nil || strings.Join(got.DebugAreas, ",") != "acme,led" {
		t.Fatalf("%+v %v", got, err)
	}
	acme := ctl.Logger(base, "acme")
	acme.Debug("acme debug line")
	if !strings.Contains(buf.String(), "acme debug line") || !strings.Contains(buf.String(), "log level of occulited changed") {
		t.Errorf("log: %q", buf.String())
	}
	if inMemory.Level != "info" || strings.Join(inMemory.DebugAreas, ",") != "acme,led" {
		t.Errorf("the in-memory configuration did not follow: %+v", inMemory)
	}

	// the next start: the file gives the same setting, and the other keys are as they were
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LogLevel != "info" || strings.Join(loaded.LogDebugAreas, ",") != "acme,led" || loaded.Auth.Mode != "off" {
		t.Errorf("stored %+v %v auth %q", loaded.LogLevel, loaded.LogDebugAreas, loaded.Auth.Mode)
	}
	next := logctl.New()
	if s, err := next.Set(logctl.Setting{Level: loaded.LogLevel, DebugAreas: loaded.LogDebugAreas}); err != nil || s.Level != "info" || strings.Join(s.DebugAreas, ",") != "acme,led" {
		t.Errorf("restart: %+v %v", s, err)
	}

	// back to plain warn: no areas key in the file
	if _, err := own.Set(context.Background(), logctl.Setting{Level: "warn"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"log_level": "warn"`) || strings.Contains(string(b), "log_debug_areas") {
		t.Errorf("file:\n%s", b)
	}

	// a refused setting and a file that cannot be written change nothing
	if _, err := own.Set(context.Background(), logctl.Setting{Level: "verbose"}); err == nil {
		t.Error("accepted verbose")
	}
	own.cfgPath = filepath.Join(dir, "missing-dir", "occulited.json")
	if _, err := own.Set(context.Background(), logctl.Setting{Level: "debug"}); err == nil {
		t.Error("stored into a missing directory")
	}
	if ctl.Get().Level != "warn" {
		t.Errorf("applied without being stored: %+v", ctl.Get())
	}
}
