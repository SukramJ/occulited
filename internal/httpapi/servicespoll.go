package httpapi

import (
	"math"
	"sync"
	"time"
)

// pollMeter is how long the Services page waits between two polls (B-83): eight times what a
// listing has recently cost, never less than 5 s and never more than 30 s. A listing that costs a
// Pi 3-class box 1.7 s (the Charly before the unit cache) makes 14 s; one of 0.1 s stays at 5 s.
// The average moves by a third of each new listing, so a single full refresh does not stretch the
// interval and a box that stays slow is noticed within three polls.
type pollMeter struct {
	mu  sync.Mutex
	avg time.Duration
	n   int
}

const (
	pollFactor = 8
	pollMin    = 5
	pollMax    = 30
)

// observe records one listing and returns the interval in seconds.
func (m *pollMeter) observe(d time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.n == 0 {
		m.avg = d
	} else {
		m.avg = (2*m.avg + d) / 3
	}
	m.n++
	return pollSeconds(m.avg)
}

func pollSeconds(avg time.Duration) int {
	s := int(math.Ceil(pollFactor * avg.Seconds()))
	return max(pollMin, min(pollMax, s))
}
