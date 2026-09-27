// Package servicemsg keeps the box's service messages (task 75, read-only; task 82 will persist
// them): which maintenance datapoints - UNREACH, LOWBAT, CONFIG_PENDING, UPDATE_PENDING, the
// ERROR_* codes, SABOTAGE, FAULT_REPORTING - are active on which device, and since when.
//
// Two sources feed one store. The sweep reads channel 0 of every device over the interface's
// own RPC (listDevices, then getParamset(<address>:0, VALUES)): at start, whenever the
// subscriber reports an interface up or restarted, and every 15 minutes as the safety net, because BidCos maintenance events are not measured yet (task 75, D-106). The
// events from the subscriber in between are what makes a message appear the moment a device
// drops. Which datapoints count is the CCU's own rule: the ones whose paramset description
// carries the service flag (FLAGS & 8), read once per device type and cached - so nothing here
// is a list to maintain, and a new device type with a new fault code is covered.
//
// The rules of a transition are task 82's, so its persistence can take the store as it is:
// inactive to active creates the entry with since = now; active to inactive removes it; the
// same active value again changes nothing; an active value that changes to another active
// value is a new message with a new since. What a sweep finds already active is "seen: start" -
// the real start is earlier; what an event made active is "seen: event".
package servicemsg

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
)

// Message is one active service message.
type Message struct {
	Interface string    `json:"interface"`
	Address   string    `json:"address"` // the device
	Channel   string    `json:"channel"` // "0" for the maintenance channel
	Key       string    `json:"key"`
	Value     any       `json:"value"`
	Since     time.Time `json:"since"`
	Seen      string    `json:"seen"` // event or start
	Type      string    `json:"type,omitempty"`
}

// Store is the in-memory store with its sweep.
type Store struct {
	// Interfaces gives the interface processes to sweep.
	Interfaces func() []interfaces.Interface
	// Timeout bounds one RPC call of the sweep.
	Timeout time.Duration
	// Every is the sweep interval; 15 minutes by default.
	Every time.Duration
	Log   *slog.Logger
	Now   func() time.Time
	// OnChange is told after every change of the active set, for the stream (may be nil).
	OnChange func()
	// Since is the state store's last change of a datapoint that holds value (openccu-lite task
	// 194: one clock for both, kept across a restart): a message the sweep finds already active
	// takes it as its since instead of the sweep's time, so a fault that stood for three weeks
	// does not read as "since the last restart". nil, or false: the sweep's time.
	Since func(iface, address, dp string, value any) (time.Time, bool)

	mu       sync.Mutex
	active   map[string]Message // key: interface|address|channel:key
	types    map[string]string  // interface|address -> device type, from the sweep
	flags    map[string]map[string]bool
	swept    time.Time
	sweepErr map[string]string
	sweeping bool
	wake     chan struct{}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func key(iface, address, channel, dp string) string {
	return iface + "|" + address + "|" + channel + ":" + dp
}

// splitChannel divides "00010000000A10:0" into the device and the channel.
func splitChannel(addr string) (device, channel string) {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i], addr[i+1:]
	}
	return addr, ""
}

// Active says whether a datapoint value is a message: true, a number other than 0, a string
// other than empty. STICKY_UNREACH acknowledged is false; FAULT_REPORTING's no-fault is 0.
func Active(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case string:
		return x != "" && x != "0" && !strings.EqualFold(x, "false")
	}
	return false
}

// knownServiceKeys are the maintenance datapoints every CCU user knows; the store counts them
// as service messages when it has no paramset description for the device type yet (an event
// before the first sweep), and the description decides afterwards.
var knownServiceKeys = map[string]bool{
	"UNREACH": true, "STICKY_UNREACH": true, "LOWBAT": true, "LOW_BAT": true, "CONFIG_PENDING": true, "UPDATE_PENDING": true,
	"SABOTAGE": true, "FAULT_REPORTING": true, "ERROR": true, "ERROR_CODE": true, "DUTY_CYCLE": true,
}

// isService says whether a datapoint of a device type counts, by the description's service flag
// when the type is known, by the well-known names when it is not.
func (s *Store) isService(iface, address, channel, dp string) bool {
	typ := s.types[iface+"|"+address]
	if f, ok := s.flags[iface+"|"+typ+"|"+channel]; ok && typ != "" {
		return f[dp]
	}
	return knownServiceKeys[dp] || strings.HasPrefix(dp, "ERROR_")
}

// apply is one transition; it says whether the active set changed.
func (s *Store) apply(iface, device, channel, dp string, value any, seen string, at time.Time) bool {
	k := key(iface, device, channel, dp)
	cur, has := s.active[k]
	switch {
	case !Active(value):
		if !has {
			return false
		}
		delete(s.active, k)
		return true
	case !has:
		s.active[k] = Message{Interface: iface, Address: device, Channel: channel, Key: dp, Value: value, Since: at, Seen: seen, Type: s.types[iface+"|"+device]}
		return true
	case cur.Value == value:
		return false
	default:
		cur.Value, cur.Since, cur.Seen = value, at, seen // a new fault: a new message
		s.active[k] = cur
		return true
	}
}

