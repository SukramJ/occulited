package runlog

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// fakeJournal is a journal that keeps what it was sent, or refuses.
type fakeJournal struct {
	mu      sync.Mutex
	entries [][]journald.Field
	fail    error
}

func (f *fakeJournal) Send(entries ...[]journald.Field) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.entries = append(f.entries, entries...)
	return nil
}

func field(e []journald.Field, key string) string {
	for _, f := range e {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

func TestRunEntries(t *testing.T) {
	j := &fakeJournal{}
	id := NewID(time.Date(2026, 9, 12, 22, 15, 0, 0, time.UTC))
	if !ValidID(id) || !strings.HasPrefix(id, "20260912T221500-") {
		t.Fatalf("id %q", id)
	}
	r := New(KindRadioFirmware, id, Options{Sender: j, Fields: []journald.Field{{Key: "OCCULITE_RUN_MODULE", Value: "HMIP-RFUSB"}}})
	r.Info("hmipserver stopped")
	r.Warn("the detection did not re-run")
	r.Err("failed: the flasher exited 1")
	r.Output("flasher", "writing 104616 bytes\r\n\n progress 50%\rprogress 100%\ndone\n", func(l string) int {
		if strings.Contains(l, "done") {
			return PriorityWarning
		}
		return PriorityInfo
	})
	if len(j.entries) != 6 {
		t.Fatalf("%d entries: %v", len(j.entries), j.entries)
	}
	for i, want := range []struct{ msg, prio, source string }{
		{"hmipserver stopped", "6", ""},
		{"the detection did not re-run", "4", ""},
		{"failed: the flasher exited 1", "3", ""},
		{"writing 104616 bytes", "6", "flasher"},
		{"progress 100%", "6", "flasher"},
		{"done", "4", "flasher"},
	} {
		e := j.entries[i]
		if field(e, "MESSAGE") != want.msg || field(e, "PRIORITY") != want.prio || field(e, FieldSource) != want.source {
			t.Errorf("entry %d: %v", i, e)
		}
		if field(e, FieldRun) != KindRadioFirmware || field(e, FieldRunID) != id || field(e, "SYSLOG_IDENTIFIER") != KindRadioFirmware || field(e, "OCCULITE_RUN_MODULE") != "HMIP-RFUSB" {
			t.Errorf("entry %d fields: %v", i, e)
		}
		// every field is one journald takes
		if dec, err := journald.Decode(journald.Encode(e)); err != nil || len(dec) != len(e) {
			t.Errorf("entry %d does not survive the protocol: %v %v", i, dec, err)
		}
	}
}

// The redaction runs before anything is written, to the journal and to the fallback alike.
func TestRunRedactsAndFallsBack(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	redact := func(s string) string { return strings.ReplaceAll(s, "hunter2", "[redacted]") }
	j := &fakeJournal{}
	r := New(KindACME, "20260912T221500-0badcafe", Options{Sender: j, Log: log, Redact: redact})
	r.Info("password hunter2 used")
	if field(j.entries[0], "MESSAGE") != "password [redacted] used" {
		t.Errorf("journal: %v", j.entries[0])
	}
	// the journal refuses: this line and the rest go to the log, with the run's id
	j.fail = errors.New("connection refused")
	r.Err("order failed for hunter2")
	r.Output("lego", "[INFO] acme: trying", nil)
	out := buf.String()
	if strings.Contains(out, "hunter2") || !strings.Contains(out, `msg="order failed for [redacted]"`) || !strings.Contains(out, "run_id=20260912T221500-0badcafe") || !strings.Contains(out, "source=lego") || !strings.Contains(out, "level=ERROR") {
		t.Errorf("fallback:\n%s", out)
	}
	if strings.Count(out, "could not be written") != 1 {
		t.Errorf("the journal's refusal said once:\n%s", out)
	}
	j.fail = nil
	buf.Reset()
	r.Info("still the log")
	if len(j.entries) != 1 || !strings.Contains(buf.String(), "still the log") {
		t.Errorf("after a refusal the run stays on the log: %d entries, %q", len(j.entries), buf.String())
	}

	// no journal at all: the log, at the line's level
	buf.Reset()
	r2 := New(KindRadioFirmware, "20260912T221501-00000001", Options{Log: log})
	r2.Warn("a warning")
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "run=radio-firmware") {
		t.Errorf("no journal: %q", buf.String())
	}
	// a nil run is silent
	var none *Run
	none.Info("x")
	none.Output("x", "y", nil)
}

func TestLinesAndIDs(t *testing.T) {
	long := strings.Repeat("ä", lineMax) // two bytes a rune
	got := Lines("a\n\n  \r\nb\r\nbar 10%\rbar 100%\n" + long)
	if len(got) != 5 || got[0] != "a" || got[1] != "b" || got[2] != "bar 100%" || got[3]+got[4] != long || !utf8Valid(got[3]) {
		t.Errorf("%d lines", len(got))
	}
	for _, bad := range []string{"", "20260912T221500", "20260912T221500-0BADCAFE", "20260912T221500-0badcafe; x", "../x", "20260912t221500-0badcafe"} {
		if ValidID(bad) {
			t.Errorf("valid: %q", bad)
		}
	}
	moment := time.Now()
	if first, second := NewID(moment), NewID(moment); first == second {
		t.Error("two ids of one moment are equal")
	}
	// the cap on a tool's output keeps the last lines and says so
	j := &fakeJournal{}
	r := New(KindACME, "20260912T221500-0badcafe", Options{Sender: j})
	var b strings.Builder
	for i := 0; i < outputLinesMax+5; i++ {
		b.WriteString("line\n")
	}
	r.Output("lego", b.String(), nil)
	if len(j.entries) != outputLinesMax+1 || !strings.Contains(field(j.entries[0], "MESSAGE"), "the first 5 lines") {
		t.Errorf("%d entries, first %v", len(j.entries), j.entries[0])
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "?") == s }
