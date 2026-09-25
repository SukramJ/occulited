package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

func rows(from, n int) []Row {
	var out []Row
	for i := from; i < from+n; i++ {
		out = append(out, Row{At: t0.Add(time.Duration(i) * time.Minute), Value: []byte(fmt.Sprint(i))})
	}
	return out
}

func values(rs []Row) []string {
	var out []string
	for _, r := range rs {
		out = append(out, string(r.Value))
	}
	return out
}

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), DirName, FileName)
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

// The ring: rows past n are deleted in the same transaction, the newest n stay, in order, and a
// reopened file answers the same.
func TestRingWrapsAtNAndKeepsOrderAfterReopen(t *testing.T) {
	db, path := openTemp(t)
	for _, c := range []struct{ from, n int }{{0, 3}, {3, 4}, {7, 2}} {
		if err := db.WriteRings([]Series{{Bucket: "health", Name: "HmIP-RF/X", Rows: rows(c.from, c.n)}}, 5); err != nil {
			t.Fatal(err)
		}
	}
	got, err := db.ReadRings("health")
	if err != nil {
		t.Fatal(err)
	}
	if v := fmt.Sprint(values(got["HmIP-RF/X"])); v != "[4 5 6 7 8]" {
		t.Fatalf("after 9 rows into a ring of 5: %s", v)
	}
	if !got["HmIP-RF/X"][0].At.Equal(t0.Add(4 * time.Minute)) {
		t.Errorf("the key is not the row's time: %v", got["HmIP-RF/X"][0].At)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, _ = db.ReadRings("health")
	if v := fmt.Sprint(values(got["HmIP-RF/X"])); v != "[4 5 6 7 8]" {
		t.Errorf("after a reopen: %s", v)
	}
	if other, _ := db.ReadRings("state"); len(other) != 0 {
		t.Errorf("an absent bucket answered %v", other)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), NoBackupTag)); err != nil {
		t.Errorf("the directory carries no .nobackup: %v", err)
	}
}

// Replace drops what the file held for the series; the default ring is DefaultRows.
func TestReplaceAndDefaultRows(t *testing.T) {
	db, _ := openTemp(t)
	defer db.Close()
	_ = db.WriteRings([]Series{{Bucket: "health", Name: "a", Rows: rows(0, 3)}, {Bucket: "health", Name: "b", Rows: rows(0, 1)}}, 0)
	if err := db.WriteRings([]Series{{Bucket: "health", Name: "a", Rows: rows(10, 2), Replace: true}}, 0); err != nil {
		t.Fatal(err)
	}
	got, _ := db.ReadRings("health")
	if v := fmt.Sprint(values(got["a"])); v != "[10 11]" {
		t.Errorf("replaced: %s", v)
	}
	if len(got["b"]) != 1 {
		t.Errorf("another series was touched: %v", got["b"])
	}
	_ = db.WriteRings([]Series{{Bucket: "health", Name: "c", Rows: rows(0, DefaultRows+20)}}, 0)
	got, _ = db.ReadRings("health")
	if len(got["c"]) != DefaultRows || string(got["c"][0].Value) != "20" {
		t.Errorf("the default ring kept %d rows from %s", len(got["c"]), got["c"][0].Value)
	}
	if db.Size() == 0 || db.Path() == "" {
		t.Error("no size or path")
	}
}

// A file that is not a database is moved aside and a fresh one made: the history is lost, the
// start is not.
func TestCorruptFileIsMovedAside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), DirName)
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, FileName)
	junk := make([]byte, 8192)
	for i := range junk {
		junk[i] = byte(i * 7)
	}
	if err := os.WriteFile(path, junk, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if db == nil || !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open = %v, %v", db, err)
	}
	defer db.Close()
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Errorf("the damaged file was not kept aside: %v", err)
	}
	if err := db.WriteRings([]Series{{Bucket: "health", Name: "a", Rows: rows(0, 1)}}, 5); err != nil {
		t.Errorf("the fresh file does not take a write: %v", err)
	}
}

// A directory that cannot be made: no database, an error, no panic.
func TestOpenFailsWithoutADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain")
	_ = os.WriteFile(file, nil, 0o600)
	if db, err := Open(filepath.Join(file, DirName, FileName)); db != nil || err == nil {
		t.Errorf("Open under a file = %v, %v", db, err)
	}
}

