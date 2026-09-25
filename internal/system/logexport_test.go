package system

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeStream is journalctl for Export: it records the command line and serves out as its output.
type fakeStream struct {
	calls    []string
	out      string
	closeErr error
	closed   bool
	ctx      context.Context
}

func (f *fakeStream) run(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	f.ctx = ctx
	return &fakeReadCloser{Reader: strings.NewReader(f.out), f: f}, nil
}

type fakeReadCloser struct {
	io.Reader
	f *fakeStream
}

func (r *fakeReadCloser) Close() error {
	r.f.closed = true
	return r.f.closeErr
}

const twoEntries = `{"__REALTIME_TIMESTAMP":"1789203930250771","_HOSTNAME":"box","SYSLOG_IDENTIFIER":"sshd-session","_PID":"3142","PRIORITY":"6","MESSAGE":"Accepted publickey","_SYSTEMD_UNIT":"sshd.service"}
not json
{"__REALTIME_TIMESTAMP":"1789203931000000","_HOSTNAME":"box","SYSLOG_IDENTIFIER":"rfd","PRIORITY":"4","MESSAGE":"late"}
`

func TestJournalExportPassesEveryFilterWithoutALimit(t *testing.T) {
	cases := []struct {
		name string
		q    LogQuery
		want string
	}{
		{"no filter", LogQuery{}, "journalctl -o json --no-pager -q"},
		{"a page limit and follow are dropped", LogQuery{Limit: 500, Follow: true}, "journalctl -o json --no-pager -q"},
		{"every filter", LogQuery{Unit: "rfd", Tag: "S61rfd.script", Severity: "warning", Since: "@1757350800", Until: "@1757354400", Contains: "Address in use", Limit: 5},
			"journalctl -o json --no-pager -q -u rfd.service -t S61rfd.script -p 4 --since @1757350800 --until @1757354400 -g Address in use --case-sensitive=false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := &fakeStream{out: twoEntries}
			var got []LogLine
			err := JournalLog{Stream: fs.run}.Export(context.Background(), c.q, func(l LogLine) error {
				got = append(got, l)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(fs.calls) != 1 || fs.calls[0] != c.want {
				t.Errorf("command: %q\nwant      %q", fs.calls, c.want)
			}
			if len(got) != 2 || got[0].Tag != "sshd-session" || got[1].Message != "late" || !fs.closed {
				t.Errorf("lines %+v, closed %v", got, fs.closed)
			}
		})
	}
}

func TestJournalExportStopsAndReports(t *testing.T) {
	stop := errors.New("stop")
	t.Run("an error from emit stops the read and kills journalctl", func(t *testing.T) {
		fs := &fakeStream{out: twoEntries, closeErr: errors.New("signal: killed")}
		n := 0
		err := JournalLog{Stream: fs.run}.Export(context.Background(), LogQuery{}, func(LogLine) error {
			n++
			return stop
		})
		if !errors.Is(err, stop) || n != 1 || !fs.closed || fs.ctx.Err() == nil {
			t.Errorf("err %v, emitted %d, closed %v, ctx %v", err, n, fs.closed, fs.ctx.Err())
		}
	})
	t.Run("journalctl failing is the error", func(t *testing.T) {
		fs := &fakeStream{closeErr: errors.New("exit status 1: Failed to parse timestamp: yesterday-ish")}
		err := JournalLog{Stream: fs.run}.Export(context.Background(), LogQuery{Since: "yesterday-ish"}, func(LogLine) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "journalctl: exit status 1: Failed to parse timestamp") {
			t.Errorf("err %v", err)
		}
	})
	t.Run("a clean end is not cancelled", func(t *testing.T) {
		fs := &fakeStream{out: twoEntries}
		if err := (JournalLog{Stream: fs.run}).Export(context.Background(), LogQuery{}, func(LogLine) error { return nil }); err != nil {
			t.Fatal(err)
		}
		// the context is cancelled by the deferred cleanup only after Close has run
		if !fs.closed {
			t.Error("not closed")
		}
	})
}

func TestLogText(t *testing.T) {
	ts := time.Date(2026, 9, 12, 9, 8, 0, 123456000, time.FixedZone("CEST", 2*3600)).Format(time.RFC3339Nano)
	utc := time.Date(2026, 9, 12, 7, 8, 0, 0, time.UTC).Format(time.RFC3339Nano)
	ind := strings.Repeat(" ", len("2026-09-12T07:08:00+00:00 box node-red: "))
	cases := []struct {
		name string
		l    LogLine
		want string
	}{
		{"a journal entry", LogLine{Timestamp: ts, Time: "Sep 12 09:08:00", Host: "openccu-lite", Tag: "sshd-session", Unit: "sshd", PID: 3142, Message: "Accepted publickey"},
			"2026-09-12T09:08:00+02:00 openccu-lite sshd-session[3142]: Accepted publickey\n"},
		{"UTC as an offset, not Z", LogLine{Timestamp: utc, Host: "box", Tag: "rfd", Message: "x"},
			"2026-09-12T07:08:00+00:00 box rfd: x\n"},
		{"no tag: the unit", LogLine{Timestamp: ts, Host: "box", Unit: "hmipserver", PID: 7, Message: "up"},
			"2026-09-12T09:08:00+02:00 box hmipserver[7]: up\n"},
		{"neither tag nor unit", LogLine{Timestamp: ts, Host: "box", Message: "bare"},
			"2026-09-12T09:08:00+02:00 box: bare\n"},
		{"a busybox line keeps its own time", LogLine{Time: "Sep  6 03:46:30", Host: "openccu", Facility: "daemon", Severity: "err", Tag: "rfd", PID: 1678, Message: "Address in use"},
			"Sep  6 03:46:30 openccu rfd[1678]: Address in use\n"},
		{"a line syslogd did not format", LogLine{Message: "not a syslog line"},
			"not a syslog line\n"},
		{"a multi-line message is indented under its start", LogLine{Timestamp: utc, Host: "box", Tag: "node-red", Message: "Error:\n  at a\n  at b\n"},
			"2026-09-12T07:08:00+00:00 box node-red: Error:\n" + ind + "  at a\n" + ind + "  at b\n"},
		{"an empty message", LogLine{Timestamp: utc, Host: "box", Tag: "x"},
			"2026-09-12T07:08:00+00:00 box x: \n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LogText(c.l); got != c.want {
				t.Errorf("\ngot  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestTailBufferKeepsTheStart(t *testing.T) {
	b := &tailBuffer{max: 5}
	for _, s := range []string{"abc", "defgh", "ij"} {
		if n, err := b.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("write %q: %d %v", s, n, err)
		}
	}
	if b.String() != "abcde" {
		t.Errorf("%q", b.String())
	}
}

func TestExecStreamReportsTheExit(t *testing.T) {
	if _, err := ExecStream(context.Background(), "/nonexistent/journalctl"); err == nil {
		t.Error("a command that cannot start must fail at once")
	}
	// ls on a path that does not exist: nothing on stdout, a message on stderr, a non-zero exit
	out, err := ExecStream(context.Background(), "ls", "/nonexistent-occulited-test")
	if err != nil {
		t.Skip("no ls here:", err)
	}
	b, _ := io.ReadAll(out)
	cerr := out.Close()
	if len(b) != 0 || cerr == nil || !strings.Contains(cerr.Error(), "exit status") || !strings.Contains(cerr.Error(), "nonexistent-occulited-test") {
		t.Errorf("out %q, close %v", b, cerr)
	}
}
