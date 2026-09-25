package bootexpect

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// maxSpan bounds everything a reboot's timing may span: a record whose request lies further back,
// or browser checkpoints spread over more, measure something else.
const maxSpan = time.Hour

// Browser holds what the page saw, as epoch milliseconds of the browser's clock: the moment it
// wrote its entry and sent the request (started), and the checkpoints it observed - the box
// stopped answering (down), lighttpd answered with its 503 (http), occulited answered (ui), the
// interfaces were up (ready). 0 is a checkpoint the page did not see.
type Browser struct {
	Started int64 `json:"started"`
	Down    int64 `json:"down,omitempty"`
	HTTP    int64 `json:"http,omitempty"`
	UI      int64 `json:"ui,omitempty"`
	Ready   int64 `json:"ready,omitempty"`
}

// Validate checks that the checkpoints are plausible: at least one of down, http and ui, none
// before the one ahead of it, and all within an hour of the request.
func (b Browser) Validate() error {
	if b.Started <= 0 {
		return errors.New("started is required")
	}
	if b.Down == 0 && b.HTTP == 0 && b.UI == 0 {
		return errors.New("at least one of down, http and ui is required")
	}
	prev := b.Started
	for _, c := range []struct {
		name string
		at   int64
	}{{"down", b.Down}, {"http", b.HTTP}, {"ui", b.UI}, {"ready", b.Ready}} {
		switch {
		case c.at == 0:
			continue
		case c.at < 0:
			return fmt.Errorf("%s is negative", c.name)
		case c.at < prev:
			return fmt.Errorf("%s is earlier than the checkpoint before it", c.name)
		}
		prev = c.at
	}
	if prev-b.Started > maxSpan.Milliseconds() {
		return errors.New("the checkpoints span more than an hour")
	}
	return nil
}

// Phases are a record's durations per phase in seconds; a phase the record cannot tell is nil.
type Phases struct {
	// Down: the request until the box stopped answering; only the browser sees it.
	Down *float64 `json:"down,omitempty"`
	// DownHTTP: the request until lighttpd was active, shutdown and boot together.
	DownHTTP *float64 `json:"down_http,omitempty"`
	// HTTP: the box stopped answering until lighttpd answered; needs Down.
	HTTP  *float64 `json:"http,omitempty"`
	UI    *float64 `json:"ui,omitempty"`
	Ready *float64 `json:"ready,omitempty"`
}

func ptr(v float64) *float64 {
	r := round1(v)
	return &r
}

func msToS(ms int64) float64 { return float64(ms) / 1000 }

// derive computes the phases of a record. The box's own figures come first (the monotonic clock
// is exact, the browser polls every few seconds); the browser's fill in what the box cannot know.
func derive(r Record) Phases {
	var p Phases
	b := r.Browser
	if b != nil && b.Down > 0 {
		p.Down = ptr(msToS(b.Down - b.Started))
	}
	switch {
	case r.HTTPMono != nil:
		p.DownHTTP = ptr(r.ToKernelS + *r.HTTPMono)
	case b != nil && b.HTTP > 0:
		p.DownHTTP = ptr(msToS(b.HTTP - b.Started))
	}
	if p.Down != nil && p.DownHTTP != nil {
		p.HTTP = ptr(max(0, *p.DownHTTP-*p.Down))
	}
	switch {
	case r.HTTPMono != nil && r.UIMono != nil:
		p.UI = ptr(max(0, *r.UIMono-*r.HTTPMono))
	case b != nil && b.HTTP > 0 && b.UI > 0:
		p.UI = ptr(msToS(b.UI - b.HTTP))
	}
	// the interfaces count from the moment the web UI answered: the later of lighttpd and occulited
	up := -1.0
	for _, v := range []*float64{r.HTTPMono, r.UIMono} {
		if v != nil && *v > up {
			up = *v
		}
	}
	switch {
	case r.ReadyMono != nil && up >= 0:
		p.Ready = ptr(max(0, *r.ReadyMono-up))
	case b != nil && b.UI > 0 && b.Ready > 0:
		p.Ready = ptr(msToS(b.Ready - b.UI))
	}
	return p
}

// median of a non-empty list: the middle value, or the mean of the two middle ones.
func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// Measured counts the records each phase's figure came from; 0 is the product default.
type Measured struct {
	Down  int `json:"down"`
	HTTP  int `json:"http"`
	UI    int `json:"ui"`
	Ready int `json:"ready"`
}

// keepPerKind is how many records per kind are kept, and how many a median is taken over.
const keepPerKind = 3

// Estimate overrides the default per phase with the median of the newest records that tell that
// phase. A record that knows only shutdown and boot together (no browser saw it) contributes to
// http with the down estimate taken off.
func Estimate(def Expect, recs []Record) (Expect, Measured) {
	if len(recs) > keepPerKind {
		recs = recs[len(recs)-keepPerKind:]
	}
	out := def
	var m Measured
	phases := make([]Phases, len(recs))
	for i, r := range recs {
		phases[i] = derive(r)
	}
	pick := func(get func(Phases) *float64) []float64 {
		var v []float64
		for _, p := range phases {
			if x := get(p); x != nil {
				v = append(v, *x)
			}
		}
		return v
	}
	if v := pick(func(p Phases) *float64 { return p.Down }); len(v) > 0 {
		out.Down, m.Down = round1(median(v)), len(v)
	}
	var https []float64
	for _, p := range phases {
		switch {
		case p.HTTP != nil:
			https = append(https, *p.HTTP)
		case p.DownHTTP != nil:
			https = append(https, max(0, *p.DownHTTP-out.Down))
		}
	}
	if len(https) > 0 {
		out.HTTP, m.HTTP = round1(median(https)), len(https)
	}
	if v := pick(func(p Phases) *float64 { return p.UI }); len(v) > 0 {
		out.UI, m.UI = round1(median(v)), len(v)
	}
	if v := pick(func(p Phases) *float64 { return p.Ready }); len(v) > 0 {
		out.Ready, m.Ready = round1(median(v)), len(v)
	}
	return out, m
}

// Answer is GET /boot-expect: the figures the page counts down with, the product default they
// started from, and how many records each phase's figure came from.
type Answer struct {
	Kind     string   `json:"kind"`
	Product  string   `json:"product"`
	Expect   Expect   `json:"expect"`
	Default  Expect   `json:"default"`
	Measured Measured `json:"measured"`
}

// DefaultAnswer is the answer of a box that has nothing recorded.
func DefaultAnswer(product, kind string) Answer {
	p := ProductOf(product)
	d := Default(p, kind)
	return Answer{Kind: kind, Product: p, Expect: d, Default: d}
}
