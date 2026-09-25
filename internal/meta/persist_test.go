package meta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	s, _ := New(nil, Saver(path))
	if _, _, err := s.SetObject(nil, "BidCos-RF.A:1", ObjectPatch{Name: strp("Lampe")}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SetObject(nil, "BidCos-RF.A:1", ObjectPatch{Name: strp("Deckenlampe")}); err != nil {
		t.Fatal(err)
	}
	res, err := Load(path)
	if err != nil || res.Fresh || res.RecoveredFromBackup {
		t.Fatalf("load: %v %+v", err, res)
	}
	if res.Doc.Revision != 2 || res.Doc.Objects["BidCos-RF.A:1"].Name != "Deckenlampe" {
		t.Fatalf("unexpected document: rev %d", res.Doc.Revision)
	}
	// the previous write is kept as .bak
	bak, err := readDoc(path + ".bak")
	if err != nil || bak.Revision != 1 {
		t.Fatalf("backup: %v rev %v", err, bak)
	}
	// no temp files left behind
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "meta.json" && e.Name() != "meta.json.bak" {
			t.Fatalf("stray file %s", e.Name())
		}
	}
}

func TestLoadFallsBackToBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "meta.json")
	s, _ := New(nil, Saver(path))
	_, _, _ = s.SetObject(nil, "BidCos-RF.A:1", ObjectPatch{Name: strp("Lampe")})
	_, _, _ = s.SetObject(nil, "BidCos-RF.A:1", ObjectPatch{Name: strp("Deckenlampe")})
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !res.RecoveredFromBackup || res.Doc.Revision != 1 {
		t.Fatalf("expected recovery from backup at revision 1, got %+v", res)
	}
}

func TestLoadFresh(t *testing.T) {
	res, err := Load(filepath.Join(t.TempDir(), "meta.json"))
	if err != nil || !res.Fresh || res.Doc.Revision != 0 || len(res.Doc.Enums) != 2 {
		t.Fatalf("fresh load: %v %+v", err, res)
	}
}

// A fresh store has rooms and functions, like a CCU, and nothing else (task 46).
func TestNewDocumentHasTheTwoDefaults(t *testing.T) {
	doc := NewDocument()
	if doc.Revision != 0 || len(doc.Objects) != 0 || len(doc.Enums) != 2 {
		t.Fatalf("fresh document: %+v", doc)
	}
	for id, want := range map[string][2]string{"room": {"Räume", "Rooms"}, "function": {"Gewerke", "Functions"}} {
		e := doc.Enums[id]
		if e == nil || e.Name["de"] != want[0] || e.Name["en"] != want[1] || len(e.Tree) != 0 {
			t.Errorf("%s: %+v", id, e)
		}
	}
	if doc.Enums["floor"] != nil {
		t.Error("a fresh store has no floor enum")
	}
}

