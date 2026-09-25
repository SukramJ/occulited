package meta

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The corpus lives at the repository root (fixtures/), shared with the TypeScript implementation.
const fixtureRoot = "../../fixtures"

func TestStoreDocuments(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtureRoot, "store", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no store fixtures: %v", err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(raw, &probe); err != nil {
				t.Fatal(err)
			}
			var reason string
			if r, ok := probe["_reason"]; ok {
				_ = json.Unmarshal(r, &reason)
				delete(probe, "_reason")
				raw, _ = json.Marshal(probe)
			}
			var doc Document
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse: %v", err)
			}
			err = doc.Validate()
			switch {
			case strings.HasPrefix(name, "valid-") && err != nil:
				t.Fatalf("expected valid, got %v", err)
			case strings.HasPrefix(name, "invalid-"):
				if err == nil {
					t.Fatalf("expected error %s, got none", reason)
				}
				if code := errCode(err); string(code) != reason {
					t.Fatalf("expected %s, got %s (%v)", reason, code, err)
				}
			}
			if err == nil {
				// a valid document must round-trip
				b, _ := Marshal(&doc)
				var again Document
				if err := json.Unmarshal(b, &again); err != nil || again.Validate() != nil {
					t.Fatalf("round trip failed: %v", err)
				}
			}
		})
	}
}

type op struct {
	Op       string          `json:"op"`
	Ref      string          `json:"ref"`
	Body     json.RawMessage `json:"body"`
	Enum     string          `json:"enum"`
	Orphaned *bool           `json:"orphaned"`
	Set      map[string]json.RawMessage
	Delete   []string        `json:"delete"`
	ID       string          `json:"id"`
	Name     json.RawMessage `json:"name"`
	Parent   *string         `json:"parent"`
	Icon     string          `json:"icon"`
	Position *int            `json:"position"`
	Path     string          `json:"path"`
	Members  string          `json:"members"`
	Document *Document       `json:"document"`
	Mode     string          `json:"mode"`
	Since    int64           `json:"since"`
	Expect   json.RawMessage `json:"expect"`
}

type testCase struct {
	Title string          `json:"title"`
	Start json.RawMessage `json:"start"`
	Ops   []op            `json:"ops"`
	End   json.RawMessage `json:"end"`
}

func errCode(err error) Code {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return Code("?" + err.Error())
}

func parsePatch(t *testing.T, raw json.RawMessage) (ObjectPatch, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("bad body: %v", err)
	}
	if _, ok := m["orphaned"]; ok {
		return ObjectPatch{}, errf(ErrForbidden, "orphaned is owner-only")
	}
	var p ObjectPatch
	if v, ok := m["name"]; ok {
		var s string
		_ = json.Unmarshal(v, &s)
		p.Name = &s
	}
	if v, ok := m["enums"]; ok {
		var e []string
		_ = json.Unmarshal(v, &e)
		p.Enums = &e
	}
	if v, ok := m["meta"]; ok {
		var mm map[string]json.RawMessage
		_ = json.Unmarshal(v, &mm)
		p.Meta = map[string]json.RawMessage{}
		for k, x := range mm {
			if string(x) == "null" {
				p.Meta[k] = nil
			} else {
				p.Meta[k] = x
			}
		}
	}
	return p, nil
}

func TestCases(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(fixtureRoot, "cases", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no case fixtures: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) { runCase(t, f) })
	}
}

