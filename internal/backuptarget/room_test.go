package backuptarget

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The tests' directories are not the user partition unless a test says so.
func init() { userfsRoot = "" }

const gb = int64(1_000_000_000)

// fakeUserfs makes every path under dir the user partition, with capacity bytes of which the
// backups in dir (sparse files of their stated size) and other bytes are used.
func fakeUserfs(t *testing.T, dir string, capacity, other int64) {
	t.Helper()
	oldOn, oldFree := onUserfs, freeBytes
	onUserfs = func(p string) bool { return strings.HasPrefix(p, dir) }
	freeBytes = func(string) (int64, error) {
		used := other
		files, _ := ListDir(filepath.Join(dir, "backup"))
		for _, f := range files {
			used += f.Size
		}
		return capacity - used, nil
	}
	t.Cleanup(func() { onUserfs, freeBytes = oldOn, oldFree })
}

// backups writes sparse backups of the given sizes, oldest first, a day apart.
func backups(t *testing.T, dir string, sizes ...int64) []string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var names []string
	base := time.Date(2026, 9, 1, 0, 7, 0, 0, time.UTC)
	for i, s := range sizes {
		n := fmt.Sprintf("lab-box-1.0.0-2026-09-%02d-0007.sbk", i+1)
		p := filepath.Join(dir, n)
		f, err := os.Create(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(s); err != nil {
			t.Fatal(err)
		}
		f.Close()
		at := base.Add(time.Duration(i) * 24 * time.Hour)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		names = append(names, n)
	}
	return names
}

func TestRoomNeeded(t *testing.T) {
	for _, c := range []struct {
		size, released, want int64
	}{
		{0, 0, UpdateRoom},
		{gb, 0, UpdateRoom + gb},
		{gb, gb, UpdateRoom},             // the staged copy gives its room back
		{40 * gb, 40 * gb, 40*gb + 4*gb}, // a backup larger than the room: the copy's own need
		{2 * gb, 0, UpdateRoom + 2*gb},
	} {
		if got := roomNeeded(c.size, c.released); got != c.want {
			t.Errorf("roomNeeded(%d, %d) = %d, want %d", c.size, c.released, got, c.want)
		}
	}
}

// B-247: before the backup is made, the oldest backups on the user partition go until a backup of
// the newest one's size and the room for an update fit; never the newest; skipped when that is not
// enough; nothing happens off the user partition.
func TestPrecheckRoom(t *testing.T) {
	for _, c := range []struct {
		name            string
		userfs          bool
		capacity, other int64
		sizes           []int64 // oldest first
		wantRemoved     int     // how many of the oldest
		wantOK          bool
	}{
		{"plenty of room", true, 100 * gb, 10 * gb, []int64{gb, gb, gb}, 0, true},
		// 29 GB, 5 used besides, 16 backups of 1.5 GB = 24 GB: 0 free; the new one in staging (1.5)
		// and then 2.75 left after the run (the staged copy gives its room back) need 4.25 - the
		// three oldest go
		{"the lab box's case", true, 29 * gb, 5 * gb, []int64{1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9, 1.5e9}, 3, true},
		{"only the newest left and still no room", true, 6 * gb, 3 * gb, []int64{gb, gb, gb}, 2, false},
		{"no backup yet, no room for an update", true, 3 * gb, 1 * gb, nil, 0, false},
		{"not the user partition", false, 3 * gb, 1 * gb, []int64{gb, gb}, 0, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "backup")
			names := backups(t, dir, c.sizes...)
			if c.userfs {
				fakeUserfs(t, root, c.capacity, c.other)
			}
			tg := Target{ID: "directory", Kind: KindDirectory, Directory: &Directory{Path: dir}}
			removed, r, ok := precheckRoom(tg, filepath.Join(root, "staging"), time.Now(), "nightly")
			if ok != c.wantOK {
				t.Fatalf("ok %v, want %v (%+v)", ok, c.wantOK, r)
			}
			if want := names[:c.wantRemoved]; len(removed) != c.wantRemoved || (c.wantRemoved > 0 && !reflect.DeepEqual(removed, want)) {
				t.Errorf("removed %v, want the oldest %v", removed, want)
			}
			if len(names) > 0 {
				if _, err := os.Stat(filepath.Join(dir, names[len(names)-1])); err != nil {
					t.Errorf("the newest backup is gone: %v", err)
				}
			}
			if !ok && (r.State != StateUpdateRoom || r.Step != "space" || !strings.Contains(r.Error, "kept free for a system update")) {
				t.Errorf("result %+v", r)
			}
			if !ok && !Failed(r.State) {
				t.Error("update-room is no failure for the Status page")
			}
		})
	}
}

// the copy itself: pruning before it on the user partition, the new file never removed, and a
// skip with update-room when there is no room; off the user partition the old rule (the copy's
// size and a tenth).
func TestDeliverLocalRoom(t *testing.T) {
	old := checkDir
	checkDir = func(Target) (string, error) { return "", nil }
	t.Cleanup(func() { checkDir = old })
	for _, c := range []struct {
		name            string
		capacity, other int64
		sizes           []int64
		wantOK          bool
		wantRemoved     int
		wantState       string
	}{
		// the staged file (not counted in the fake) is on the same partition: it gives its room back
		{"fits", 20 * gb, 2 * gb, []int64{gb, gb}, true, 0, StateWritable},
		{"the oldest goes", 7 * gb, 2 * gb, []int64{gb, gb, gb}, true, 1, StateWritable},
		{"two old ones go", 6 * gb, 2 * gb, []int64{gb, gb, gb}, true, 2, StateWritable},
		{"no room", 4 * gb, 2 * gb, []int64{gb}, false, 0, StateUpdateRoom},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "backup")
			names := backups(t, dir, c.sizes...)
			fakeUserfs(t, root, c.capacity, c.other)
			staged := filepath.Join(root, "staging", "lab-box-1.0.0-2026-09-24-0007.sbk")
			if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
				t.Fatal(err)
			}
			content := []byte("a backup")
			if err := os.WriteFile(staged, content, 0o600); err != nil {
				t.Fatal(err)
			}
			sum, size, _ := hashFile(staged)
			tg := Target{ID: "directory", Kind: KindDirectory, Directory: &Directory{Path: dir}}
			r := deliverLocal(context.Background(), tg, StagedFile{Name: filepath.Base(staged), Path: staged, SHA256: sum, Size: size}, "lab-box", os.Getgid())
			if r.OK != c.wantOK || r.State != c.wantState {
				t.Fatalf("ok %v state %q, want %v %q (%+v)", r.OK, r.State, c.wantOK, c.wantState, r)
			}
			if len(r.Removed) != c.wantRemoved || (c.wantRemoved > 0 && !reflect.DeepEqual(r.Removed, names[:c.wantRemoved])) {
				t.Errorf("removed %v, want the oldest %d of %v", r.Removed, c.wantRemoved, names)
			}
			if _, err := os.Stat(filepath.Join(dir, names[len(names)-1])); err != nil {
				t.Errorf("the newest old backup is gone: %v", err)
			}
			if c.wantOK {
				if _, err := os.Stat(filepath.Join(dir, filepath.Base(staged))); err != nil {
					t.Errorf("the new backup is not there: %v", err)
				}
			}
		})
	}
}
