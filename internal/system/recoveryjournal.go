package system

import (
	"fmt"
	"html"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hobbyquaker/occulited/internal/journald"
	"github.com/hobbyquaker/occulited/internal/runlog"
)

// ---- the recovery system's install log in the journal (openccu-lite task 145, D-64) -----------
//
// The recovery system runs from its own initramfs with busybox init and no journald; an update's
// install log exists only as /tmp/fwinstall.log there and is gone with the reboot. The recovery
// (S90AutoUpdate) copies it to the userfs as RecoveryLogDir/<UTC time>.log before it reboots, on
// success and on failure, and occulited carries every such file into the journal at its next
// start: identifier recovery, one entry per line, the file's time as RECOVERY_TIME, a line's own
// timestamp as SYSLOG_TIMESTAMP where it has one (fwinstall.sh's lines have none), the file's name
// as RECOVERY_LOG, and at most the last recoveryLinesMax lines; then the file is removed through
// the helper (the recovery wrote it as root). A file that cannot be read, is not text or is empty
// is skipped with one warning entry and removed as well. When the journal cannot be written the
// file stays for the next start.
//
// Not /usr/local/tmp, where task 84 first put it: upstream's S06InitSystem empties that directory
// at every boot (B-137, kept as upstream's rule, D-107), long before occulited starts.

const (
	// RecoveryLogDir is where the recovery system leaves its install log for the next boot.
	RecoveryLogDir = "/usr/local/var/recovery"
	// recoveryTimeLayout is the file's name without .log: the UTC time the recovery wrote it,
	// with dashes for the colons a file name is better off without.
	recoveryTimeLayout = "2006-01-02T15-04-05Z"
	recoveryIdent      = "recovery"
	recoveryLinesMax   = 5000
	recoveryFileMax    = 8 << 20 // larger is not an install log
	priorityWarning    = 4
)

// recoveryLineTime is a timestamp at the start of a line, ISO 8601 with a T or a space.
var recoveryLineTime = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)\b`)

// recoveryHTMLTag is what fwinstall.sh puts into its lines for the recovery's browser view.
var recoveryHTMLTag = regexp.MustCompile(`<[^<>]*>`)

// RecoveryImporter carries the recovery system's install logs into the journal.
type RecoveryImporter struct {
	Root    Root
	Journal runlog.Sender
	Log     *slog.Logger
}

// Import handles every file in RecoveryLogDir once, in name order (oldest first). It returns how
// many files were imported and how many skipped.
func (r *RecoveryImporter) Import() (imported, skipped int) {
	log := r.Log
	if log == nil {
		log = slog.Default()
	}
	dir := r.Root.join(RecoveryLogDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn("recovery logs: the directory cannot be read", "dir", RecoveryLogDir, "err", err)
		}
		return 0, 0
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".log") {
			continue
		}
		path := filepath.Join(dir, name)
		if reason := r.importFile(path, name, log); reason == "" {
			imported++
		} else if reason != "kept" {
			skipped++
		}
	}
	return imported, skipped
}

// importFile carries one file into the journal and removes it. It returns "" when the file was
// imported, "kept" when the journal could not be written (the file stays), or the reason it was
// skipped (one warning entry written, the file removed).
func (r *RecoveryImporter) importFile(path, name string, log *slog.Logger) string {
	skip := func(reason string) string {
		msg := fmt.Sprintf("recovery log %s skipped: %s", name, reason)
		log.Warn(msg)
		if r.Journal != nil {
			if err := r.Journal.Send([]journald.Field{
				{Key: "MESSAGE", Value: msg},
				{Key: "PRIORITY", Value: strconv.Itoa(priorityWarning)},
				{Key: "SYSLOG_IDENTIFIER", Value: recoveryIdent},
				{Key: "RECOVERY_LOG", Value: name},
			}); err != nil {
				log.Warn("recovery logs: the journal could not be written", "err", err)
			}
		}
		if err := remove(path); err != nil {
			log.Warn("recovery logs: the file could not be removed", "file", name, "err", err)
		}
		return reason
	}
	info, err := os.Lstat(path)
	if err != nil {
		return skip("cannot be read: " + err.Error())
	}
	if !info.Mode().IsRegular() {
		return skip("not a regular file")
	}
	if info.Size() > recoveryFileMax {
		return skip(fmt.Sprintf("%d bytes, larger than an install log (%d at most)", info.Size(), recoveryFileMax))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return skip("cannot be read: " + err.Error())
	}
	if !utf8.Valid(data) {
		return skip("not text (invalid UTF-8)")
	}
	lines := recoveryLines(string(data))
	if len(lines) == 0 {
		return skip("empty")
	}
	if r.Journal == nil {
		log.Warn("recovery logs: no journal to write to", "file", name)
		return "kept"
	}
	written, ok := time.Parse(recoveryTimeLayout, strings.TrimSuffix(name, ".log"))
	base := func(msg string, priority int) []journald.Field {
		f := []journald.Field{
			{Key: "MESSAGE", Value: msg},
			{Key: "PRIORITY", Value: strconv.Itoa(priority)},
			{Key: "SYSLOG_IDENTIFIER", Value: recoveryIdent},
			{Key: "RECOVERY_LOG", Value: name},
		}
		if ok == nil {
			f = append(f, journald.Field{Key: "RECOVERY_TIME", Value: written.UTC().Format(time.RFC3339)})
		}
		return f
	}
	from := name
	if ok == nil {
		from = written.UTC().Format(time.RFC3339)
	}
	entries := [][]journald.Field{base(fmt.Sprintf("install log of the recovery system from %s (%d lines)", from, len(lines)), journald.PriorityInfo)}
	for _, l := range lines {
		e := base(l, recoveryLinePriority(l))
		if m := recoveryLineTime.FindStringSubmatch(l); m != nil {
			e = append(e, journald.Field{Key: "SYSLOG_TIMESTAMP", Value: m[1]})
		}
		entries = append(entries, e)
	}
	if err := r.Journal.Send(entries...); err != nil {
		log.Warn("recovery logs: the journal could not be written, the file stays", "file", name, "err", err)
		return "kept"
	}
	log.Info("recovery log imported", "file", name, "lines", len(lines))
	if err := remove(path); err != nil {
		log.Warn("recovery logs: the imported file could not be removed", "file", name, "err", err)
	}
	return ""
}

// recoveryLinePriority: fwinstall.sh marks what went wrong with ERROR and what to note with
// WARNING, in the line itself.
func recoveryLinePriority(l string) int {
	switch {
	case strings.Contains(l, "ERROR"):
		return journald.PriorityErr
	case strings.Contains(l, "WARNING"):
		return priorityWarning
	}
	return journald.PriorityInfo
}

// recoveryLines is the file as the journal's lines: fwinstall.sh's HTML (its lines end in <br/>,
// and its entities are the browser's) taken out, then as journalLines does it - blank lines left
// out, carriage returns resolved, long lines split, and at most the last recoveryLinesMax lines
// after a line that says how many are left out.
func recoveryLines(s string) []string {
	s = recoveryHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return journalLinesN(s, recoveryLinesMax)
}
