package devstate

import (
	"encoding/binary"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/store"
)

// The datapoint history (openccu-lite task 195, decided 2026-09-24): small and deliberate - what
// the App's cards draw, never a time-series database. One ring of N = 500 rows per series (the
// same N as the health history), kept in occulited's database file (bucket "history", one bucket
// per series named <interface>/<address>/<datapoint>, the key the row's time).
//
//   - Two shapes. A sampled value (a temperature, a level) writes at most one row a minute, and no
//     row when it moved less than its dead-band (0.1 for °C, 1 for %) since the last row - a
//     thermostat reporting the same temperature every three minutes keeps days, not hours. An
//     event-shaped value (a contact's STATE, MOTION, a key press) writes a row at every change,
//     and the row carries how long the value before it stood.
//   - No maximum age: rows stay until the ring overwrites them; the read answers real times.
//   - The list: HistoryDefaults below, what the cards draw (task 193's card code); the setting
//     (occulited.json store.history) adds or removes datapoint names.
//   - A cap on the number of series (MaxSeries) bounds the file on a big installation (500 rows
//     of ~20 bytes each: ~10 KB a series; bbolt maps the file on 32-bit boxes too).

// Shapes of a series.
const (
	ShapeSampled = "sampled"
	ShapeEvent   = "event"
)

// MinSpacing is the least time between two rows of a sampled series.
const MinSpacing = time.Minute

// MaxSeries caps the number of series.
const MaxSeries = 1000

// HistoryBucket is the history's top-level bucket.
const HistoryBucket = "history"

// Recorded is one datapoint of the list: its shape and its dead-band (sampled only).
type Recorded struct {
	Shape    string
	DeadBand float64
}

// HistoryDefaults is the built-in list, derived from task 193's card code (channels.ts): the
// state of every widget a card could draw over time - the thermostat's and the sensors'
// temperatures and humidity, the set point, a dimmer's, a blind's or a valve's LEVEL, the
// illumination, a meter's power; a contact's or a switch's STATE, motion and presence, a key's
// presses, the smoke alarm.
var HistoryDefaults = map[string]Recorded{
	"ACTUAL_TEMPERATURE":    {ShapeSampled, 0.1},
	"TEMPERATURE":           {ShapeSampled, 0.1},
	"SET_POINT_TEMPERATURE": {ShapeSampled, 0.1},
	"SET_TEMPERATURE":       {ShapeSampled, 0.1},
	"HUMIDITY":              {ShapeSampled, 1},
	"LEVEL":                 {ShapeSampled, 0.01}, // 0..1: 1 %
	"LEVEL_2":               {ShapeSampled, 0.01},
	"ILLUMINATION":          {ShapeSampled, 0},
	"CURRENT_ILLUMINATION":  {ShapeSampled, 0},
	"POWER":                 {ShapeSampled, 0},

	"STATE":                       {ShapeEvent, 0},
	"MOTION":                      {ShapeEvent, 0},
	"PRESENCE_DETECTION_STATE":    {ShapeEvent, 0},
	"PRESS_SHORT":                 {ShapeEvent, 0},
	"PRESS_LONG":                  {ShapeEvent, 0},
	"SMOKE_DETECTOR_ALARM_STATUS": {ShapeEvent, 0},
	"WINDOW_STATE":                {ShapeEvent, 0},
}

var dpNameRe = regexp.MustCompile(`^[A-Z0-9_]{1,64}$`)

// ValidDatapoint says whether a name can be added to the list.
func ValidDatapoint(name string) bool { return dpNameRe.MatchString(name) }

// Point is one row as the read answers it.
type Point struct {
	At    time.Time
	Value any
	// ForS is how long the value before this row stood (event rows after the first)
	ForS *float64
}

type series struct {
	shape string
	rows  []Point
	// appended counts every row ever added, synced how many of them the file has
	appended, synced uint64
}

// History is the datapoint history.
type History struct {
	Rows int // the ring's length; store.DefaultRows when 0
	// Kick is the database manager's: after a new row.
	Kick func()

	mu     sync.Mutex
	list   map[string]Recorded
	series map[string]*series
	capped uint64 // reports dropped at the series cap
}

func (h *History) rows() int {
	if h.Rows > 0 {
		return h.Rows
	}
	return store.DefaultRows
}

