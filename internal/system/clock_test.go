package system

import (
	"context"
	"errors"
	"testing"
	"time"
)

const trackingSynced = `Reference ID    : 3650BCFC (2003:a:87f:c37c::3)
Stratum         : 3
Ref time (UTC)  : Sat Sep 12 15:36:51 2026
System time     : 0.000138701 seconds slow of NTP time
Update interval : 64.9 seconds
Leap status     : Normal
`

const trackingUnsynced = `Reference ID    : 00000000 ()
Stratum         : 0
Ref time (UTC)  : Thu Jan 01 00:00:00 1970
System time     : 0.000000000 seconds fast of NTP time
Leap status     : Not synchronised
`

func TestLeapNormal(t *testing.T) {
	for _, c := range []struct {
		out  string
		want bool
	}{
		{trackingSynced, true},
		{trackingUnsynced, false},
		{"", false},
		{"506 Cannot talk to daemon\n", false},
		{"Leap status     : Insert second\n", false},
	} {
		if got := leapNormal(c.out); got != c.want {
			t.Errorf("%q: %v", c.out, got)
		}
	}
}

// task 94: the notice's source - the gate's file, and chrony only after a timeout, not on every poll
func TestClockCheck(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	var calls int
	answer, fail := trackingUnsynced, false
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if name != "chronyc" || len(args) != 2 || args[0] != "-n" || args[1] != "tracking" {
			t.Errorf("ran %s %v", name, args)
		}
		if fail {
			return nil, errors.New("exit status 1")
		}
		return []byte(answer), nil
	}

	// no gate on this image: nothing
	r := rootWith(t, map[string]string{})
	c := &ClockCheck{Root: r, Run: run, Now: func() time.Time { return now }}
	if st := c.Status(ctx); st != nil || calls != 0 {
		t.Fatalf("without the file: %+v, %d calls", st, calls)
	}

	for _, state := range []string{"rtc", "ntp"} {
		r := rootWith(t, map[string]string{"run/occulite/clock-state": state + "\n"})
		c := &ClockCheck{Root: r, Run: run}
		if st := c.Status(ctx); st == nil || st.State != state || !st.Synchronised || calls != 0 {
			t.Errorf("%s: %+v, %d calls", state, st, calls)
		}
	}

	r = rootWith(t, map[string]string{"run/occulite/clock-state": "timeout\n"})
	c = &ClockCheck{Root: r, Run: run, Now: func() time.Time { return now }}
	if st := c.Status(ctx); st == nil || st.State != "timeout" || st.Synchronised || calls != 1 {
		t.Fatalf("timeout, chrony unsynchronised: %+v, %d calls", st, calls)
	}
	// the page polls every 10 s: chrony is asked again only after clockRecheck
	now = now.Add(10 * time.Second)
	answer = trackingSynced
	if st := c.Status(ctx); st.Synchronised || calls != 1 {
		t.Errorf("within the recheck: %+v, %d calls", st, calls)
	}
	now = now.Add(clockRecheck)
	if st := c.Status(ctx); !st.Synchronised || calls != 2 {
		t.Errorf("after the recheck: %+v, %d calls", st, calls)
	}
	// synchronised once: not asked again, even if chrony would say otherwise now
	answer = trackingUnsynced
	now = now.Add(time.Hour)
	if st := c.Status(ctx); !st.Synchronised || calls != 2 {
		t.Errorf("after the sync: %+v, %d calls", st, calls)
	}

	// chrony cannot be asked: the timeout stands; a clock stepped backwards asks again at once
	c = &ClockCheck{Root: r, Run: run, Now: func() time.Time { return now }}
	fail = true
	if st := c.Status(ctx); st.Synchronised || calls != 3 {
		t.Errorf("chronyc failing: %+v, %d calls", st, calls)
	}
	fail, answer = false, trackingSynced
	now = now.Add(-24 * time.Hour)
	if st := c.Status(ctx); !st.Synchronised || calls != 4 {
		t.Errorf("clock stepped back: %+v, %d calls", st, calls)
	}
}
