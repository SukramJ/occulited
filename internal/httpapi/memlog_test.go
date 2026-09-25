package httpapi

import (
	"context"
	"strings"

	"github.com/hobbyquaker/occulited/internal/system"
)

// memLog is a log held in memory for the API's tests: the journal's filtering is journalctl's
// (JournalLog's own tests cover the arguments), so these tests need a reader that applies the
// same filters to a few fixed lines - the tag, the severity floor, the text and a run's id.
type memLog []system.LogLine

var memSeverity = map[string]int{"debug": 0, "info": 1, "notice": 2, "warning": 3, "warn": 3, "err": 4, "error": 4, "crit": 5, "alert": 6, "emerg": 7}

func (m memLog) match(q system.LogQuery) func(system.LogLine) bool {
	floor, hasFloor := memSeverity[strings.ToLower(q.Severity)]
	contains := strings.ToLower(q.Contains)
	return func(l system.LogLine) bool {
		if q.Tag != "" && !strings.EqualFold(l.Tag, q.Tag) {
			return false
		}
		if q.Run != "" && !strings.Contains(l.Message, "run_id="+q.Run) {
			return false
		}
		if q.Area != "" && l.Area != q.Area {
			return false
		}
		if hasFloor {
			if r, ok := memSeverity[l.Severity]; !ok || r < floor {
				return false
			}
		}
		return contains == "" || strings.Contains(strings.ToLower(l.Message), contains)
	}
}

func (m memLog) Read(q system.LogQuery) ([]system.LogLine, error) {
	if q.Limit <= 0 {
		q.Limit = 500
	}
	match := m.match(q)
	out := []system.LogLine{}
	for _, l := range m {
		if match(l) {
			out = append(out, l)
		}
	}
	if len(out) > q.Limit {
		out = out[len(out)-q.Limit:]
	}
	return out, nil
}

func (m memLog) Export(ctx context.Context, q system.LogQuery, emit func(system.LogLine) error) error {
	match := m.match(q)
	for _, l := range m {
		if err := ctx.Err(); err != nil {
			return err
		}
		if match(l) {
			if err := emit(l); err != nil {
				return err
			}
		}
	}
	return nil
}

// testLog is the log of the API tests that need one: the line fakeRoot's /var/log/messages held.
var testLog = memLog{{Time: "Sep  6 03:46:30", Host: "openccu", Tag: "rfd", PID: 1, Severity: "err", Message: "Address in use"}}
