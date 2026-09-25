package radio

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// B-89: the port from HMServer.conf, and the non-loopback listeners on it.
func TestHMServerPortOpen(t *testing.T) {
	root := t.TempDir()
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	// 39292 = 0x997C, 32010 = 0x7D0A
	loop6 := "   0: 0000000000000000FFFF00000100007F:997C 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  8113        0 1111 1\n"
	any6 := "   1: 00000000000000000000000000000000:997C 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  8113        0 2222 1\n"
	xmlrpc6 := "   2: 0000000000000000FFFF00000100007F:7D0A 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000  8113        0 3333 1\n"
	est4 := "   0: 0100007F:997C 0100007F:C350 01 00000000:00000000 00:00000000 00000000  8113        0 4444 1\n"
	any4 := "   1: 00000000:997C 00000000:0000 0A 00000000:00000000 00:00000000 00000000  8113        0 5555 1\n"

	if p, open := HMServerPortOpen(root); p != 39292 || open != nil {
		t.Fatalf("nothing there: %d %v", p, open)
	}
	// the shim took: only the IPv4-mapped loopback, which net.IP counts as the loopback
	write("/proc/net/tcp6", head+loop6+xmlrpc6)
	write("/proc/net/tcp", head+est4)
	if _, open := HMServerPortOpen(root); open != nil {
		t.Fatalf("on the loopback: %v", open)
	}
	// the shim did not load: the wildcard, on both families
	write("/proc/net/tcp6", head+any6+xmlrpc6)
	write("/proc/net/tcp", head+any4)
	if _, open := HMServerPortOpen(root); !reflect.DeepEqual(open, []string{"0.0.0.0", "::"}) {
		t.Fatalf("open: %v", open)
	}
	// another port in HMServer.conf
	write(HMServerConfPath, "hmServerPort=32010\nregaPort=31999\n")
	if p, open := HMServerPortOpen(root); p != 32010 || open != nil {
		t.Fatalf("32010: %d %v", p, open)
	}
}
