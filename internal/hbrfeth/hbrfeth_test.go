package hbrfeth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestValidAddress(t *testing.T) {
	for _, ok := range []string{"192.168.1.50", "10.0.0.1", " 192.0.2.9 "} {
		if err := ValidAddress(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "hb-rf-eth.local", "fe80::1", "::ffff:192.168.1.5", "0.0.0.0", "255.255.255.255", "224.0.0.251", "127.0.0.1", "192.168.1", "192.168.1.300"} {
		if err := ValidAddress(bad); err == nil {
			t.Errorf("%q taken", bad)
		}
	}
}

// the fake board: /sysinfo.json as the firmware writes it (src/webui.cpp)
const sysinfo = `{"sysInfo":{"serial":"ABCDEF1234","currentVersion":"1.3.0","latestVersion":"1.3.1","memoryUsage":40.1,"cpuUsage":3.2,
"rawUartRemoteAddress":"%s","radioModuleType":"HM-MOD-RPI-PCB","radioModuleSerial":"MEQ9000005",
"radioModuleBidCosRadioMAC":"0x3D0A01","radioModuleHmIPRadioMAC":"0x000000","radioModuleSGTIN":"3014F711A0000E000000AB12"}}`

func fakeBoard(t *testing.T, connectedTo string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sysinfo.json" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(sprintf(sysinfo, connectedTo)))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func sprintf(f, a string) string {
	out := []byte{}
	for i := 0; i < len(f); i++ {
		if f[i] == '%' && i+1 < len(f) && f[i+1] == 's' {
			out = append(out, a...)
			i++
			continue
		}
		out = append(out, f[i])
	}
	return string(out)
}

func TestInfoAndState(t *testing.T) {
	srv := fakeBoard(t, "192.0.2.7")
	c := &Client{BaseURL: func(string) string { return srv.URL }}
	b, err := c.Info(context.Background(), "192.0.2.50")
	if err != nil || b.Address != "192.0.2.50" || b.Serial != "ABCDEF1234" || b.Firmware != "1.3.0" || b.ModuleType != "HM-MOD-RPI-PCB" ||
		b.ModuleSerial != "MEQ9000005" || b.BidCosRadio != "0x3D0A01" || b.HmIPRadio != "" || b.ConnectedTo != "192.0.2.7" {
		t.Fatalf("%v %+v", err, b)
	}
	if StateFor(b, []string{"192.0.2.7"}) != "this" || StateFor(b, []string{"192.0.2.8"}) != "other" || StateFor(Board{}, nil) != "free" {
		t.Error("state")
	}
	free := fakeBoard(t, "0.0.0.0")
	c2 := &Client{BaseURL: func(string) string { return free.URL }}
	if b, _ := c2.Info(context.Background(), "x"); b.ConnectedTo != "" {
		t.Errorf("0.0.0.0 is free: %+v", b)
	}
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"hello":1}`)) }))
	defer other.Close()
	c3 := &Client{BaseURL: func(string) string { return other.URL }}
	if _, err := c3.Info(context.Background(), "x"); err == nil {
		t.Error("another device's JSON taken for a board")
	}
	c4 := &Client{HTTP: &http.Client{Timeout: 300 * time.Millisecond}, BaseURL: func(string) string { return "http://127.0.0.1:1" }}
	if _, err := c4.Info(context.Background(), "x"); err == nil {
		t.Error("no answer is no error")
	}
}

// a fake mDNS responder: answers the PTR query with the instance and an A record
func fakeResponder(t *testing.T, a [4]byte) string {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			var m dnsmessage.Message
			if m.Unpack(buf[:n]) != nil || len(m.Questions) != 1 || m.Questions[0].Name.String() != Service || m.Questions[0].Class&(1<<15) == 0 {
				continue
			}
			svc, _ := dnsmessage.NewName(Service)
			inst, _ := dnsmessage.NewName("HB-RF-ETH-ABCDEF1234." + Service)
			host, _ := dnsmessage.NewName("hb-rf-eth-abcdef1234.local.")
			ans := dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true},
				Answers:     []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: svc, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.PTRResource{PTR: inst}}},
				Additionals: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: host, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.AResource{A: a}}}}
			out, _ := ans.Pack()
			_, _ = pc.WriteTo(out, from)
			// noise: another service's answer is not a board
			other, _ := dnsmessage.NewName("_http._tcp.local.")
			noise := dnsmessage.Message{Header: dnsmessage.Header{Response: true}, Answers: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: other, Type: dnsmessage.TypePTR, Class: dnsmessage.ClassINET}, Body: &dnsmessage.PTRResource{PTR: inst}}}}
			out, _ = noise.Pack()
			_, _ = pc.WriteTo(out, from)
		}
	}()
	return pc.LocalAddr().String()
}

func TestBrowseAndFind(t *testing.T) {
	mdns := fakeResponder(t, [4]byte{192, 0, 2, 50})
	srv := fakeBoard(t, "0.0.0.0")
	c := &Client{MDNSAddr: mdns, BaseURL: func(string) string { return srv.URL }}
	found, err := c.Browse(context.Background(), 300*time.Millisecond)
	if err != nil || len(found) != 1 || found["192.0.2.50"] != "HB-RF-ETH-ABCDEF1234" {
		t.Fatalf("browse: %v %v", err, found)
	}
	// the configured board is read even when mDNS does not see it
	boards, err := c.Find(context.Background(), 300*time.Millisecond, "198.51.100.9", []string{"192.0.2.1"})
	if err != nil || len(boards) != 2 || boards[0].Address != "192.0.2.50" || boards[0].State != "free" || boards[0].Name != "HB-RF-ETH-ABCDEF1234" || boards[1].Address != "198.51.100.9" {
		t.Fatalf("find: %v %+v", err, boards)
	}
}
