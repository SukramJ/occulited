package acme

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/journald"
	"github.com/hobbyquaker/occulited/internal/runlog"
)

// testJournal keeps every entry it is sent.
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

func (j *testJournal) ofRun(id string) [][]journald.Field {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out [][]journald.Field
	for _, e := range j.entries {
		if jField(e, runlog.FieldRunID) == id {
			out = append(out, e)
		}
	}
	return out
}

func (j *testJournal) messages(id string) []string {
	var out []string
	for _, e := range j.ofRun(id) {
		out = append(out, jField(e, "MESSAGE"))
	}
	return out
}

func jField(e []journald.Field, key string) string {
	for _, f := range e {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

// chattyIssuer logs as lego does - its own lines and lego's, a warning, and a failure that
// quotes the provider's token - and fails.
type chattyIssuer struct{}

func (chattyIssuer) Issue(_ context.Context, req Request) (*Result, error) {
	token := req.DNSFields["api_token"]
	req.Log("directory " + req.DirectoryURL)
	req.LibLog("[INFO] [box.example.org] acme: Obtaining bundled SAN certificate")
	req.LibLog("[WARN] [box.example.org] acme: cloudflare: retrying with token=" + token)
	return nil, fmt.Errorf("order: cloudflare: 403 Forbidden for Authorization: Bearer %s (token %s)", token, token)
}

// task 102: every line of an ACME run is a journal entry with the run's fields; lego's lines are
// marked as lego's, a [WARN] is a warning, the failure an error; the provider's token is in no
// entry, not in the attempt's error and not in last.json.
func TestAttemptLinesInTheJournal(t *testing.T) {
	const token = "cf-SECRET-token-0123456789abcdef"
	s := newService(t, chattyIssuer{}, &fakeInstaller{})
	j := &testJournal{}
	s.Journal = j
	if _, err := s.SetSettings(Update{Mode: ModeACME, Directory: DirLetsEncrypt, Names: []string{"box.example.org", "alias.example.org"}, Challenge: ChallengeDNS01, DNSProvider: "cloudflare", DNSCredentials: map[string]string{"api_token": token}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(KindIssue); err != nil {
		t.Fatal(err)
	}
	a := wait(t, s)
	if a.OK || !runlog.ValidID(a.RunID) {
		t.Fatalf("%+v", a)
	}
	entries := j.ofRun(a.RunID)
	type want struct{ msg, prio, source string }
	wants := []want{
		{"issue: box.example.org, alias.example.org via dns-01", "6", ""},
		{"directory https://acme-v02.api.letsencrypt.org/directory", "6", ""},
		{"[INFO] [box.example.org] acme: Obtaining bundled SAN certificate", "6", "lego"},
		{"[WARN] [box.example.org] acme: cloudflare: retrying with token=[redacted]", "4", "lego"},
		{"failed: order: cloudflare: 403 Forbidden for Authorization: Bearer [redacted] (token [redacted])", "3", ""},
	}
	if len(entries) != len(wants) {
		t.Fatalf("%d entries: %v", len(entries), j.messages(a.RunID))
	}
	for i, w := range wants {
		e := entries[i]
		if jField(e, "MESSAGE") != w.msg || jField(e, "PRIORITY") != w.prio || jField(e, runlog.FieldSource) != w.source {
			t.Errorf("entry %d: %v", i, e)
		}
		if jField(e, runlog.FieldRun) != "acme" || jField(e, "OCCULITE_RUN_KIND") != "issue" || jField(e, "OCCULITE_RUN_NAMES") != "box.example.org,alias.example.org" || jField(e, "SYSLOG_IDENTIFIER") != "acme" {
			t.Errorf("entry %d fields: %v", i, e)
		}
	}
	for _, e := range j.entries {
		for _, f := range e {
			if strings.Contains(f.Value, token) {
				t.Fatalf("the token reached the journal: %v", e)
			}
		}
	}
	if strings.Contains(a.Error, token) || !strings.Contains(a.Error, "[redacted]") {
		t.Errorf("attempt error %q", a.Error)
	}
	b, _ := os.ReadFile(s.st.path(fileLast))
	if strings.Contains(string(b), token) || strings.Contains(string(b), `"lines"`) || !strings.Contains(string(b), a.RunID) {
		t.Errorf("last.json:\n%s", b)
	}
}

// task 102: a last.json from before keeps its lines until the first start with a journal: they go
// into it under a run id of the attempt's start, at the priority each says, and the file loses them.
func TestMigrateLog(t *testing.T) {
	s := newService(t, &fakeIssuer{}, &fakeInstaller{})
	started := time.Date(2026, 9, 11, 3, 0, 0, 0, time.UTC)
	old := map[string]any{"kind": "renew", "started": started, "finished": started.Add(time.Minute), "ok": false, "error": "order: boom", "directory": "https://ca.lan/acme/acme/directory", "names": []string{"box.lan"},
		"lines": []string{"03:00:00 renew: box.lan via http-01", "03:00:30 warning: the account could not be saved: disk full", "03:01:00 failed: order: boom"}, "installed": false}
	if err := s.st.writeJSON(fileLast, old); err != nil {
		t.Fatal(err)
	}
	s2, err := New(s.st.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	j := &testJournal{}
	s2.Journal = j
	s2.MigrateLog()
	last := s2.Status().Last
	if last == nil || !strings.HasPrefix(last.RunID, "20260911T030000-") || last.Error != "order: boom" {
		t.Fatalf("%+v", last)
	}
	entries := j.ofRun(last.RunID)
	if len(entries) != 3 || jField(entries[0], "PRIORITY") != "6" || jField(entries[1], "PRIORITY") != "4" || jField(entries[2], "PRIORITY") != "3" || jField(entries[0], "OCCULITE_RUN_KIND") != "renew" {
		t.Fatalf("%v", entries)
	}
	var file map[string]any
	b, _ := os.ReadFile(s2.st.path(fileLast))
	if json.Unmarshal(b, &file) != nil || file["lines"] != nil || file["run_id"] != last.RunID {
		t.Fatalf("last.json:\n%s", b)
	}
	// once only
	s2.MigrateLog()
	if len(j.ofRun(last.RunID)) != 3 {
		t.Fatal("migrated twice")
	}
	// a service without an attempt file has nothing to do
	empty := newService(t, &fakeIssuer{}, &fakeInstaller{})
	empty.Journal = j
	empty.MigrateLog()
}
