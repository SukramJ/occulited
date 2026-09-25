package store

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The storage modes, the journal's vocabulary (task 194, internal/system/journalconf.go):
//
//	ram         no database at all: memory only, empty after every restart
//	ram-sync    memory in front of the file, committed at the interval and at a clean stop - the
//	            card sees one transaction per interval; a power loss loses what came since
//	persistent  every round goes into the file as it is taken (one transaction per sample round);
//	            what a keeper marks deferrable - the state store's timestamp-only refreshes, 97 %
//	            of the events (task 194's measurement) - waits for the interval as in ram-sync
//
// A USB stick takes a snapshot of ram-sync's file, never the file itself (task 229, mirror.go).
const (
	ModeRAM        = "ram"
	ModeRAMSync    = "ram-sync"
	ModePersistent = "persistent"
)

// The copy interval of ram-sync: 15min to 1d, 1h when none is set.
const (
	DefaultInterval = "1h"
	minInterval     = 15 * time.Minute
	maxInterval     = 24 * time.Hour
)

// Intervals are the choices the UI offers.
var Intervals = []string{"15min", "1h", "6h", "24h"}

// DefaultMode is the product's default, as the journal's (lite-journal-persist): the products that
// live on a host's disk write straight to it, the SD-card products keep the data in memory and
// write it at the interval.
func DefaultMode(platform string) string {
	switch platform {
	case "ova", "oci", "lxc":
		return ModePersistent
	}
	return ModeRAMSync
}

// ValidMode says whether m is a mode or empty (the product's default).
func ValidMode(m string) bool {
	switch m {
	case "", ModeRAM, ModeRAMSync, ModePersistent:
		return true
	}
	return false
}

var intervalRe = regexp.MustCompile(`^([0-9]+)(min|h|d)$`)

// ParseInterval reads an interval as the journal writes one (30min, 6h, 1d); empty is the default.
func ParseInterval(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = DefaultInterval
	}
	m := intervalRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("the write interval is minutes, hours or days, e.g. 15min, 1h or 1d")
	}
	n, _ := strconv.Atoi(m[1])
	d := time.Duration(n) * map[string]time.Duration{"min": time.Minute, "h": time.Hour, "d": 24 * time.Hour}[m[2]]
	if d < minInterval || d > maxInterval {
		return 0, fmt.Errorf("the write interval is 15min to 1d")
	}
	return d, nil
}

// A Keeper is one owner of data in the file: the health history now, the state store and the
// datapoint history later. The Manager opens and closes the file and decides when to write; the
// keeper holds the data in memory and says what is new.
type Keeper interface {
	// Bucket is the keeper's top-level bucket.
	Bucket() string
	// Restore hands over what the file holds for the bucket right after it was opened, each series
	// oldest first. Memory may already hold rows (a switch out of ram): the keeper merges.
	Restore(series map[string][]Row)
	// Pending returns the rows not written yet - all of them, as Replace series, when all is set
	// (after an open) - and a function the Manager calls once they are committed.
	Pending(all bool) ([]Series, func())
}

// An EntryKeeper keeps keyed entries instead of rings (the state store, task 194): one bucket
// per group (the interface) under its top-level bucket, one key per entry.
type EntryKeeper interface {
	Bucket() string
	// RestoreEntries hands over what the file holds right after it was opened, by group and key.
	RestoreEntries(groups map[string]map[string][]byte)
	// PendingEntries returns the entries the file lacks - every one after an open (all) - and the
	// function the Manager calls once they are committed. urgent is a persistent commit between
	// two intervals: the keeper returns only what must not wait (a value that changed), and
	// keeps the rest for the next interval.
	PendingEntries(all, urgent bool) ([]Entry, func())
}

// A Counter says how many things a keeper holds, for the status (datapoints, series).
type Counter interface {
	Count() int
}

// DefaultKickDelay gathers the kicks of one burst - a device's multicall of a dozen events - into
// one commit in persistent mode.
const DefaultKickDelay = 2 * time.Second