func TestIntervalsAndModes(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
		bad  bool
	}{
		{"", time.Hour, false},
		{"15min", 15 * time.Minute, false},
		{"6h", 6 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"5min", 0, true},
		{"2d", 0, true},
		{"1 h", 0, true},
		{"1w", 0, true},
	} {
		d, err := ParseInterval(c.in)
		if (err != nil) != c.bad || (!c.bad && d != c.want) {
			t.Errorf("ParseInterval(%q) = %v, %v", c.in, d, err)
		}
	}
	for p, want := range map[string]string{"ova": ModePersistent, "lxc": ModePersistent, "oci": ModePersistent, "rpi4": ModeRAMSync, "rpi3": ModeRAMSync, "": ModeRAMSync} {
		if got := DefaultMode(p); got != want {
			t.Errorf("DefaultMode(%q) = %s", p, got)
		}
	}
	if Validate("disk", "", "") == nil || Validate("ram", "x", "") == nil || Validate("", "", "") != nil || Validate(ModeRAMSync, "15min", DefaultLocation) != nil {
		t.Error("Validate")
	}
}

// fakeKeeper is a keeper with a counter of rows: every add is one new row of series "s".
type fakeKeeper struct {
	mu       sync.Mutex
	rows     []Row
	synced   int
	restored map[string][]Row
}

func (f *fakeKeeper) Bucket() string { return "health" }
func (f *fakeKeeper) Restore(s map[string][]Row) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored = s
	f.rows = append(append([]Row(nil), s["s"]...), f.rows...)
	f.synced = len(s["s"])
}
func (f *fakeKeeper) add(i int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows = append(f.rows, rows(i, 1)...)
}
func (f *fakeKeeper) Pending(all bool) ([]Series, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	from := f.synced
	if all {
		from = 0
	}
	if from >= len(f.rows) {
		return nil, nil
	}
	n := len(f.rows)
	return []Series{{Bucket: "health", Name: "s", Rows: append([]Row(nil), f.rows[from:]...), Replace: all}}, func() {
		f.mu.Lock()
		f.synced = n
		f.mu.Unlock()
	}
}

func fileRows(t *testing.T, path string) []string {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, _ := db.ReadRings("health")
	return values(got["s"])
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ram opens no database and writes nothing.
func TestModeRAMWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	k := &fakeKeeper{}
	m := &Manager{Path: path, Platform: "rpi4"}
	m.Register(k)
	m.Start(ModeRAM, "")
	k.add(1)
	m.Kick()
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	cancel()
	<-m.Done()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("ram made a file: %v", err)
	}
	if st := m.Status("", ""); st.Open || st.Effective != ModeRAM || st.Mode != ModeRAM {
		t.Errorf("status %+v", st)
	}
}

// persistent writes every round at the kick; a restart restores what was written.
func TestModePersistentWritesPerRound(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	k := &fakeKeeper{}
	m := &Manager{Path: path, Platform: "ova", KickDelay: 5 * time.Millisecond}
	m.Register(k)
	m.Start("", "")
	// the next sync is the interval's commit of what a keeper deferred (task 194)
	if st := m.Status("", ""); !st.Open || st.Effective != ModePersistent || st.NextSync == nil {
		t.Fatalf("the ova default: %+v", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	k.add(1)
	m.Kick()
	waitFor(t, func() bool { return m.Status("", "").LastSync != nil })
	k.add(2)
	m.Kick()
	waitFor(t, func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		return k.synced == 2
	})
	cancel()
	<-m.Done()
	if v := fmt.Sprint(fileRows(t, path)); v != "[1 2]" {
		t.Errorf("file after two rounds: %s", v)
	}
	// the next start: the keeper gets the file
	k2 := &fakeKeeper{}
	m2 := &Manager{Path: path, Platform: "ova"}
	m2.Register(k2)
	m2.Start("", "")
	if v := fmt.Sprint(values(k2.restored["s"])); v != "[1 2]" {
		t.Errorf("restored %s", v)
	}
	_ = m2.Flush()
	_ = m2.db.Close()
}

// ram-sync writes nothing at a kick, only at the interval and at the stop.
func TestModeRAMSyncWritesAtTheIntervalAndAtStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	k := &fakeKeeper{}
	m := &Manager{Path: path, Platform: "rpi3"}
	m.Register(k)
	m.Start("", "15min")
	if st := m.Status("15min", ""); st.Effective != ModeRAMSync || st.NextSync == nil || st.SyncInterval != "15min" {
		t.Fatalf("status %+v", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	k.add(1)
	m.Kick()
	time.Sleep(50 * time.Millisecond)
	if st := m.Status("", ""); st.LastSync != nil {
		t.Fatalf("ram-sync wrote at a kick: %+v", st)
	}
	// the interval: move the next write into the past and wake the loop
	m.mu.Lock()
	m.nextSync = time.Now().Add(-time.Second)
	m.mu.Unlock()
	m.changed <- struct{}{}
	waitFor(t, func() bool { return m.Status("", "").LastSync != nil })
	k.add(2)
	cancel()
	<-m.Done()
	if v := fmt.Sprint(fileRows(t, path)); v != "[1 2]" {
		t.Errorf("file after the interval and the stop: %s", v)
	}
}

// A switch at runtime: out of ram the file is opened and written whole, into ram it is written a
// last time and closed; a bad setting is refused and changes nothing.
func TestSetSwitchesAtRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	k := &fakeKeeper{}
	m := &Manager{Path: path, Platform: "rpi4"}
	m.Register(k)
	m.Start(ModeRAM, "")
	k.add(1)
	k.add(2)
	if err := m.Set("bogus", "", ""); err == nil {
		t.Error("a bogus mode was taken")
	}
	if err := m.Set(ModePersistent, "5min", ""); err == nil {
		t.Error("a short interval was taken")
	}
	if m.Status("", "").Open {
		t.Fatal("a refused setting opened the file")
	}
	if err := m.Set(ModePersistent, "", ""); err != nil {
		t.Fatal(err)
	}
	k.add(3)
	if err := m.Set(ModeRAM, "", ""); err != nil {
		t.Fatal(err)
	}
	if m.Status("", "").Open {
		t.Error("ram left the file open")
	}
	if v := fmt.Sprint(fileRows(t, path)); v != "[1 2 3]" {
		t.Errorf("file after ram → persistent → ram: %s", v)
	}
}

// A file that cannot be opened leaves the data in memory, says so, and does not stop the start.
func TestStartWithAnUnusableDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plain")
	_ = os.WriteFile(file, nil, 0o600)
	k := &fakeKeeper{}
	m := &Manager{Path: filepath.Join(file, DirName, FileName), Platform: "rpi4"}
	m.Register(k)
	m.Start("nonsense", "nonsense")
	st := m.Status("", "")
	if st.Open || st.Error == "" || st.Mode != "" || st.Effective != ModeRAMSync {
		t.Errorf("status %+v", st)
	}
	k.add(1)
	if err := m.Flush(); err != nil {
		t.Errorf("a flush without a file: %v", err)
	}
}

