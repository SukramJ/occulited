package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/store"
)

// task 94: the sampler asks for the interface list at every poll, so an InterfacesList.xml that
// occu-init-hs485d rewrites after occulited started - an interface added, one gone - is what the
// next poll samples, not the list the daemon saw at its own start.
func TestSamplerReadsTheListAtEveryPoll(t *testing.T) {
	// a port nobody listens on: every call is refused at once and lands in the errors
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := ln.Addr().String()
	_ = ln.Close()

	list := []struct{ Name, URL string }{{"BidCos-RF", "xmlrpc_bin://" + closed}}
	asked := 0
	s := &Sampler{Timeout: 2 * time.Second, Interfaces: func() []interfaces.Interface {
		asked++
		return interfaces.FromList(list)
	}}
	s.Poll(context.Background())
	if st := s.Status(); len(st.Errors) != 1 || st.Errors["BidCos-RF"] == "" {
		t.Fatalf("first poll: %+v", st.Errors)
	}

	list = append(list, struct{ Name, URL string }{"HmIP-RF", "xmlrpc://" + closed})
	s.Poll(context.Background())
	st := s.Status()
	if asked != 2 {
		t.Errorf("the list was asked for %d times in two polls", asked)
	}
	if len(st.Errors) != 2 || st.Errors["HmIP-RF"] == "" {
		t.Errorf("second poll: %+v", st.Errors)
	}

	list = list[1:]
	s.Poll(context.Background())
	if st := s.Status(); len(st.Errors) != 1 || st.Errors["BidCos-RF"] != "" {
		t.Errorf("an interface that left the list is not sampled: %+v", st.Errors)
	}
}

// task 94: PollSoon polls once in the background, and not again while that runs or within the gap
func TestPollSoon(t *testing.T) {
	if (&Sampler{}).PollSoon() {
		t.Error("a sampler without an interface list polled")
	}
	polls := make(chan struct{}, 8)
	s := &Sampler{Timeout: time.Second, Interfaces: func() []interfaces.Interface {
		polls <- struct{}{}
		return nil
	}}
	idle := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s.mu.Lock()
			busy := s.soon
			s.mu.Unlock()
			if !busy {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("the background poll did not end")
	}
	if !s.PollSoon() {
		t.Fatal("the first PollSoon did not poll")
	}
	select {
	case <-polls:
	case <-time.After(5 * time.Second):
		t.Fatal("no poll")
	}
	idle()
	if s.Status().Polled.IsZero() {
		t.Error("the background poll recorded nothing")
	}
	if s.PollSoon() {
		t.Error("a second PollSoon within the gap polled again")
	}
	// the gap is over: once more
	s.mu.Lock()
	s.soonAt = time.Now().Add(-pollSoonGap)
	s.mu.Unlock()
	if !s.PollSoon() {
		t.Error("PollSoon after the gap did not poll")
	}
	idle()
	if len(polls) != 1 {
		t.Errorf("%d more polls, want 1", len(polls))
	}
}

// task 151: a sample carries the carrier sense when the interface reported one - 0 % included -
// and leaves the field out when it reported none, which the Status page draws as a gap
func TestSampleCarrierSense(t *testing.T) {
	zero, four := 0, 4
	at := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		cs   *int
		want string
	}{
		{nil, `{"t":"2026-09-18T12:00:00Z","dc":3,"up":true}`},
		{&zero, `{"t":"2026-09-18T12:00:00Z","dc":3,"cs":0,"up":true}`},
		{&four, `{"t":"2026-09-18T12:00:00Z","dc":3,"cs":4,"up":true}`},
	} {
		b, err := json.Marshal(Sample{Time: at, DutyCycle: 3, CarrierSense: c.cs, Connected: true})
		if err != nil || string(b) != c.want {
			t.Errorf("%s, want %s (%v)", b, c.want, err)
		}
	}
}

