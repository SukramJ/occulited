// Package journallog is occulited's slog handler for the systemd journal (task 186). Every
// process occulited runs - the daemon, the helper, the syslog forwarder - logs through it.
//
// An entry goes over journald's native protocol (internal/journald) with the fields a reader
// filters on, and nothing the journal stamps itself:
//
//	MESSAGE            the record's message, then its attributes as key=value like slog's text
//	                   handler writes them ("metadata: loaded path=/… revision=1"); no time, no level
//	PRIORITY           from the level: debug 7, info 6, warn 4, error 3
//	SYSLOG_IDENTIFIER  occulited, occulited-helper, occu-syslog-forward
//	OCCULITED_AREA     the area of an area logger (logctl.Areas), for the Log page's filter
//	CODE_FILE/_LINE/_FUNC  where the line was logged, journald's own names (journalctl -o verbose)
//
// A message with a newline stays one entry. When the socket cannot be reached the line goes to
// stderr with journald's <N> prefix instead (SyslogLevelPrefix=, on by default), its newlines
// written as \n, so a unit that has stderr on the journal still files it at its priority.
package journallog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// Priority is the syslog priority of a slog level.
func Priority(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return 3
	case l >= slog.LevelWarn:
		return 4
	case l >= slog.LevelInfo:
		return 6
	default:
		return 7
	}
}

// Handler writes records to the journal. It lets every level through; the level is logctl's
// decision (logctl.Control wraps it), or the caller's through Options.Level.
type Handler struct {
	out   *output
	level slog.Leveler
	area  string
	// ops replays WithAttrs and WithGroup onto the text handler that formats the attributes
	ops []func(slog.Handler) slog.Handler
}

// Options configure a Handler.
type Options struct {
	// Identifier is SYSLOG_IDENTIFIER.
	Identifier string
	// Level is the lowest level written; nil = debug (everything).
	Level slog.Leveler
	// Socket is journald's native socket; "" = journald.DefaultSocket.
	Socket string
	// Fallback takes the <N>-prefixed lines when the socket cannot be reached; nil = os.Stderr.
	Fallback io.Writer
}

// New is a handler writing to the journal.
func New(o Options) *Handler {
	sock := o.Socket
	if sock == "" {
		sock = journald.DefaultSocket
	}
	fb := o.Fallback
	if fb == nil {
		fb = os.Stderr
	}
	return &Handler{out: &output{ident: o.Identifier, socket: sock, fallback: fb}, level: o.Level}
}

// WithArea is the handler of an area logger: its entries carry OCCULITED_AREA (logctl.Logger
// asks for it).
func (h *Handler) WithArea(area string) slog.Handler {
	c := *h
	c.area = area
	return &c
}

func (h *Handler) Enabled(_ context.Context, l slog.Level) bool {
	if h.level == nil {
		return true
	}
	return l >= h.level.Level()
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	c := *h
	c.ops = append(append([]func(slog.Handler) slog.Handler{}, h.ops...), func(t slog.Handler) slog.Handler { return t.WithAttrs(attrs) })
	return &c
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.ops = append(append([]func(slog.Handler) slog.Handler{}, h.ops...), func(t slog.Handler) slog.Handler { return t.WithGroup(name) })
	return &c
}

func (h *Handler) Handle(_ context.Context, r slog.Record) error {
	msg := h.message(r)
	fields := []journald.Field{
		{Key: "MESSAGE", Value: msg},
		{Key: "PRIORITY", Value: strconv.Itoa(Priority(r.Level))},
	}
	if h.out.ident != "" {
		fields = append(fields, journald.Field{Key: "SYSLOG_IDENTIFIER", Value: h.out.ident})
	}
	if h.area != "" {
		fields = append(fields, journald.Field{Key: "OCCULITED_AREA", Value: h.area})
	}
	if r.PC != 0 {
		f, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
		if f.File != "" {
			fields = append(fields,
				journald.Field{Key: "CODE_FILE", Value: f.File},
				journald.Field{Key: "CODE_LINE", Value: strconv.Itoa(f.Line)},
				journald.Field{Key: "CODE_FUNC", Value: f.Function})
		}
	}
	return h.out.write(fields, Priority(r.Level), msg)
}

// message is the record's message and its attributes, the way slog's text handler writes them,
// without the time, the level and the msg= key.
func (h *Handler) message(r slog.Record) string {
	var b strings.Builder
	var t slog.Handler = slog.NewTextHandler(&b, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: dropBuiltins})
	for _, op := range h.ops {
		t = op(t)
	}
	rr := slog.NewRecord(time.Time{}, r.Level, "", 0)
	r.Attrs(func(a slog.Attr) bool {
		rr.AddAttrs(a)
		return true
	})
	_ = t.Handle(context.Background(), rr)
	attrs := strings.TrimRight(b.String(), "\n")
	switch {
	case attrs == "":
		return r.Message
	case r.Message == "":
		return attrs
	default:
		return r.Message + " " + attrs
	}
}

func dropBuiltins(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
		return slog.Attr{}
	}
	return a
}

// output is the connection all handlers derived from one New share.
type output struct {
	ident    string
	socket   string
	fallback io.Writer

	mu   sync.Mutex
	conn *net.UnixConn
	// down is when the socket last failed: the fallback is used for a while before the next try,
	// so a missing journal does not cost a dial per line
	down time.Time
}

const retryAfter = 10 * time.Second

func (o *output) write(fields []journald.Field, prio int, msg string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.down.IsZero() || time.Since(o.down) > retryAfter {
		if err := o.send(fields); err == nil {
			o.down = time.Time{}
			return nil
		}
		o.down = time.Now()
	}
	_, err := fmt.Fprintf(o.fallback, "<%d>%s\n", prio, strings.ReplaceAll(msg, "\n", `\n`))
	return err
}

func (o *output) send(fields []journald.Field) error {
	b := journald.Encode(journald.Fit(fields, journald.DefaultMaxDatagram))
	for attempt := 0; attempt < 2; attempt++ {
		if o.conn == nil {
			c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: o.socket, Net: "unixgram"})
			if err != nil {
				return err
			}
			o.conn = c
		}
		_ = o.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := o.conn.Write(b)
		if err == nil {
			return nil
		}
		// a socket whose buffer is smaller than the entry: a shorter one once more
		if errors.Is(err, syscall.EMSGSIZE) {
			b = journald.Encode(journald.Fit(fields, 2<<10))
			continue
		}
		// journald restarted: its old socket is gone, a new connection reaches the new one
		o.conn.Close()
		o.conn = nil
	}
	return fmt.Errorf("journal: entry not written")
}

// OnJournal says whether stderr is connected to the journal: systemd sets $JOURNAL_STREAM to the
// stream's device:inode when it connects stdout or stderr there (systemd.exec(5)), and stderr is
// that stream when its own device and inode match.
func OnJournal() bool {
	v := os.Getenv("JOURNAL_STREAM")
	dev, ino, ok := strings.Cut(v, ":")
	if !ok {
		return false
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(int(os.Stderr.Fd()), &st); err != nil {
		return false
	}
	return strconv.FormatUint(uint64(st.Dev), 10) == dev && strconv.FormatUint(st.Ino, 10) == ino
}
