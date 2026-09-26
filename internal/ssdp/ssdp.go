// Package ssdp is the system's SSDP announcement and its UPnP device description (task 165,
// D-108: "occulited replaces it").
//
// It takes over from OpenCCU's `ssdpd` (a C daemon that ran as the user `ssdp`) and from the Tcl
// CGI `/www/upnp/basic_dev.cgi` that served the description. Both leave the openccu-lite image:
// the CGI's path was already dead there (everything not /api or /addons is the shell, so the URL
// answered with the UI page instead of XML), and a daemon of its own for six lines of UDP is not
// worth a user, a unit and a restart policy.
//
// What it does, exactly as ssdpd did, because discovery tools depend on the shapes:
//
//   - joins 239.255.255.250:1900 and sends NOTIFY … ssdp:alive at start and every 30 minutes;
//   - answers an M-SEARCH whose MAN is "ssdp:discover" and whose ST is upnp:rootdevice, ssdp:all
//     or this device's own uuid, after a random delay of up to MX seconds (the spec's way of not
//     answering a broadcast all at once);
//   - USN uuid:upnp-BasicDevice-1_0-<serial>, the serial from /var/board_sgtin or
//     /var/board_serial, "for compatibility with existing discovery tools (e.g., eQ-3 NetFinder)";
//   - IPv4 only.
//
// What it does differently: ssdpd read the first IPv4 address once at its start and never again,
// and never said goodbye. Here the LOCATION and the multicast membership follow the address the
// system has now (the responder re-joins when it changes), and a ssdp:byebye goes out when the
// daemon stops.
//
// Who this is for: the Windows Explorer *Network* view and generic UPnP scanners. None of our own
// tools use it - homematic-manager, hm2mqtt.js and node-red-contrib-ccu discover over UDP 43439
// (task 163).
package ssdp

