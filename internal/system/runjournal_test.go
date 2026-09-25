package system

import (
	"sync"

	"github.com/hobbyquaker/occulited/internal/journald"
)

// testJournal is the journal as the radio firmware tests see it: it keeps every entry it is sent.
type testJournal struct {
	mu      sync.Mutex
	entries [][]journald.Field
}

func (j *testJournal) Send(entries ...[]journald.Field) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, entries...)
	return nil
}

// ofRun is the entries of one run, in the order they were written.
func (j *testJournal) ofRun(id string) [][]journald.Field {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out [][]journald.Field
	for _, e := range j.entries {
		if rjField(e, "OCCULITE_RUN_ID") == id {
			out = append(out, e)
		}
	}
	return out
}

func (j *testJournal) messages(id string) []string {
	var out []string
	for _, e := range j.ofRun(id) {
		out = append(out, rjField(e, "MESSAGE"))
	}
	return out
}

func rjField(e []journald.Field, key string) string {
	for _, f := range e {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}