// Manager owns the file for its keepers: which mode is in force, when the file is open, when
// the pending rows are committed.
type Manager struct {
	Path     string
	Platform string
	Rows     int // the ring's length, DefaultRows when 0
	Log      *slog.Logger
	// KickDelay is how long a persistent commit waits after a kick for more of the same burst;
	// DefaultKickDelay when 0.
	KickDelay time.Duration

	mu       sync.Mutex
	keepers  []Keeper
	entries  []EntryKeeper
	mode     string // as configured, "" = the product's default
	interval time.Duration
	db       *DB
	// full: the next write is a whole one (Replace), the file having just been opened
	full     bool
	openErr  string
	lastSync time.Time
	lastErr  string
	nextSync time.Time
	kickAt   time.Time // persistent: the commit a kick asked for, after KickDelay
	kick     chan struct{}
	changed  chan struct{}
	done     chan struct{}
	// ms is the snapshot on a USB stick (task 229, mirror.go)
	ms mirrorState
}

// Register adds a keeper; before Start.
func (m *Manager) Register(k Keeper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.keepers = append(m.keepers, k)
}

// RegisterEntries adds a keeper of keyed entries; before Start.
func (m *Manager) RegisterEntries(k EntryKeeper) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = append(m.entries, k)
}

func (m *Manager) kickDelay() time.Duration {
	if m.KickDelay > 0 {
		return m.KickDelay
	}
	return DefaultKickDelay
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Effective is the mode in force: the configured one, or the product's default.
func (m *Manager) effective() string {
	if m.mode != "" {
		return m.mode
	}
	return DefaultMode(m.Platform)
}

// Start takes the configured mode and interval and, unless the mode is ram, opens the file and
// hands its content to the keepers. A file that cannot be opened leaves the data in memory, with
// a warning: occulited starts all the same.
func (m *Manager) Start(mode, interval string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.kick == nil {
		m.kick, m.changed, m.done = make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{})
	}
	if !ValidMode(mode) {
		m.log().Warn("store: the mode in occulited.json is not one of ram, ram-sync, persistent - the product's default applies", "mode", mode)
		mode = ""
	}
	d, err := ParseInterval(interval)
	if err != nil {
		m.log().Warn("store: the write interval in occulited.json is not valid - "+DefaultInterval+" applies", "interval", interval, "err", err)
		d, _ = ParseInterval("")
	}
	m.mode, m.interval = mode, d
	m.applyLocked()
}

// applyLocked opens or closes the file for the mode in force.
func (m *Manager) applyLocked() {
	eff := m.effective()
	if eff == ModeRAM {
		if m.db != nil {
			m.writeLocked(false) // what memory holds beyond the file, before it stops being written
			if err := m.db.Close(); err != nil {
				m.log().Warn("store: closing the database", "err", err)
			}
			m.db = nil
		}
		m.openErr, m.nextSync = "", time.Time{}
		return
	}
	if m.db == nil {
		// task 229: a newer snapshot on the USB stick replaces the local file before it is opened
		m.importLocked()
		db, err := Open(m.Path)
		switch {
		case db == nil:
			m.openErr = err.Error()
			m.log().Warn("store: the database could not be opened - the data stays in memory", "path", m.Path, "err", err)
			return
		case err != nil:
			m.log().Warn("store: the database was unreadable - moved aside, starting empty", "path", m.Path, "err", err)
		}
		m.db, m.openErr = db, ""
		for _, k := range m.keepers {
			series, err := db.ReadRings(k.Bucket())
			if err != nil {
				m.log().Warn("store: the database could not be read", "bucket", k.Bucket(), "err", err)
				continue
			}
			k.Restore(series)
		}
		for _, k := range m.entries {
			groups, err := db.ReadEntries(k.Bucket())
			if err != nil {
				m.log().Warn("store: the database could not be read", "bucket", k.Bucket(), "err", err)
				continue
			}
			k.RestoreEntries(groups)
		}
		m.full = true
		m.log().Info("store: database open", "path", m.Path, "mode", eff, "size", db.Size())
	}
	// ram-sync commits everything at the interval; persistent commits what was deferred
	m.nextSync = time.Now().Add(m.interval)
}

