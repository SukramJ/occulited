package system

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeResolvconf keeps openresolv's records under the test root, as `resolvconf -a/-d` does on
// the box, and records every call.
type fakeResolvconf struct {
	root  Root
	mu    sync.Mutex
	calls []string
}

func (f *fakeResolvconf) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	f.mu.Unlock()
	if name != "/sbin/resolvconf" {
		return nil, nil
	}
	dir := f.root.join(resolvconfInterfaces)
	var iface, record string
	del := false
	for i, a := range args {
		switch {
		case a == "-a" && i+1 < len(args):
			iface = args[i+1]
		case a == "-d" && i+1 < len(args):
			iface, del = args[i+1], true
		case strings.HasPrefix(a, "<"):
			record = strings.TrimPrefix(a, "<") + "\n"
		}
	}
	if del {
		_ = os.Remove(filepath.Join(dir, iface))
		return nil, nil
	}
	_ = os.MkdirAll(dir, 0o755)
	return nil, os.WriteFile(filepath.Join(dir, iface), []byte(record), 0o644)
}

func (f *fakeResolvconf) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := f.calls
	f.calls = nil
	return c
}

func (f *fakeResolvconf) record() string {
	return readFile(f.root.join(resolvconfInterfaces + "/" + DNSOverrideRecord))
}

