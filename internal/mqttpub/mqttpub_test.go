package mqttpub

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/meta"
)

// a broker that accepts CONNECT, records retained PUBLISHes and answers PINGREQ
type fakeBroker struct {
	ln       net.Listener
	mu       sync.Mutex
	retained map[string]string
	user     string
}

func newBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{ln: ln, retained: map[string]string{}}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go b.serve(c)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return b
}

func (b *fakeBroker) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		h, err := r.ReadByte()
		if err != nil {
			return
		}
		n, mult := 0, 1
		for {
			x, err := r.ReadByte()
			if err != nil {
				return
			}
			n += int(x&0x7F) * mult
			if x&0x80 == 0 {
				break
			}
			mult *= 128
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		switch h & 0xF0 {
		case 0x10:
			// skip protocol name (2+4), level, flags, keepalive, then client id, then user
			flags := body[7]
			rest := body[10:]
			l := int(rest[0])<<8 | int(rest[1])
			rest = rest[2+l:]
			if flags&0x80 != 0 {
				l = int(rest[0])<<8 | int(rest[1])
				b.mu.Lock()
				b.user = string(rest[2 : 2+l])
				b.mu.Unlock()
			}
			c.Write([]byte{0x20, 2, 0, 0})
		case 0x30:
			l := int(body[0])<<8 | int(body[1])
			topic := string(body[2 : 2+l])
			b.mu.Lock()
			if h&0x01 != 0 {
				b.retained[topic] = string(body[2+l:])
			}
			b.mu.Unlock()
		case 0xC0:
			c.Write([]byte{0xD0, 0})
		}
	}
}

func (b *fakeBroker) get(topic string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.retained[topic]
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestMirrorAndEvents(t *testing.T) {
	br := newBroker(t)
	store, _ := meta.New(nil, nil)
	doc := meta.NewDocument()
	doc.Enums["room"].Tree = []*meta.Node{{ID: "eg", Name: "EG"}}
	doc.Objects["BidCos-RF.ABC0000001:1"] = &meta.Object{Name: "Licht", Enums: []string{"room/eg"}}
	_, _, _ = store.Import(nil, doc, meta.ImportReplace)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := &Publisher{Config: Config{Broker: br.ln.Addr().String(), Username: "u", Password: "p", Prefix: "t"}, Store: store}
	go p.Run(ctx)

	waitFor(t, func() bool { return br.get("t/meta/revision") == "1" })
	if got := br.get("t/meta/object/BidCos-RF.ABC0000001:1"); !strings.Contains(got, `"name":"Licht"`) {
		t.Fatalf("object: %q", got)
	}
	if got := br.get("t/meta/enum/room"); !strings.Contains(got, `"id":"eg"`) {
		t.Fatalf("enum: %q", got)
	}
	if br.user != "u" {
		t.Errorf("credentials not sent: %q", br.user)
	}
	// an update and a delete flow through the events
	name := "Lampe"
	_, _, _ = store.SetObject(nil, "BidCos-RF.ABC0000001:1", meta.ObjectPatch{Name: &name})
	waitFor(t, func() bool { return strings.Contains(br.get("t/meta/object/BidCos-RF.ABC0000001:1"), "Lampe") })
	waitFor(t, func() bool { return br.get("t/meta/revision") == "2" })
	_, _, _ = store.DeleteObject(nil, "BidCos-RF.ABC0000001:1")
	waitFor(t, func() bool { return br.get("t/meta/object/BidCos-RF.ABC0000001:1") == "" })
	waitFor(t, func() bool { return br.get("t/meta/revision") == "3" })
}
