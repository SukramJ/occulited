package store

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeStick is a USB stick's folder: there or not, and the helper's copy into it.
type fakeStick struct {
	dir     string
	present bool
	fail    error
	copies  int
}

func (f *fakeStick) Target() (string, string) { return "LOGSTICK", "occulited" }
func (f *fakeStick) Dir() (string, bool)      { return f.dir, f.present }
func (f *fakeStick) Copy(src string) error {
	if f.fail != nil {
		return f.fail
	}
	f.copies++
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(filepath.Join(f.dir, SnapshotName))
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func stickRig(t *testing.T) (*Manager, *fakeKeeper, *fakeStick, string) {
	t.Helper()
	old := SnapshotPath
	SnapshotPath = filepath.Join(t.TempDir(), "snap.db")
	t.Cleanup(func() { SnapshotPath = old })
	state := t.TempDir()
	stick := &fakeStick{dir: t.TempDir(), present: true}
	k := &fakeKeeper{}
	m := &Manager{Path: Path(state), Platform: "rpi4"}
	m.Register(k)
	m.SetMirror(stick)
	return m, k, stick, state
}

// task 229: ram-sync's writes (and the stop) copy a snapshot to the stick; a missing stick keeps
// everything local and says so; a newer snapshot on the stick is loaded at the open.
func TestSnapshotToTheStick(t *testing.T) {
	m, k, stick, state := stickRig(t)
	m.Start(ModeRAMSync, "15min")
	k.add(1)
	if err := m.Set(ModeRAMSync, "15min", ""); err != nil {
		t.Fatal(err)
	}
	if stick.copies != 1 {
		t.Fatalf("copies %d", stick.copies)
	}
	st := m.Status("", "usb:LOGSTICK/occulited")
	if st.Copy == nil || !st.Copy.Present || st.Copy.LastCopy == nil || st.Copy.Label != "LOGSTICK" || st.Copy.LastError != "" {
		t.Fatalf("%+v", st.Copy)
	}
	if _, err := os.Stat(SnapshotPath); !os.IsNotExist(err) {
		t.Fatal("the snapshot in RAM was left behind")
	}
	// the stick pulled: nothing copied, the status says so
	stick.present = false
	k.add(2)
	_ = m.Set(ModeRAMSync, "15min", "")
	if stick.copies != 1 || m.Status("", "").Copy.Present {
		t.Fatalf("copied without a stick: %d", stick.copies)
	}
	// a copy that fails: the error, kept for the warning
	stick.present, stick.fail = true, errors.New("the stick is read-only")
	_ = m.Set(ModeRAMSync, "15min", "")
	if e := m.Status("", "").Copy.LastError; !strings.Contains(e, "read-only") {
		t.Fatalf("%q", e)
	}
	stick.fail = nil
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	cancel()
	<-m.Done()
	if stick.copies != 2 {
		t.Fatalf("the stop's copy: %d", stick.copies)
	}
	// the card replaced: an empty userfs, the stick's snapshot is loaded at the open
	_ = os.RemoveAll(filepath.Join(state, DirName))
	m2 := &Manager{Path: Path(state), Platform: "rpi4"}
	k2 := &fakeKeeper{}
	m2.Register(k2)
	m2.SetMirror(stick)
	m2.Start(ModeRAMSync, "15min")
	if got := k2.restored["s"]; len(got) != 2 {
		t.Fatalf("restored %v", k2.restored)
	}
	if st := m2.Status("", ""); st.Copy.Loaded == nil {
		t.Fatalf("%+v", st.Copy)
	}
}

// the stick came after the open (it mounts later at boot): before the first copy, its newer
// snapshot is loaded and merged with what memory holds - never overwritten by an empty file
func TestSnapshotStickComesLate(t *testing.T) {
	m, k, stick, state := stickRig(t)
	m.Start(ModeRAMSync, "15min")
	k.add(1)
	_ = m.Set(ModeRAMSync, "15min", "")
	m.mu.Lock()
	_ = m.db.Close()
	m.db = nil
	m.mu.Unlock()
	// a new card: no local file; the stick is not there at the open
	_ = os.RemoveAll(filepath.Join(state, DirName))
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(filepath.Join(stick.dir, SnapshotName), future, future)
	stick.present = false
	m2 := &Manager{Path: Path(state), Platform: "rpi4"}
	k2 := &fakeKeeper{}
	m2.Register(k2)
	m2.SetMirror(stick)
	m2.Start(ModeRAMSync, "15min")
	k2.add(3)
	if len(k2.restored["s"]) != 0 {
		t.Fatal("loaded without the stick")
	}
	stick.present = true
	_ = m2.Set(ModeRAMSync, "15min", "")
	if len(k2.restored["s"]) != 1 {
		t.Fatalf("the late stick's snapshot was not loaded: %v", k2.restored)
	}
	// memory kept its own row beside the loaded one, and the file and the copy have both
	k2.mu.Lock()
	n := len(k2.rows)
	k2.mu.Unlock()
	if n != 2 {
		t.Fatalf("rows %d", n)
	}
}

func TestValidateForUSB(t *testing.T) {
	if err := ValidateFor("rpi4", "", "", "usb:LOGSTICK/db"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ platform, mode string }{{"rpi4", ModePersistent}, {"rpi4", ModeRAM}, {"ova", ""}} {
		if err := ValidateFor(c.platform, c.mode, "", "usb:LOGSTICK/db"); err == nil || !strings.Contains(err.Error(), "ram-sync") {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if FilePath("usb:LOGSTICK/db", "/s") != "/s/data/occulited.db" {
		t.Fatal(FilePath("usb:LOGSTICK/db", "/s"))
	}
}
