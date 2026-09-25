package httpapi

import (
	"testing"
	"time"
)

// B-83: the interval follows what the listing costs, within 5 and 30 s
func TestPollSeconds(t *testing.T) {
	cases := []struct {
		avg  time.Duration
		want int
	}{
		{0, 5},
		{80 * time.Millisecond, 5},
		{600 * time.Millisecond, 5},
		{700 * time.Millisecond, 6},
		{1740 * time.Millisecond, 14}, // the Charly's listing before the unit cache
		{3 * time.Second, 24},
		{10 * time.Second, 30},
	}
	for _, c := range cases {
		if got := pollSeconds(c.avg); got != c.want {
			t.Errorf("%s: %d, want %d", c.avg, got, c.want)
		}
	}
}

// one expensive listing among cheap ones does not stretch the interval; a box that stays slow does
func TestPollMeter(t *testing.T) {
	var m pollMeter
	if got := m.observe(1740 * time.Millisecond); got != 14 {
		t.Errorf("first listing: %d", got)
	}
	var got int
	for range 5 {
		got = m.observe(60 * time.Millisecond)
	}
	if got != 5 {
		t.Errorf("after cheap listings: %d", got)
	}
	if got = m.observe(1100 * time.Millisecond); got != 5 {
		t.Errorf("one full refresh among cheap listings: %d", got)
	}
	for range 3 {
		got = m.observe(1740 * time.Millisecond)
	}
	if got < 10 {
		t.Errorf("a box that stays slow: %d", got)
	}
}