import (
	"bufio"
	"bytes"
	"fmt"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The group, the port and the address SSDP lives at.
const (
	Group = "239.255.255.250"
	Port  = 1900
)

// Addr is the multicast address as a string, for a Dial or a WriteTo.
const Addr = Group + ":" + "1900"

// MaxAge is the CACHE-CONTROL of every message, in seconds, as ssdpd sent it: a listener that
// hears nothing for that long may drop the system. Interval is how often ssdp:alive goes out, well
// inside it.
const (
	MaxAge   = 5000
	Interval = 30 * time.Minute
)

// MaxMX bounds the delay an M-SEARCH may ask for. The spec allows 1..5 and says a larger value is
// to be treated as 5; a request that asks for a minute must not hold an answer that long.
const MaxMX = 5

// DescriptionPath is where the description is served. The path is OpenCCU's, kept so that caches,
// NetFinder and anything that stored a LOCATION still find it.
const DescriptionPath = "/upnp/basic_dev.cgi"

// Device is the system as it announces itself.
type Device struct {
	// Serial is /var/board_sgtin, else /var/board_serial, else the hostname: whatever it is, it is
	// what the UDN is built from, so it must not change between a boot and the next.
	Serial string
	// SerialFunc, when set, is asked instead of Serial at every use: an Identity's Serial, which
	// follows the radio detection's files once they are written (B-199).
	SerialFunc func() string
	// Hostname is the system's own name, for the friendly name a scanner shows.
	Hostname string
	// Server is the SERVER header: "<OS>/<version> UPnP/1.0 <product>/<version>" by the spec.
	Server string
	// Presentation is the presentationURL for the root URL the description was read over
	// (PresentationFor with the system's names and its certificate); nil = PresentationURL(root).
	Presentation func(root string) string
	// Settled, when set, says whether the identity is final (an Identity's Settled): the
	// responder holds its first announcement a little while it is not (B-245). nil = final.
	Settled func() bool
}

// Product, Manufacturer and the URL are what a scanner shows beside the name. "the system" is the
// user-facing wording (task 160); these are the product's name, not a word for the machine.
const (
	Product      = "openccu-lite"
	Manufacturer = "openccu-lite"
	DeviceType   = "urn:schemas-upnp-org:device:Basic:1"
	// UPC is what OpenCCU's description carried; kept so nothing that parsed it trips.
	UPC = "123456789002"
)

// UDN is the device's unique name, uuid:upnp-BasicDevice-1_0-<serial>. The format is eQ-3's and is
// what NetFinder and the like recognise.
func (d Device) UDN() string { return "uuid:upnp-BasicDevice-1_0-" + d.serial() }

func (d Device) serial() string {
	if d.SerialFunc != nil {
		return d.SerialFunc()
	}
	return d.Serial
}

// FriendlyName is what the Windows *Network* view prints under the icon.
func (d Device) FriendlyName() string {
	if d.Hostname == "" {
		return Product
	}
	return Product + " - " + d.Hostname
}

func (d Device) server() string {
	if d.Server == "" {
		return "Linux UPnP/1.0 " + Product
	}
	return d.Server
}

// Search is what an M-SEARCH asked for.
type Search struct {
	// ST is the search target, verbatim.
	ST string
	// MX is the most seconds the sender is willing to wait, 0 when it named none or named
	// something that is not a number. Bounded by MaxMX.
	MX int
}

// ParseSearch reads a datagram as an M-SEARCH. ok is false for anything else: a NOTIFY, an answer
// somebody else sent, a request without MAN: "ssdp:discover", or bytes that are not HTTP at all.
// The parse is deliberately its own, not http.ReadRequest: the datagram is "M-SEARCH * HTTP/1.1",
// whose target is a bare "*", and nothing about it needs a body, a URL or a host.
func ParseSearch(b []byte) (Search, bool) {
	r := bufio.NewReader(bytes.NewReader(b))
	line, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(line)), "M-SEARCH ") {
		return Search{}, false
	}
	// a datagram that stops after the last header, without the empty line that ends them, is
	// answered by ReadMIMEHeader with an unexpected EOF and the headers it did read. Those are
	// what matters here, so only an empty result is a failure.
	head, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil && len(head) == 0 {
		return Search{}, false
	}
	// MAN is quoted in the spec and quoted by every implementation, but a stray space inside the
	// quotes is common enough to allow for
	if strings.Trim(strings.TrimSpace(head.Get("Man")), `" `) != "ssdp:discover" {
		return Search{}, false
	}
	s := Search{ST: strings.TrimSpace(head.Get("St"))}
	if mx, err := strconv.Atoi(strings.TrimSpace(head.Get("Mx"))); err == nil && mx > 0 {
		s.MX = min(mx, MaxMX)
	}
	return s, true
}

// Answers says whether this device is one of the things that ST asked for: every root device,
// everything at all, or this device by its own uuid. ssdpd answered exactly these three and
// nothing else, and a device that answers an ST it does not carry is a bug in the spec's terms.
func (d Device) Answers(st string) bool {
	switch strings.TrimSpace(st) {
	case "upnp:rootdevice", "ssdp:all":
		return true
	case d.UDN():
		return true
	}
	return false
}

// Reply is the unicast answer to an M-SEARCH, sent back to whoever asked. location is the URL of
// the description, built from the address the request came in on.
func (d Device) Reply(location string) []byte {
	return message("HTTP/1.1 200 OK", [][2]string{
		{"CACHE-CONTROL", "max-age=" + strconv.Itoa(MaxAge)},
		{"EXT", ""},
		{"LOCATION", location},
		{"SERVER", d.server()},
		{"ST", "upnp:rootdevice"},
		{"USN", d.UDN() + "::upnp:rootdevice"},
	})
}

// The two NTS values a NOTIFY carries.
const (
	Alive  = "ssdp:alive"
	Byebye = "ssdp:byebye"
)

// Notify is an announcement to the group: ssdp:alive at start and every Interval, ssdp:byebye when
// the daemon stops. A byebye carries neither LOCATION nor CACHE-CONTROL - there is nothing to
// fetch any more - which is what the spec says and what a listener expects.
func (d Device) Notify(location, nts string) []byte {
	return d.NotifyAs(d.UDN(), location, nts)
}

