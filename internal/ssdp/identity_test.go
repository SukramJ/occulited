package ssdp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeVar(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadSerialOrder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"nothing: the hostname", nil, "ccu-vm-1"},
		{"the SGTIN first", map[string]string{"var/board_sgtin": "3014F711A000040000000A02\n", "var/board_serial": "0000000A02\n"}, "3014F711A000040000000A02"},
		{"the serial without an SGTIN (BidCos only)", map[string]string{"var/board_serial": "JEQ9000002\n"}, "JEQ9000002"},
		{"an empty SGTIN file is skipped", map[string]string{"var/board_sgtin": "\n", "var/board_serial": "JEQ9000002"}, "JEQ9000002"},
		{"the kernel module's parameter last", map[string]string{"sys/module/plat_eq3ccu2/parameters/board_serial": "NEQ1234567\n"}, "NEQ1234567"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for n, c := range tc.files {
				writeVar(t, root, n, c)
			}
			if got := ReadSerial(root, "ccu-vm-1"); got != tc.want {
				t.Errorf("ReadSerial = %q, want %q", got, tc.want)
			}
		})
	}
}

// B-199: occulited starts beside the radio detection; the files appear seconds later, and from
// then on the answers carry the SGTIN - not the host name read at start, for good.
func TestIdentityFollowsTheDetection(t *testing.T) {
	root := t.TempDir()
	id := &Identity{Root: root, Hostname: "ccu-vm-1"}
	if got := id.Serial(); got != "ccu-vm-1" {
		t.Fatalf("before the detection: %q, want the hostname", got)
	}
	d := Device{SerialFunc: id.Serial, Hostname: "ccu-vm-1"}
	if u := d.UDN(); u != "uuid:upnp-BasicDevice-1_0-ccu-vm-1" {
		t.Errorf("UDN before the detection %q", u)
	}

	writeVar(t, root, "var/board_serial", "0000000A02\n")
	writeVar(t, root, "var/board_sgtin", "3014F711A000040000000A02\n")
	if got := id.Serial(); got != "3014F711A000040000000A02" {
		t.Fatalf("after the detection: %q, want the SGTIN", got)
	}
	if u := d.UDN(); u != "uuid:upnp-BasicDevice-1_0-3014F711A000040000000A02" {
		t.Errorf("UDN after the detection %q", u)
	}
	x := d.DescriptionXML("http://192.0.2.116:80/")
	if !strings.Contains(x, "<serialNumber>3014F711A000040000000A02</serialNumber>") ||
		!strings.Contains(x, "<modelDescription>openccu-lite 3014F711A000040000000A02</modelDescription>") {
		t.Errorf("the description does not carry the SGTIN:\n%s", x)
	}
	if r := string(d.Reply("http://x/")); !strings.Contains(r, "USN: uuid:upnp-BasicDevice-1_0-3014F711A000040000000A02::upnp:rootdevice") {
		t.Errorf("the M-SEARCH reply does not carry the SGTIN:\n%s", r)
	}

	// once found, it stays: a later rewrite (a re-detection with another module) does not change
	// the identity under the clients while the daemon runs
	writeVar(t, root, "var/board_sgtin", "3014F711A0001F0000000A03\n")
	if got := id.Serial(); got != "3014F711A000040000000A02" {
		t.Errorf("the identity changed while running: %q", got)
	}
	if err := os.Remove(filepath.Join(root, "var/board_sgtin")); err != nil {
		t.Fatal(err)
	}
	if got := id.Serial(); got != "3014F711A000040000000A02" {
		t.Errorf("the identity fell back after the file went: %q", got)
	}
}

// The SSDP and the discovery responders ask the same Identity from their own goroutines (-race).
func TestIdentityConcurrent(t *testing.T) {
	root := t.TempDir()
	id := &Identity{Root: root, Hostname: "h"}
	if err := os.MkdirAll(filepath.Join(root, "var"), 0o755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i == 4 {
				// written whole, then renamed in, as the radio unit's writer does it
				tmp := filepath.Join(root, "var/.board_serial")
				if err := os.WriteFile(tmp, []byte("JEQ9000002"), 0o644); err != nil {
					t.Error(err)
				} else if err := os.Rename(tmp, filepath.Join(root, "var/board_serial")); err != nil {
					t.Error(err)
				}
			}
			for range 50 {
				if s := id.Serial(); s != "h" && s != "JEQ9000002" {
					t.Errorf("serial %q", s)
				}
			}
		}()
	}
	wg.Wait()
	if s := id.Serial(); s != "JEQ9000002" {
		t.Errorf("after all: %q", s)
	}
}

// A Device without SerialFunc keeps its fixed Serial, as every test and development root has it.
func TestDeviceFixedSerial(t *testing.T) {
	if got := (Device{Serial: "S"}).serial(); got != "S" {
		t.Errorf("%q", got)
	}
}
