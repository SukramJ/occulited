package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/hobbyquaker/occulited/internal/journallog"
)

// logHandler is the base handler of one of occulited's processes (task 186), named by the --log
// target:
//
//   - journal: the journal's native protocol - the message alone, the priority from the level,
//     identifier, area and code location as fields (internal/journallog).
//   - text: slog's text handler on stderr, with time and level - for a terminal or a test.
//   - auto: journal when stderr is the journal's stream (systemd's $JOURNAL_STREAM), else text.
//
// "stderr" and "syslog" are the targets of before task 186 and mean auto: a hot-deployed binary
// keeps an older unit, whose --log stderr has stderr on the journal - and gets the journal. The
// busybox init's --log syslog now finds no journal and writes text to its stderr.
func logHandler(target, ident string, level slog.Leveler) (slog.Handler, error) {
	if level == nil {
		level = slog.LevelDebug
	}
	switch target {
	case "", "auto", "stderr", "syslog":
		if journallog.OnJournal() {
			return journallog.New(journallog.Options{Identifier: ident, Level: level}), nil
		}
		return textHandler(level), nil
	case "journal":
		return journallog.New(journallog.Options{Identifier: ident, Level: level}), nil
	case "text":
		return textHandler(level), nil
	}
	return nil, fmt.Errorf("--log %q: auto, journal or text", target)
}

func textHandler(level slog.Leveler) slog.Handler {
	return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
}
