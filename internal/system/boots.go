package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The boots of the journal (task 93): the Log page's boot selector, and the boot a log query is
// narrowed to.

// Boot is one boot the journal holds.
type Boot struct {
	// Index is journalctl's offset: 0 the newest boot in the journal, -1 the one before it
	Index  int    `json:"index"`
	BootID string `json:"boot_id"`
	First  string `json:"first,omitempty"` // RFC 3339: the boot's first entry in the journal
	Last   string `json:"last,omitempty"`  // and its last one
}

var (
	bootIDRe     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	bootOffsetRe = regexp.MustCompile(`^(0|-[1-9][0-9]{0,3})$`)
)

// ValidBoot tells whether s names a boot the way the log routes take it: "", a boot id of 32 hex
// digits in lower case, 0 or a negative offset. A positive offset, which journalctl counts from the
// oldest boot, is not taken: it names another boot every time the journal is vacuumed.
func ValidBoot(s string) bool {
	return s == "" || bootIDRe.MatchString(s) || bootOffsetRe.MatchString(s)
}

// NormalizeBoot writes a boot as the log routes take it: lower case, and a boot id in /proc's
// UUID form without its dashes, as journalctl prints ids.
func NormalizeBoot(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) == 36 && strings.Count(s, "-") == 4 {
		s = strings.ReplaceAll(s, "-", "")
	}
	return s
}

// BootID is this boot's id as journalctl prints it; "" where /proc does not tell.
func (r Root) BootID() string {
	return NormalizeBoot(readFile(r.join("/proc/sys/kernel/random/boot_id")))
}

// IsThisBoot tells whether b names the running boot: "", 0 or this boot's own id.
func (r Root) IsThisBoot(b string) bool {
	if b == "" || b == "0" {
		return true
	}
	id := r.BootID()
	return id != "" && b == id
}

// Uptime is the time since the kernel started, the clock of systemd's monotonic stamps and of the
// kernel's own.
func (r Root) Uptime() (time.Duration, bool) {
	f := strings.Fields(readFile(r.join("/proc/uptime")))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil || s <= 0 {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// Boots lists the boots the journal holds, oldest first as journalctl lists them.
func (j JournalLog) Boots(ctx context.Context) ([]Boot, error) {
	args := append([]string{"--list-boots", "-o", "json", "--no-pager", "-q"}, j.sourceArgs()...)
	var out []byte
	var err error
	if j.Run != nil {
		out, err = j.Run(ctx, "journalctl", args...)
	} else {
		out, err = exec.CommandContext(ctx, "journalctl", args...).Output()
	}
	if err != nil {
		return nil, fmt.Errorf("journalctl --list-boots: %w", err)
	}
	return ParseListBoots(out, time.Local)
}

// listBootsRe is one row of the text table an older journalctl prints whatever -o says:
// " -1 1e2b…0c Thu 2026-09-10 08:12:03 CEST Thu 2026-09-10 21:40:11 CEST". Before systemd 252 the
// two times were joined by a dash instead of a space, and there was no header line.
var listBootsRe = regexp.MustCompile(`^\s*(-?\d+)\s+([0-9a-f]{32})\s+(?:\w{2,3},?\s+)?(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)(?:\s+[A-Za-z][\w+-]*)?\s*[—–-]?\s*(?:\w{2,3},?\s+)?(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)`)

// ParseListBoots reads `journalctl --list-boots`: the JSON array of systemd 254 and later (index,
// boot_id, first_entry and last_entry in microseconds), one JSON object per line, or the text
// table, whose local times are read in loc.
func ParseListBoots(out []byte, loc *time.Location) ([]Boot, error) {
	trimmed := bytes.TrimSpace(out)
	boots := []Boot{}
	if len(trimmed) == 0 {
		return boots, nil
	}
	var rows []map[string]any
	switch trimmed[0] {
	case '[':
		if err := json.Unmarshal(trimmed, &rows); err != nil {
			return nil, fmt.Errorf("journalctl --list-boots: %w", err)
		}
	case '{':
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		for sc.Scan() {
			var row map[string]any
			if json.Unmarshal(sc.Bytes(), &row) == nil {
				rows = append(rows, row)
			}
		}
	default:
		sc := bufio.NewScanner(bytes.NewReader(trimmed))
		for sc.Scan() {
			m := listBootsRe.FindStringSubmatch(sc.Text())
			if m == nil {
				continue
			}
			b := Boot{BootID: m[2]}
			b.Index, _ = strconv.Atoi(m[1])
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", m[3], loc); err == nil {
				b.First = t.Format(time.RFC3339)
			}
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", m[4], loc); err == nil {
				b.Last = t.Format(time.RFC3339)
			}
			boots = append(boots, b)
		}
		return boots, nil
	}
	for _, row := range rows {
		id, _ := row["boot_id"].(string)
		id = NormalizeBoot(id)
		if !bootIDRe.MatchString(id) {
			continue
		}
		b := Boot{BootID: id}
		for _, k := range []string{"index", "idx"} {
			if v, ok := row[k].(float64); ok {
				b.Index = int(v)
				break
			}
		}
		b.First = usecAnyTime(row["first_entry"], loc)
		b.Last = usecAnyTime(row["last_entry"], loc)
		boots = append(boots, b)
	}
	return boots, nil
}

// usecAnyTime is a microsecond stamp of a JSON answer, a number or a numeric string, as RFC 3339.
func usecAnyTime(v any, loc *time.Location) string {
	var us int64
	switch x := v.(type) {
	case float64:
		us = int64(x)
	case string:
		us, _ = strconv.ParseInt(x, 10, 64)
	}
	if us <= 0 {
		return ""
	}
	return time.UnixMicro(us).In(loc).Format(time.RFC3339)
}
