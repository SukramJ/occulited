package system

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// The log download: every entry a filter lets through, oldest first, without the page's limit,
// handed over one at a time as it is read - the journal is never held in memory.

// LogExporter streams every matching entry, oldest first, to emit. An error from emit stops the
// read at once and is returned as it is.
type LogExporter interface {
	Export(ctx context.Context, q LogQuery, emit func(LogLine) error) error
}

// StreamRunner starts a command and hands over its standard output while it is written. Close
// waits for the command and reports how it ended; cancelling ctx before that kills it.
type StreamRunner func(ctx context.Context, name string, args ...string) (io.ReadCloser, error)

// ExecStream runs the command for real.
func ExecStream(ctx context.Context, name string, args ...string) (io.ReadCloser, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stderr := &tailBuffer{max: 2048}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &cmdStream{ReadCloser: out, cmd: cmd, stderr: stderr}, nil
}

type cmdStream struct {
	io.ReadCloser
	cmd    *exec.Cmd
	stderr *tailBuffer
}

// Close closes the read end first - a command still writing then ends on SIGPIPE - and waits.
func (c *cmdStream) Close() error {
	_ = c.ReadCloser.Close()
	err := c.cmd.Wait()
	if msg := strings.TrimSpace(c.stderr.String()); err != nil && msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// tailBuffer keeps the first max bytes written to it: enough for a command's error message, and
// never more than that whatever the command prints.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if room := t.max - len(t.b); room > 0 {
		t.b = append(t.b, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// Export streams `journalctl -o json` with the query's filters and no line count: everything the
// journal holds that matches, oldest first. The query's Limit and Follow are ignored.
func (j JournalLog) Export(ctx context.Context, q LogQuery, emit func(LogLine) error) error {
	q.Limit, q.Follow = 0, false
	run := j.Stream
	if run == nil {
		run = ExecStream
	}
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	out, err := run(cmdCtx, "journalctl", j.args(q)...)
	if err != nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	place := j.kernelClock(ctx) // B-116: the download's kernel lines at their boot's start plus their stamp
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var emitErr error
	for sc.Scan() {
		l, ok := parseJournalLine(sc.Bytes(), place)
		if !ok {
			continue
		}
		if emitErr = emit(l); emitErr != nil {
			break
		}
	}
	scanErr := sc.Err()
	if emitErr != nil || scanErr != nil {
		// stopped early: journalctl is killed, and how it ended says nothing about the read. Only
		// here - cancelling after a clean end would turn journalctl's own exit into "killed".
		cancel()
		_ = out.Close()
		if emitErr != nil {
			return emitErr
		}
		return fmt.Errorf("journalctl: %w", scanErr)
	}
	if err := out.Close(); err != nil && ctx.Err() == nil {
		return fmt.Errorf("journalctl: %w", err)
	}
	return ctx.Err()
}

// LogText is one entry as text, in the shape `journalctl -o short-iso` prints:
//
//	2026-09-12T11:05:30+02:00 openccu-lite sshd-session[3142]: Accepted publickey for root
//
// The source is the syslog tag (SYSLOG_IDENTIFIER, as journalctl shows it) or else the unit. A
// busybox line has no year and keeps its own time ("Sep  6 03:46:28"); a line syslogd did not
// format is its text alone. The further lines of a multi-line message are indented to where the
// message starts, as journalctl indents them, so a line at the left edge is always a new entry.
// The result ends in a newline.
func LogText(l LogLine) string {
	var head []string
	ts := l.Time
	if t, err := time.Parse(time.RFC3339Nano, l.Timestamp); err == nil {
		ts = t.Format("2006-01-02T15:04:05-07:00")
	}
	if ts != "" {
		head = append(head, ts)
	}
	if l.Host != "" {
		head = append(head, l.Host)
	}
	src := l.Tag
	if src == "" {
		src = l.Unit
	}
	if src != "" {
		if l.PID > 0 {
			src += "[" + strconv.Itoa(l.PID) + "]"
		}
		head = append(head, src)
	}
	prefix := ""
	if len(head) > 0 {
		prefix = strings.Join(head, " ") + ": "
	}
	msg := strings.Split(strings.TrimRight(l.Message, "\r\n"), "\n")
	var sb strings.Builder
	sb.WriteString(prefix)
	sb.WriteString(msg[0])
	sb.WriteByte('\n')
	indent := strings.Repeat(" ", utf8.RuneCountInString(prefix))
	for _, m := range msg[1:] {
		sb.WriteString(indent)
		sb.WriteString(m)
		sb.WriteByte('\n')
	}
	return sb.String()
}