// task 228's shape: a location id and a folder; the userfs inside occulited's own part, a USB stick
// for the snapshot (task 229), never a share.
func TestLocations(t *testing.T) {
	for _, c := range []struct {
		loc string
		ok  bool
	}{
		{"", true},
		{DefaultLocation, true},
		{"userfs:etc/occulite/other", true},
		{"userfs:etc/occulite", true},
		{"userfs:var/lib/x", false},
		{"userfs:etc/occulite/../x", false},
		{"userfs:../etc", false},
		{"userfs:/usr/local/etc/occulite", false},
		{"userfs:", false},
		{"share:nas/openccu", false},
		{"usb:LOGSTICK/db", true},
		{"usb:LOGSTICK/a/b", true},
		{"usb:LOGSTICK", false},
		{"usb:LOGSTICK/../x", false},
		{"usb:LOG STICK/db", false},
		{"usb:LOGSTICK/a/b/c/d/e", false},
		{"/usr/local/etc/occulite/data", false},
		{"nfs:x/y", false},
	} {
		if err := ValidateLocation(c.loc); (err == nil) != c.ok {
			t.Errorf("ValidateLocation(%q) = %v", c.loc, err)
		}
	}
	if err := ValidateLocation("share:nas/x"); err == nil || !strings.Contains(err.Error(), "network share") {
		t.Errorf("a share's refusal does not say why: %v", err)
	}
	if got := FilePath("", "/tmp/state"); got != "/tmp/state/data/occulited.db" {
		t.Errorf("default: %s", got)
	}
	if got := FilePath(DefaultLocation, "/tmp/state"); got != "/tmp/state/data/occulited.db" {
		t.Errorf("the default spelled out: %s", got)
	}
	if got := FilePath("userfs:etc/occulite/other", "/x"); got != "/usr/local/etc/occulite/other/occulited.db" {
		t.Errorf("a userfs folder: %s", got)
	}
}

// Another path at runtime: the old file is written and closed, the new one takes it all.
func TestSetMovesTheFile(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a", FileName), filepath.Join(dir, "b", FileName)
	k := &fakeKeeper{}
	m := &Manager{Path: a, Platform: "ova"}
	m.Register(k)
	m.Start("", "")
	k.add(1)
	if err := m.Set("", "", b); err != nil {
		t.Fatal(err)
	}
	k.add(2)
	_ = m.Flush()
	_ = m.db.Close()
	m.db = nil
	if v := fmt.Sprint(fileRows(t, a)); v != "[1]" {
		t.Errorf("the old file: %s", v)
	}
	if v := fmt.Sprint(fileRows(t, b)); v != "[1 2]" {
		t.Errorf("the new file: %s", v)
	}
}

// fakeEntries is an entry keeper: urgent entries go at every commit, the others only when the
// commit is not urgent - the state store's value changes and its timestamp-only refreshes.
type fakeEntries struct {
	mu       sync.Mutex
	pending  map[string]string // key -> value ("" deletes)
	urgent   map[string]bool
	restored map[string]map[string][]byte
	commits  []string
}

