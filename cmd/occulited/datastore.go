package main

import (
	"fmt"
	"os"
	"sort"
	"sync"

	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/devstate"
	"github.com/hobbyquaker/occulited/internal/httpapi"
	"github.com/hobbyquaker/occulited/internal/store"
	"github.com/hobbyquaker/occulited/internal/system"
)

// dataStore is httpapi.DataStore: the store manager's mode and interval, kept in occulited.json's
// store block (task 214). The file is read on every write - another writer may have rewritten it
// - and written back around one lock.
type dataStore struct {
	mu       sync.Mutex
	path     string
	stateDir string
	m        *store.Manager
	h        *devstate.History
	// root finds a USB stick by its label (task 229's snapshot); "" = the system's
	root system.Root
}

// stickMirror is the USB stick the database's snapshot goes to (task 229): found by its label
// wherever it is mounted, written through the helper.
type stickMirror struct {
	root          system.Root
	label, folder string
}

func (s stickMirror) Target() (string, string) { return s.label, s.folder }

func (s stickMirror) Dir() (string, bool) {
	st, ok := s.root.USBStickByLabel(s.label)
	if !ok || st.ReadOnly {
		return "", false
	}
	return s.root.Path(st.Mount + "/" + s.folder), true
}

func (s stickMirror) Copy(src string) error {
	dir, ok := s.Dir()
	if !ok {
		return fmt.Errorf("the USB stick %s is not plugged in", s.label)
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	return system.Priv.StoreCopy(f, dir)
}

// mirrorFor is the stick of a usb: location, nil for any other.
func mirrorFor(root system.Root, location string) store.Mirror {
	label, folder, err := store.ParseUSB(location)
	if err != nil {
		return nil
	}
	if root == "" {
		root = "/"
	}
	return stickMirror{root: root, label: label, folder: folder}
}

// History is the datapoint history's list as the setting and the defaults make it (task 195).
func (d *dataStore) History() httpapi.HistoryView {
	d.mu.Lock()
	defer d.mu.Unlock()
	v := httpapi.HistoryView{Datapoints: d.h.List(), Add: []string{}, Remove: []string{}, Series: d.h.Count(), Capped: d.h.Capped(), RowsPerSeries: store.DefaultRows, MaxSeries: devstate.MaxSeries}
	for k := range devstate.HistoryDefaults {
		v.Defaults = append(v.Defaults, k)
	}
	sort.Strings(v.Defaults)
	if cfg, err := config.Load(d.path); err == nil {
		v.Add = append(v.Add, cfg.Store.HistoryAdd...)
		v.Remove = append(v.Remove, cfg.Store.HistoryRemove...)
	}
	return v
}

// SetHistory stores and applies the list's additions and removals.
func (d *dataStore) SetHistory(add, remove []string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, k := range append(append([]string{}, add...), remove...) {
		if !devstate.ValidDatapoint(k) {
			return fmt.Errorf("%q is not a datapoint name (A-Z, 0-9, _)", k)
		}
	}
	cfg, err := config.Load(d.path)
	if err != nil {
		return err
	}
	cfg.Store.HistoryAdd, cfg.Store.HistoryRemove = add, remove
	if err := config.Save(d.path, cfg); err != nil {
		return err
	}
	d.h.SetList(add, remove)
	return nil
}

func (d *dataStore) Status() store.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	interval, location := "", ""
	if cfg, err := config.Load(d.path); err == nil {
		interval, location = cfg.Store.SyncInterval, cfg.Store.Location
	}
	return d.m.Status(interval, location)
}

func (d *dataStore) Set(mode, interval, location string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := store.ValidateFor(d.m.Platform, mode, interval, location); err != nil {
		return err
	}
	cfg, err := config.Load(d.path)
	if err != nil {
		return err
	}
	cfg.Store.Mode, cfg.Store.SyncInterval, cfg.Store.Location = mode, interval, location
	if err := config.Save(d.path, cfg); err != nil {
		return err
	}
	d.m.SetMirror(mirrorFor(d.root, location))
	return d.m.Set(mode, interval, store.FilePath(location, d.stateDir))
}
