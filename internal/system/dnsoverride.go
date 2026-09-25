package system

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
)

// The DNS override of a DHCP system (B-167). The Network page offers it under DHCP as "what is
// entered here replaces the servers DHCP gives", and the resolver there is openresolv's:
// /bin/dhcp.script hands the lease to `resolvconf -a eth0`, which writes /etc/resolv.conf. An
// override is a record of its own, marked exclusive (`resolvconf -x -a lo.occulite`), so the
// lease's servers are not used while it is there and a renewal - which rewrites eth0's record
// only - does not undo it. The record carries the lease's domain and search lines, which the
// box's name (FQDN, ACME) is taken from.
//
// Durable in <state>/dns-override, not in netconfig: netconfig's NAMESERVER1/2 are the static
// setup's, and on a DHCP system they hold whatever the factory or an old static setup left there
// (the template's 192.168.1.1) - reading them as an override would break name resolution on every
// converted system. /run is empty at boot, so occulited puts the record back at start and keeps
// it with the lease (KeepDNSOverride).

// DNSOverrideRecord is the resolvconf interface name of the override's record.
const DNSOverrideRecord = "lo.occulite"

// resolvconfInterfaces is where openresolv keeps the records it was handed.
const resolvconfInterfaces = "/run/resolvconf/interfaces"

// DNSOverride is the stored override: one server per line in its file.
type DNSOverride struct {
	Path string // <state>/dns-override; empty = no override support (tests, development)
}

// Servers is the stored override, nil when there is none.
func (o DNSOverride) Servers() []string {
	if o.Path == "" {
		return nil
	}
	b, err := os.ReadFile(o.Path)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Fields(string(b)) {
		if ipv4(l) != nil && len(out) < 2 {
			out = append(out, l)
		}
	}
	return out
}

// Save stores dns as the override; an empty list removes it.
func (o DNSOverride) Save(dns []string) error {
	if o.Path == "" {
		return nil
	}
	if len(dns) == 0 {
		if err := os.Remove(o.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(o.Path, []byte(strings.Join(dns, "\n")+"\n"), 0o644)
}

// overrideRecord is the record's text: the lease's domain and search lines (from iface's record),
// then the override's servers.
func (a NetApplier) overrideRecord(dns []string) string {
	lease := parseResolv(readFile(a.Root.join(resolvconfInterfaces + "/" + a.iface())))
	var b strings.Builder
	if lease.Domain != "" {
		b.WriteString("domain " + lease.Domain + "\n")
	}
	if len(lease.Search) > 0 {
		b.WriteString("search " + strings.Join(lease.Search, " ") + "\n")
	}
	for _, d := range dns {
		b.WriteString("nameserver " + d + "\n")
	}
	return b.String()
}

// setDNSOverride puts the override's record in place (dns non-empty) or takes it away.
func (a NetApplier) setDNSOverride(ctx context.Context, dns []string) error {
	if len(dns) == 0 {
		if readFile(a.Root.join(resolvconfInterfaces+"/"+DNSOverrideRecord)) == "" {
			return nil
		}
		if out, err := a.run()(ctx, "/sbin/resolvconf", "-f", "-d", DNSOverrideRecord); err != nil {
			return fmt.Errorf("resolvconf -d: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	return a.resolvconfAdd(ctx, []string{"-x", "-a", DNSOverrideRecord}, a.overrideRecord(dns))
}

// dnsOverrideCurrent says whether the record in place is the one dns asks for, with the lease's
// domain and search as they are now.
func (a NetApplier) dnsOverrideCurrent(dns []string) bool {
	have := readFile(a.Root.join(resolvconfInterfaces + "/" + DNSOverrideRecord))
	if len(dns) == 0 {
		return have == ""
	}
	got, want := parseResolv(have), parseResolv(a.overrideRecord(dns))
	return strings.Join(got.Nameservers, " ") == strings.Join(want.Nameservers, " ") && got.Domain == want.Domain &&
		strings.Join(got.Search, " ") == strings.Join(want.Search, " ")
}

// KeepDNSOverride puts the stored override's record back where it is missing or out of date: at
// start (/run is empty after a boot), and when a lease brought another domain or search list. It
// leaves a DHCP system without an override and a static one alone, apart from taking a stale
// record away, and waits while a network change waits for its confirmation.
func (t *NetTx) KeepDNSOverride(ctx context.Context, every time.Duration, log *slog.Logger) {
	for {
		t.keepDNSOverrideOnce(ctx, log)
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (t *NetTx) keepDNSOverrideOnce(ctx context.Context, log *slog.Logger) {
	if t.Pending() != nil {
		return
	}
	var dns []string
	if t.Root.ReadNetwork().Mode == "dhcp" {
		dns = t.Override.Servers()
	}
	if t.Applier.dnsOverrideCurrent(dns) {
		return
	}
	if err := t.Applier.setDNSOverride(ctx, dns); err != nil {
		log.Warn("network: the DNS override could not be applied", "err", err)
		return
	}
	if len(dns) == 0 {
		log.Info("network: a stale DNS override record removed")
		return
	}
	log.Info("network: the DNS override applied", "servers", strings.Join(dns, " "))
}
