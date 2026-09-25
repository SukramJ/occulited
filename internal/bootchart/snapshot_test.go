package bootchart

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func finished(id string, totalMS int64) *Timeline {
	total := totalMS
	return &Timeline{BootID: id, Finished: true, Started: "2026-09-12T19:06:11+02:00", Summary: Summary{TotalMS: &total}, Units: []Unit{{ID: "rfd.service", Activating: 20, DurationMS: 4247, State: "active"}}, CriticalChain: []string{Root}}
}

func bootID(n int) string { return fmt.Sprintf("%032x", n+1) }

func TestStoreKeepsTenNewestFirst(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "boots")
	now := time.Date(2026, 9, 12, 20, 0, 0, 0, time.UTC)
	s := &Store{Dir: dir, Now: func() time.Time { return now }}
	for i := 0; i < 12; i++ {
		if err := s.Save(finished(bootID(i), int64(100000+i))); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	list := s.List()
	if len(list) != DefaultKeep || list[0].BootID != bootID(11) || list[9].BootID != bootID(2) || *list[0].TotalMS != 100011 {
		t.Fatalf("%d kept: %+v", len(list), list)
	}
	if s.Has(bootID(0)) || s.Has(bootID(1)) || !s.Has(bootID(2)) {
		t.Error("the two oldest go")
	}
	// the marker for the backup, and gzipped JSON files of a few KB
	if _, err := os.Stat(filepath.Join(dir, ".nobackup")); err != nil {
		t.Errorf("no .nobackup: %v", err)
	}
	f, err := os.Open(filepath.Join(dir, bootID(5)+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.NewDecoder(zr).Decode(&raw); err != nil || raw["boot_id"] != bootID(5) || raw["recorded_ms"] == nil || raw["snapshot"] != nil {
		t.Errorf("file: %v %v", err, raw)
	}
	f.Close()
	if st, _ := os.Stat(filepath.Join(dir, bootID(5)+".json.gz")); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	// no temporary file left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left: %s", e.Name())
		}
	}

	// once per boot: a second save changes nothing
	before, _ := os.Stat(filepath.Join(dir, bootID(11)+".json.gz"))
	now = now.Add(time.Hour)
	if err := s.Save(finished(bootID(11), 1)); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(filepath.Join(dir, bootID(11)+".json.gz")); !after.ModTime().Equal(before.ModTime()) || *s.List()[0].TotalMS != 100011 {
		t.Error("saved twice")
	}

	loaded, err := s.Load(bootID(7))
	if err != nil || !loaded.Snapshot || loaded.BootID != bootID(7) || len(loaded.Units) != 1 || loaded.Units[0].DurationMS != 4247 {
		t.Errorf("load: %v %+v", err, loaded)
	}
	for _, id := range []string{bootID(0), "nope", "../../etc/passwd", ""} {
		if _, err := s.Load(id); !errors.Is(err, ErrNoSnapshot) {
			t.Errorf("load %q: %v", id, err)
		}
	}

	// the previous boot: of a kept one the one before it, of the running one the newest
	if p := s.Previous(bootID(7)); p == nil || p.BootID != bootID(6) {
		t.Errorf("previous of 7: %+v", p)
	}
	if p := s.Previous(bootID(2)); p != nil {
		t.Errorf("the oldest has none: %+v", p)
	}
	if p := s.Previous("ffffffffffffffffffffffffffffffff"); p == nil || p.BootID != bootID(11) {
		t.Errorf("previous of the running boot: %+v", p)
	}
	if p := (&Store{Dir: filepath.Join(t.TempDir(), "none")}).Previous(bootID(1)); p != nil {
		t.Errorf("nothing kept: %+v", p)
	}
}

func TestStoreRefusesAndSkips(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir, Keep: 3}
	unfinished := finished(bootID(1), 5)
	unfinished.Finished = false
	for _, tl := range []*Timeline{nil, unfinished, finished("not-an-id", 5)} {
		if err := s.Save(tl); err == nil {
			t.Errorf("saved %+v", tl)
		}
	}
	// a broken file and a stranger in the directory are left out of the list
	_ = os.WriteFile(filepath.Join(dir, bootID(9)+".json.gz"), []byte("not gzip"), 0o640)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o640)
	if err := s.Save(finished(bootID(2), 5)); err != nil {
		t.Fatal(err)
	}
	if list := s.List(); len(list) != 1 || list[0].BootID != bootID(2) {
		t.Errorf("%+v", list)
	}
}

func TestRecord(t *testing.T) {
	dir := t.TempDir()
	root := t.TempDir()
	write := func(p, c string) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("proc/sys/kernel/random/boot_id", "6c97f676-81ee-40c7-8e69-d3bc0c03efea\n")
	write("proc/uptime", "527.58 1848.78\n")
	var finishedNow atomic.Bool
	var clockOK atomic.Bool
	calls := atomic.Int32{}
	r := &Reader{Root: root, Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		calls.Add(1)
		if strings.Join(args, " ") == strings.Join(ManagerArgs(), " ") {
			if !finishedNow.Load() {
				return []byte("UserspaceTimestampMonotonic=8714083\nFinishTimestampMonotonic=0\n"), nil
			}
			return read(t, "manager-charly.txt"), nil
		}
		return read(t, "show-units-charly.txt"), nil
	}}
	s := &Store{Dir: dir}
	done := make(chan struct{})
	go func() {
		s.Record(context.Background(), r, func(context.Context) bool { return clockOK.Load() }, 5*time.Millisecond, 5*time.Second)
		close(done)
	}()
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 0 {
		t.Error("read before the clock was trusted")
	}
	clockOK.Store(true)
	time.Sleep(40 * time.Millisecond)
	if s.Has("6c97f67681ee40c78e69d3bc0c03efea") {
		t.Error("kept before the boot finished")
	}
	finishedNow.Store(true)
	// the reader keeps an unfinished boot for a few seconds; Record reads it meanwhile
	r.mu.Lock()
	r.cached = nil
	r.mu.Unlock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Record did not return")
	}
	kept, err := s.Load("6c97f67681ee40c78e69d3bc0c03efea")
	if err != nil || len(kept.Units) != 209 || kept.CriticalChain[0] != Root {
		t.Fatalf("%v %+v", err, kept)
	}
	// kept already: returns at once, without reading
	n := calls.Load()
	s.Record(context.Background(), r, nil, time.Millisecond, time.Second)
	if calls.Load() != n {
		t.Error("read again for a kept boot")
	}
	// never finishing: gives up
	r2 := &Reader{Root: root, Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
		return []byte("UserspaceTimestampMonotonic=1\nFinishTimestampMonotonic=0\n"), nil
	}}
	write("proc/sys/kernel/random/boot_id", "648df1b0-f925-453b-bf4e-4cf221fc0545\n")
	start := time.Now()
	s.Record(context.Background(), r2, nil, time.Millisecond, 50*time.Millisecond)
	if time.Since(start) > 2*time.Second || s.Has("648df1b0f925453bbf4e4cf221fc0545") {
		t.Error("did not give up")
	}
}