func runCase(t *testing.T, file string) {
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var tc testCase
	if err := json.Unmarshal(raw, &tc); err != nil {
		t.Fatal(err)
	}
	var start *Document
	if string(tc.Start) != `"empty"` {
		start = &Document{}
		if err := json.Unmarshal(tc.Start, start); err != nil {
			t.Fatal(err)
		}
	}
	s, err := New(start, nil)
	if err != nil {
		t.Fatalf("start document: %v", err)
	}
	for i, o := range tc.Ops {
		desc := fmt.Sprintf("op %d %s", i+1, o.Op)
		var exp map[string]json.RawMessage
		if err := json.Unmarshal(o.Expect, &exp); err != nil {
			t.Fatalf("%s: bad expect: %v", desc, err)
		}
		var (
			rev     uint64
			changed bool
			opErr   error
			read    any
		)
		switch o.Op {
		case "object.set":
			p, perr := parsePatch(t, o.Body)
			if perr != nil {
				opErr = perr
			} else {
				rev, changed, opErr = s.SetObject(nil, o.Ref, p)
			}
		case "object.put":
			var b struct {
				Name  string                     `json:"name"`
				Enums []string                   `json:"enums"`
				Meta  map[string]json.RawMessage `json:"meta"`
			}
			_ = json.Unmarshal(o.Body, &b)
			rev, changed, opErr = s.PutObject(nil, o.Ref, b.Name, b.Enums, b.Meta)
		case "object.get":
			read, opErr = s.GetObject(o.Ref)
		case "object.delete":
			rev, changed, opErr = s.DeleteObject(nil, o.Ref)
		case "objects.query":
			read, opErr = s.Query(o.Enum, o.Orphaned)
		case "objects.bulk":
			set := map[string]ObjectPatch{}
			for ref, b := range o.Set {
				p, perr := parsePatch(t, b)
				if perr != nil {
					opErr = perr
					break
				}
				set[ref] = p
			}
			if opErr == nil {
				rev, changed, opErr = s.Bulk(nil, set, o.Delete)
			}
		case "enum.create":
			var name map[string]string
			_ = json.Unmarshal(o.Name, &name)
			rev, changed, opErr = s.CreateEnum(nil, o.ID, name)
		case "enum.delete":
			rev, changed, opErr = s.DeleteEnum(nil, o.ID, o.Members == "detach")
		case "node.create":
			var name string
			_ = json.Unmarshal(o.Name, &name)
			rev, changed, opErr = s.CreateNode(nil, o.Enum, o.Parent, o.ID, name, o.Icon, o.Position)
		case "node.update":
			var b struct {
				Name     *string `json:"name"`
				Icon     *string `json:"icon"`
				Parent   *string `json:"parent"`
				Position *int    `json:"position"`
			}
			var probe map[string]json.RawMessage
			_ = json.Unmarshal(o.Body, &probe)
			_ = json.Unmarshal(o.Body, &b)
			if v, ok := probe["parent"]; ok && string(v) == "null" {
				empty := ""
				b.Parent = &empty
			}
			rev, changed, opErr = s.UpdateNode(nil, o.Path, NodePatch{Name: b.Name, Icon: b.Icon, Parent: b.Parent, Position: b.Position})
		case "node.delete":
			rev, changed, opErr = s.DeleteNode(nil, o.Path, o.Members == "detach")
		case "orphan.set":
			rev, changed, opErr = s.SetOrphaned(o.Ref, o.Orphaned != nil && *o.Orphaned)
		case "import":
			mode := ImportReplace
			if o.Mode == "merge" {
				mode = ImportMerge
			}
			rev, changed, opErr = s.Import(nil, o.Document, mode)
		case "events.since":
			evs, ok := s.EventsSince(o.Since)
			if !ok {
				read = map[string]any{"resync": true}
			} else {
				kinds := []string{}
				for _, e := range evs {
					kinds = append(kinds, e.Kind)
				}
				read = map[string]any{"kinds": kinds}
			}
		default:
			t.Fatalf("%s: unknown op", desc)
		}
		// --- compare with expect ---
		if e, ok := exp["error"]; ok {
			var code string
			_ = json.Unmarshal(e, &code)
			if opErr == nil {
				t.Fatalf("%s: expected error %s, got none (rev %d)", desc, code, s.Revision())
			}
			if got := errCode(opErr); string(got) != code {
				t.Fatalf("%s: expected error %s, got %s (%v)", desc, code, got, opErr)
			}
			continue
		}
		if opErr != nil {
			t.Fatalf("%s: unexpected error %v", desc, opErr)
		}
		if _, ok := exp["ok"]; ok {
			if u, ok := exp["unchanged"]; ok && string(u) == "true" {
				if changed {
					t.Fatalf("%s: expected no change, but revision moved to %d", desc, rev)
				}
				continue
			}
			var want uint64
			_ = json.Unmarshal(exp["revision"], &want)
			if !changed {
				t.Fatalf("%s: expected a change to revision %d, but nothing changed", desc, want)
			}
			if rev != want {
				t.Fatalf("%s: expected revision %d, got %d", desc, want, rev)
			}
			continue
		}
		// a read: compare structurally against the expect object
		got := toJSONAny(t, read)
		if v, ok := exp["value"]; ok {
			assertSubset(t, desc, toJSONAny(t, json.RawMessage(v)), got)
			continue
		}
		if r, ok := exp["refs"]; ok {
			var want []string
			_ = json.Unmarshal(r, &want)
			gotRefs, _ := read.([]string)
			sort.Strings(want)
			if !reflect.DeepEqual(want, gotRefs) {
				t.Fatalf("%s: expected refs %v, got %v", desc, want, gotRefs)
			}
			continue
		}
		assertSubset(t, desc, toJSONAny(t, json.RawMessage(o.Expect)), got)
	}
	if len(tc.End) > 0 {
		var want map[string]any
		_ = json.Unmarshal(tc.End, &want)
		got := toJSONAny(t, s.Snapshot())
		if wr, ok := want["revision"]; ok {
			if gr := got.(map[string]any)["revision"]; gr != wr {
				t.Fatalf("end: expected revision %v, got %v", wr, gr)
			}
		}
		assertSubset(t, "end", want, got)
	}
}

func toJSONAny(t *testing.T, v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

// assertSubset checks that every key in want exists in got with an equal value (recursively for
// objects); arrays must match exactly.
func assertSubset(t *testing.T, desc string, want, got any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("%s: expected object, got %v", desc, got)
		}
		for k, wv := range w {
			gv, ok := g[k]
			if !ok {
				t.Fatalf("%s: missing key %q (got %v)", desc, k, g)
			}
			assertSubset(t, desc+"."+k, wv, gv)
		}
	default:
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("%s: expected %v, got %v", desc, want, got)
		}
	}
}

var _ = sortedKeys[int]