// Event is the bus's: a datapoint change (rpcsub.Handler).
func (s *Store) Event(e rpcsub.Event) {
	device, channel := splitChannel(e.Address)
	s.mu.Lock()
	if s.active == nil {
		s.active = map[string]Message{}
	}
	changed := s.isService(e.Interface, device, channel, e.Key) && s.apply(e.Interface, device, channel, e.Key, e.Value, "event", e.Time)
	s.mu.Unlock()
	if changed {
		s.log().Info("service messages: changed by an event", "interface", e.Interface, "address", e.Address, "key", e.Key, "value", e.Value)
		s.changed()
	}
}

// Interface is the bus's: an interface that came up or restarted is swept.
func (s *Store) Interface(_ string, state string, _ time.Time) {
	if state == "up" || state == "restarted" {
		s.SweepSoon()
	}
}

// Devices is the bus's: a device list change is swept.
func (s *Store) Devices(string, string, []string) { s.SweepSoon() }

func (s *Store) changed() {
	if s.OnChange != nil {
		s.OnChange()
	}
}

// SweepSoon asks the run loop for a sweep.
func (s *Store) SweepSoon() {
	s.mu.Lock()
	w := s.wake
	s.mu.Unlock()
	if w != nil {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// Run sweeps at start, on request and every Every, until ctx ends.
func (s *Store) Run(ctx context.Context) {
	every := s.Every
	if every == 0 {
		every = 15 * time.Minute
	}
	s.mu.Lock()
	s.wake = make(chan struct{}, 1)
	w := s.wake
	s.mu.Unlock()
	t := time.NewTicker(every)
	defer t.Stop()
	s.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w:
			// requests come in bursts (a restart's up, then the reconnect): one sweep per second
			time.Sleep(time.Second)
			for len(w) > 0 {
				<-w
			}
		}
		s.Sweep(ctx)
	}
}

// Sweep reads every device's maintenance channel and reconciles the store with it: active and
// stored stays with its since; active and new is created as seen at start; stored and no longer
// active goes. An interface that does not answer keeps its entries - a daemon that is down is
// not a device that recovered.
func (s *Store) Sweep(ctx context.Context) {
	if s.Interfaces == nil {
		return
	}
	s.mu.Lock()
	if s.sweeping {
		s.mu.Unlock()
		return
	}
	s.sweeping = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sweeping = false
		s.mu.Unlock()
	}()
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	ifs := s.Interfaces()
	now := s.now()
	errs := map[string]string{}
	changed := false
	for _, i := range ifs {
		if ctx.Err() != nil {
			return
		}
		devs, flags, values, err := interfaces.MaintenanceValues(ctx, i, timeout)
		if err != nil {
			errs[i.Name] = err.Error()
			continue
		}
		s.mu.Lock()
		if s.active == nil {
			s.active = map[string]Message{}
		}
		if s.types == nil {
			s.types = map[string]string{}
		}
		if s.flags == nil {
			s.flags = map[string]map[string]bool{}
		}
		seen := map[string]bool{}
		for _, d := range devs {
			s.types[i.Name+"|"+d.Address] = d.Type
		}
		for typ, f := range flags {
			s.flags[i.Name+"|"+typ+"|0"] = f
		}
		for addr, vals := range values {
			device, channel := splitChannel(addr)
			for dp, v := range vals {
				if !s.isService(i.Name, device, channel, dp) {
					continue
				}
				k := key(i.Name, device, channel, dp)
				seen[k] = true
				if _, has := s.active[k]; has {
					// active and stored: the since stays, the value follows only if it changed
					if s.apply(i.Name, device, channel, dp, v, "start", now) {
						changed = true
					}
					continue
				}
				at := now
				if s.Since != nil {
					if t, ok := s.Since(i.Name, addr, dp, v); ok && t.Before(now) {
						at = t
					}
				}
				if s.apply(i.Name, device, channel, dp, v, "start", at) {
					changed = true
				}
			}
		}
		// stored, and this interface's sweep did not find it active: gone
		for k, m := range s.active {
			if m.Interface == i.Name && !seen[k] {
				delete(s.active, k)
				changed = true
			}
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.swept, s.sweepErr = now, errs
	n := len(s.active)
	s.mu.Unlock()
	s.log().Debug("service messages: swept", "active", n, "errors", len(errs))
	if changed {
		s.changed()
	}
}

// Snapshot is what the API shows.
type Snapshot struct {
	Count    int               `json:"count"`
	Messages []Message         `json:"messages"`
	Swept    time.Time         `json:"swept"`
	Errors   map[string]string `json:"errors"`
}

// List is the active messages, the oldest first.
func (s *Store) List() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Snapshot{Messages: []Message{}, Swept: s.swept, Errors: map[string]string{}}
	for _, m := range s.active {
		out.Messages = append(out.Messages, m)
	}
	for k, v := range s.sweepErr {
		out.Errors[k] = v
	}
	sort.Slice(out.Messages, func(a, b int) bool {
		if !out.Messages[a].Since.Equal(out.Messages[b].Since) {
			return out.Messages[a].Since.Before(out.Messages[b].Since)
		}
		return out.Messages[a].Address+out.Messages[a].Key < out.Messages[b].Address+out.Messages[b].Key
	})
	out.Count = len(out.Messages)
	return out
}
