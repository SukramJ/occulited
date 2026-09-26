package backuptarget

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Room for a system update (openccu-lite B-247): a directory target on the system's own user
// partition - the default /media/usb0/backup without a USB stick - shares it with the staged
// system update, which the recovery unpacks there. A nightly backup there keeps UpdateRoom free:
// before the backup is made (with the newest backup's size as its estimate) and again before the
// copy, the oldest backups in the directory are removed until it fits - never the newest one, and
// only on the user partition, which is this system's own; when even then it does not fit, the
// backup is skipped with the state update-room (a Status warning, a journal line).

// UpdateRoom is what a backup on the user partition leaves free: an openccu-lite release unpacks
// to 2.42 GB on every product (1.0.0-dev.25/26: 2,419,010,426 bytes on the Pis, 2,420,058,488 on
// the OVA; the recovery refuses the update with less), plus a margin for the journal, the state
// and the image growing.
const UpdateRoom int64 = 2_750_000_000

// userfsRoot is the user partition's mount point.
var userfsRoot = "/usr/local"

// onUserfs reports whether path (or its deepest existing parent) is on the user partition (a
// test swaps it).
var onUserfs = func(path string) bool {
	existing := path
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		existing = parent
	}
	var a, b syscall.Stat_t
	return syscall.Stat(existing, &a) == nil && syscall.Stat(userfsRoot, &b) == nil && a.Dev == b.Dev
}

// freeBytes is the space free for dir (a test swaps it).
var freeBytes = func(dir string) (int64, error) {
	sp, err := StatSpace(dir)
	return sp.Free, err
}

// roomNeeded is the free space a copy of size bytes onto the user partition needs: the copy
// itself with a tenth to spare, and UpdateRoom left after the run - when the staged file is on
// the same partition (released bytes), it is removed at the run's end and gives its room back.
func roomNeeded(size, released int64) int64 {
	need := size + size/10
	if after := UpdateRoom + size - released; after > need {
		need = after
	}
	return need
}

// makeRoom removes the oldest backups in dir until need bytes are free, never the newest one
// nor keep. It answers what it removed, the free space at the end, and whether that is enough.
func makeRoom(dir string, need int64, keep string) (removed []string, free int64, ok bool) {
	free, err := freeBytes(dir)
	if err != nil {
		return nil, 0, false
	}
	if free >= need {
		return nil, free, true
	}
	files, err := ListDir(dir)
	if err != nil {
		return nil, free, false
	}
	var candidates []BackupFile
	for _, f := range files {
		if f.Name != keep {
			candidates = append(candidates, f)
		}
	}
	// ListDir is newest first: the first stays, the rest go from the end
	for i := len(candidates) - 1; i >= 1 && free < need; i-- {
		if err := os.Remove(filepath.Join(dir, candidates[i].Name)); err != nil {
			continue
		}
		removed = append(removed, candidates[i].Name)
		if free, err = freeBytes(dir); err != nil {
			return removed, 0, false
		}
	}
	return removed, free, free >= need
}

// roomError is the result's text when even the pruning left too little.
func roomError(free, need int64) string {
	return fmt.Sprintf("%d bytes free on the system's own storage, %d needed: the backup and %d kept free for a system update (a USB stick or a share takes the backups instead)", free, need, UpdateRoom)
}

// precheckRoom is the room check before the backup is made: a directory target on the user
// partition, with the newest backup's size as the estimate and the staging directory's use
// counted. ok false: skip, with the result r.
func precheckRoom(t Target, staging string, at time.Time, instance string) (removed []string, r Result, ok bool) {
	if t.Kind != KindDirectory || t.IsMount() || !onUserfs(t.Dir()) {
		return nil, Result{}, true
	}
	dir := t.Dir()
	var est int64
	if files, err := ListDir(dir); err == nil && len(files) > 0 {
		est = files[0].Size
	}
	need := roomNeeded(est, 0)
	if onUserfs(staging) {
		// the backup is made in the staging directory first: it takes est there before the copy
		need = est + roomNeeded(est, est)
	}
	removed, free, ok := makeRoom(dir, need, "")
	if ok {
		return removed, Result{}, true
	}
	return removed, Result{At: at, Instance: instance, State: StateUpdateRoom, Step: "space", Error: roomError(free, need), Removed: removed}, false
}
