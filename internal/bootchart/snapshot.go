package bootchart

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Snapshots (task 93): the timeline of a finished boot kept on the userfs - systemd has the units'
// stamps of the running boot only - so the Services page can show an earlier boot and compare with
// the previous one. <state dir>/boots/<boot id>.json.gz: the Charly's 209 units are 51 KB of JSON
// and 7.5 KB gzipped. The last 10 are kept on every box, the SD card boxes included (D-64), and the
// directory carries .nobackup, so the nightly backup leaves it out.

// DefaultKeep is how many boots are kept.
const DefaultKeep = 10

// ErrNoSnapshot: no kept timeline of that boot.
var ErrNoSnapshot = errors.New("no timeline of that boot is kept")

var snapshotID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// snapshot is a kept timeline as written: when it was recorded, on a clock that was trusted then.
type snapshot struct {
	Timeline
	RecordedMS int64 `json:"recorded_ms"`
}

// Kept is one kept boot as GET /boots lists it.
type Kept struct {
	BootID      string `json:"boot_id"`
	Started     string `json:"started,omitempty"`
	RecordedMS  int64  `json:"recorded_ms"`
	TotalMS     *int64 `json:"total_ms,omitempty"`
	UserspaceMS *int64 `json:"userspace_ms,omitempty"`
}

// Store keeps the snapshots in a directory.
type Store struct {
	// Dir is <state dir>/boots.
	Dir string
	// Keep is how many boots stay; 0 = DefaultKeep.
	Keep int
	// Now is the wall clock; nil = time.Now.
	Now func() time.Time
	Log *slog.Logger
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) keep() int {
	if s.Keep > 0 {
		return s.Keep
	}
	return DefaultKeep
}

func (s *Store) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Store) path(id string) string { return filepath.Join(s.Dir, id+".json.gz") }

func (s *Store) netPath(id string) string { return filepath.Join(s.Dir, id+".net.json") }

// SaveNetwork keeps a boot's no-link record (openccu-lite B-249) beside its timeline, replacing an
// earlier one of the same boot: the watch writes it at its decision and again at its end. It is
// written whether or not the boot's timeline is ever kept - a boot without a network may never get
// a trusted clock - and goes with the boot when that is pruned.
func (s *Store) SaveNetwork(id string, v any) error {
	if !snapshotID.MatchString(id) {
		return errors.New("not a boot id")
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.Dir, ".nobackup"), nil, 0o644); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.Dir, ".net-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.netPath(id)); err != nil {
		return err
	}
	return s.prune()
}

// Network is a boot's no-link record, nil when it has none.
func (s *Store) Network(id string) json.RawMessage {
	if !snapshotID.MatchString(id) {
		return nil
	}
	b, err := os.ReadFile(s.netPath(id))
	if err != nil || !json.Valid(b) {
		return nil
	}
	return json.RawMessage(b)
}

// Has tells whether a boot's timeline is kept.
func (s *Store) Has(id string) bool {
	if !snapshotID.MatchString(id) {
		return false
	}
	_, err := os.Stat(s.path(id))
	return err == nil
}

// Save keeps the timeline of a finished boot - once per boot - and drops the oldest beyond Keep.
func (s *Store) Save(t *Timeline) error {
	if t == nil || !t.Finished || !snapshotID.MatchString(t.BootID) {
		return errors.New("only a finished boot's timeline is kept")
	}
	if s.Has(t.BootID) {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o750); err != nil {
		return err
	}
	// the nightly backup (createBackup.sh, tar --exclude-tag=.nobackup) leaves the directory out
	if err := os.WriteFile(filepath.Join(s.Dir, ".nobackup"), nil, 0o644); err != nil {
		return err
	}
	snap := snapshot{Timeline: *t, RecordedMS: s.now().UnixMilli()}
	snap.Snapshot = false
	snap.Previous = nil
	snap.Network = nil // kept beside it (SaveNetwork)
	tmp, err := os.CreateTemp(s.Dir, ".boot-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := gzip.NewWriter(tmp)
	if err := json.NewEncoder(zw).Encode(snap); err != nil {
		tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), s.path(t.BootID)); err != nil {
		return err
	}
	return s.prune()
}

