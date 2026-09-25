package system

import (
	"fmt"
	"testing"

	"github.com/hobbyquaker/occulited/internal/firewall"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// Rows taken from a real /proc/net: lighttpd on 80 (0.0.0.0), occulited on 8183 (127.0.0.1),
// mosquitto on 1883 (0.0.0.0), an established connection that is not a listener, and a udp
// socket. The addresses are host byte order per 4-byte word, which is what splitProcAddr undoes.
const procNetTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:1FF7 00000000:0000 0A 00000000:00000000 00:00000000 00000000 30000        0 12346 1 0000000000000000 100 0 0 10 0
   2: 00000000:075B 00000000:0000 0A 00000000:00000000 00:00000000 00000000 30004        0 12347 1 0000000000000000 100 0 0 10 0
   3: 7717AC0A:0050 0117AC0A:C1B4 01 00000000:00000000 00:00000000 00000000     0        0 12348 1 0000000000000000 20 4 30 10 -1
`

const procNetUDP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
   0: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 12349 2 0000000000000000 0
`

func TestParseProcNetFindsListenersOnly(t *testing.T) {
	got := parseProcNet(procNetTCP, "tcp")
	if len(got) != 3 {
		t.Fatalf("want the three LISTEN rows, got %d: %+v", len(got), got)
	}
	want := []struct {
		port     int
		addr     string
		loopback bool
	}{{80, "0.0.0.0", false}, {8183, "127.0.0.1", true}, {1883, "0.0.0.0", false}}
	for i, w := range want {
		if got[i].Port != w.port || got[i].Address != w.addr || got[i].Loopback != w.loopback {
			t.Errorf("row %d: %+v, want port %d addr %s loopback %v", i, got[i], w.port, w.addr, w.loopback)
		}
	}
	// a udp socket has no LISTEN state; every bound one counts
	if u := parseProcNet(procNetUDP, "udp"); len(u) != 1 || u[0].Port != 5353 {
		t.Errorf("udp: %+v", u)
	}
}

// task 157: each socket with its owner (by inode) and, per family it serves, the first rule for its
// port or the policy; a socket on :: serves both families
func TestListeningCoverage(t *testing.T) {
	const tcp6 = "  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 00000000000000000000000000000000:0016 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 12350 1 0000000000000000 100 0 0 10 0\n"
	r := rootWith(t, map[string]string{
		"proc/net/tcp":  procNetTCP,
		"proc/net/tcp6": tcp6,
		"proc/net/udp":  procNetUDP,
	})
	c := firewall.Config{Policy: firewall.Policy{V4: firewall.Drop, V6: firewall.Accept}, Rules: []firewall.Rule{
		{ID: "w80", Port: 80, Proto: "tcp", Source: firewall.LocalNetworks, Family: firewall.FamilyBoth, Target: firewall.Accept, Owner: firewall.OwnerWeb},
		{ID: "s22", Port: 22, Proto: "tcp", Source: "192.168.1.0/24", Family: firewall.FamilyV4, Target: firewall.Accept, Owner: firewall.OwnerSSH},
		{ID: "u53", Port: 5353, Proto: "tcp", Source: firewall.AnySource, Family: firewall.FamilyBoth, Target: firewall.Accept},
	}}
	owners := []priv.SocketOwner{{Inode: 12345, PID: 412, Comm: "lighttpd", Unit: "lighttpd.service"}, {Inode: 12350, PID: 88, Comm: "sshd", Unit: "sshd.service"}}
	all := r.ListeningPorts(c, owners)
	if len(all) != 5 {
		t.Fatalf("want five sockets, got %d: %+v", len(all), all)
	}
	by := map[string]Listener{}
	for _, l := range all {
		by[fmt.Sprintf("%s/%d", l.Proto, l.Port)] = l
	}
	if l := by["tcp/80"]; l.PID != 412 || l.Process != "lighttpd" || l.Unit != "lighttpd.service" || l.Cover["ipv4"] != (Coverage{Rule: "w80", Target: "ACCEPT", Source: firewall.LocalNetworks, Owner: firewall.OwnerWeb}) || len(l.Cover) != 1 {
		t.Errorf("80: %+v", l)
	}
	// sshd on :: - IPv4 by its rule, IPv6 by the policy (the rule is IPv4 only)
	if l := by["tcp6/22"]; l.Process != "sshd" || l.Cover["ipv4"].Rule != "s22" || l.Cover["ipv6"] != (Coverage{Target: "ACCEPT"}) {
		t.Errorf("22: %+v", l)
	}
	// mosquitto: no rule, the IPv4 policy; udp 5353: a tcp rule does not cover it
	if l := by["tcp/1883"]; l.Cover["ipv4"] != (Coverage{Target: "DROP"}) || l.PID != 0 {
		t.Errorf("1883: %+v", l)
	}
	if l := by["udp/5353"]; l.Cover["ipv4"].Rule != "" {
		t.Errorf("5353: %+v", l)
	}
	if !by["tcp/8183"].Loopback {
		t.Errorf("8183 should be loopback: %+v", by["tcp/8183"])
	}
}

// B-80: one port bound on several addresses, and two sockets on one address and port (seen on a
// Pi 3: udp 5353 twice). The page keyed the rows by protocol and port and threw; protocol, address
// and port are unique in the answer now.
func TestListeningPortsOneRowPerAddress(t *testing.T) {
	const tcpHead = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	const udpHead = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops\n"
	// 1880 on 0.0.0.0 and on 127.0.0.1; 5353 on 0.0.0.0 twice and on 224.0.0.251
	tcp := tcpHead +
		"   0: 00000000:0758 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0000000000000000 100 0 0 10 0\n" +
		"   1: 0100007F:0758 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0000000000000000 100 0 0 10 0\n"
	udp := udpHead +
		"   0: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 3 2 0000000000000000 0\n" +
		"   1: FB0000E0:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 4 2 0000000000000000 0\n" +
		"   2: 00000000:14E9 00000000:0000 07 00000000:00000000 00:00000000 00000000     0        0 5 2 0000000000000000 0\n"
	r := rootWith(t, map[string]string{
		"proc/net/tcp": tcp,
		"proc/net/udp": udp,
	})
	got := r.ListeningPorts(firewall.Default(), nil)
	want := []struct {
		proto, addr string
		port        int
	}{
		{"tcp", "0.0.0.0", 1880},
		{"tcp", "127.0.0.1", 1880},
		{"udp", "0.0.0.0", 5353},
		{"udp", "224.0.0.251", 5353},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Proto != w.proto || got[i].Address != w.addr || got[i].Port != w.port {
			t.Errorf("row %d: %+v, want %s %s:%d", i, got[i], w.proto, w.addr, w.port)
		}
	}
}
