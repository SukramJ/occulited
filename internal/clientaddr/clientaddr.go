// Package clientaddr says which address a request came from.
//
// lighttpd faces the LAN and proxies to occulited on the loopback (D-29). Its mod_proxy appends
// the connecting client's address to X-Forwarded-For (proxy.forwarded "for"); it does not replace
// a header the client sent. So of that header only the last element is lighttpd's: everything
// before it is the client's own word. And the header means anything only on a connection from
// the loopback, i.e. from lighttpd; anyone else is the address of the connection (B-230).
package clientaddr

import (
	"net"
	"net/http"
	"strings"
)

// Of is the address the request came from: behind lighttpd the last element of X-Forwarded-For,
// else the connection's own address.
func Of(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return host
	}
	vs := r.Header.Values("X-Forwarded-For")
	if len(vs) == 0 {
		return host
	}
	last := vs[len(vs)-1]
	if i := strings.LastIndexByte(last, ','); i >= 0 {
		last = last[i+1:]
	}
	// an element lighttpd did not write (empty, not an address) is never the loopback's: it
	// stays what it is, which matches no range and passes no loopback check
	return strings.TrimSpace(last)
}
