package system

import (
	"bytes"
	"os"
	"strings"
)

// Container says whether this box is a container and of which kind - "lxc" for the Proxmox
// products (task 34), "oci"/"docker" for upstream's container image (openccu-lite has none, D-94) - or
// "" on a machine or a VM. Three sources, in this order:
//
//  1. /run/systemd/container: PID 1 writes it when it finds itself in a container (from the
//     container= variable in its own environment). World-readable, which is the point:
//     /proc/1/environ is 0400 root and the daemon runs as occulite.
//  2. /proc/1/environ, container=<kind>: readable when the daemon runs as root (development,
//     tests), the same variable PID 1 saw.
//  3. /VERSION: PLATFORM=lxc or PLATFORM=oci is what upstream's board scripts write and what
//     every "HM_HOST =~ oci|lxc" test in the init scripts keys on; D-31 keeps it on the lite
//     products, so the product says so even where the first two files are not there.
//
// What hangs on it: the network is the host's (the veth is configured by the container
// manager, so the Network page is read-only and writes /etc/config/netconfig for nobody), the
// clock is the host's (settimeofday is EPERM in an unprivileged container, there is no time
// daemon), and the rootfs is a template that is swapped on the host, so the D-31 update path
// - stage a file, arm the recovery, reboot - does not exist.
func (r Root) Container() string {
	if b, err := os.ReadFile(r.join("/run/systemd/container")); err == nil {
		if kind := strings.ToLower(strings.TrimSpace(string(b))); kind != "" {
			return kind
		}
	}
	if b, err := os.ReadFile(r.join("/proc/1/environ")); err == nil {
		for _, kv := range bytes.Split(b, []byte{0}) {
			if v, ok := strings.CutPrefix(string(kv), "container="); ok && strings.TrimSpace(v) != "" {
				return strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	switch v := r.ReadVersion(); strings.ToLower(v.Platform) {
	case "lxc", "oci":
		return strings.ToLower(v.Platform)
	}
	return ""
}

// HostManaged is the one question the API asks: does the host own the network, the clock and
// the rootfs of this box.
func (r Root) HostManaged() bool { return r.Container() != "" }
