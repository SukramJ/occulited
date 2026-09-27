package servicemsg

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/rpcsub"
	"github.com/mdzio/go-hmccu/v2/itf/xmlrpc"
)

// a fake interface process for the sweep: two devices, channel 0's description with the
// service flag on UNREACH, LOWBAT and CONFIG_PENDING but not on RSSI_DEVICE, and values a test
// sets
type fakeIface struct {
	mu     sync.Mutex
	values map[string]map[string]any // channel -> key -> value
	srv    *httptest.Server
}

func newFakeIface(t *testing.T) *fakeIface {
	f := &fakeIface{values: map[string]map[string]any{}}
	d := &xmlrpc.BasicDispatcher{}
	desc := func(addr, typ, parent string, ch int) *xmlrpc.Value {
		m := map[string]interface{}{"ADDRESS": addr, "TYPE": typ, "PARENT": parent, "CHILDREN": []interface{}{}, "FIRMWARE": "1.0", "AVAILABLE_FIRMWARE": ""}
		if parent == "" {
			m["CHILDREN"] = []interface{}{addr + ":0", addr + ":1"}
		}
		v, _ := xmlrpc.NewMap(m)
		return v
	}
	d.HandleFunc("listDevices", func(*xmlrpc.Value) (*xmlrpc.Value, error) {
		return &xmlrpc.Value{Array: &xmlrpc.Array{Data: []*xmlrpc.Value{
			desc("ABC0000001", "HmIP-WRC2", "", 0), desc("ABC0000001:0", "MAINTENANCE", "ABC0000001", 0), desc("ABC0000001:1", "KEY", "ABC0000001", 1),
			desc("ABC0000002", "HmIP-PDT", "", 0), desc("ABC0000002:0", "MAINTENANCE", "ABC0000002", 0),
		}}}, nil
	})
	d.HandleFunc("getParamsetDescription", func(*xmlrpc.Value) (*xmlrpc.Value, error) {
		p := func(flags int) map[string]interface{} { return map[string]interface{}{"FLAGS": flags, "TYPE": "BOOL"} }
		v, _ := xmlrpc.NewMap(map[string]interface{}{"UNREACH": p(9), "LOWBAT": p(9), "CONFIG_PENDING": p(9), "RSSI_DEVICE": p(1)})
		return v, nil
	})
	d.HandleFunc("getParamset", func(args *xmlrpc.Value) (*xmlrpc.Value, error) {
		ch := xmlrpc.Q(args).Idx(0).String()
		f.mu.Lock()
		defer f.mu.Unlock()
		vals := map[string]interface{}{}
		for k, v := range f.values[ch] {
			vals[k] = v
		}
		v, _ := xmlrpc.NewMap(vals)
		return v, nil
	})
	f.srv = httptest.NewServer(&xmlrpc.Handler{Dispatcher: d})
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeIface) set(ch string, vals map[string]any) {
	f.mu.Lock()
	f.values[ch] = vals
	f.mu.Unlock()
}

func (f *fakeIface) interfaces() []interfaces.Interface {
	return interfaces.FromList([]struct{ Name, URL string }{{"HmIP-RF", "xmlrpc://" + strings.TrimPrefix(f.srv.URL, "http://")}})
}

func keys(s Snapshot) string {
	var out []string
	for _, m := range s.Messages {
		out = append(out, m.Address+":"+m.Channel+":"+m.Key+"="+m.Seen)
	}
	return strings.Join(out, " ")
}

