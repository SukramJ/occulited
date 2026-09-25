package backupcrypt

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func TestStoreSetupRotationAndSwitch(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	s := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }}
	v, err := s.View()
	if err != nil || v.Enabled || v.Recovery != nil || v.Box != nil || len(v.Previous) != 0 {
		t.Fatalf("%v %+v", err, v)
	}
	if _, err := s.SetEnabled(true); !errors.Is(err, ErrNoRecoveryKey) {
		t.Errorf("enabled without a key: %v", err)
	}
	if _, _, _, err := s.Recipients(); !errors.Is(err, ErrNoRecoveryKey) {
		t.Errorf("recipients without a key: %v", err)
	}
	// setup with a browser-made recipient: nothing is saved before the confirmation
	sec1, _ := NewSecret()
	pid, fp, err := s.BeginRecovery(sec1.Recipient())
	if err != nil || fp != sec1.Fingerprint() {
		t.Fatal(err, fp)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, StateFile)); !errors.Is(err, os.ErrNotExist) {
		t.Error("saved before the confirmation")
	}
	if _, err := s.Confirm("nope"); !errors.Is(err, ErrPendingUnknown) {
		t.Errorf("unknown pending: %v", err)
	}
	st, err := s.Confirm(pid)
	if err != nil || !st.Enabled || st.Recovery.Recipient != sec1.Recipient() || !st.Recovery.Created.Equal(now) {
		t.Fatalf("%v %+v", err, st)
	}
	if _, err := s.Confirm(pid); !errors.Is(err, ErrPendingUnknown) {
		t.Error("a pending id confirms once")
	}
	// the tag was written before the identity, and the identity exists now
	if _, err := os.Stat(filepath.Join(s.Dir, KeyDir, IdentityTag)); err != nil {
		t.Error("no .nobackup")
	}
	box, _, ok := s.BoxIdentity()
	if !ok {
		t.Fatal("no box identity")
	}
	v, _ = s.View()
	if !v.Enabled || v.Recovery.Fingerprint != sec1.Fingerprint() || v.Box == nil || v.Box.Fingerprint != Fingerprint(box.Recipient().String()) {
		t.Errorf("%+v", v)
	}
	b, r, meta, err := s.Recipients()
	if err != nil || b != box.Recipient().String() || r != sec1.Recipient() || meta.RecoveryFingerprint != sec1.Fingerprint() {
		t.Errorf("%v %s %s %+v", err, b, r, meta)
	}
	if kind, _ := s.Match(sec1.Recipient()); kind != "current" {
		t.Error(kind)
	}
	// a generated key (the HTTP fallback): the code is returned once
	now = now.Add(time.Hour)
	pid2, fp2, code, err := s.BeginGenerated()
	if err != nil || code == "" {
		t.Fatal(err)
	}
	sec2, _ := ParseSecret(code)
	if sec2.Fingerprint() != fp2 {
		t.Error("the code does not give the pending recipient")
	}
	st, err = s.Confirm(pid2)
	if err != nil || st.Recovery.Recipient != sec2.Recipient() || len(st.Previous) != 1 || st.Previous[0].Recipient != sec1.Recipient() || !st.Previous[0].Retired.Equal(now) {
		t.Fatalf("%v %+v", err, st)
	}
	if kind, k := s.Match(sec1.Recipient()); kind != "previous" || k.Fingerprint != sec1.Fingerprint() {
		t.Error(kind)
	}
	if kind, _ := s.MatchFingerprint(sec2.Fingerprint()); kind != "current" {
		t.Error(kind)
	}
	if kind, _ := s.MatchFingerprint("0000-0000-0000-0000"); kind != "unknown" {
		t.Error(kind)
	}
	if kind, _ := s.MatchFingerprint(""); kind != "unknown" {
		t.Error(kind)
	}
	other, _ := NewSecret()
	if kind, _ := s.Match(other.Recipient()); kind != "none" {
		t.Error(kind)
	}
	// the box identity survives the rotation
	if b2, _, _ := s.BoxIdentity(); b2.String() != box.String() {
		t.Error("the box identity changed at a rotation")
	}
	// the same key confirmed again rotates nothing
	pid3, _, _ := s.BeginRecovery(sec2.Recipient())
	st, _ = s.Confirm(pid3)
	if len(st.Previous) != 1 || st.Recovery.Recipient != sec2.Recipient() {
		t.Errorf("%+v", st)
	}
	// an old key set up again comes back to the front, and the current one retires
	pid4, _, _ := s.BeginRecovery(sec1.Recipient())
	st, _ = s.Confirm(pid4)
	if st.Recovery.Recipient != sec1.Recipient() || len(st.Previous) != 1 || st.Previous[0].Recipient != sec2.Recipient() {
		t.Errorf("%+v", st)
	}
	// off and on
	if st, err := s.SetEnabled(false); err != nil || st.Enabled || s.Enabled() {
		t.Error("off")
	}
	if st, err := s.SetEnabled(true); err != nil || !st.Enabled || !s.Enabled() {
		t.Error("on")
	}
	// the view never carries a recipient string
	raw, _ := os.ReadFile(filepath.Join(s.Dir, StateFile))
	if !strings.Contains(string(raw), sec1.Recipient()) {
		t.Error("the state file should hold the recipient")
	}
	v, _ = s.View()
	if v.Recovery.Fingerprint != sec1.Fingerprint() || len(v.Previous) != 1 || v.Previous[0].Retired == nil {
		t.Errorf("%+v", v)
	}
}