// SetList applies the setting: the defaults, plus add (a sampled value when its name says
// temperature, humidity, level or illumination, else an event), minus remove.
func (h *History) SetList(add, remove []string) {
	l := map[string]Recorded{}
	for k, v := range HistoryDefaults {
		l[k] = v
	}
	for _, k := range add {
		if !ValidDatapoint(k) {
			continue
		}
		if _, ok := l[k]; !ok {
			l[k] = guess(k)
		}
	}
	for _, k := range remove {
		delete(l, k)
	}
	h.mu.Lock()
	h.list = l
	h.mu.Unlock()
}

// guess is the shape of a datapoint the defaults do not know.
func guess(name string) Recorded {
	switch {
	case strings.Contains(name, "TEMPERATURE"):
		return Recorded{ShapeSampled, 0.1}
	case strings.Contains(name, "HUMIDITY"), strings.Contains(name, "MOISTURE"):
		return Recorded{ShapeSampled, 1}
	case strings.HasPrefix(name, "LEVEL"):
		return Recorded{ShapeSampled, 0.01}
	case strings.Contains(name, "ILLUMINATION"), strings.Contains(name, "POWER"), strings.Contains(name, "VOLTAGE"),
		strings.Contains(name, "CURRENT"), strings.Contains(name, "ENERGY"), strings.Contains(name, "CONCENTRATION"),
		strings.Contains(name, "WIND"), strings.Contains(name, "RAIN_COUNTER"):
		return Recorded{ShapeSampled, 0}
	}
	return Recorded{ShapeEvent, 0}
}

// List is the datapoints recorded, sorted, with their shape.
func (h *History) List() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]string{}
	for k, v := range h.listLocked() {
		out[k] = v.Shape
	}
	return out
}

func (h *History) listLocked() map[string]Recorded {
	if h.list == nil {
		return HistoryDefaults
	}
	return h.list
}

// SeriesName is a series' name in the file: <interface>/<address>/<datapoint>.
func SeriesName(iface, address, dp string) string { return iface + "/" + address + "/" + dp }

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case float64:
		return x, true
	}
	return 0, false
}

// Record takes one report (the state store's OnObserve): a row when the series' rules say so.
func (h *History) Record(iface, address, dp string, v any, at time.Time) {
	h.mu.Lock()
	rec, ok := h.listLocked()[dp]
	if !ok {
		h.mu.Unlock()
		return
	}
	name := SeriesName(iface, address, dp)
	s := h.series[name]
	if s == nil {
		if len(h.series) >= MaxSeries {
			h.capped++
			h.mu.Unlock()
			return
		}
		if h.series == nil {
			h.series = map[string]*series{}
		}
		s = &series{shape: rec.Shape}
		h.series[name] = s
	}
	s.shape = rec.Shape
	p := Point{At: at, Value: v}
	if n := len(s.rows); n > 0 {
		last := s.rows[n-1]
		if !at.After(last.At) {
			h.mu.Unlock()
			return // an older report (a sweep's read overtaken by an event)
		}
		switch rec.Shape {
		case ShapeSampled:
			f, isNum := number(v)
			lf, lastNum := number(last.Value)
			if at.Sub(last.At) < MinSpacing {
				h.mu.Unlock()
				return
			}
			if isNum && lastNum && math.Abs(f-lf) < rec.DeadBand {
				h.mu.Unlock()
				return
			}
			if !isNum && Same(v, last.Value) {
				h.mu.Unlock()
				return
			}
		default:
			if Same(v, last.Value) {
				h.mu.Unlock()
				return
			}
			d := seconds(at.Sub(last.At))
			p.ForS = &d
		}
	}
	s.rows = append(s.rows, p)
	if n := h.rows(); len(s.rows) > n {
		s.rows = s.rows[len(s.rows)-n:]
	}
	s.appended++
	h.mu.Unlock()
	if h.Kick != nil {
		h.Kick()
	}
}

