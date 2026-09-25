package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/logctl"
	"github.com/hobbyquaker/occulited/internal/runlog"
	"github.com/hobbyquaker/occulited/internal/system"
)

// logDownloadCap is where a log download stops. The VM's journal may hold gigabytes; a file of this
// size is more than anybody reads, and the line at its end says how to fetch the rest.
const logDownloadCap int64 = 50 << 20

var errLogCap = errors.New("the download reached its size limit")

// logQuery reads the filters that /log, /log/stream and /log/download share.
func logQuery(v url.Values, limit int) system.LogQuery {
	kernel := v.Get("kernel")
	return system.LogQuery{Tag: v.Get("tag"), Unit: v.Get("unit"), Severity: v.Get("severity"), Contains: v.Get("q"), Since: v.Get("since"), Until: v.Get("until"), Limit: limit,
		Kernel: kernel == "1" || kernel == "true", NoKernel: kernel == "0" || kernel == "false", Boot: system.NormalizeBoot(v.Get("boot")), Run: v.Get("run"), Area: v.Get("area")}
}

// logQueryChecked is logQuery that refuses a boot the journal would not take, and a run id that
// is not one (it becomes a journalctl match, so nothing else may pass).
func logQueryChecked(v url.Values, limit int) (system.LogQuery, error) {
	q := logQuery(v, limit)
	if !system.ValidBoot(q.Boot) {
		return q, errors.New("boot is a boot id (32 hex digits), 0 for this boot or a negative offset such as -1")
	}
	if q.Run != "" && !runlog.ValidID(q.Run) {
		return q, errors.New("run is a run id as an attempt names it (run_id)")
	}
	// task 186: an area becomes a journalctl match too, so only a known one passes
	if q.Area != "" && !slices.Contains(logctl.Areas, q.Area) {
		return q, errors.New("area is one of " + strings.Join(logctl.Areas, ", "))
	}
	return q, nil
}

// logDownload streams the log as a file: /log's filters without its limit, oldest first, as text
// in journalctl's short-iso shape or as /log's line objects one per line. The entries go from the
// reader to the response one at a time; a download that stops before the end of the log - at the
// cap, or on an error - ends with a line that says so.
func (a *SystemAPI) logDownload(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	format := v.Get("format")
	ext, ctype := "txt", "text/plain; charset=utf-8"
	switch format {
	case "", "text":
		format = "text"
	case "json":
		ext, ctype = "jsonl", "application/x-ndjson"
	default:
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "format must be text or json"})
		return
	}
	q, err := logQueryChecked(v, 0)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	a.resolveBoot(r.Context(), &q) // B-114: an earlier boot by its id, in the file name too
	reader, source := a.logReader(q)
	exp, ok := reader.(system.LogExporter)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "this log cannot be downloaded"})
		return
	}
	limit := a.logCap
	if limit <= 0 {
		limit = logDownloadCap
	}
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", `attachment; filename="`+logFileName(a.Root.Hostname(), q, source == "journald", time.Now(), ext)+`"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// the mux answers HEAD on a GET route: the headers are the whole answer, the journal is not read
	if r.Method == http.MethodHead {
		return
	}
	// the headers go out before the first entry is found: a filter over a long journal takes a
	// while, and the browser shows the download as started meanwhile
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	bw := bufio.NewWriterSize(w, 32<<10)
	var written int64
	err = exp.Export(r.Context(), q, func(l system.LogLine) error {
		line := logDownloadLine(l, format)
		if written+int64(len(line)) > limit {
			return errLogCap
		}
		n, err := bw.WriteString(line)
		written += int64(n)
		return err
	})
	switch {
	case errors.Is(err, errLogCap):
		_, _ = bw.WriteString(logCutLine(format, "limit", limit, fmt.Sprintf("Cut off here: the download stops at %s. The later entries are not in this file; a later start of the time range or narrower filters fetch them.", sizeName(limit))))
	case err != nil && r.Context().Err() == nil:
		_, _ = bw.WriteString(logCutLine(format, "error", 0, "Cut off here: the log could not be read any further ("+err.Error()+")."))
	}
	_ = bw.Flush()
}

func logDownloadLine(l system.LogLine, format string) string {
	if format == "json" {
		b, _ := json.Marshal(l)
		return string(b) + "\n"
	}
	return system.LogText(l)
}

// logCut is the last line of a download that did not reach the end of the log, so a cut file never
// passes for a complete one. The text form writes it in journalctl's own marker style, "-- … --";
// the JSON form as an object with `cut` in place of a log line's fields.
type logCut struct {
	Cut        string `json:"cut"` // "limit" or "error"
	LimitBytes int64  `json:"limit_bytes,omitempty"`
	Message    string `json:"message"`
}

func logCutLine(format, cut string, limit int64, msg string) string {
	if format == "json" {
		b, _ := json.Marshal(logCut{Cut: cut, LimitBytes: limit, Message: msg})
		return string(b) + "\n"
	}
	return "-- " + msg + " --\n"
}