// ValidateFor is Validate on a product: a USB stick takes the snapshot of ram-sync only (task 229) -
// the mode chosen, or the product's default, must be ram-sync.
func ValidateFor(platform, mode, interval, location string) error {
	if err := Validate(mode, interval, location); err != nil {
		return err
	}
	eff := mode
	if eff == "" {
		eff = DefaultMode(platform)
	}
	if IsUSB(location) && eff != ModeRAMSync {
		return fmt.Errorf("a USB stick takes a copy of the database, made at every write of ram-sync: choose RAM, written to the userfs (ram-sync) - the database itself never lives on a stick")
	}
	return nil
}

// Validate checks a mode, an interval and a location as the API takes them.
func Validate(mode, interval, location string) error {
	if !ValidMode(mode) {
		return fmt.Errorf("the storage is ram, ram-sync or persistent, or empty for the product's default")
	}
	if _, err := ParseInterval(interval); err != nil {
		return err
	}
	return ValidateLocation(location)
}

// Set changes the mode, the interval and the file at runtime (the API's PUT): a switch into ram
// writes what memory holds and closes the file; a switch out of it opens the file, merges and
// writes it whole. Another path is the same: the old file is written and closed, the new one
// opened, merged and written whole. path empty keeps the current one.
func (m *Manager) Set(mode, interval, path string) error {
	if err := Validate(mode, interval, ""); err != nil {
		return err
	}
	d, _ := ParseInterval(interval)
	m.mu.Lock()
	if path != "" && path != m.Path {
		if m.db != nil {
			m.writeLocked(false)
			if err := m.db.Close(); err != nil {
				m.log().Warn("store: closing the database", "err", err)
			}
			m.db = nil
		}
		m.Path = path
	}
	m.mode, m.interval = mode, d
	m.applyLocked()
	if m.db != nil && m.full {
		m.writeLocked(false) // memory and file agree from here on
	}
	if m.db != nil && m.effective() == ModeRAMSync {
		m.copyLocked()
	}
	m.mu.Unlock()
	select {
	case m.changed <- struct{}{}:
	default:
	}
	return nil
}

// Kick is the keepers' "a round was taken" or "a value changed": in persistent mode it is written
// after KickDelay (in the Run loop, not under the keeper's lock), what is deferrable excepted.
func (m *Manager) Kick() {
	if m.kick == nil {
		return
	}
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

// Flush writes what is pending now, whatever the mode (a clean stop, a test).
func (m *Manager) Flush() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.writeLocked(false)
}

// writeLocked commits the keepers' pending rows and entries in one transaction; urgent is a
// persistent commit between two intervals, which leaves the deferrable entries for the interval.
func (m *Manager) writeLocked(urgent bool) error {
	if m.db == nil {
		return nil
	}
	var all []Series
	var ents []Entry
	var marks []func()
	for _, k := range m.keepers {
		s, done := k.Pending(m.full)
		all = append(all, s...)
		if done != nil {
			marks = append(marks, done)
		}
	}
	for _, k := range m.entries {
		e, done := k.PendingEntries(m.full, urgent && !m.full)
		ents = append(ents, e...)
		if done != nil {
			marks = append(marks, done)
		}
	}
	now := time.Now()
	if len(all) > 0 || len(ents) > 0 {
		if err := m.db.Write(all, ents, m.Rows); err != nil {
			m.lastErr = err.Error()
			m.log().Warn("store: writing the database failed - the rows stay in memory and are tried again", "err", err)
			return err
		}
	}
	for _, f := range marks {
		f()
	}
	m.full, m.lastSync, m.lastErr = false, now, ""
	return nil
}