// NotifyAs is Notify under a given UDN: the byebye for an identity this device announced before
// it was final (B-245), so a listener drops that entry instead of keeping it beside the real one.
func (d Device) NotifyAs(udn, location, nts string) []byte {
	h := [][2]string{{"HOST", Addr}}
	if nts != Byebye {
		h = append(h, [2]string{"CACHE-CONTROL", "max-age=" + strconv.Itoa(MaxAge)}, [2]string{"LOCATION", location})
	}
	h = append(h,
		[2]string{"NT", "upnp:rootdevice"},
		[2]string{"NTS", nts},
		[2]string{"SERVER", d.server()},
		[2]string{"USN", udn + "::upnp:rootdevice"},
	)
	return message("NOTIFY * HTTP/1.1", h)
}

// message writes a start line, the headers and the empty line that ends them, CRLF throughout.
func message(start string, headers [][2]string) []byte {
	var b strings.Builder
	b.WriteString(start)
	b.WriteString("\r\n")
	for _, h := range headers {
		b.WriteString(h[0])
		b.WriteString(": ")
		b.WriteString(h[1])
		b.WriteString("\r\n")
	}
	b.WriteString("\r\n")
	return []byte(b.String())
}

// Location is the URL of the description for a request that arrived on ip, or for an announcement
// sent from it. Plain HTTP and no port: that is where SSDP's LOCATION has always pointed, and the
// description is public, read-only XML.
func Location(ip string) string {
	return "http://" + hostPart(ip) + DescriptionPath
}

// hostPart brackets an IPv6 address for a URL and leaves an IPv4 one alone. IPv6 is not announced
// today, but a LOCATION built from an address must not be malformed if it ever is.
func hostPart(ip string) string {
	if strings.Contains(ip, ":") {
		return "[" + ip + "]"
	}
	return ip
}

// DescriptionXML is the UPnP Basic:1 device description, the answer at DescriptionPath. No
// serviceList: an empty one made control points ask for a SCPD that does not exist, which is why
// OpenCCU's CGI left it out, and there is no service here either. Every text node is escaped.
//
// root is the URL the request came in on ("https://ccu.example" or "http://192.168.1.5"), so that
// URLBase and presentationURL name the address the reader can actually reach.
func (d Device) DescriptionXML(root string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\r\n")
	b.WriteString(`<root xmlns="urn:schemas-upnp-org:device-1-0">` + "\r\n")
	b.WriteString("\t<specVersion>\r\n\t\t<major>1</major>\r\n\t\t<minor>0</minor>\r\n\t</specVersion>\r\n")
	el := func(indent, name, text string) {
		b.WriteString(indent)
		b.WriteString("<" + name + ">")
		b.WriteString(escapeXML(text))
		b.WriteString("</" + name + ">\r\n")
	}
	el("\t", "URLBase", root)
	b.WriteString("\t<device>\r\n")
	el("\t\t", "deviceType", DeviceType)
	el("\t\t", "presentationURL", d.presentation(root))
	el("\t\t", "friendlyName", d.FriendlyName())
	el("\t\t", "manufacturer", Manufacturer)
	serial := d.serial()
	el("\t\t", "modelDescription", Product+" "+serial)
	el("\t\t", "modelName", Product)
	el("\t\t", "serialNumber", serial)
	el("\t\t", "UDN", d.UDN())
	el("\t\t", "UPC", UPC)
	b.WriteString("\t</device>\r\n</root>\r\n")
	return b.String()
}

func (d Device) presentation(root string) string {
	if d.Presentation != nil {
		return d.Presentation(root)
	}
	return PresentationURL(root)
}

// PresentationFor is the page a double click opens, named so that the browser gets no certificate
// warning (task 165, found 2026-09-23: Windows' network view opened https://<IP>/, which the
// certificate does not name, and failed). The first name the certificate covers wins - the host the
// description was read over, then each of names (the system's <host>.<domain>, then its host name);
// with none covered it is the address the description was read over.
//
// The scheme is plain http:// in every case, the shape every CCU's description has had and the one
// Windows' network view is known to open in a browser (B-245: with https://<name>/ Windows
// downloaded the page into its IE cache and opened the file instead). lighttpd redirects http://
// to https:// under the same host, so a covered name still ends on the web UI without a warning,
// one hop later.
func PresentationFor(root string, names []string, covered func(host string) bool) string {
	host := hostOf(root)
	if covered != nil {
		for _, n := range append([]string{host}, names...) {
			if n = strings.ToLower(strings.TrimSpace(n)); n != "" && covered(n) {
				return "http://" + n + "/"
			}
		}
	}
	return "http://" + host + "/"
}

