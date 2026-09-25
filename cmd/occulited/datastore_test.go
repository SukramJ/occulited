package main

import (
	"path/filepath"
	"testing"

	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/devstate"
	"github.com/hobbyquaker/occulited/internal/store"
)

// task 195: the history list's setting is kept in occulited.json beside the mode, and a change
// of the mode keeps it.
func TestDataStoreHistorySetting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "occulited.json")
	if err := config.Save(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	h := &devstate.History{}
	m := &store.Manager{Path: filepath.Join(dir, "data", "occulited.db"), Platform: "rpi4"}
	m.Register(h)
	m.Start("ram", "")
	d := &dataStore{path: path, stateDir: dir, m: m, h: h}
	if err := d.SetHistory([]string{"DOOR_STATE"}, []string{"PRESS_LONG"}); err != nil {
		t.Fatal(err)
	}
	if err := d.SetHistory([]string{"no good"}, nil); err == nil {
		t.Error("a bad name was taken")
	}
	v := d.History()
	if v.Datapoints["DOOR_STATE"] != devstate.ShapeEvent || v.Datapoints["PRESS_LONG"] != "" || len(v.Add) != 1 || len(v.Remove) != 1 || len(v.Defaults) != len(devstate.HistoryDefaults) || v.RowsPerSeries != 500 {
		t.Errorf("view %+v", v)
	}
	if err := d.Set("ram", "6h", ""); err != nil {
		t.Fatal(err)
	}
	cfg, _ := config.Load(path)
	if len(cfg.Store.HistoryAdd) != 1 || cfg.Store.SyncInterval != "6h" {
		t.Errorf("the mode's save lost the list: %+v", cfg.Store)
	}
}