// Read is one series from..to (zero: open), every nth row when there are more than maxPoints
// (the newest always kept); ok false when the datapoint is not recorded at all.
func (h *History) Read(iface, address, dp string, from, to time.Time, maxPoints int) (shape string, points []Point, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	rec, listed := h.listLocked()[dp]
	s := h.series[SeriesName(iface, address, dp)]
	if !listed && s == nil {
		return "", nil, false
	}
	shape = rec.Shape
	points = []Point{}
	if s == nil {
		return shape, points, true
	}
	shape = s.shape
	for _, p := range s.rows {
		if !from.IsZero() && p.At.Before(from) || !to.IsZero() && p.At.After(to) {
			continue
		}
		points = append(points, p)
	}
	if maxPoints > 0 && len(points) > maxPoints {
		step := int(math.Ceil(float64(len(points)) / float64(maxPoints)))
		var thin []Point
		// counted from the newest, so the last row is always in
		for i := len(points) - 1; i >= 0; i -= step {
			thin = append(thin, points[i])
		}
		sort.Slice(thin, func(a, b int) bool { return thin[a].At.Before(thin[b].At) })
		points = thin
	}
	return shape, points, true
}

// Count is how many series the history holds (store.Counter).
func (h *History) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.series)
}

// Capped is how many reports the series cap turned away since the start.
func (h *History) Capped() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.capped
}

// ---- the database file (store.Keeper) --------------------------------------------------------

// Bucket is the keeper's top-level bucket.
func (h *History) Bucket() string { return HistoryBucket }

// Restore takes the file's rows in front of memory's (a switch out of ram keeps both).
func (h *History) Restore(all map[string][]store.Row) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.series == nil {
		h.series = map[string]*series{}
	}
	for name, rows := range all {
		s := h.series[name]
		if s == nil {
			s = &series{}
			h.series[name] = s
		}
		var merged []Point
		for _, r := range rows {
			p, shape, ok := decodeRow(r)
			if !ok || len(s.rows) > 0 && !p.At.Before(s.rows[0].At) {
				continue
			}
			if s.shape == "" {
				s.shape = shape
			}
			merged = append(merged, p)
		}
		mem := len(s.rows)
		merged = append(merged, s.rows...)
		if n := h.rows(); len(merged) > n {
			merged = merged[len(merged)-n:]
		}
		s.rows = merged
		s.appended += uint64(len(merged) - mem)
		s.synced = s.appended - uint64(min(mem, len(merged)))
	}
}

// Pending returns the rows the file lacks, or every series whole (Replace) after an open.
func (h *History) Pending(all bool) ([]store.Series, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []store.Series
	upTo := map[string]uint64{}
	for name, s := range h.series {
		n := len(s.rows)
		if !all {
			if d := s.appended - s.synced; d < uint64(n) {
				n = int(d)
			}
		}
		if n == 0 {
			continue
		}
		rows := make([]store.Row, 0, n)
		for _, p := range s.rows[len(s.rows)-n:] {
			rows = append(rows, encodeRow(p, s.shape))
		}
		out = append(out, store.Series{Bucket: HistoryBucket, Name: name, Rows: rows, Replace: all})
		upTo[name] = s.appended
	}
	return out, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		for name, v := range upTo {
			if s := h.series[name]; s != nil {
				s.synced = v
			}
		}
	}
}

// A row in the file: the time is the key; the value is a version byte, the shape (1 sampled,
// 2 event), a flags byte (1 = for_s follows), the value as the state store encodes one, then
// for_s as 8-byte milliseconds.
const rowV1 = 1

func encodeRow(p Point, shape string) store.Row {
	b := []byte{rowV1, 1, 0}
	if shape == ShapeEvent {
		b[1] = 2
	}
	if p.ForS != nil {
		b[2] = 1
	}
	b = appendValue(b, p.Value)
	if p.ForS != nil {
		b = binary.BigEndian.AppendUint64(b, uint64(int64(math.Round(*p.ForS*1000))))
	}
	return store.Row{At: p.At, Value: b}
}

func decodeRow(r store.Row) (Point, string, bool) {
	b := r.Value
	if len(b) < 4 || b[0] != rowV1 {
		return Point{}, "", false
	}
	shape := ShapeSampled
	if b[1] == 2 {
		shape = ShapeEvent
	}
	v, rest, ok := readValue(b[3:])
	if !ok {
		return Point{}, "", false
	}
	p := Point{At: r.At, Value: v}
	if b[2]&1 != 0 {
		if len(rest) < 8 {
			return Point{}, "", false
		}
		f := float64(int64(binary.BigEndian.Uint64(rest[:8]))) / 1000
		p.ForS = &f
	}
	return p, shape, true
}