func sizeName(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return strconv.FormatInt(n>>20, 10) + " MB"
	}
	return strconv.FormatInt(n, 10) + " bytes"
}

// logNameLayout is a time in a file name: no colon, which Windows does not allow in one.
const logNameLayout = "2006-01-02T1504"

// logFileName is `<host>-log-<from>-<to>.<ext>`: the time range when one applies (journald), the
// download time when none does. A range open at its end runs to the download time; one open at its
// start is `until-<to>`. A time journalctl would read but this does not ("3 weeks ago") stands in the
// name as it was given, reduced to what a file name can carry.
func logFileName(host string, q system.LogQuery, ranged bool, now time.Time, ext string) string {
	stamp := now.Format(logNameLayout)
	span := stamp
	if ranged {
		switch {
		case q.Since != "" && q.Until != "":
			span = logTimeName(q.Since, now) + "-" + logTimeName(q.Until, now)
		case q.Since != "":
			span = logTimeName(q.Since, now) + "-" + stamp
		case q.Until != "":
			span = "until-" + logTimeName(q.Until, now)
		}
	}
	// task 93: the kernel log says so, and a boot of the journal is named by its id's first eight
	// digits (or its offset); busybox has neither boots nor names them
	kind := "log"
	switch {
	case q.Kernel:
		kind = "kernel"
	case q.NoKernel && ranged:
		kind = "system"
	}
	if b := bootName(q.Boot); ranged && b != "" {
		kind += "-boot-" + b
	}
	name := kind + "-" + span + "." + ext
	if h := nameSafe(host); h != "" {
		name = h + "-" + name
	}
	return name
}

func logTimeName(expr string, now time.Time) string {
	if t, ok := journalTime(expr, now); ok {
		return t.Format(logNameLayout)
	}
	if s := nameSafe(expr); s != "" {
		return s
	}
	return "range"
}

var nameUnsafe = regexp.MustCompile(`[^A-Za-z0-9.]+`)

// nameSafe keeps letters, digits and dots, turns every other run into one dash, and is at most 40
// characters long: nothing in it can end the quoted filename or reach outside the download folder.
func nameSafe(s string) string {
	s = strings.Trim(nameUnsafe.ReplaceAllString(s, "-"), "-.")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-.")
	}
	return s
}

// journalTime reads the time expressions the Log page sends, and journalctl's other common ones:
// "@<epoch seconds>", a relative "-1h" or "-1h30min", "now", "today", "yesterday", "tomorrow", and
// a date with or without a time. Only for the file name - journalctl itself reads the filter.
func journalTime(expr string, now time.Time) (time.Time, bool) {
	s := strings.TrimSpace(expr)
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	switch s {
	case "now":
		return now, true
	case "today":
		return midnight, true
	case "yesterday":
		return midnight.AddDate(0, 0, -1), true
	case "tomorrow":
		return midnight.AddDate(0, 0, 1), true
	}
	if rest, ok := strings.CutPrefix(s, "@"); ok {
		sec, err := strconv.ParseInt(rest, 10, 64)
		return time.Unix(sec, 0).In(now.Location()), err == nil
	}
	if s != "" && (s[0] == '-' || s[0] == '+') {
		span, ok := parseSpan(s[1:])
		if !ok {
			return time.Time{}, false
		}
		if s[0] == '-' {
			span = -span
		}
		return now.Add(span), true
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02", "2006-01-02T15:04:05", "2006-01-02T15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// spanPart is one number and unit of a systemd time span; the longer spellings come first, so
// "min" is not read as "m" followed by "in".
var spanPart = regexp.MustCompile(`^\s*(\d+)\s*(usec|us|msec|ms|seconds|second|sec|s|minutes|minute|min|m|hours|hour|hr|h|days|day|d|weeks|week|w)`)

var spanUnit = map[string]time.Duration{
	"usec": time.Microsecond, "us": time.Microsecond, "msec": time.Millisecond, "ms": time.Millisecond,
	"seconds": time.Second, "second": time.Second, "sec": time.Second, "s": time.Second,
	"minutes": time.Minute, "minute": time.Minute, "min": time.Minute, "m": time.Minute,
	"hours": time.Hour, "hour": time.Hour, "hr": time.Hour, "h": time.Hour,
	"days": 24 * time.Hour, "day": 24 * time.Hour, "d": 24 * time.Hour,
	"weeks": 7 * 24 * time.Hour, "week": 7 * 24 * time.Hour, "w": 7 * 24 * time.Hour,
}

func parseSpan(s string) (time.Duration, bool) {
	var total time.Duration
	parts := 0
	for strings.TrimSpace(s) != "" {
		m := spanPart.FindStringSubmatch(s)
		if m == nil {
			return 0, false
		}
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0, false
		}
		total += time.Duration(n) * spanUnit[m[2]]
		s = s[len(m[0]):]
		parts++
	}
	return total, parts > 0
}
