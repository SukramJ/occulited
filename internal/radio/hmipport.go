package radio

import (
	"encoding/hex"
	"net"
	"strconv"
	"strings"
)

// B-89: hmipserver binds its HTTP port - HMServer.conf's hmServerPort, 39292: VirtualDevices, the
// group pages - to every interface and has no setting for a bind address. The image's LD_PRELOAD
// shim (libbindlo.so, set in hmipserver's unit) turns that bind into the loopback. A shim that
// ld.so cannot load is only logged there, and the port is open again: the ready step and the
// Status page's warning check the listening sockets.

// HMServerConfPath is hmipserver's ReGa-facing configuration, as the radio stack renders it.
const HMServerConfPath = "/var/etc/HMServer.conf"

// DefaultHMServerPort is the port where HMServer.conf does not name one.
const DefaultHMServerPort = 39292

// HMServerPort is hmServerPort from HMServer.conf, or the default.
func HMServerPort(root string) int {
	for _, line := range strings.Split(readFile(root+HMServerConfPath), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(k) == "hmServerPort" {
			if p, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && p > 0 && p < 65536 {
				return p
			}
		}
	}
	return DefaultHMServerPort
}

// HMServerPortOpen answers hmipserver's HTTP port and the local addresses other than the loopback
// it listens on; none when the shim holds it on the loopback or nothing listens on it.
func HMServerPortOpen(root string) (int, []string) {
	port := HMServerPort(root)
	var open []string
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		for _, a := range listeningOn(readFile(root+f), port) {
			if !a.IsLoopback() {
				open = append(open, a.String())
			}
		}
	}
	return port, open
}

// listeningOn parses /proc/net/tcp{,6}: the local addresses of the sockets in LISTEN (0A) on port.
func listeningOn(table string, port int) []net.IP {
	var out []net.IP
	for i, line := range strings.Split(table, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 4 || f[3] != "0A" {
			continue
		}
		addr, p, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		if n, err := strconv.ParseUint(p, 16, 16); err != nil || int(n) != port {
			continue
		}
		if ip := procIP(addr); ip != nil {
			out = append(out, ip)
		}
	}
	return out
}

// procIP decodes the kernel's hex address: 4 bytes (IPv4) or 16, each 32-bit word in host order,
// which on every product (x86_64, aarch64) is little-endian.
func procIP(h string) net.IP {
	b, err := hex.DecodeString(h)
	if err != nil || (len(b) != 4 && len(b) != 16) {
		return nil
	}
	for w := 0; w < len(b); w += 4 {
		b[w], b[w+1], b[w+2], b[w+3] = b[w+3], b[w+2], b[w+1], b[w]
	}
	return net.IP(b)
}
