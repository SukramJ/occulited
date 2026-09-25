package system

import (
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/rpcsub"
)

// ---- who is subscribed to an interface process -------------------------------------------------
//
// rfd, hs485d and the HmIP server each keep the XML-RPC callbacks that have registered with them in
// a file under /var, rewritten whenever a client registers or deregisters. /var is a tmpfs, so
// these are runtime state: they do not survive a reboot, and a file is absent while its daemon is
// not running (on a box with no HM-Wired there is no HS485D.handlers at all).
//
// Two formats, because two of the daemons are C++ and one is Java:
//
//	/var/RFD.handlers, /var/HS485D.handlers, /var/HMSERVER.handlers
//	    <callback url><whitespace><id>
//	    http://127.0.0.1:39292/bidcos	BidCos-RF_java
//
//	/var/LegacyService.handlers   — a Java properties file, so it is id=url, the colons are
//	                                escaped, and it carries a "#" comment header
//	    HmIP-RF_java=http\://127.0.0.1\:39292/bidcos
//
// The id is the client's own name and is the useful half: "nr_<something>_BidCos-RF" is
// node-red-contrib-ccu, "BidCos-RF_java" is the box's own Java server, a bare number is ReGa's
// (a process id), and anything else is whatever registered itself.

// InterfaceSubscriber is one registered XML-RPC callback.
type InterfaceSubscriber struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// Local is true when the callback points at this box; a remote subscriber is a client
	// somewhere else on the network and is the interesting case for D-29.
	Local bool `json:"local"`
	// Duplicate is true when the same id is registered with another callback too (task 76): a
	// client's old registration beside its new one. Which of the two is the stale one cannot be
	// told from the file, so both carry it; it is a hint for the page, nothing acts on it.
	Duplicate bool `json:"duplicate,omitempty"`
	// Own is true for the RPC process's registration (task 75, D-80): the system's own subscriber,
	// which the page marks and offers no removal for - it registers itself again anyway.
	Own bool `json:"own,omitempty"`
}

// handlerFiles maps the interface name in InterfacesList.xml to the file its daemon writes.
// Verified against a CCU3 running all four: RFD is BidCos-RF, HS485D is the wired bus,
// LegacyService is the HmIP server's legacy (BidCos-compatible) face, and HMSERVER is the Java
// server that carries VirtualDevices.
var handlerFiles = map[string]string{
	"BidCos-RF":      "/var/RFD.handlers",
	"BidCos-Wired":   "/var/HS485D.handlers",
	"HM-Wired":       "/var/HS485D.handlers",
	"HmIP-RF":        "/var/LegacyService.handlers",
	"VirtualDevices": "/var/HMSERVER.handlers",
}

// InterfaceURL is the URL InterfacesList.xml gives the interface of that name, "" when it has none.
func (r Root) InterfaceURL(name string) string {
	for _, i := range parseInterfacesList(readFile(r.join("/etc/config/InterfacesList.xml"))) {
		if i.Name == name {
			return i.URL
		}
	}
	return ""
}

// InterfaceSubscribers reads the registered callbacks for one interface name. An unknown name, a
// missing file or an empty one all give an empty list rather than an error: not running is a
// normal state here, not a fault.
func (r Root) InterfaceSubscribers(name string) []InterfaceSubscriber {
	path, ok := handlerFiles[name]
	if !ok {
		return []InterfaceSubscriber{}
	}
	body := readFile(r.join(path))
	if strings.TrimSpace(body) == "" {
		return []InterfaceSubscriber{}
	}
	if strings.HasSuffix(path, "LegacyService.handlers") {
		return parseJavaHandlers(body)
	}
	return parsePlainHandlers(body)
}

// parsePlainHandlers reads "<url><whitespace><id>" lines.
func parsePlainHandlers(body string) []InterfaceSubscriber {
	out := []InterfaceSubscriber{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		out = append(out, newSubscriber(strings.Join(f[1:], " "), f[0]))
	}
	return sorted(out)
}

// parseJavaHandlers reads a Java properties file: id=url, with the colons backslash-escaped and a
// "#" comment header. Only the escapes java.util.Properties actually writes here are undone.
func parseJavaHandlers(body string) []InterfaceSubscriber {
	out := []InterfaceSubscriber{}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		id, url, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		unescape := strings.NewReplacer(`\:`, ":", `\=`, "=", `\\`, `\`)
		out = append(out, newSubscriber(unescape.Replace(strings.TrimSpace(id)), unescape.Replace(strings.TrimSpace(url))))
	}
	return sorted(out)
}

func newSubscriber(id, url string) InterfaceSubscriber {
	return InterfaceSubscriber{ID: id, URL: url, Local: isLocalCallback(url), Own: strings.HasPrefix(id, rpcsub.IDPrefix)}
}

// isLocalCallback says whether a callback points back at this box. Only the host part is looked
// at, and only the loopback literals: a callback naming the box's own LAN address is still a
// client that reaches it over the network.
func isLocalCallback(url string) bool {
	_, rest, ok := strings.Cut(url, "://")
	if !ok {
		return false
	}
	host, _, _ := strings.Cut(rest, "/")
	if strings.HasPrefix(host, "[") { // [v6]:port
		host = strings.TrimPrefix(host, "[")
		host, _, _ = strings.Cut(host, "]")
	} else {
		host, _, _ = strings.Cut(host, ":")
	}
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return true
	}
	// 28.6: a callback registered at the box's own LAN address (an addon that bound 0.0.0.0 and
	// announced the external address) is on this box too, not "on the network"
	return ownAddresses()[host]
}

var ownAddrCache struct {
	sync.Mutex
	at  time.Time
	set map[string]bool
}

// ownAddresses is the set of this box's interface addresses, refreshed at most every 30 s.
func ownAddresses() map[string]bool {
	ownAddrCache.Lock()
	defer ownAddrCache.Unlock()
	if ownAddrCache.set != nil && time.Since(ownAddrCache.at) < 30*time.Second {
		return ownAddrCache.set
	}
	set := map[string]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				set[ipn.IP.String()] = true
			}
		}
	}
	ownAddrCache.set, ownAddrCache.at = set, time.Now()
	return set
}

// sorted orders the subscribers by id, then by callback.
//
// An interface process keeps a client's old registration beside its new one: rfd listed
// hmm_VirtualDevices twice after Homematic Manager restarted. Both lines stay when their callbacks
// differ - the daemon calls both - and a line that is there twice with the same callback is one
// subscriber, so id and callback together are unique in what is returned (the page keys its rows
// by them).
func sorted(in []InterfaceSubscriber) []InterfaceSubscriber {
	sort.Slice(in, func(a, b int) bool {
		if in[a].ID != in[b].ID {
			return in[a].ID < in[b].ID
		}
		return in[a].URL < in[b].URL
	})
	out := in[:0]
	for _, s := range in {
		if n := len(out); n > 0 && out[n-1].ID == s.ID && out[n-1].URL == s.URL {
			continue
		}
		out = append(out, s)
	}
	for n := range out {
		out[n].Duplicate = (n > 0 && out[n-1].ID == out[n].ID) || (n+1 < len(out) && out[n+1].ID == out[n].ID)
	}
	return out
}

// HasSubscriber says whether the pair is registered with the interface right now.
func (r Root) HasSubscriber(name, id, url string) bool {
	for _, s := range r.InterfaceSubscribers(name) {
		if s.ID == id && s.URL == url {
			return true
		}
	}
	return false
}
