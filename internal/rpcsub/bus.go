package rpcsub

import (
	"sync"
	"time"
)

// The bus: what the listener publishes, with a ring for a reader that comes late (the remote
// stream's Last-Event-ID resume, D-79: 5 minutes or 5,000 events), and the in-process handlers
// occulited attaches - the sampler, the service-message store, the pages. One goroutine per
// attached handler drains its own bounded queue, so a slow consumer delays nobody's answer to a
// daemon and loses only its own messages, which it notices as a gap it can sweep over.

const (
	ringMax = 5000
	ringAge = 5 * time.Minute
)

// Event is one datapoint change as an interface process sent it.
type Event struct {
	Interface string
	Address   string // device or channel address, as the daemon says it: 000193C9951175:0
	Key       string
	Value     any
	Time      time.Time
	Batch     uint64
}

// Handler receives what the subscriber gets. Every method is called from the handler's own
// goroutine, one message at a time, in order.
type Handler interface {
	Event(Event)
	// Interface is a state change of an interface: up, down, restarted, added, removed.
	Interface(name, state string, at time.Time)
	// Devices is a device-list change: new, deleted, updated, replaced, readded.
	Devices(iface, op string, addresses []string)
}

type bus struct {
	mu      sync.Mutex
	next    uint64
	ring    []Message
	ringAt  []time.Time
	readers map[*reader]struct{}
	now     func() time.Time
	enrich  func(m *Message, at time.Time)
}

type reader struct {
	ch      chan Message
	dropped uint64
}

func newBus(now func() time.Time) *bus {
	return &bus{readers: map[*reader]struct{}{}, now: now}
}

func (b *bus) seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.next
}

// publish stamps the message and hands it to the ring and every reader.
func (b *bus) publish(m Message) {
	now := b.now()
	b.mu.Lock()
	b.next++
	m.Seq = b.next
	if m.TS == "" {
		m.TS = now.UTC().Format(time.RFC3339Nano)
	}
	if b.enrich != nil {
		b.enrich(&m, now)
	}
	b.ring = append(b.ring, m)
	b.ringAt = append(b.ringAt, now)
	cut := 0
	for cut < len(b.ring) && (len(b.ring)-cut > ringMax || now.Sub(b.ringAt[cut]) > ringAge) {
		cut++
	}
	b.ring, b.ringAt = b.ring[cut:], b.ringAt[cut:]
	for r := range b.readers {
		select {
		case r.ch <- m:
		default:
			r.dropped++
		}
	}
	b.mu.Unlock()
}

// Replay is the ring after seq; ok false when seq is older than what the ring holds.
func (b *bus) Replay(since uint64) (out []Message, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if since >= b.next {
		return nil, true
	}
	if len(b.ring) == 0 || b.ring[0].Seq > since+1 {
		return nil, false
	}
	for _, m := range b.ring {
		if m.Seq > since {
			out = append(out, m)
		}
	}
	return out, true
}

func (b *bus) attach() *reader {
	r := &reader{ch: make(chan Message, 1024)}
	b.mu.Lock()
	b.readers[r] = struct{}{}
	b.mu.Unlock()
	return r
}

func (b *bus) detach(r *reader) {
	b.mu.Lock()
	delete(b.readers, r)
	b.mu.Unlock()
}

// Attach hands every message to the handlers, each from a goroutine of its own, until Run's ctx
// ends. Call it before Run, or any time after: a handler attached later sees what comes after.
func (s *Subscriber) Attach(handlers ...Handler) {
	for _, h := range handlers {
		r := s.bus.attach()
		s.mu.Lock()
		s.handlers = append(s.handlers, r)
		s.mu.Unlock()
		go s.drain(r, h)
	}
}

func (s *Subscriber) drain(r *reader, h Handler) {
	for m := range r.ch {
		at, _ := time.Parse(time.RFC3339Nano, m.TS)
		switch m.Type {
		case "event":
			h.Event(Event{Interface: m.Interface, Address: m.Address, Key: m.Key, Value: m.Value, Time: at, Batch: m.Batch})
		case "interface":
			h.Interface(m.Interface, m.State, at)
		case "devices":
			h.Devices(m.Interface, m.Op, m.Addresses)
		}
	}
}

// Messages is a raw reader for the remote stream (task 77): a channel of every message from
// now on, how many the reader missed because its queue was full (an overflow the stream tells
// its client about), and the function that ends it. The ring's Replay gives what came before.
func (s *Subscriber) Messages() (ch <-chan Message, dropped func() uint64, stop func()) {
	r := s.bus.attach()
	dropped = func() uint64 {
		s.bus.mu.Lock()
		defer s.bus.mu.Unlock()
		return r.dropped
	}
	return r.ch, dropped, func() { s.bus.detach(r) }
}

// Publish puts a message of occulited's own on the bus (task 194: the state store's
// confirmations, type state); a TS set by the caller is kept.
func (s *Subscriber) Publish(m Message) { s.bus.publish(m) }

// Replay is the ring after seq (see bus.Replay).
func (s *Subscriber) Replay(since uint64) ([]Message, bool) { return s.bus.Replay(since) }

// Seq is the last message's sequence number.
func (s *Subscriber) Seq() uint64 { return s.bus.seq() }