// Run writes at the interval (ram-sync: everything; persistent: what was deferred) and, in
// persistent mode, KickDelay after a kick, until ctx ends; then it writes what is pending and
// closes the file. Done is closed after that.
func (m *Manager) Run(ctx context.Context) {
	defer close(m.done)
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		m.mu.Lock()
		wait := time.Minute
		if m.db != nil && !m.nextSync.IsZero() {
			wait = max(time.Until(m.nextSync), time.Second)
		}
		if !m.kickAt.IsZero() {
			wait = min(wait, max(time.Until(m.kickAt), 0))
		}
		m.mu.Unlock()
		t.Reset(wait)
		select {
		case <-ctx.Done():
			m.mu.Lock()
			if m.db != nil {
				if m.writeLocked(false) == nil && m.effective() == ModeRAMSync {
					m.copyLocked() // the stop's copy to the USB stick (task 229)
				}
				if err := m.db.Close(); err != nil {
					m.log().Warn("store: closing the database", "err", err)
				}
				m.db = nil
			}
			m.mu.Unlock()
			return
		case <-m.changed:
		case <-m.kick:
			m.mu.Lock()
			if m.effective() == ModePersistent && m.kickAt.IsZero() {
				m.kickAt = time.Now().Add(m.kickDelay())
			}
			m.mu.Unlock()
		case <-t.C:
			m.mu.Lock()
			now := time.Now()
			if !m.kickAt.IsZero() && !now.Before(m.kickAt) {
				m.kickAt = time.Time{}
				if m.effective() == ModePersistent {
					_ = m.writeLocked(true)
				}
			}
			if m.db != nil && m.effective() != ModeRAM && !m.nextSync.IsZero() && !now.Before(m.nextSync) {
				if m.writeLocked(false) == nil && m.effective() == ModeRAMSync {
					m.copyLocked() // the interval's copy to the USB stick (task 229)
				}
				if m.db != nil {
					m.nextSync = now.Add(m.interval)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Done is closed once Run has written and closed the file at its end.
func (m *Manager) Done() <-chan struct{} { return m.done }

// Status is the API's view.
type Status struct {
	Mode string `json:"mode"`
	// Location is the setting as stored (task 228's id + folder shape), "" = DefaultLocation
	Location        string     `json:"location"`
	DefaultLocation string     `json:"default_location"`
	SyncInterval    string     `json:"sync_interval"`
	DefaultMode     string     `json:"default_mode"`
	DefaultInterval string     `json:"default_sync_interval"`
	Intervals       []string   `json:"sync_intervals"`
	Platform        string     `json:"platform"`
	Effective       string     `json:"effective"`
	Open            bool       `json:"open"`
	Path            string     `json:"path"`
	Size            int64      `json:"size"`
	Rows            int        `json:"rows_per_series"`
	LastSync        *time.Time `json:"last_sync"`
	LastSyncError   string     `json:"last_sync_error,omitempty"`
	NextSync        *time.Time `json:"next_sync"`
	Error           string     `json:"error,omitempty"`
	// Kept is how many things each counting keeper holds, by bucket: the state store's
	// datapoints, the datapoint history's series
	Kept map[string]int `json:"kept"`
	// Copy is the snapshot on a USB stick (task 229); absent without one
	Copy *CopyStatus `json:"copy,omitempty"`
}

// Status reports the mode, the file and the last write. interval is what the configuration
// says (empty = the default), so the form shows what is stored.
func (m *Manager) Status(interval, location string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{Mode: m.mode, Location: location, DefaultLocation: DefaultLocation, SyncInterval: interval, DefaultMode: DefaultMode(m.Platform), DefaultInterval: DefaultInterval, Intervals: Intervals, Platform: m.Platform, Effective: m.effective(), Open: m.db != nil, Path: m.Path, Rows: m.Rows, LastSyncError: m.lastErr, Error: m.openErr}
	if st.Rows <= 0 {
		st.Rows = DefaultRows
	}
	if m.db != nil {
		st.Size = m.db.Size()
	}
	if !m.lastSync.IsZero() {
		t := m.lastSync
		st.LastSync = &t
	}
	if !m.nextSync.IsZero() && m.db != nil {
		t := m.nextSync
		st.NextSync = &t
	}
	st.Copy = m.copyStatusLocked()
	st.Kept = map[string]int{}
	for _, k := range m.keepers {
		if c, ok := k.(Counter); ok {
			st.Kept[k.Bucket()] = c.Count()
		}
	}
	for _, k := range m.entries {
		if c, ok := k.(Counter); ok {
			st.Kept[k.Bucket()] = c.Count()
		}
	}
	return st
}
