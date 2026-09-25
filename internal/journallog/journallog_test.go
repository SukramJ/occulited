package journallog

import (
	"bytes"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// fakeJournal is a datagram socket that takes native entries like journald.
func fakeJournal(t *testing.T) (string, func() map[string]string) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "journal.socket")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return sock, func() map[string]string {
		t.Helper()
		buf := make([]byte, 1<<17)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			t.Fatalf("no entry: %v", err)
		}
		fields, err := journald.Decode(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, f := range fields {
			m[f.Key] = f.Value
		}
		return m
	}
}

func TestEntryIsTheMessageWithItsAttributesAndAPriority(t *testing.T) {
	sock, next := fakeJournal(t)
	log := slog.New(New(Options{Identifier: "occulited", Socket: sock}))

	log.Info("metadata: loaded", "path", "/usr/local/etc/occulite/meta.json", "revision", 1, "note", "two words")
	e := next()
	if e["MESSAGE"] != `metadata: loaded path=/usr/local/etc/occulite/meta.json revision=1 note="two words"` {
		t.Errorf("MESSAGE = %q", e["MESSAGE"])
	}
	if e["PRIORITY"] != "6" || e["SYSLOG_IDENTIFIER"] != "occulited" {
		t.Errorf("PRIORITY %q, SYSLOG_IDENTIFIER %q", e["PRIORITY"], e["SYSLOG_IDENTIFIER"])
	}
	if !strings.HasSuffix(e["CODE_FILE"], "journallog_test.go") || e["CODE_LINE"] == "" || !strings.Contains(e["CODE_FUNC"], "TestEntryIs") {
		t.Errorf("CODE_* = %q %q %q", e["CODE_FILE"], e["CODE_LINE"], e["CODE_FUNC"])
	}
	if _, ok := e["OCCULITED_AREA"]; ok {
		t.Error("a general line has no area")
	}

	for _, c := range []struct {
		level slog.Level
		want  string
	}{{slog.LevelDebug, "7"}, {slog.LevelWarn, "4"}, {slog.LevelError, "3"}, {slog.LevelError + 4, "3"}} {
		log.Log(t.Context(), c.level, "x")
		if got := next()["PRIORITY"]; got != c.want {
			t.Errorf("%v: PRIORITY %q, want %q", c.level, got, c.want)
		}
	}
}

func TestAttributesAndGroupsOfDerivedLoggers(t *testing.T) {
	sock, next := fakeJournal(t)
	log := slog.New(New(Options{Socket: sock})).With("unit", "rfd").WithGroup("req")
	log.Warn("restart", "n", 2)
	if got := next()["MESSAGE"]; got != "restart unit=rfd req.n=2" {
		t.Errorf("MESSAGE = %q", got)
	}
}

func TestAreaAndMultiLineMessage(t *testing.T) {
	sock, next := fakeJournal(t)
	h := New(Options{Identifier: "occulited", Socket: sock})
	slog.New(h.WithArea("acme")).Debug("order:\nfinalized")
	e := next()
	if e["OCCULITED_AREA"] != "acme" || e["MESSAGE"] != "order:\nfinalized" || e["PRIORITY"] != "7" {
		t.Errorf("entry %v", e)
	}
}

func TestLevelOption(t *testing.T) {
	var lv slog.LevelVar
	lv.Set(slog.LevelWarn)
	h := New(Options{Level: &lv, Socket: filepath.Join(t.TempDir(), "none")})
	if h.Enabled(t.Context(), slog.LevelInfo) || !h.Enabled(t.Context(), slog.LevelWarn) {
		t.Error("the level option is not applied")
	}
}

func TestFallbackWithoutTheSocket(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(New(Options{Identifier: "occulited", Socket: filepath.Join(t.TempDir(), "none"), Fallback: &buf}))
	log.Warn("config: keys ignored", "keys", "a, b")
	log.Error("two\nlines")
	want := "<4>config: keys ignored keys=\"a, b\"\n<3>two\\nlines\n"
	if buf.String() != want {
		t.Errorf("fallback wrote %q, want %q", buf.String(), want)
	}
}

func TestOnJournal(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "")
	if OnJournal() {
		t.Error("no JOURNAL_STREAM, yet on the journal")
	}
	t.Setenv("JOURNAL_STREAM", "1:2")
	if OnJournal() {
		t.Error("a JOURNAL_STREAM of another file matched stderr")
	}
}
