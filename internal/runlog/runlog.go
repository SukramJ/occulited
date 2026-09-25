// Package runlog writes the lines of one run - a radio module firmware flash, an ACME attempt -
// into the journal, one entry per line (task 102, D-59). The journal is the one log of the box:
// a run keeps no lines of its own in a state file any more, only its result and its id, and the
// pages read the lines back from the journal by that id.
//
// Every entry carries OCCULITE_RUN (the kind), OCCULITE_RUN_ID (one id per run), the run's own
// fields (the module, the names), SYSLOG_IDENTIFIER (the kind, so the Log page's Tag filter names
// it) and a priority that fits the line. A tool's output - the flasher's, lego's - is written the
// same way, one entry per line, with OCCULITE_RUN_SOURCE naming the tool.
//
// Where there is no journal (a busybox box, a development daemon) or the journal refuses a line,
// the line goes to occulited's own logger instead, with run and run_id as attributes: busybox
// syslog then carries "run_id=<id>" in the message, which the Log API's run filter finds there.
package runlog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// The fields every entry of a run carries.
const (
	FieldRun    = "OCCULITE_RUN"
	FieldRunID  = "OCCULITE_RUN_ID"
	FieldSource = "OCCULITE_RUN_SOURCE"
)

// The kinds of run.
const (
	KindRadioFirmware = "radio-firmware"
	KindACME          = "acme"
)

// Syslog priorities of the PRIORITY field.
const (
	PriorityErr     = 3
	PriorityWarning = 4
	PriorityInfo    = 6
)

const (
	// a longer line is split into entries of this size
	lineMax = 16 << 10
	// the most lines one Output writes: the last ones, where a tool fails
	outputLinesMax = 2000
)

// Sender is where the entries go: journald.Writer.
type Sender interface {
	Send(entries ...[]journald.Field) error
}

var idRe = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}-[0-9a-f]{8}$`)

// ValidID says whether id has the shape NewID gives: the Log API takes nothing else as a run.
func ValidID(id string) bool { return idRe.MatchString(id) }

// NewID is a run id: the start in UTC to the second, and eight random hex digits.
func NewID(now time.Time) string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// no randomness: the nanoseconds still tell two runs of one second apart
		return now.UTC().Format("20060102T150405") + "-" + fmt.Sprintf("%08x", uint32(now.Nanosecond()))
	}
	return now.UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b[:])
}

// Options configure a run.
type Options struct {
	// Sender is the journal; nil = the lines go to Log.
	Sender Sender
	// Log is occulited's logger, the fallback; nil = slog's default.
	Log *slog.Logger
	// Redact is applied to every line before it is written anywhere; nil = none.
	Redact func(string) string
	// Fields are the run's own fields (OCCULITE_RUN_MODULE, OCCULITE_RUN_NAMES, ...).
	Fields []journald.Field
}

// Run is one run's writer. The methods of a nil *Run do nothing.
type Run struct {
	Kind string
	ID   string

	o      Options
	mu     sync.Mutex
	failed bool // the journal refused a line: this run's lines go to the log from then on
}

// New is the writer of the run id of kind.
func New(kind, id string, o Options) *Run {
	return &Run{Kind: kind, ID: id, o: o}
}

// Info, Warn and Err write one line at that priority.
func (r *Run) Info(msg string) { r.Line(PriorityInfo, msg) }
func (r *Run) Warn(msg string) { r.Line(PriorityWarning, msg) }
func (r *Run) Err(msg string)  { r.Line(PriorityErr, msg) }

// Line writes one entry; a message with newlines is written as several.
func (r *Run) Line(priority int, msg string) {
	if r == nil {
		return
	}
	r.write(priority, "", Lines(msg))
}

// Output writes a tool's output, one entry per line, marked with the source. priority decides a
// line's priority; nil = info for every line.
func (r *Run) Output(source, text string, priority func(line string) int) {
	if r == nil {
		return
	}
	lines := Lines(text)
	if len(lines) > outputLinesMax {
		left := len(lines) - outputLinesMax
		lines = append([]string{fmt.Sprintf("(the first %d lines of the output are left out here)", left)}, lines[left:]...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range lines {
		p := PriorityInfo
		if priority != nil {
			p = priority(l)
		}
		r.writeLocked(p, source, l)
	}
}

func (r *Run) write(priority int, source string, lines []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range lines {
		r.writeLocked(priority, source, l)
	}
}

func (r *Run) writeLocked(priority int, source, msg string) {
	if r.o.Redact != nil {
		msg = r.o.Redact(msg)
	}
	if r.o.Sender != nil && !r.failed {
		err := r.o.Sender.Send(r.Entry(priority, source, msg))
		if err == nil {
			return
		}
		r.failed = true
		r.logger().Warn("journal: a run's lines could not be written; the rest of the run goes to occulited's log", "run", r.Kind, "run_id", r.ID, "err", err)
	}
	attrs := []any{"run", r.Kind, "run_id", r.ID}
	if source != "" {
		attrs = append(attrs, "source", source)
	}
	r.logger().Log(context.Background(), slogLevel(priority), msg, attrs...)
}

// Entry is one line's journal entry.
func (r *Run) Entry(priority int, source, msg string) []journald.Field {
	f := []journald.Field{
		{Key: "MESSAGE", Value: msg},
		{Key: "PRIORITY", Value: strconv.Itoa(priority)},
		{Key: "SYSLOG_IDENTIFIER", Value: r.Kind},
		{Key: FieldRun, Value: r.Kind},
		{Key: FieldRunID, Value: r.ID},
	}
	f = append(f, r.o.Fields...)
	if source != "" {
		f = append(f, journald.Field{Key: FieldSource, Value: source})
	}
	return f
}

func (r *Run) logger() *slog.Logger {
	if r.o.Log != nil {
		return r.o.Log
	}
	return slog.Default()
}

// KeptPriority is the priority of a line an attempt file kept before its lines went to the journal
// ("15:04:05 failed: ...", "15:04:05 warning: ..."): what the line itself says it is.
func KeptPriority(line string) int {
	rest := line
	if len(line) > 9 && line[2] == ':' && line[5] == ':' && line[8] == ' ' {
		rest = line[9:]
	}
	switch {
	case strings.HasPrefix(rest, "failed:"):
		return PriorityErr
	case strings.HasPrefix(rest, "warning:"):
		return PriorityWarning
	}
	return PriorityInfo
}

func slogLevel(priority int) slog.Level {
	switch {
	case priority <= PriorityErr:
		return slog.LevelError
	case priority == PriorityWarning:
		return slog.LevelWarn
	case priority >= 7:
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

// Lines is text as the journal's lines: blank lines left out, a line redrawn with carriage
// returns (a progress bar) as the text a terminal ends up showing, a very long line split.
func Lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimRight(l, " \t\r")
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = l[i+1:]
		}
		if strings.TrimSpace(l) == "" {
			continue
		}
		for len(l) > lineMax {
			n := lineMax
			for n > 0 && !utf8.RuneStart(l[n]) {
				n--
			}
			if n == 0 {
				n = lineMax
			}
			out = append(out, l[:n])
			l = l[n:]
		}
		out = append(out, l)
	}
	return out
}
