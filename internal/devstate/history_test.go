package devstate

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/store"
)

func pointsOf(ps []Point) string {
	var out []string
	for _, p := range ps {
		s := fmt.Sprintf("%d=%v", int(p.At.Sub(t0).Seconds()), p.Value)
		if p.ForS != nil {
			s += fmt.Sprintf("/%g", *p.ForS)
		}
		out = append(out, s)
	}
	return fmt.Sprint(out)
}

// Sampled: not more often than a minute, not for less than the dead-band. Events: every change,
// with the previous value's duration; the same value again is no row.
func TestHistoryRules(t *testing.T) {
	kicks := 0
	h := &History{Kick: func() { kicks++ }}
	for _, r := range []struct {
		s int
		v any
	}{{0, 21.0}, {30, 22.0}, {60, 21.05}, {61, 21.2}, {200, 21.25}, {300, 23}} {
		h.Record("HmIP-RF", "A:1", "ACTUAL_TEMPERATURE", r.v, at(r.s))
	}
	_, ps, ok := h.Read("HmIP-RF", "A:1", "ACTUAL_TEMPERATURE", time.Time{}, time.Time{}, 0)
	if !ok || pointsOf(ps) != "[0=21 61=21.2 300=23]" {
		t.Errorf("sampled %s", pointsOf(ps))
	}
	for _, r := range []struct {
		s int
		v any
	}{{0, false}, {5, true}, {6, true}, {65, false}, {70, false}} {
		h.Record("HmIP-RF", "B:1", "STATE", r.v, at(r.s))
	}
	shape, ps, _ := h.Read("HmIP-RF", "B:1", "STATE", time.Time{}, time.Time{}, 0)
	if shape != ShapeEvent || pointsOf(ps) != "[0=false 5=true/5 65=false/60]" {
		t.Errorf("events %s %s", shape, pointsOf(ps))
	}
	// not in the list: nothing; an older report: nothing
	h.Record("HmIP-RF", "A:1", "RSSI_DEVICE", -50, at(0))
	h.Record("HmIP-RF", "B:1", "STATE", true, at(1))
	if _, _, ok := h.Read("HmIP-RF", "A:1", "RSSI_DEVICE", time.Time{}, time.Time{}, 0); ok || h.Count() != 2 || kicks != 6 {
		t.Errorf("count %d kicks %d", h.Count(), kicks)
	}
	// listed, never reported: an empty series
	if shape, ps, ok := h.Read("HmIP-RF", "C:1", "HUMIDITY", time.Time{}, time.Time{}, 0); !ok || shape != ShapeSampled || len(ps) != 0 {
		t.Errorf("empty: %v %v %v", shape, ps, ok)
	}
}

// The ring holds N rows; from/to; max_points keeps every nth counted from the newest.
func TestHistoryRingAndRead(t *testing.T) {
	h := &History{Rows: 10}
	for i := 0; i < 25; i++ {
		h.Record("HmIP-RF", "A:1", "STATE", i%2 == 0, at(i*10))
	}
	_, ps, _ := h.Read("HmIP-RF", "A:1", "STATE", time.Time{}, time.Time{}, 0)
	if len(ps) != 10 || !ps[0].At.Equal(at(150)) || !ps[9].At.Equal(at(240)) {
		t.Fatalf("ring %s", pointsOf(ps))
	}
	_, ps, _ = h.Read("HmIP-RF", "A:1", "STATE", at(200), at(220), 0)
	if len(ps) != 3 {
		t.Errorf("from/to %s", pointsOf(ps))
	}
	_, ps, _ = h.Read("HmIP-RF", "A:1", "STATE", time.Time{}, time.Time{}, 4)
	if len(ps) != 4 || !ps[3].At.Equal(at(240)) || !ps[0].At.Equal(at(150)) {
		t.Errorf("max_points %s", pointsOf(ps))
	}
}

func TestHistoryList(t *testing.T) {
	h := &History{}
	h.SetList([]string{"CARBON_DIOXIDE_CONCENTRATION", "DOOR_STATE", "bad name"}, []string{"STATE"})
	l := h.List()
	if l["CARBON_DIOXIDE_CONCENTRATION"] != ShapeSampled || l["DOOR_STATE"] != ShapeEvent || l["STATE"] != "" || l["bad name"] != "" || l["MOTION"] != ShapeEvent {
		t.Errorf("list %v", l)
	}
	h.Record("HmIP-RF", "A:1", "STATE", true, at(0))
	if h.Count() != 0 {
		t.Error("a removed datapoint recorded")
	}
	for name, want := range map[string]string{"SOIL_MOISTURE": ShapeSampled, "LEVEL_3": ShapeSampled, "FOO": ShapeEvent, "WIND_SPEED": ShapeSampled} {
		if g := guess(name); g.Shape != want {
			t.Errorf("%s: %v", name, g)
		}
	}
}

func TestHistoryRowCodec(t *testing.T) {
	f := 12.5
	for _, p := range []Point{{At: at(1), Value: 21.5}, {At: at(2), Value: true, ForS: &f}, {At: at(3), Value: "x"}} {
		shape := ShapeSampled
		if p.ForS != nil {
			shape = ShapeEvent
		}
		got, gs, ok := decodeRow(encodeRow(p, shape))
		if !ok || gs != shape || !got.At.Equal(p.At) || got.Value != p.Value || (p.ForS != nil) != (got.ForS != nil) || p.ForS != nil && *got.ForS != *p.ForS {
			t.Errorf("%+v -> %+v %s %v", p, got, gs, ok)
		}
	}
	for _, bad := range [][]byte{nil, {9, 1, 0, 0}, {rowV1, 2, 1, tagTrue, 1}} {
		if _, _, ok := decodeRow(store.Row{At: at(0), Value: bad}); ok {
			t.Errorf("decoded %v", bad)
		}
	}
}

// Through the file: written, restored after a restart in front of what memory has.
func TestHistoryWithTheDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.DirName, store.FileName)
	h := &History{}
	m := &store.Manager{Path: path, Platform: "ova", KickDelay: time.Millisecond}
	m.Register(h)
	h.Kick = m.Kick
	m.Start("", "")
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	h.Record("HmIP-RF", "A:1", "MOTION", true, at(0))
	h.Record("HmIP-RF", "A:1", "MOTION", false, at(90))
	if st := m.Status("", ""); st.Kept[HistoryBucket] != 1 {
		t.Errorf("kept %v", st.Kept)
	}
	cancel()
	<-m.Done()
	h2 := &History{}
	m2 := &store.Manager{Path: path, Platform: "ova"}
	m2.Register(h2)
	m2.Start("", "")
	shape, ps, _ := h2.Read("HmIP-RF", "A:1", "MOTION", time.Time{}, time.Time{}, 0)
	if shape != ShapeEvent || pointsOf(ps) != "[0=true 90=false/90]" {
		t.Errorf("restored %s %s", shape, pointsOf(ps))
	}
	h2.Record("HmIP-RF", "A:1", "MOTION", true, at(100))
	if p, _ := h2.Pending(false); len(p) != 1 || len(p[0].Rows) != 1 {
		t.Errorf("pending after restore %+v", p)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	go m2.Run(ctx2)
	cancel2()
	<-m2.Done()
}