func (f *fakeEntries) Bucket() string { return "state" }
func (f *fakeEntries) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pending)
}
func (f *fakeEntries) RestoreEntries(g map[string]map[string][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored = g
}
func (f *fakeEntries) set(k, v string, urgent bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending, f.urgent = map[string]string{}, map[string]bool{}
	}
	f.pending[k], f.urgent[k] = v, urgent
}
func (f *fakeEntries) PendingEntries(all, urgent bool) ([]Entry, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Entry
	var keys []string
	for k, v := range f.pending {
		if urgent && !f.urgent[k] {
			continue
		}
		e := Entry{Bucket: "state", Group: "HmIP-RF", Key: k}
		if v != "" {
			e.Value = []byte(v)
		}
		out = append(out, e)
		keys = append(keys, k)
	}
	return out, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, k := range keys {
			delete(f.pending, k)
		}
		f.commits = append(f.commits, fmt.Sprint(len(keys)))
	}
}

// Entries: put, overwrite, delete, and read back by group and key.
func TestEntriesWriteReadDelete(t *testing.T) {
	db, _ := openTemp(t)
	defer db.Close()
	if err := db.Write(nil, []Entry{{"state", "HmIP-RF", "A:1\x00STATE", []byte("1")}, {"state", "HmIP-RF", "A:1\x00LEVEL", []byte("x")}, {"state", "BidCos-RF", "B:1\x00STATE", []byte("2")}}, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(nil, []Entry{{"state", "HmIP-RF", "A:1\x00LEVEL", nil}, {"state", "HmIP-RF", "A:1\x00STATE", []byte("3")}, {"state", "gone", "k", nil}}, 0); err != nil {
		t.Fatal(err)
	}
	got, err := db.ReadEntries("state")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || string(got["HmIP-RF"]["A:1\x00STATE"]) != "3" || len(got["HmIP-RF"]) != 1 || string(got["BidCos-RF"]["B:1\x00STATE"]) != "2" {
		t.Errorf("entries %q", got)
	}
	if none, err := db.ReadEntries("nothing"); err != nil || len(none) != 0 {
		t.Errorf("absent bucket: %v %v", none, err)
	}
}

// persistent: a kick commits the urgent entries after the delay and leaves the deferred ones for
// the interval; ram-sync commits nothing at a kick. The status counts the keeper.
func TestPersistentDefersTheNonUrgentEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	e := &fakeEntries{}
	m := &Manager{Path: path, Platform: "ova", KickDelay: 5 * time.Millisecond}
	m.RegisterEntries(e)
	m.Start("", "")
	_ = m.Flush() // the whole write after the open (it takes everything)
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	e.set("changed", "v1", true)
	e.set("ts-only", "v2", false)
	if st := m.Status("", ""); st.Kept["state"] != 2 {
		t.Errorf("kept %v", st.Kept)
	}
	m.Kick()
	waitFor(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.commits) > 1 // the flush's, then the kick's
	})
	e.mu.Lock()
	_, deferred := e.pending["ts-only"]
	_, left := e.pending["changed"]
	e.mu.Unlock()
	if !deferred || left {
		t.Fatal("the timestamp-only entry was committed at a kick")
	}
	// the interval: the deferred one goes
	m.mu.Lock()
	m.nextSync = time.Now().Add(-time.Second)
	m.mu.Unlock()
	m.changed <- struct{}{}
	waitFor(t, func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.pending) == 0
	})
	cancel()
	<-m.Done()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.ReadEntries("state")
	db.Close()
	if string(got["HmIP-RF"]["changed"]) != "v1" || string(got["HmIP-RF"]["ts-only"]) != "v2" {
		t.Errorf("file %q", got)
	}
	// the next start restores them
	e2 := &fakeEntries{}
	m2 := &Manager{Path: path, Platform: "ova"}
	m2.RegisterEntries(e2)
	m2.Start("", "")
	if string(e2.restored["HmIP-RF"]["ts-only"]) != "v2" {
		t.Errorf("restored %q", e2.restored)
	}
	_ = m2.db.Close()
}

func TestRAMSyncIgnoresKicksForEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), DirName, FileName)
	e := &fakeEntries{}
	m := &Manager{Path: path, Platform: "rpi3", KickDelay: time.Millisecond}
	m.RegisterEntries(e)
	m.Start("", "")
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	e.set("changed", "v1", true)
	m.Kick()
	time.Sleep(50 * time.Millisecond)
	e.mu.Lock()
	n := len(e.commits)
	e.mu.Unlock()
	if n != 0 {
		t.Errorf("ram-sync committed at a kick")
	}
	cancel()
	<-m.Done()
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pending) != 0 {
		t.Errorf("the stop did not write: %v", e.pending)
	}
}