func TestSweepAndTransitions(t *testing.T) {
	fi := newFakeIface(t)
	fi.set("ABC0000001:0", map[string]any{"UNREACH": false, "LOWBAT": true, "CONFIG_PENDING": false, "RSSI_DEVICE": -60})
	fi.set("ABC0000002:0", map[string]any{"UNREACH": true, "LOWBAT": false, "CONFIG_PENDING": false})
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	changes := 0
	st := &Store{Interfaces: fi.interfaces, Timeout: 5 * time.Second, Now: func() time.Time { return now }, OnChange: func() { changes++ }}
	st.Sweep(context.Background())
	// what a sweep finds is "seen at start"; RSSI_DEVICE has no service flag
	if got := keys(st.List()); got != "ABC0000001:0:LOWBAT=start ABC0000002:0:UNREACH=start" {
		t.Fatalf("after the sweep: %q", got)
	}
	if changes != 1 || st.List().Messages[0].Type != "HmIP-WRC2" || !st.List().Messages[0].Since.Equal(now) {
		t.Fatalf("changes %d, first %+v", changes, st.List().Messages[0])
	}
	// an event makes one active (seen: event), the same value again changes nothing, and false clears it
	later := now.Add(time.Minute)
	st.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "ABC0000001:0", Key: "UNREACH", Value: true, Time: later})
	if got := keys(st.List()); got != "ABC0000001:0:LOWBAT=start ABC0000002:0:UNREACH=start ABC0000001:0:UNREACH=event" || changes != 2 {
		t.Fatalf("after the event: %q (%d)", got, changes)
	}
	st.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "ABC0000001:0", Key: "UNREACH", Value: true, Time: later.Add(time.Second)})
	if changes != 2 {
		t.Fatal("the same value again counted as a change")
	}
	st.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "ABC0000001:0", Key: "UNREACH", Value: false, Time: later.Add(2 * time.Second)})
	if got := keys(st.List()); got != "ABC0000001:0:LOWBAT=start ABC0000002:0:UNREACH=start" || changes != 3 {
		t.Fatalf("after the clear: %q", got)
	}
	// a datapoint without the flag is not a message, whatever its value
	st.Event(rpcsub.Event{Interface: "HmIP-RF", Address: "ABC0000001:0", Key: "RSSI_DEVICE", Value: -50, Time: later})
	if st.List().Count != 2 {
		t.Fatal("RSSI_DEVICE became a message")
	}
	// the next sweep: the stored LOWBAT keeps its since, a cleared UNREACH goes, a new fault code is a new message
	fi.set("ABC0000001:0", map[string]any{"UNREACH": false, "LOWBAT": true, "CONFIG_PENDING": true})
	fi.set("ABC0000002:0", map[string]any{"UNREACH": false, "LOWBAT": false, "CONFIG_PENDING": false})
	now = now.Add(15 * time.Minute)
	st.Sweep(context.Background())
	got := st.List()
	if keys(got) != "ABC0000001:0:LOWBAT=start ABC0000001:0:CONFIG_PENDING=start" {
		t.Fatalf("after the second sweep: %q", keys(got))
	}
	if !got.Messages[0].Since.Equal(now.Add(-15*time.Minute)) || !got.Messages[1].Since.Equal(now) {
		t.Fatalf("since kept / new: %v %v", got.Messages[0].Since, got.Messages[1].Since)
	}
}

func TestActiveValues(t *testing.T) {
	for v, want := range map[any]bool{true: true, false: false, 0: false, 4: true, 0.0: false, "": false, "0": false, "x": true} {
		if Active(v) != want {
			t.Errorf("Active(%v) = %v", v, !want)
		}
	}
	// an enum fault code changing is a new message with a new since
	st := &Store{Now: time.Now}
	st.active = map[string]Message{}
	st.Event(rpcsub.Event{Interface: "BidCos-RF", Address: "JEQ9000001:0", Key: "FAULT_REPORTING", Value: 4, Time: time.Unix(100, 0)})
	st.Event(rpcsub.Event{Interface: "BidCos-RF", Address: "JEQ9000001:0", Key: "FAULT_REPORTING", Value: 6, Time: time.Unix(200, 0)})
	l := st.List()
	if l.Count != 1 || l.Messages[0].Value != 6 || !l.Messages[0].Since.Equal(time.Unix(200, 0)) {
		t.Fatalf("%+v", l.Messages)
	}
}

func TestSweepKeepsEntriesOfAnInterfaceThatDoesNotAnswer(t *testing.T) {
	fi := newFakeIface(t)
	fi.set("ABC0000001:0", map[string]any{"UNREACH": true})
	st := &Store{Interfaces: fi.interfaces, Timeout: time.Second}
	st.Sweep(context.Background())
	if st.List().Count != 1 {
		t.Fatal("no message after the sweep")
	}
	fi.srv.Close()
	st.Sweep(context.Background())
	l := st.List()
	if l.Count != 1 || l.Errors["HmIP-RF"] == "" {
		t.Fatalf("a dead interface took its messages with it: %+v", l)
	}
}
