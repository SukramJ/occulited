package ssdp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var dev = Device{Serial: "3014F711A0001F0000000A03", Hostname: "ccu-pi3-1", Server: "Linux/6.6 UPnP/1.0 openccu-lite/1.0.0"}

func search(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n") + "\r\n\r\n") }

func TestParseSearch(t *testing.T) {
	cases := []struct {
		name string
		in   []byte
		ok   bool
		st   string
		mx   int
	}{
		{"a plain M-SEARCH", search("M-SEARCH * HTTP/1.1", "HOST: 239.255.255.250:1900", `MAN: "ssdp:discover"`, "MX: 3", "ST: upnp:rootdevice"), true, "upnp:rootdevice", 3},
		{"the headers in any case and order", search("m-search * HTTP/1.1", "st: ssdp:all", `man: "ssdp:discover"`, "mx: 1", "host: 239.255.255.250:1900"), true, "ssdp:all", 1},
		{"MAN with a space inside the quotes", search("M-SEARCH * HTTP/1.1", `MAN: " ssdp:discover "`, "ST: ssdp:all"), true, "ssdp:all", 0},
		{"no MX: the answer goes at once", search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: ssdp:all"), true, "ssdp:all", 0},
		{"an MX beyond the spec is capped", search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: ssdp:all", "MX: 120"), true, "ssdp:all", MaxMX},
		{"a negative MX is none", search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: ssdp:all", "MX: -4"), true, "ssdp:all", 0},
		{"an MX that is not a number is none", search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:discover"`, "ST: ssdp:all", "MX: soon"), true, "ssdp:all", 0},
		{"without the last empty line", []byte("M-SEARCH * HTTP/1.1\r\nMAN: \"ssdp:discover\"\r\nST: ssdp:all\r\n"), true, "ssdp:all", 0},
		// not a search
		{"without MAN", search("M-SEARCH * HTTP/1.1", "ST: ssdp:all"), false, "", 0},
		{"another MAN", search("M-SEARCH * HTTP/1.1", `MAN: "ssdp:something"`, "ST: ssdp:all"), false, "", 0},
		{"a NOTIFY of somebody else", search("NOTIFY * HTTP/1.1", "NTS: ssdp:alive", "NT: upnp:rootdevice"), false, "", 0},
		{"an answer of somebody else", search("HTTP/1.1 200 OK", "ST: upnp:rootdevice"), false, "", 0},
		{"rubbish", []byte{0x00, 0x01, 0x02}, false, "", 0},
		{"empty", nil, false, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseSearch(c.in)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if ok && (got.ST != c.st || got.MX != c.mx) {
				t.Errorf("got %+v, want ST %q MX %d", got, c.st, c.mx)
			}
		})
	}
}

func TestAnswers(t *testing.T) {
	yes := []string{"upnp:rootdevice", "ssdp:all", dev.UDN(), " ssdp:all "}
	for _, st := range yes {
		if !dev.Answers(st) {
			t.Errorf("ST %q must be answered", st)
		}
	}
	no := []string{"", "urn:schemas-upnp-org:device:InternetGatewayDevice:1", "uuid:upnp-BasicDevice-1_0-SOMEONEELSE", "upnp:rootdevices"}
	for _, st := range no {
		if dev.Answers(st) {
			t.Errorf("ST %q must not be answered", st)
		}
	}
}

func TestReplyAndNotify(t *testing.T) {
	const loc = "http://192.168.1.5/upnp/basic_dev.cgi"
	reply := string(dev.Reply(loc))
	for _, want := range []string{
		"HTTP/1.1 200 OK\r\n",
		"CACHE-CONTROL: max-age=5000\r\n",
		"EXT: \r\n",
		"LOCATION: " + loc + "\r\n",
		"SERVER: Linux/6.6 UPnP/1.0 openccu-lite/1.0.0\r\n",
		"ST: upnp:rootdevice\r\n",
		"USN: uuid:upnp-BasicDevice-1_0-3014F711A0001F0000000A03::upnp:rootdevice\r\n",
	} {
		if !strings.Contains(reply, want) {
			t.Errorf("the answer misses %q:\n%s", want, reply)
		}
	}
	if !strings.HasSuffix(reply, "\r\n\r\n") {
		t.Errorf("the answer must end with an empty line:\n%q", reply)
	}

	alive := string(dev.Notify(loc, Alive))
	for _, want := range []string{"NOTIFY * HTTP/1.1\r\n", "HOST: 239.255.255.250:1900\r\n", "NTS: ssdp:alive\r\n", "NT: upnp:rootdevice\r\n", "LOCATION: " + loc + "\r\n", "CACHE-CONTROL: max-age=5000\r\n"} {
		if !strings.Contains(alive, want) {
			t.Errorf("the alive misses %q:\n%s", want, alive)
		}
	}
	// a goodbye has nothing to fetch, so it carries neither a location nor a lifetime
	bye := string(dev.Notify(loc, Byebye))
	if strings.Contains(bye, "LOCATION") || strings.Contains(bye, "CACHE-CONTROL") {
		t.Errorf("the goodbye must carry neither:\n%s", bye)
	}
	if !strings.Contains(bye, "NTS: ssdp:byebye\r\n") || !strings.Contains(bye, "USN: "+dev.UDN()+"::upnp:rootdevice\r\n") {
		t.Errorf("the goodbye is wrong:\n%s", bye)
	}
}

func TestUDNAndFriendlyName(t *testing.T) {
	if dev.UDN() != "uuid:upnp-BasicDevice-1_0-3014F711A0001F0000000A03" {
		t.Errorf("UDN = %q", dev.UDN())
	}
	if dev.FriendlyName() != "openccu-lite - ccu-pi3-1" {
		t.Errorf("friendly name = %q", dev.FriendlyName())
	}
	if (Device{Serial: "x"}).FriendlyName() != "openccu-lite" {
		t.Error("without a hostname the name is the product alone")
	}
}

func TestDescriptionXML(t *testing.T) {
	x := dev.DescriptionXML("https://ccu.example")
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<root xmlns="urn:schemas-upnp-org:device-1-0">`,
		"<URLBase>https://ccu.example</URLBase>",
		"<deviceType>urn:schemas-upnp-org:device:Basic:1</deviceType>",
		"<presentationURL>http://ccu.example/</presentationURL>",
		"<friendlyName>openccu-lite - ccu-pi3-1</friendlyName>",
		"<manufacturer>openccu-lite</manufacturer>",
		"<modelName>openccu-lite</modelName>",
		"<serialNumber>3014F711A0001F0000000A03</serialNumber>",
		"<UDN>uuid:upnp-BasicDevice-1_0-3014F711A0001F0000000A03</UDN>",
	} {
		if !strings.Contains(x, want) {
			t.Errorf("the description misses %q:\n%s", want, x)
		}
	}
	// no serviceList: an empty one made control points ask for a SCPD that does not exist
	if strings.Contains(x, "serviceList") || strings.Contains(x, "SCPD") {
		t.Errorf("no services are declared:\n%s", x)
	}
	// every text node escaped, whatever the system's own files hold
	odd := Device{Serial: `A&B<C>`, Hostname: `"quoted"`}
	x = odd.DescriptionXML("http://1.2.3.4")
	for _, bad := range []string{"A&B", "<C>", `"quoted"`} {
		if strings.Contains(x, bad) {
			t.Errorf("%q is not escaped:\n%s", bad, x)
		}
	}
	if !strings.Contains(x, "A&amp;B&lt;C&gt;") || !strings.Contains(x, "&quot;quoted&quot;") {
		t.Errorf("the escaping is wrong:\n%s", x)
	}
}

func TestDescribe(t *testing.T) {
	// the route lighttpd hands through: no session, GET and HEAD only
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, DescriptionPath, nil)
	r.Host = "ccu.example"
	r.Header.Set("X-Forwarded-Proto", "https")
	dev.Describe(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != `text/xml; charset="utf-8"` {
		t.Errorf("content type %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "<URLBase>https://ccu.example</URLBase>") {
		t.Errorf("the URLs follow the request:\n%s", rec.Body.String())
	}
	if rec.Header().Get("Content-Length") == "" {
		t.Error("a control point wants the length")
	}

	// without lighttpd's header the request came over plain HTTP
	rec = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, DescriptionPath, nil)
	r.Host = "192.168.1.5"
	dev.Describe(rec, r)
	body := rec.Body.String()
	if !strings.Contains(body, "<URLBase>http://192.168.1.5</URLBase>") {
		t.Errorf("plain HTTP:\n%s", body)
	}
	// the page a double click opens is the web UI over HTTPS, whatever this was fetched over
	if !strings.Contains(body, "<presentationURL>http://192.168.1.5/</presentationURL>") {
		t.Errorf("the presentation URL is the HTTPS one:\n%s", body)
	}

	// HEAD: the headers, no body
	rec = httptest.NewRecorder()
	dev.Describe(rec, httptest.NewRequest(http.MethodHead, DescriptionPath, nil))
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d, %d bytes", rec.Code, rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	dev.Describe(rec, httptest.NewRequest(http.MethodPost, DescriptionPath, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}

func TestLocation(t *testing.T) {
	if got := Location("192.168.1.5"); got != "http://192.168.1.5/upnp/basic_dev.cgi" {
		t.Errorf("location = %q", got)
	}
	if got := Location("fd00::1"); got != "http://[fd00::1]/upnp/basic_dev.cgi" {
		t.Errorf("an IPv6 address is bracketed: %q", got)
	}
}

func TestPresentationURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://ccu.example":     "http://ccu.example/",
		"http://192.168.1.5":      "http://192.168.1.5/",
		"http://192.168.1.5:8080": "http://192.168.1.5/",
		"http://[fd00::1]":        "http://[fd00::1]/",
		"http://[fd00::1]:8080":   "http://[fd00::1]/",
	} {
		if got := PresentationURL(in); got != want {
			t.Errorf("PresentationURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// Task 165, found 2026-09-23: Windows' network view opened https://<IP>/, which the certificate
// does not name. The presentationURL is the first name the certificate covers, else the address.
// B-245: always http:// - the CCU's shape, which Windows opens in a browser; with https:// it
// downloaded the page into its IE cache - and lighttpd's redirect takes the browser to HTTPS
// to the address.
func TestPresentationFor(t *testing.T) {
	cert := map[string]bool{"ccu-vm-1": true, "ccu-vm-1.lan.example": true}
	covered := func(h string) bool { return cert[h] }
	names := []string{"ccu-vm-1.lan.example", "ccu-vm-1"}
	for _, tc := range []struct {
		name, root string
		names      []string
		covered    func(string) bool
		want       string
	}{
		{"the FQDN the certificate covers", "http://192.0.2.119", names, covered, "http://ccu-vm-1.lan.example/"},
		{"the port dropped", "http://192.0.2.119:8080", names, covered, "http://ccu-vm-1.lan.example/"},
		{"the host name when the FQDN is not covered", "http://192.0.2.119", []string{"ccu-vm-1.other.example", "ccu-vm-1"}, covered, "http://ccu-vm-1/"},
		{"no domain known", "http://192.0.2.119", []string{"", "ccu-vm-1"}, covered, "http://ccu-vm-1/"},
		{"read over a covered name: that name", "https://CCU-VM-1", names, covered, "http://ccu-vm-1/"},
		{"nothing covered: plain http to the address", "http://192.0.2.119", []string{"x.example", "x"}, covered, "http://192.0.2.119/"},
		{"no certificate", "http://192.0.2.119", names, nil, "http://192.0.2.119/"},
		{"IPv6", "http://[fd00::1]:80", nil, covered, "http://[fd00::1]/"},
	} {
		if got := PresentationFor(tc.root, tc.names, tc.covered); got != tc.want {
			t.Errorf("%s: PresentationFor(%q, %q) = %q, want %q", tc.name, tc.root, tc.names, got, tc.want)
		}
	}
	d := Device{Serial: "S", Hostname: "h", Presentation: func(string) string { return "https://h.example/" }}
	if x := d.DescriptionXML("http://10.0.0.1"); !strings.Contains(x, "<presentationURL>https://h.example/</presentationURL>") {
		t.Errorf("the device's Presentation is not used:\n%s", x)
	}
}