func (s *Store) read(id string) (*snapshot, error) {
	if !snapshotID.MatchString(id) {
		return nil, ErrNoSnapshot
	}
	f, err := os.Open(s.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoSnapshot
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("boot %s: %w", id, err)
	}
	defer zr.Close()
	var snap snapshot
	if err := json.NewDecoder(zr).Decode(&snap); err != nil {
		return nil, fmt.Errorf("boot %s: %w", id, err)
	}
	if snap.BootID != id {
		return nil, fmt.Errorf("boot %s: the file holds boot %q", id, snap.BootID)
	}
	return &snap, nil
}

// Load is a kept boot's timeline, marked as a snapshot.
func (s *Store) Load(id string) (*Timeline, error) {
	snap, err := s.read(id)
	if err != nil {
		return nil, err
	}
	t := snap.Timeline
	t.Snapshot = true
	t.Network = s.Network(id)
	return &t, nil
}

// List is the kept boots, newest first. A file that cannot be read is left out (and logged).
func (s *Store) List() []Kept {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		return []Kept{}
	}
	list := []Kept{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json.gz")
		if !ok || e.IsDir() || !snapshotID.MatchString(id) {
			continue
		}
		snap, err := s.read(id)
		if err != nil {
			s.log().Warn("boot timeline: a kept boot cannot be read", "boot", id, "err", err)
			continue
		}
		list = append(list, Kept{BootID: id, Started: snap.Started, RecordedMS: snap.RecordedMS, TotalMS: snap.Summary.TotalMS, UserspaceMS: snap.Summary.UserspaceMS})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].RecordedMS != list[j].RecordedMS {
			return list[i].RecordedMS > list[j].RecordedMS
		}
		return list[i].BootID < list[j].BootID
	})
	return list
}

// Previous is the boot kept before the given one: for a kept boot the one recorded before it, for
// the running boot (not kept yet, or kept last) the newest other one. nil when there is none.
func (s *Store) Previous(id string) *Kept {
	list := s.List()
	for i, k := range list {
		if k.BootID == id {
			if i+1 < len(list) {
				return &list[i+1]
			}
			return nil
		}
	}
	if len(list) > 0 {
		return &list[0]
	}
	return nil
}

func (s *Store) prune() error {
	list := s.List()
	var errs []error
	kept := map[string]bool{}
	for _, k := range list[:min(len(list), s.keep())] {
		kept[k.BootID] = true
	}
	for _, k := range list[min(len(list), s.keep()):] {
		for _, p := range []string{s.path(k.BootID), s.netPath(k.BootID)} {
			if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	// no-link records of boots whose timeline was never kept: the newest Keep of them stay
	type rec struct {
		path string
		mod  time.Time
	}
	var orphans []rec
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".net.json")
		if !ok || kept[id] {
			continue
		}
		if fi, err := e.Info(); err == nil {
			orphans = append(orphans, rec{filepath.Join(s.Dir, e.Name()), fi.ModTime()})
		}
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].mod.After(orphans[j].mod) })
	for _, o := range orphans[min(len(orphans), s.keep()):] {
		if err := os.Remove(o.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Record keeps this boot's timeline once the boot has finished and the wall clock can be trusted -
// a box without an RTC starts with a clock months off, and a kept boot is named by when it began.
// It looks every interval (15 s), gives up after patience (30 min: a unit that never stops starting,
// a clock never synchronised), and returns when the timeline is kept, when it gives up or when ctx
// ends.
func (s *Store) Record(ctx context.Context, r *Reader, trusted func(context.Context) bool, interval, patience time.Duration) {
	if r == nil || r.Systemctl == nil {
		return
	}
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if patience <= 0 {
		patience = 30 * time.Minute
	}
	if id := r.BootID(); id == "" || s.Has(id) {
		return
	}
	since := time.Now() // Go's monotonic clock: the wall clock may jump while this waits
	for {
		if trusted == nil || trusted(ctx) {
			t, err := r.Current(ctx)
			switch {
			case err != nil:
				s.log().Warn("boot timeline: this boot could not be read", "err", err)
			case t.Finished:
				if err := s.Save(t); err != nil {
					s.log().Warn("boot timeline: this boot could not be kept", "err", err)
				}
				return
			}
		}
		if time.Since(since) >= patience {
			s.log().Warn("boot timeline: this boot is not kept - it did not finish starting, or the clock was never trustworthy")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
