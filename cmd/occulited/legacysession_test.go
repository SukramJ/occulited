package main

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/hobbyquaker/occulited/internal/config"
)

// Task 125: the switches live in occulited.json's addons block; absent means on.
func TestLegacySwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occulited.json")
	l := &legacySwitches{path: path}
	if on, off := l.LegacySession(); !on || len(off) != 0 {
		t.Fatalf("without a file: %v %v", on, off)
	}
	if err := l.SetLegacySession(false, []string{"hmm"}); err != nil {
		t.Fatal(err)
	}
	if on, off := l.LegacySession(); on || !slices.Equal(off, []string{"hmm"}) {
		t.Errorf("stored: %v %v", on, off)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Addons.LegacySessionOn() || cfg.Addons.DefaultMode != "confined" {
		t.Errorf("the file: %v %+v", err, cfg.Addons)
	}
	if err := l.SetLegacySession(true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(path)
	if cfg.Addons.LegacySession != nil || cfg.Addons.LegacySessionOff != nil {
		t.Errorf("on is the absent default: %+v", cfg.Addons)
	}
}

// Task 119: the early start's switches live beside them; absent means on, and On answers per addon.
func TestEarlySwitches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "occulited.json")
	e := &earlySwitches{path: path}
	if on, off := e.EarlyStart(); !on || len(off) != 0 || !e.On("hmm") {
		t.Fatalf("without a file: %v %v", on, off)
	}
	if err := e.SetEarlyStart(true, []string{"hmm"}); err != nil {
		t.Fatal(err)
	}
	if e.On("hmm") || !e.On("redmatic") {
		t.Errorf("hmm off: %v %v", e.On("hmm"), e.On("redmatic"))
	}
	// the legacy session's switches in the same block are left alone
	l := &legacySwitches{path: path}
	if err := l.SetLegacySession(false, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.SetEarlyStart(false, []string{"hmm"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil || cfg.Addons.EarlyStartOn() || !slices.Equal(cfg.Addons.EarlyStartOff, []string{"hmm"}) || cfg.Addons.LegacySessionOn() {
		t.Errorf("the file: %v %+v", err, cfg.Addons)
	}
	if e.On("redmatic") {
		t.Error("globally off, redmatic still on")
	}
	if err := e.SetEarlyStart(true, nil); err != nil {
		t.Fatal(err)
	}
	cfg, _ = config.Load(path)
	if cfg.Addons.EarlyStart != nil || cfg.Addons.EarlyStartOff != nil {
		t.Errorf("on is the absent default: %+v", cfg.Addons)
	}
}
