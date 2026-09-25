package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// The snapshot on a USB stick (task 229, the maintainer's Q&A of 2026-09-25): the live file stays
// on the userfs in ram-sync; at every write of the interval and at a clean stop a consistent copy
// (bbolt's Tx.WriteTo) goes to the stick, found by its label - written by the privilege helper,
// since occulited may write only its own directory. At the open, and at the first write after the
// stick came when it was missing then, a snapshot on the stick newer than the local file is loaded:
// a card replaced, or a userfs made anew, gets the history back. A stick that is not there keeps
// everything on the userfs and in memory, with a warning (store-target).

// Mirror is the stick the snapshot goes to.
type Mirror interface {
	// Target is the stick's label and the folder on it, as the setting names them.
	Target() (label, folder string)
	// Dir is the folder's path while the stick is plugged in; ok is false while it is not.
	Dir() (dir string, ok bool)
	// Copy writes the snapshot at src into the folder as SnapshotName, atomically and synced
	// (the helper, as root).
	Copy(src string) error
}

// SnapshotTolerance: a snapshot on the stick is newer than the local file only by more than this -
// the copy of a clean stop is written just after the file itself, and exFAT keeps times in steps.
const SnapshotTolerance = 5 * time.Second

// CopyStatus is the snapshot's part of the status.
type CopyStatus struct {
	Label     string     `json:"label"`
	Folder    string     `json:"folder"`
	Present   bool       `json:"present"`
	Dir       string     `json:"dir,omitempty"`
	LastCopy  *time.Time `json:"last_copy"`
	LastError string     `json:"last_error,omitempty"`
	// Loaded is when a snapshot from the stick was loaded because it was newer than the local file.
	Loaded *time.Time `json:"loaded,omitempty"`
}

// mirrorState is the Manager's side of it.
type mirrorState struct {
	mirror  Mirror
	checked bool      // the stick was looked at for a newer snapshot since the file was opened
	stamp   time.Time // the local file's time before this run first wrote it
	present bool
	dir     string
	last    time.Time
	lastErr string
	loaded  time.Time
}

// SetMirror sets the stick the snapshot goes to (nil = none); the next write copies, and a newer
// snapshot there is looked for first.
func (m *Manager) SetMirror(mi Mirror) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ms = mirrorState{mirror: mi, stamp: fileTime(m.Path)}
}

func fileTime(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// newerSnapshotLocked answers the stick's snapshot when it is newer than the local file was when
// this run opened it; it marks the stick as looked at when the stick is there.
func (m *Manager) newerSnapshotLocked() (string, bool) {
	if m.ms.mirror == nil || m.ms.checked {
		return "", false
	}
	dir, ok := m.ms.mirror.Dir()
	m.ms.present, m.ms.dir = ok, dir
	if !ok {
		return "", false
	}
	m.ms.checked = true
	snap := filepath.Join(dir, SnapshotName)
	fi, err := os.Stat(snap)
	if err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	if !fi.ModTime().After(m.ms.stamp.Add(SnapshotTolerance)) {
		return "", false
	}
	return snap, true
}

// loadSnapshot puts the stick's snapshot in place of the local file (the file closed): a copy
// beside it, synced, renamed over it. A snapshot bbolt cannot open is not taken.
func loadSnapshot(snap, path string) error {
	probe, err := bolt.Open(snap, 0o600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("the snapshot on the stick is not readable: %w", err)
	}
	_ = probe.Close()
	in, err := os.Open(snap)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".load"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// importLocked loads a newer snapshot from the stick into the local file. The file must be closed;
// the caller opens it afterwards (the keepers merge it with what memory holds).
func (m *Manager) importLocked() {
	snap, ok := m.newerSnapshotLocked()
	if !ok {
		return
	}
	if err := loadSnapshot(snap, m.Path); err != nil {
		m.log().Warn("store: the snapshot on the USB stick could not be loaded - the local file stays", "snapshot", snap, "err", err)
		return
	}
	m.ms.loaded = time.Now()
	m.log().Info("store: the snapshot on the USB stick was newer than the local file and was loaded", "snapshot", snap)
}

// SnapshotPath is where the snapshot is written before the helper copies it: occulited's private
// /tmp, which is RAM on the images (the helper gets the open file, never this path).
var SnapshotPath = filepath.Join(os.TempDir(), "occulited-store-snapshot.db")

// Snapshot writes a consistent copy of the open file to path (a read transaction: writers wait
// only for the time it takes).
func (d *DB) Snapshot(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	err = d.bolt.View(func(tx *bolt.Tx) error {
		_, err := tx.WriteTo(f)
		return err
	})
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// copyLocked writes the snapshot to the stick after a write of the file: at the interval, at a
// setting's change and at the stop. Before the first copy with the stick there, a newer snapshot
// on it is loaded (the stick came after the open).
func (m *Manager) copyLocked() {
	if m.ms.mirror == nil || m.db == nil {
		return
	}
	if !m.ms.checked {
		if snap, ok := m.newerSnapshotLocked(); ok {
			// the file closed without a write: memory holds everything since the open
			if err := m.db.Close(); err != nil {
				m.log().Warn("store: closing the database", "err", err)
			}
			m.db = nil
			if err := loadSnapshot(snap, m.Path); err != nil {
				m.log().Warn("store: the snapshot on the USB stick could not be loaded - the local file stays", "snapshot", snap, "err", err)
			} else {
				m.ms.loaded = time.Now()
				m.log().Info("store: the snapshot on the USB stick was newer than the local file and was loaded", "snapshot", snap)
			}
			m.applyLocked()
			if m.db == nil {
				return
			}
			_ = m.writeLocked(false)
		}
	}
	dir, ok := m.ms.mirror.Dir()
	m.ms.present, m.ms.dir = ok, dir
	if !ok {
		return
	}
	src := SnapshotPath
	err := m.db.Snapshot(src)
	if err == nil {
		err = m.ms.mirror.Copy(src)
		os.Remove(src)
	}
	if err != nil {
		label, _ := m.ms.mirror.Target()
		m.ms.lastErr = err.Error()
		m.log().Warn("store: the snapshot could not be copied to the USB stick", "label", label, "err", err)
		return
	}
	m.ms.last, m.ms.lastErr = time.Now(), ""
}

// copyStatusLocked is the status's copy part; nil without a stick.
func (m *Manager) copyStatusLocked() *CopyStatus {
	if m.ms.mirror == nil {
		return nil
	}
	label, folder := m.ms.mirror.Target()
	dir, ok := m.ms.mirror.Dir()
	cs := &CopyStatus{Label: label, Folder: folder, Present: ok, Dir: dir, LastError: m.ms.lastErr}
	if !m.ms.last.IsZero() {
		t := m.ms.last
		cs.LastCopy = &t
	}
	if !m.ms.loaded.IsZero() {
		t := m.ms.loaded
		cs.Loaded = &t
	}
	return cs
}
