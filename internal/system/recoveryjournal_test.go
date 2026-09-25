package system

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// all reads every entry that arrives until none comes for a while.
func (j *fakeJournal) all() []map[string]string {
	j.t.Helper()
	var out []map[string]string
	buf := make([]byte, 1<<20)
	for {
		_ = j.conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))
		n, err := j.conn.Read(buf)
		if err != nil {
			return out
		}
		fields, err := journald.Decode(buf[:n])
		if err != nil {
			j.t.Fatal(err)
		}
		m := map[string]string{}
		for _, f := range fields {
			m[f.Key] = f.Value
		}
		out = append(out, m)
	}
}

func recoveryRoot(t *testing.T, files map[string][]byte) (Root, string) {
	t.Helper()
	r := rootWith(t, nil)
	dir := r.join(RecoveryLogDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return r, dir
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)) }

// A recovery log becomes entries with the identifier recovery: a header with the file's time,
// then one entry per line, fwinstall's HTML taken out, ERROR lines at priority err, a line's own
// timestamp as SYSLOG_TIMESTAMP; the file is removed afterwards.
func TestRecoveryImport(t *testing.T) {
	j := newFakeJournal(t)
	r, dir := recoveryRoot(t, map[string][]byte{
		"2026-09-23T15-20-01Z.log": []byte("Starting firmware update (DO NOT INTERRUPT!!!):<br/>\n[1/5] Validate update directory... OK<br/>\n\n[2/5] Checking &lt;free&gt; disk space... ERROR: (not enough)<br/>\nprogress 10%\rprogress 100%\r\n2026-09-23T15:21:07Z WARNING: something to note<br/>\nFinished firmware update successfully.<br/>\n"),
		"notes.txt":                []byte("not a log\n"),
		".hidden.log":              []byte("a dotfile\n"),
	})
	imp := &RecoveryImporter{Root: r, Journal: &journald.Writer{Socket: j.sock}, Log: quietLog()}
	imported, skipped := imp.Import()
	if imported != 1 || skipped != 0 {
		t.Fatalf("imported %d skipped %d", imported, skipped)
	}
	got := j.all()
	wantMsgs := []string{
		"install log of the recovery system from 2026-09-23T15:20:01Z (6 lines)",
		"Starting firmware update (DO NOT INTERRUPT!!!):",
		"[1/5] Validate update directory... OK",
		"[2/5] Checking <free> disk space... ERROR: (not enough)",
		"progress 100%",
		"2026-09-23T15:21:07Z WARNING: something to note",
		"Finished firmware update successfully.",
	}
	if len(got) != len(wantMsgs) {
		t.Fatalf("%d entries, want %d: %+v", len(got), len(wantMsgs), got)
	}
	wantPrio := []string{"6", "6", "6", "3", "6", "4", "6"}
	for i, m := range got {
		if m["MESSAGE"] != wantMsgs[i] {
			t.Errorf("entry %d: %q, want %q", i, m["MESSAGE"], wantMsgs[i])
		}
		if m["PRIORITY"] != wantPrio[i] {
			t.Errorf("entry %d: priority %s, want %s (%q)", i, m["PRIORITY"], wantPrio[i], m["MESSAGE"])
		}
		if m["SYSLOG_IDENTIFIER"] != "recovery" || m["RECOVERY_LOG"] != "2026-09-23T15-20-01Z.log" || m["RECOVERY_TIME"] != "2026-09-23T15:20:01Z" {
			t.Errorf("entry %d: fields %+v", i, m)
		}
		if i == 5 {
			if m["SYSLOG_TIMESTAMP"] != "2026-09-23T15:21:07Z" {
				t.Errorf("the timestamped line: SYSLOG_TIMESTAMP=%q", m["SYSLOG_TIMESTAMP"])
			}
		} else if _, has := m["SYSLOG_TIMESTAMP"]; has {
			t.Errorf("entry %d carries a SYSLOG_TIMESTAMP: %+v", i, m)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-09-23T15-20-01Z.log")); !os.IsNotExist(err) {
		t.Error("the imported file was not removed")
	}
	for _, keep := range []string{"notes.txt", ".hidden.log"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("%s should be left alone: %v", keep, err)
		}
	}
	// a second run has nothing to do
	if imported, skipped := imp.Import(); imported != 0 || skipped != 0 {
		t.Errorf("second run: imported %d skipped %d", imported, skipped)
	}
	j.nothing()
}

