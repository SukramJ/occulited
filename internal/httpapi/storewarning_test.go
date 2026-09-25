package httpapi

import (
	"context"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/store"
)

type copyStore struct{ c *store.CopyStatus }

func (s copyStore) Status() store.Status             { return store.Status{Copy: s.c} }
func (s copyStore) Set(string, string, string) error { return nil }

// openccu-lite task 229: store-target - the stick of the database's copy is not plugged in, or the
// last copy to it failed; nothing without a stick or with a good copy.
func TestStoreTargetWarning(t *testing.T) {
	a := &SystemAPI{}
	if w, ok := a.storeWarnings(context.Background()); !ok || len(w) != 0 {
		t.Fatalf("%v %v", w, ok)
	}
	now := time.Now()
	for _, c := range []struct {
		copy    *store.CopyStatus
		variant string
	}{
		{nil, ""},
		{&store.CopyStatus{Label: "L", Folder: "db", Present: true, LastCopy: &now}, ""},
		{&store.CopyStatus{Label: "L", Folder: "db"}, "usb"},
		{&store.CopyStatus{Label: "L", Folder: "db", Present: true, LastError: "read-only"}, "failed"},
	} {
		a.DataStore = copyStore{c.copy}
		w, ok := a.storeWarnings(context.Background())
		if !ok || (c.variant == "" && len(w) != 0) || (c.variant != "" && (len(w) != 1 || w[0].ID != "store-target" || w[0].Variant != c.variant || w[0].Params["label"] != "L")) {
			t.Errorf("%+v: %+v", c.copy, w)
		}
	}
}
