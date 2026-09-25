package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/httpapi"
	"github.com/hobbyquaker/occulited/internal/meta"
	"github.com/hobbyquaker/occulited/internal/system"
)

// B-170: a marker that records a failed import is retried only while the retry can happen - the
// store empty, the file there. Otherwise it is given up once, in the marker, and never logged
// again; before, every start logged the old error as "trying again" and tried nothing.
func TestFirstBootImportFailedMarker(t *testing.T) {
	const oldErr = "open /etc/config/homematic.regadom: permission denied"
	failed := `{"at":"2026-09-06T21:39:25+02:00","source":"/etc/config/homematic.regadom","error":"` + oldErr + `","disabled_addons":["redmatic"]}`
	for _, tc := range []struct {
		name      string
		marker    string
		regadom   bool // an (unparsable) homematic.regadom is there
		named     bool // the store holds a room already
		wantNil   bool
		wantGave  string
		wantLog   string
		wantError bool // the marker still records an error that will be retried
	}{
		{name: "store holds names", marker: failed, regadom: true, named: true, wantNil: true, wantGave: "the store holds names already", wantLog: "not tried again"},
		{name: "file gone", marker: failed, wantNil: true, wantGave: "the ReGa database is gone", wantLog: "not tried again"},
		{name: "retry possible", marker: failed, regadom: true, wantLog: "trying again", wantError: true},
		{name: "given up earlier", marker: `{"error":"x","gave_up":"the store holds names already"}`, regadom: true, wantNil: true, wantGave: "the store holds names already"},
		{name: "succeeded earlier", marker: `{"objects":3}`, regadom: true, named: true},
		{name: "no marker, store holds names", regadom: true, named: true, wantNil: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			if tc.regadom {
				if err := os.MkdirAll(filepath.Join(root, "etc/config"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "etc/config/homematic.regadom"), []byte("not a regadom"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			marker := filepath.Join(state, "regadom-imported.json")
			if tc.marker != "" {
				if err := os.WriteFile(marker, []byte(tc.marker), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			doc := meta.NewDocument()
			if tc.named {
				doc.Enums["room"].Tree = []*meta.Node{{ID: "kitchen", Name: "Kitchen"}}
			}
			store, err := meta.New(doc, nil)
			if err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logs, nil))

			fb := firstBootImport(store, system.Root(root), state, log, nil)
			if (fb == nil) != tc.wantNil {
				t.Fatalf("report = %+v, want nil %v", fb, tc.wantNil)
			}
			if tc.wantLog == "" && strings.Contains(logs.String(), "recorded first-boot import") {
				t.Errorf("logged %q", logs.String())
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("log %q lacks %q", logs.String(), tc.wantLog)
			}
			var got httpapi.FirstBootImport
			if b, err := os.ReadFile(marker); err == nil {
				if err := json.Unmarshal(b, &got); err != nil {
					t.Fatal(err)
				}
			}
			if got.GaveUp != tc.wantGave {
				t.Errorf("gave_up = %q, want %q", got.GaveUp, tc.wantGave)
			}
			if tc.wantGave != "" && tc.marker == failed && (got.Error != oldErr || len(got.DisabledAddons) != 1) {
				t.Errorf("the given-up marker lost what it recorded: %+v", got)
			}
			if tc.wantError && (got.Error == "" || got.Error == oldErr) {
				t.Errorf("the retry recorded %q, want its own error", got.Error)
			}

			// the next start is silent once the import is given up
			if tc.wantGave != "" {
				logs.Reset()
				if fb := firstBootImport(store, system.Root(root), state, log, nil); fb != nil || logs.Len() != 0 {
					t.Errorf("second start: report %+v, log %q", fb, logs.String())
				}
			}
		})
	}
}
