package favorites

import (
	"encoding/json"
	"testing"

	"github.com/hobbyquaker/occulited/internal/meta"
)

func newStore(t *testing.T) *meta.Store {
	t.Helper()
	s, err := meta.New(meta.NewDocument(), func(*meta.Document) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func nodes(s *meta.Store) map[string]string {
	out := map[string]string{}
	if e := s.Snapshot().Enums[EnumID]; e != nil {
		for _, n := range e.Tree {
			out[n.ID] = n.Name
		}
	}
	return out
}

func TestSyncFollowsAccounts(t *testing.T) {
	st := newStore(t)
	sy := &Sync{Store: st}
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "0badf00d", Name: "kid"}})
	if got := nodes(st); len(got) != 2 || got["a1b2c3d4"] != "admin" || got["0badf00d"] != "kid" {
		t.Fatalf("nodes %v", got)
	}
	if e := st.Snapshot().Enums[EnumID]; e.Name["de"] != "Favoriten" || e.Name["en"] != "Favorites" {
		t.Fatalf("enum %v", e.Name)
	}
	rev := st.Revision()
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "0badf00d", Name: "kid"}})
	if st.Revision() != rev {
		t.Fatal("a sync that changes nothing wrote")
	}
	// a rename follows
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "root"}, {ID: "0badf00d", Name: "kid"}})
	if got := nodes(st); got["a1b2c3d4"] != "root" || got["0badf00d"] != "kid" {
		t.Fatalf("after the rename %v", got)
	}
}

// occulited task 1: a deleted account takes its node along, members detached, in one change;
// the waiting nodes stay, and a sync does not bring the node back
func TestDeletedAccountTakesItsNode(t *testing.T) {
	st := newStore(t)
	sy := &Sync{Store: st}
	both := []Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "0badf00d", Name: "kid"}}
	sy.Run(both)
	if _, _, err := st.CreateNode(nil, EnumID, nil, "ccu-oma", "Oma", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutObject(nil, "HmIP-RF.000A:1", "Lampe", []string{"favorite/0badf00d", "favorite/a1b2c3d4"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutObject(nil, "HmIP-RF.000B:1", "Rollo", []string{"favorite/ccu-oma"}, nil); err != nil {
		t.Fatal(err)
	}
	rev := st.Revision()
	admin := both[:1]
	sy.Run(admin)
	if st.Revision() != rev+1 {
		t.Fatalf("the delete took %d revisions, want 1", st.Revision()-rev)
	}
	got := nodes(st)
	if _, ok := got["0badf00d"]; ok || got["a1b2c3d4"] != "admin" || got["ccu-oma"] != "Oma" {
		t.Fatalf("nodes after the delete %v", got)
	}
	doc := st.Snapshot()
	if l := doc.Objects["HmIP-RF.000A:1"]; len(l.Enums) != 1 || l.Enums[0] != "favorite/a1b2c3d4" {
		t.Fatalf("lamp enums %v", l.Enums)
	}
	if r := doc.Objects["HmIP-RF.000B:1"]; len(r.Enums) != 1 || r.Enums[0] != "favorite/ccu-oma" {
		t.Fatalf("the waiting node's member %v", r.Enums)
	}
	// the same list again, or an older one read before the delete through RunFrom, changes nothing
	rev = st.Revision()
	sy.Run(admin)
	sy.RunFrom(func() []Account { return admin })
	if st.Revision() != rev {
		t.Fatal("a sync after the delete wrote")
	}
	if _, ok := nodes(st)["0badf00d"]; ok {
		t.Fatal("the node came back")
	}
	// no accounts at all (users.json gone) is not everyone deleted
	sy.Run(nil)
	if _, ok := nodes(st)["a1b2c3d4"]; !ok {
		t.Fatal("an empty account list removed a node")
	}
	// a fresh sync (a restart) has seen nothing: a node of an account unknown to it stays
	if _, _, err := st.CreateNode(nil, EnumID, nil, "deadbeef", "gone", "", nil); err != nil {
		t.Fatal(err)
	}
	(&Sync{Store: st}).Run(admin)
	if _, ok := nodes(st)["deadbeef"]; !ok {
		t.Fatal("a restart removed a node it had not seen as an account")
	}
}

// an account deleted and another of its name created in one change: the old node goes and its
// members are not adopted as a waiting node's
func TestDeletedAndRecreatedByName(t *testing.T) {
	st := newStore(t)
	sy := &Sync{Store: st}
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "0badf00d", Name: "kid"}})
	if _, _, err := st.PutObject(nil, "HmIP-RF.000A:1", "Lampe", []string{"favorite/0badf00d"}, nil); err != nil {
		t.Fatal(err)
	}
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "11111111", Name: "kid"}})
	got := nodes(st)
	if _, ok := got["0badf00d"]; ok || got["11111111"] != "kid" {
		t.Fatalf("nodes %v", got)
	}
	if l := st.Snapshot().Objects["HmIP-RF.000A:1"]; l != nil && len(l.Enums) != 0 {
		t.Fatalf("the old account's member moved: %v", l.Enums)
	}
}

func TestAdoptWaitingNode(t *testing.T) {
	st := newStore(t)
	sy := &Sync{Store: st}
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}})
	// the CCU import left a waiting node for "Sebastian" with two members, one ranked
	if _, _, err := st.CreateNode(nil, EnumID, nil, "ccu-sebastian", "Sebastian", "", nil); err != nil {
		t.Fatal(err)
	}
	order, _ := json.Marshal(map[string]any{"order": map[string]any{"favorite/ccu-sebastian": 2000}})
	if _, _, err := st.PutObject(nil, "HmIP-RF.000A:1", "Lampe", []string{"favorite/ccu-sebastian"}, map[string]json.RawMessage{"occulite": order}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutObject(nil, "HmIP-RF.000B:1", "Rollo", []string{"favorite/ccu-sebastian"}, nil); err != nil {
		t.Fatal(err)
	}
	// the account of that name (case does not matter) appears: its node takes the members
	sy.Run([]Account{{ID: "a1b2c3d4", Name: "admin"}, {ID: "beefcafe", Name: "sebastian"}})
	got := nodes(st)
	if got["beefcafe"] != "sebastian" || got["ccu-sebastian"] != "" {
		t.Fatalf("nodes after adoption %v", got)
	}
	doc := st.Snapshot()
	l := doc.Objects["HmIP-RF.000A:1"]
	if len(l.Enums) != 1 || l.Enums[0] != "favorite/beefcafe" {
		t.Fatalf("lamp enums %v", l.Enums)
	}
	if r, ok := Rank(l, "favorite/beefcafe"); !ok || r != 2000 {
		t.Fatalf("rank moved: %v %v", r, ok)
	}
	if _, ok := Rank(l, "favorite/ccu-sebastian"); ok {
		t.Fatal("the old rank stayed")
	}
	ro := doc.Objects["HmIP-RF.000B:1"]
	if len(ro.Enums) != 1 || ro.Enums[0] != "favorite/beefcafe" {
		t.Fatalf("rollo enums %v", ro.Enums)
	}
	if _, ok := Rank(ro, "favorite/beefcafe"); ok {
		t.Fatal("an unranked object got a rank")
	}
}