// B-167: the Network page's DNS override under DHCP was taken, confirmed and dropped. It is a
// resolvconf record of its own now, exclusive over the lease's, durable in the state directory.
func TestDNSOverrideUnderDHCP(t *testing.T) {
	r := rootWith(t, map[string]string{
		"etc/config/netconfig":           netconfigDHCP,
		"var/run/udhcpc_eth0.pid":        "1233\n",
		"run/resolvconf/interfaces/eth0": "domain lan.example\nnameserver 192.0.2.1\nnameserver 192.0.2.225\n",
	})
	f := &fakeResolvconf{root: r}
	state := t.TempDir()
	tx := &NetTx{Root: r, Applier: NetApplier{Root: r, Iface: "eth0", Run: f.run}, Window: time.Minute, Override: DNSOverride{Path: filepath.Join(state, "dns-override")}}

	// netconfig's NAMESERVER1 (the template's 192.168.1.1) is no override
	settings, current := tx.Settings(r.ReadNetwork())
	if len(settings.DNS) != 0 || strings.Join(current.DNS, " ") != "192.0.2.1 192.0.2.225" {
		t.Fatalf("before: settings %v, current %v", settings.DNS, current.DNS)
	}

	// set: the record only - no DHCP restart, which would flush the address
	p, err := tx.Begin(context.Background(), NetworkSettings{Hostname: "openccu", Mode: "dhcp", DNS: []string{"192.0.2.53"}})
	if err != nil || p == nil {
		t.Fatalf("begin: %v %v", err, p)
	}
	if calls := strings.Join(f.take(), "\n"); calls != "/sbin/resolvconf -x -a lo.occulite <domain lan.example\nnameserver 192.0.2.53" {
		t.Fatalf("calls:\n%s", calls)
	}
	if len(p.Previous.DNS) != 0 {
		t.Errorf("the previous state's DNS is the (empty) override, not the lease's: %v", p.Previous.DNS)
	}
	if err := tx.Confirm(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if got := tx.Override.Servers(); strings.Join(got, " ") != "192.0.2.53" {
		t.Fatalf("stored: %v", got)
	}
	if nc := readFile(r.join("/etc/config/netconfig")); !strings.Contains(nc, "NAMESERVER1=192.168.1.1\n") {
		t.Errorf("netconfig's static servers must stay:\n%s", nc)
	}
	settings, current = tx.Settings(r.ReadNetwork())
	if strings.Join(settings.DNS, " ") != "192.0.2.53" || strings.Join(current.DNS, " ") != "192.0.2.53" {
		t.Fatalf("after: settings %v, current %v", settings.DNS, current.DNS)
	}

	// a change that is not confirmed goes back to the stored override
	p, err = tx.Begin(context.Background(), NetworkSettings{Hostname: "openccu", Mode: "dhcp", DNS: []string{"192.0.2.54", "192.0.2.55"}})
	if err != nil || p == nil {
		t.Fatalf("begin: %v", err)
	}
	if !strings.Contains(f.record(), "nameserver 192.0.2.55") {
		t.Fatalf("record %q", f.record())
	}
	f.take()
	if err := tx.Revert(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if rec := f.record(); rec != "domain lan.example\nnameserver 192.0.2.53\n" {
		t.Fatalf("reverted record %q", rec)
	}
	for _, c := range f.take() {
		if strings.Contains(c, "udhcpc") || strings.HasPrefix(c, "kill") {
			t.Errorf("the revert restarted DHCP: %s", c)
		}
	}

	// emptied: the record goes, the lease's servers are used again
	p, err = tx.Begin(context.Background(), NetworkSettings{Hostname: "openccu", Mode: "dhcp", DNS: []string{}})
	if err != nil || p == nil {
		t.Fatalf("begin: %v", err)
	}
	if calls := strings.Join(f.take(), "\n"); calls != "/sbin/resolvconf -f -d lo.occulite" || f.record() != "" {
		t.Fatalf("calls %q, record %q", calls, f.record())
	}
	if err := tx.Confirm(context.Background(), p.Token); err != nil {
		t.Fatal(err)
	}
	if tx.Override.Servers() != nil {
		t.Errorf("stored: %v", tx.Override.Servers())
	}
}

// After a boot /run is empty: the stored override's record is put back, follows the lease's
// domain, and goes when the system runs a static setup.
func TestKeepDNSOverride(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/netconfig": netconfigDHCP})
	f := &fakeResolvconf{root: r}
	state := t.TempDir()
	tx := &NetTx{Root: r, Applier: NetApplier{Root: r, Iface: "eth0", Run: f.run}, Override: DNSOverride{Path: filepath.Join(state, "dns-override")}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	// no override: nothing happens
	tx.keepDNSOverrideOnce(context.Background(), log)
	if c := f.take(); len(c) != 0 {
		t.Fatalf("no override, calls %v", c)
	}
	if err := tx.Override.Save([]string{"192.0.2.53"}); err != nil {
		t.Fatal(err)
	}
	// at start, before the lease: the servers alone
	tx.keepDNSOverrideOnce(context.Background(), log)
	if rec := f.record(); rec != "nameserver 192.0.2.53\n" {
		t.Fatalf("record %q", rec)
	}
	f.take()
	tx.keepDNSOverrideOnce(context.Background(), log)
	if c := f.take(); len(c) != 0 {
		t.Fatalf("an up-to-date record rewritten: %v", c)
	}
	// the lease arrives with a domain: the record follows it
	_ = os.MkdirAll(r.join(resolvconfInterfaces), 0o755)
	_ = os.WriteFile(r.join(resolvconfInterfaces+"/eth0"), []byte("domain lan.example\nsearch lan.example\nnameserver 192.0.2.1\n"), 0o644)
	tx.keepDNSOverrideOnce(context.Background(), log)
	if rec := f.record(); rec != "domain lan.example\nsearch lan.example\nnameserver 192.0.2.53\n" {
		t.Fatalf("record %q", rec)
	}
	// a static setup: the record would hide its servers
	_ = os.WriteFile(r.join("/etc/config/netconfig"), []byte(strings.Replace(netconfigDHCP, "MODE=DHCP", "MODE=MANUAL", 1)), 0o644)
	tx.keepDNSOverrideOnce(context.Background(), log)
	if f.record() != "" {
		t.Fatalf("record under static: %q", f.record())
	}
}

// A static apply hands its servers to resolvconf and removes a DHCP override's record.
func TestStaticApplyRemovesTheOverride(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/netconfig": netconfigDHCP, "run/resolvconf/interfaces/lo.occulite": "nameserver 192.0.2.53\n"})
	f := &fakeResolvconf{root: r}
	a := NetApplier{Root: r, Iface: "eth0", Run: f.run}
	if err := a.Apply(context.Background(), NetworkSettings{Hostname: "openccu", Mode: "static", Address: "192.0.2.200", Netmask: "255.255.255.0", DNS: []string{"192.0.2.1"}}); err != nil {
		t.Fatal(err)
	}
	calls := strings.Join(f.take(), "\n")
	if !strings.Contains(calls, "/sbin/resolvconf -a eth0 <nameserver 192.0.2.1") || !strings.Contains(calls, "/sbin/resolvconf -f -d lo.occulite") || f.record() != "" {
		t.Fatalf("calls:\n%s\nrecord %q", calls, f.record())
	}
}