// A store written before the defaults shrank still carries its "floor" enum; loading is not a
// migration (task 46): same revision, the enum untouched, no event.
func TestLoadKeepsAnExistingFloorEnum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")
	raw := `{"format":1,"revision":7,"objects":{},"enums":{` +
		`"room":{"name":{"de":"Räume","en":"Rooms"},"tree":[]},` +
		`"function":{"name":{"de":"Gewerke","en":"Functions"},"tree":[]},` +
		`"floor":{"name":{"de":"Etagen","en":"Floors"},"tree":[{"id":"eg","name":"EG"}]}}}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Load(path)
	if err != nil || res.Fresh || res.RecoveredFromBackup {
		t.Fatalf("load: %v %+v", err, res)
	}
	floor := res.Doc.Enums["floor"]
	if res.Doc.Revision != 7 || len(res.Doc.Enums) != 3 || floor == nil || floor.Name["en"] != "Floors" || len(floor.Tree) != 1 || floor.Tree[0].ID != "eg" {
		t.Fatalf("floor enum not kept: rev %d, enums %+v", res.Doc.Revision, res.Doc.Enums)
	}
	// and a store built on it does not touch it either: still revision 7, nothing to replay
	s, err := New(res.Doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.Revision != 7 || snap.Enums["floor"] == nil {
		t.Fatalf("store changed the loaded document: rev %d, floor %v", snap.Revision, snap.Enums["floor"])
	}
	if events, ok := s.EventsSince(7); !ok || len(events) != 0 {
		t.Fatalf("events after a plain load: %v ok=%v", events, ok)
	}
}

func TestLoadRejectsNewerFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.json")
	_ = os.WriteFile(path, []byte(`{"format":2,"revision":0,"objects":{},"enums":{}}`), 0o644)
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error for format 2")
	}
}

func TestIfMatch(t *testing.T) {
	s, _ := New(nil, nil)
	wrong := uint64(5)
	if _, _, err := s.SetObject(&wrong, "BidCos-RF.A:1", ObjectPatch{Name: strp("x")}); errCode(err) != ErrRevisionConflict {
		t.Fatalf("expected revision-conflict, got %v", err)
	}
	right := uint64(0)
	if rev, changed, err := s.SetObject(&right, "BidCos-RF.A:1", ObjectPatch{Name: strp("x")}); err != nil || !changed || rev != 1 {
		t.Fatalf("expected success at revision 1: %d %v %v", rev, changed, err)
	}
}

func TestSubscribe(t *testing.T) {
	s, _ := New(nil, nil)
	ch := s.Subscribe()
	defer s.Unsubscribe(ch)
	_, _, _ = s.SetObject(nil, "BidCos-RF.A:1", ObjectPatch{Name: strp("x")})
	e := <-ch
	if e.Kind != "object.updated" || e.Revision != 1 || e.Ref != "BidCos-RF.A:1" {
		t.Fatalf("unexpected event %+v", e)
	}
}

func strp(s string) *string { return &s }

func TestReconcile(t *testing.T) {
	s, _ := New(nil, nil)
	_, _, _ = s.SetObject(nil, "HmIP-RF.A:1", ObjectPatch{Name: strp("a")})
	_, _, _ = s.SetObject(nil, "HmIP-RF.B:1", ObjectPatch{Name: strp("b")})
	_, _, _ = s.SetObject(nil, "BidCos-RF.C:1", ObjectPatch{Name: strp("c")})
	// HmIP answered without B; BidCos did not answer at all
	n, err := s.Reconcile(map[string][]string{"HmIP-RF": {"A", "A:1"}})
	if err != nil || n != 1 {
		t.Fatalf("reconcile: %d %v", n, err)
	}
	b, _ := s.GetObject("HmIP-RF.B:1")
	c, _ := s.GetObject("BidCos-RF.C:1")
	a, _ := s.GetObject("HmIP-RF.A:1")
	if !b.Orphaned || c.Orphaned || a.Orphaned {
		t.Fatalf("flags: a=%v b=%v c=%v", a.Orphaned, b.Orphaned, c.Orphaned)
	}
	// B comes back
	n, _ = s.Reconcile(map[string][]string{"HmIP-RF": {"A:1", "B:1"}})
	b, _ = s.GetObject("HmIP-RF.B:1")
	if n != 1 || b.Orphaned {
		t.Fatalf("return: %d %v", n, b.Orphaned)
	}
	// nothing changes on a repeat
	if n, _ = s.Reconcile(map[string][]string{"HmIP-RF": {"A:1", "B:1"}}); n != 0 {
		t.Fatalf("repeat changed %d", n)
	}
}

func TestImportUnchangedIsNotARevision(t *testing.T) {
	s, err := New(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc := NewDocument()
	doc.Enums["room"].Tree = []*Node{{ID: "eg", Name: "EG"}}
	doc.Objects["BidCos-RF.ABC0000001:1"] = &Object{Name: "Licht", Enums: []string{"room/eg"}}
	rev, changed, err := s.Import(nil, doc, ImportMerge)
	if err != nil || !changed || rev != 1 {
		t.Fatalf("first: %d %v %v", rev, changed, err)
	}
	rev, changed, err = s.Import(nil, doc, ImportMerge)
	if err != nil || changed || rev != 1 {
		t.Fatalf("second: %d %v %v", rev, changed, err)
	}
	rev, changed, err = s.Import(nil, doc, ImportReplace)
	if err != nil || changed || rev != 1 {
		t.Fatalf("replace same: %d %v %v", rev, changed, err)
	}
	doc.Objects["BidCos-RF.ABC0000001:1"].Name = "Lampe"
	if rev, changed, _ = s.Import(nil, doc, ImportMerge); !changed || rev != 2 {
		t.Fatalf("changed: %d %v", rev, changed)
	}
}
