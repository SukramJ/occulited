package mqttpub

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/meta"
)

// Config is the mqtt section of occulited.json.
type Config struct {
	Enabled  bool   `json:"enabled"`
	Broker   string `json:"broker"` // host:port, e.g. 127.0.0.1:1883
	Username string `json:"username"`
	Password string `json:"password"`
	Prefix   string `json:"prefix"` // default occulite
	ClientID string `json:"client_id"`
}

// Publisher mirrors a store to a broker: everything on (re)connect, then each event.
type Publisher struct {
	Config Config
	Store  *meta.Store
	Log    *slog.Logger
}

// Run connects, mirrors and follows the store until ctx ends; it reconnects with backoff.
func (p *Publisher) Run(ctx context.Context) {
	if p.Config.Prefix == "" {
		p.Config.Prefix = "occulite"
	}
	if p.Config.ClientID == "" {
		p.Config.ClientID = "occulited"
	}
	if p.Log == nil {
		p.Log = slog.Default()
	}
	backoff := 5 * time.Second
	for {
		err := p.session(ctx)
		if ctx.Err() != nil {
			return
		}
		p.Log.Warn("mqtt: connection lost", "broker", p.Config.Broker, "err", err, "retry_in", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 2*time.Minute {
			backoff *= 2
		}
	}
}

func (p *Publisher) session(ctx context.Context) error {
	c, err := Dial(ctx, p.Config.Broker, p.Config.ClientID, p.Config.Username, p.Config.Password, 60*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()
	p.Log.Info("mqtt: connected", "broker", p.Config.Broker, "prefix", p.Config.Prefix)
	// the reader notices a dead connection; its error ends the session
	dead := make(chan error, 1)
	go func() { dead <- c.Serve() }()
	if err := p.mirror(c); err != nil {
		return err
	}
	events := p.Store.Subscribe()
	defer p.Store.Unsubscribe(events)
	ping := time.NewTicker(30 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-dead:
			return err
		case <-ping.C:
			if err := c.Ping(); err != nil {
				return err
			}
		case ev, ok := <-events:
			if !ok {
				return nil
			}
			if err := p.publishEvent(c, ev); err != nil {
				return err
			}
		}
	}
}

func (p *Publisher) topic(parts ...string) string {
	return p.Config.Prefix + "/" + strings.Join(parts, "/")
}

// mirror publishes the whole store: every object, every enum, then the revision last so a
// consumer that waits for the revision sees a complete picture.
func (p *Publisher) mirror(c *Conn) error {
	snap := p.Store.Snapshot()
	for ref, o := range snap.Objects {
		b, _ := json.Marshal(o)
		if err := c.Publish(p.topic("meta", "object", ref), b); err != nil {
			return err
		}
	}
	for id, e := range snap.Enums {
		b, _ := json.Marshal(e)
		if err := c.Publish(p.topic("meta", "enum", id), b); err != nil {
			return err
		}
	}
	return c.Publish(p.topic("meta", "revision"), []byte(itoa(snap.Revision)))
}

func (p *Publisher) publishEvent(c *Conn, ev meta.Event) error {
	switch ev.Kind {
	case "object.updated":
		b, _ := json.Marshal(ev.Value)
		if err := c.Publish(p.topic("meta", "object", ev.Ref), b); err != nil {
			return err
		}
	case "object.deleted":
		if err := c.Publish(p.topic("meta", "object", ev.Ref), nil); err != nil {
			return err
		}
	case "enum.created", "enum.updated", "node.created", "node.updated", "node.deleted", "node.moved":
		// the enum's tree changed: republish the whole enum from the store
		if e := p.Store.Snapshot().Enums[ev.Enum]; e != nil {
			b, _ := json.Marshal(e)
			if err := c.Publish(p.topic("meta", "enum", ev.Enum), b); err != nil {
				return err
			}
		}
		if ev.Kind == "node.moved" || ev.Kind == "node.deleted" {
			// member paths changed too: objects of that enum are republished by a full mirror
			return p.mirror(c)
		}
	case "enum.deleted":
		if err := c.Publish(p.topic("meta", "enum", ev.Enum), nil); err != nil {
			return err
		}
		return p.mirror(c)
	case "import":
		return p.mirror(c)
	}
	return c.Publish(p.topic("meta", "revision"), []byte(itoa(ev.Revision)))
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