// hostOf is the host of a root URL, without the scheme and the port.
func hostOf(root string) string {
	host := root
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	// an IPv6 address is bracketed, so the last colon outside the brackets is the port
	if i := strings.LastIndex(host, ":"); i > strings.LastIndex(host, "]") {
		host = host[:i]
	}
	return host
}

// PresentationURL is the page a scanner opens on a double click when the device has no
// Presentation of its own: the web UI at the host the description was read over, plain http://
// (see PresentationFor; lighttpd redirects on). A port in the address is dropped: it belongs to
// the description's own URL, not to the UI's.
func PresentationURL(root string) string {
	return "http://" + hostOf(root) + "/"
}

// escapeXML escapes a text node. The serial and the hostname come off the system and are not
// chosen here, so nothing is assumed about them.
func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

// Describe answers DescriptionPath: the XML above, to anyone who asks, with no session. It is the
// same information the announcement already carries over the air.
func (d Device) Describe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body := d.DescriptionXML(RootURL(r))
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write([]byte(body))
}

// RootURL is the scheme and authority the request came in on - what the reader typed or what the
// LOCATION sent them to - so the URLs in the description lead back the same way. lighttpd is what
// faces the LAN and sets X-Forwarded-Proto (proxy.forwarded in deploy/lighttpd/occulited.conf);
// without it the request came over plain HTTP.
func RootURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.ToLower(strings.TrimSpace(strings.Split(p, ",")[0]))
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if host == "" {
		host = "127.0.0.1"
	}
	return scheme + "://" + host
}

func init() {
	// the constant above is written out; keep the two in step if the port is ever moved
	if Addr != fmt.Sprintf("%s:%d", Group, Port) {
		panic("ssdp: Addr and Port disagree")
	}
}

// ReadSerial is the identity the UDN is built from, the same three files ssdpd and the CGI read in
// the same order: /var/board_sgtin, else /var/board_serial, else the kernel module's parameter. A
// system with none of them - a container, a development box - falls back to its hostname, as the
// CGI did, so the UDN is still stable for that system.
func ReadSerial(root, hostname string) string {
	if s, ok := readSerialFile(root); ok {
		return s
	}
	return hostname
}

func readSerialFile(root string) (string, bool) {
	for _, p := range []string{"/var/board_sgtin", "/var/board_serial", "/sys/module/plat_eq3ccu2/parameters/board_serial"} {
		if b, err := os.ReadFile(filepath.Join(root, p)); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s, true
			}
		}
	}
	return "", false
}

// Identity is ReadSerial for a daemon that starts before the radio detection has written
// /var/board_sgtin and /var/board_serial - occulited and occu-init-rf-hardware start side by side,
// and the detection takes seconds (B-199: a system booted with an HB-RF-ETH answered the eQ-3
// discovery and SSDP with its host name, for good, because the serial was read once at start).
// Until one of the files has a value, every call reads them again and answers the hostname in the
// meantime; the first value found is kept, so the identity does not change again while the
// daemon runs. Safe for concurrent use: the SSDP and the discovery responders share one.
type Identity struct {
	Root     string // "/" on a system
	Hostname string // the fallback while no file has a value

	mu    sync.Mutex
	found string
}

// Settled says whether the serial has been read from a file: the identity does not change again
// once it has. Before, Serial answers the hostname.
func (i *Identity) Settled() bool {
	return i.Serial() != i.Hostname || i.hasFile()
}

func (i *Identity) hasFile() bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.found != ""
}

// Serial is the system's serial now: the files' value once there is one, the hostname before.
func (i *Identity) Serial() string {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.found != "" {
		return i.found
	}
	if s, ok := readSerialFile(i.Root); ok {
		i.found = s
		return s
	}
	return i.Hostname
}
