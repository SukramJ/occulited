package main

import (
	"log/slog"
	"testing"

	"github.com/hobbyquaker/occulited/internal/journallog"
)

// task 186: the --log targets. stderr and syslog are the older units' and mean auto, so a
// hot-deployed binary under an old unit still logs to the journal.
func TestLogHandlerTargets(t *testing.T) {
	t.Setenv("JOURNAL_STREAM", "") // stderr is not the journal here
	for _, target := range []string{"auto", "", "stderr", "syslog", "text"} {
		h, err := logHandler(target, "occulited", slog.LevelDebug)
		if err != nil {
			t.Fatalf("%q: %v", target, err)
		}
		if _, ok := h.(*slog.TextHandler); !ok {
			t.Errorf("%q off the journal: %T, want text", target, h)
		}
	}
	h, err := logHandler("journal", "occulited", nil)
	if _, ok := h.(*journallog.Handler); err != nil || !ok {
		t.Errorf("journal: %T %v", h, err)
	}
	if _, err := logHandler("file", "occulited", nil); err == nil {
		t.Error("an unknown target was taken")
	}
}