func TestStorePendingExpires(t *testing.T) {
	now := time.Now()
	s := &Store{Dir: t.TempDir(), Now: func() time.Time { return now }}
	sec, _ := NewSecret()
	pid, _, _ := s.BeginRecovery(sec.Recipient())
	now = now.Add(PendingTTL + time.Second)
	if _, err := s.Confirm(pid); !errors.Is(err, ErrPendingUnknown) {
		t.Errorf("expired pending confirmed: %v", err)
	}
	if _, _, err := s.BeginRecovery("age1pq1qqqq"); !errors.Is(err, ErrUnsupported) {
		t.Error(err)
	}
	if _, _, err := s.BeginRecovery("hello"); !errors.Is(err, ErrFormat) {
		t.Error(err)
	}
}

func TestStoreBoxIdentityTagAndCarry(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if b, _ := s.ExportBoxIdentity(); b != nil {
		t.Error("export without identity")
	}
	id, err := s.EnsureBoxIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(s.Dir, KeyDir)
	if st, err := os.Stat(filepath.Join(dir, IdentityFile)); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("identity file: %v %v", err, st)
	}
	// the tag deleted: re-created before the next use of the identity
	_ = os.Remove(filepath.Join(dir, IdentityTag))
	if id2, err := s.EnsureBoxIdentity(); err != nil || id2.String() != id.String() {
		t.Fatal("identity changed", err)
	}
	if _, err := os.Stat(filepath.Join(dir, IdentityTag)); err != nil {
		t.Error("tag not re-created")
	}
	// the carry-over: exported with its creation time, adopted only where none exists, and the
	// adopted identity keeps that time (openccu-lite B-194: not the restore boot's pre-NTP clock)
	made := time.Date(2026, 9, 24, 7, 30, 38, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(dir, IdentityFile), made, made); err != nil {
		t.Fatal(err)
	}
	carried, created := s.ExportBoxIdentity()
	if string(carried) != id.String()+"\n" || !created.Equal(made) {
		t.Errorf("%q %v", carried, created)
	}
	if adopted, err := s.AdoptBoxIdentity(carried, created); err != nil || adopted {
		t.Error("adopted over an own identity", err)
	}
	fresh := &Store{Dir: t.TempDir()}
	if adopted, err := fresh.AdoptBoxIdentity(carried, created); err != nil || !adopted {
		t.Fatal("not adopted", err)
	}
	if got, gotCreated, ok := fresh.BoxIdentity(); !ok || got.String() != id.String() || !gotCreated.Equal(made) {
		t.Errorf("adopted identity differs: %v created %v", ok, gotCreated)
	}
	if _, err := os.Stat(filepath.Join(fresh.Dir, KeyDir, IdentityTag)); err != nil {
		t.Error("the adopted identity has no tag")
	}
	// without a time the adoption's own moment stands
	fresh2 := &Store{Dir: t.TempDir()}
	if adopted, err := fresh2.AdoptBoxIdentity(carried, time.Time{}); err != nil || !adopted {
		t.Fatal("not adopted", err)
	}
	if _, gotCreated, _ := fresh2.BoxIdentity(); time.Since(gotCreated) > time.Minute {
		t.Errorf("created %v", gotCreated)
	}
	if _, err := (&Store{Dir: t.TempDir()}).AdoptBoxIdentity([]byte("garbage"), created); err == nil {
		t.Error("garbage adopted")
	}
	// a created record
	if s.CreatedHere("abc") {
		t.Error("created before any record")
	}
	for i := 0; i < maxCreated+5; i++ {
		_ = s.RecordCreated(Created{SHA256: strings.Repeat("a", 60) + string(rune('0'+i%10)) + string(rune('0'+i/10%10)) + string(rune('0'+i/100)), Name: "x.sbk.age", Size: 1, Encrypted: true})
	}
	_ = s.RecordCreated(Created{SHA256: "abc", Name: "y.sbk", Size: 2})
	if !s.CreatedHere("abc") {
		t.Error("not found")
	}
	list, _ := s.created()
	if len(list) != maxCreated || list[0].SHA256 != "abc" {
		t.Errorf("%d entries, first %s", len(list), list[0].SHA256)
	}
	_ = age.Identity(id)
}

// GNU tar with createBackup.sh:67's options over a state directory leaves the key directory out
// of the archive and the state file in it.
func TestNobackupTagWithTar(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("no tar")
	}
	root := t.TempDir()
	state := filepath.Join(root, "usr/local/etc/occulite")
	s := &Store{Dir: state}
	sec, _ := NewSecret()
	pid, _, _ := s.BeginRecovery(sec.Recipient())
	if _, err := s.Confirm(pid); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordCreated(Created{SHA256: "abc"})
	cmd := exec.Command("tar", "-C", root, "--exclude=usr/local/tmp", "--exclude=usr/local/.*", "--exclude-tag=.nobackup", "-cvf", "/dev/null", "usr/local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "unrecognized option") {
			t.Skip("not GNU tar")
		}
		t.Fatalf("%v: %s", err, out)
	}
	list := string(out)
	if strings.Contains(list, IdentityFile) || strings.Contains(list, CreatedFile) {
		t.Errorf("the identity is in the archive:\n%s", list)
	}
	if !strings.Contains(list, StateFile) || !strings.Contains(list, KeyDir+"/"+IdentityTag) {
		t.Errorf("the state file or the tag is missing:\n%s", list)
	}
}