// Only the last 5,000 lines of a long log go in, after a line that says how many are left out.
func TestRecoveryImportCap(t *testing.T) {
	j := newFakeJournal(t)
	var b strings.Builder
	for i := 1; i <= recoveryLinesMax+250; i++ {
		fmt.Fprintf(&b, "line %d<br/>\n", i)
	}
	r, _ := recoveryRoot(t, map[string][]byte{"2026-01-02T03-04-05Z.log": []byte(b.String())})
	imp := &RecoveryImporter{Root: r, Journal: &journald.Writer{Socket: j.sock}, Log: quietLog()}
	// read while importing, as journald does: 5,000 datagrams do not fit a socket's receive
	// buffer, and Send waits for room
	done := make(chan int, 1)
	go func() { n, _ := imp.Import(); done <- n }()
	got := j.all()
	if imported := <-done; imported != 1 {
		t.Fatal("not imported")
	}
	if len(got) != recoveryLinesMax+2 {
		t.Fatalf("%d entries, want %d", len(got), recoveryLinesMax+2)
	}
	if !strings.HasSuffix(got[0]["MESSAGE"], fmt.Sprintf("(%d lines)", recoveryLinesMax+1)) {
		t.Errorf("header: %q", got[0]["MESSAGE"])
	}
	if got[1]["MESSAGE"] != "(the first 250 lines of the output are left out here)" {
		t.Errorf("the left-out line: %q", got[1]["MESSAGE"])
	}
	if got[2]["MESSAGE"] != "line 251" || got[len(got)-1]["MESSAGE"] != fmt.Sprintf("line %d", recoveryLinesMax+250) {
		t.Errorf("first kept %q, last %q", got[2]["MESSAGE"], got[len(got)-1]["MESSAGE"])
	}
}

// A file that is not text, or empty, is skipped with one warning entry and removed; a name
// without a parseable time still imports, without RECOVERY_TIME. Files go oldest first.
func TestRecoveryImportBrokenAndOrder(t *testing.T) {
	j := newFakeJournal(t)
	r, dir := recoveryRoot(t, map[string][]byte{
		"2026-02-01T00-00-00Z.log": {0xff, 0xfe, 0x00, 'x'},
		"2026-01-01T00-00-00Z.log": []byte("first<br/>\n"),
		"2026-03-01T00-00-00Z.log": []byte("\n\n"),
		"manual.log":               []byte("by hand\n"),
	})
	imp := &RecoveryImporter{Root: r, Journal: &journald.Writer{Socket: j.sock}, Log: quietLog()}
	imported, skipped := imp.Import()
	if imported != 2 || skipped != 2 {
		t.Fatalf("imported %d skipped %d", imported, skipped)
	}
	got := j.all()
	msgs := make([]string, len(got))
	for i, m := range got {
		msgs[i] = m["MESSAGE"]
	}
	want := []string{
		"install log of the recovery system from 2026-01-01T00:00:00Z (1 lines)", "first",
		"recovery log 2026-02-01T00-00-00Z.log skipped: not text (invalid UTF-8)",
		"recovery log 2026-03-01T00-00-00Z.log skipped: empty",
		"install log of the recovery system from manual.log (1 lines)", "by hand",
	}
	if strings.Join(msgs, "|") != strings.Join(want, "|") {
		t.Errorf("entries:\n%s\nwant:\n%s", strings.Join(msgs, "\n"), strings.Join(want, "\n"))
	}
	if got[2]["PRIORITY"] != "4" || got[2]["SYSLOG_IDENTIFIER"] != "recovery" || got[2]["RECOVERY_LOG"] != "2026-02-01T00-00-00Z.log" {
		t.Errorf("the skip entry: %+v", got[2])
	}
	if _, has := got[4]["RECOVERY_TIME"]; has {
		t.Errorf("a name without a time carries RECOVERY_TIME: %+v", got[4])
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Errorf("files left: %d", len(left))
	}
}

// When the journal cannot be written the file stays for the next start; without the directory
// there is nothing to do.
func TestRecoveryImportKeepsFileWithoutJournal(t *testing.T) {
	r, dir := recoveryRoot(t, map[string][]byte{"2026-01-01T00-00-00Z.log": []byte("kept<br/>\n")})
	imp := &RecoveryImporter{Root: r, Journal: &journald.Writer{Socket: filepath.Join(t.TempDir(), "nowhere.sock"), Timeout: 200 * time.Millisecond}, Log: quietLog()}
	if imported, skipped := imp.Import(); imported != 0 || skipped != 0 {
		t.Errorf("imported %d skipped %d", imported, skipped)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-01-01T00-00-00Z.log")); err != nil {
		t.Errorf("the file should stay: %v", err)
	}
	none := &RecoveryImporter{Root: rootWith(t, nil), Journal: &journald.Writer{}, Log: quietLog()}
	if imported, skipped := none.Import(); imported != 0 || skipped != 0 {
		t.Errorf("no directory: imported %d skipped %d", imported, skipped)
	}
}