// hs485d answers listBidcosInterfaces with a fault: the Status page shows it up, not "not
// answering" - it is in answering, not in the errors.
func TestSamplerAFaultIsAnswering(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><fault><value><struct><member><name>faultCode</name><value><i4>-1</i4></value></member><member><name>faultString</name><value>listBidcosInterfaces: unknown method name</value></member></struct></value></fault></methodResponse>`))
	}))
	defer srv.Close()
	s := &Sampler{Timeout: 2 * time.Second, Interfaces: func() []interfaces.Interface {
		return interfaces.FromList([]struct{ Name, URL string }{{"BidCos-Wired", "xmlrpc://" + strings.TrimPrefix(srv.URL, "http://")}})
	}}
	s.Poll(t.Context())
	st := s.Status()
	if len(st.Errors) != 0 || len(st.Answering) != 1 || st.Answering[0] != "BidCos-Wired" || len(st.Interfaces) != 0 {
		t.Fatalf("%+v", st)
	}
	b, _ := json.Marshal(st)
	if !strings.Contains(string(b), `"answering":["BidCos-Wired"]`) {
		t.Errorf("json: %s", b)
	}
}

// task 214: the ring is Rows samples per series, and the sampler is the store's keeper - what it
// wrote comes back after a restart in order, with the carrier sense and the link.
func TestHistorySurvivesARestartThroughTheStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), store.DirName, store.FileName)
	t0 := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cs := 7
	s := &Sampler{Rows: 4}
	m := &store.Manager{Path: path, Platform: "ova", Rows: 4}
	m.Register(s)
	m.Start("", "")
	s.mu.Lock()
	for i := 0; i < 6; i++ {
		sm := Sample{Time: t0.Add(time.Duration(i) * time.Minute), DutyCycle: i, Connected: i != 2}
		if i == 5 {
			sm.CarrierSense = &cs
		}
		s.appendLocked("HmIP-RF/3014F711A0", sm)
	}
	s.mu.Unlock()
	if h := s.Status().History["HmIP-RF/3014F711A0"]; len(h) != 4 || h[0].DutyCycle != 2 {
		t.Fatalf("the ring in memory: %+v", h)
	}
	if err := m.Flush(); err != nil {
		t.Fatal(err)
	}
	// nothing new: nothing pending
	if series, _ := s.Pending(false); len(series) != 0 {
		t.Errorf("pending after a write: %+v", series)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	cancel()
	<-m.Done()

	s2 := &Sampler{Rows: 4}
	m2 := &store.Manager{Path: path, Platform: "ova", Rows: 4}
	m2.Register(s2)
	m2.Start("", "")
	h := s2.Status().History["HmIP-RF/3014F711A0"]
	if len(h) != 4 {
		t.Fatalf("restored %d samples", len(h))
	}
	for i, sm := range h {
		if !sm.Time.Equal(t0.Add(time.Duration(i+2)*time.Minute)) || sm.DutyCycle != i+2 || sm.Connected != (i+2 != 2) {
			t.Errorf("sample %d: %+v", i, sm)
		}
	}
	if h[3].CarrierSense == nil || *h[3].CarrierSense != 7 || h[2].CarrierSense != nil {
		t.Errorf("the carrier sense did not come back: %v %v", h[3].CarrierSense, h[2].CarrierSense)
	}
	// a restored history is written, nothing is pending but the whole write after the open
	if series, _ := s2.Pending(false); len(series) != 0 {
		t.Errorf("restored rows are pending again: %+v", series)
	}
	// a new sample: pending is exactly that one
	s2.mu.Lock()
	s2.appendLocked("HmIP-RF/3014F711A0", Sample{Time: t0.Add(10 * time.Minute), DutyCycle: 9, Connected: true})
	s2.mu.Unlock()
	series, done := s2.Pending(false)
	if len(series) != 1 || len(series[0].Rows) != 1 || series[0].Replace {
		t.Fatalf("pending: %+v", series)
	}
	done()
	if series, _ := s2.Pending(false); len(series) != 0 {
		t.Errorf("pending after done: %+v", series)
	}
	if all, _ := s2.Pending(true); len(all) != 1 || len(all[0].Rows) != 4 || !all[0].Replace {
		t.Errorf("a whole write: %+v", all)
	}
	_ = m2.Flush()
}

// After a switch out of ram, memory's samples stay and the file's older ones come in front.
func TestRestoreMergesWithMemory(t *testing.T) {
	t0 := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	s := &Sampler{Rows: 5}
	s.mu.Lock()
	s.appendLocked("a", Sample{Time: t0.Add(3 * time.Minute), DutyCycle: 3})
	s.appendLocked("a", Sample{Time: t0.Add(4 * time.Minute), DutyCycle: 4})
	s.mu.Unlock()
	var file []store.Row
	for i := 0; i < 5; i++ { // 3 and 4 overlap memory and are left out
		file = append(file, encodeSample(Sample{Time: t0.Add(time.Duration(i) * time.Minute), DutyCycle: i}))
	}
	file = append(file, store.Row{At: t0, Value: []byte{9}}) // an unknown encoding is skipped
	s.Restore(map[string][]store.Row{"a": file})
	h := s.Status().History["a"]
	var got []int
	for _, sm := range h {
		got = append(got, sm.DutyCycle)
	}
	if fmt.Sprint(got) != "[0 1 2 3 4]" {
		t.Errorf("merged %v", got)
	}
	// memory's two are pending, the file's three are not
	if series, _ := s.Pending(false); len(series) != 1 || len(series[0].Rows) != 2 {
		t.Errorf("pending %+v", series)
	}
}
