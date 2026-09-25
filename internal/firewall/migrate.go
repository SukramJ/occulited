package firewall

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OldService is one [SERVICE] block of libfirewall's firewall.conf.
type OldService struct {
	ID     string
	Ports  []int
	Access string // full, restricted, none
}

// Old is what firewall.conf said, and the facts around it the conversion needs.
type Old struct {
	Mode      string // MOST_OPEN or RESTRICTIVE
	IPs       []string
	UserPorts []string
	Services  []OldService
	// AllowExternal: /etc/config/AllowExternalAccess existed, so the web server and SSH were open
	// to every address rather than the local networks
	AllowExternal bool
}

// DeadServices are the [SERVICE] blocks openccu-lite has no process behind; libfirewall.tcl's
// defaults carry every service a full CCU runs, and firewall.conf inherits them:
//
//	REGA       ports 1999, 8181, 41999, 48181 - ReGaHss, which D-1 removes outright
//	NEOSERVER  ports 1901, 1902, 5987, 8088, 9099, 10000, 48899, 49880, which arrives
//	           Access = full, i.e. an open rule for something that does not exist
//
// The conversion makes no rule for them; one that was open gets its own line in the notice, so
// the user knows the ports are closed now.
var DeadServices = map[string]bool{"REGA": true, "NEOSERVER": true}

// MigrateInputs is the conversion's input.
type MigrateInputs struct {
	Old Old
	// Owners are the owners switched on now (SSH, HmIP access points, the addons' opened ports);
	// the web server is always added.
	Owners map[string][]PortSpec
}

// Migrate converts a firewall.conf into the rule list, once (D-105, decision 11): the owners'
// automatic rules, a full service's ports from 0/0, a restricted one's ports from each allowed
// address, and each user port for tcp and udp (libfirewall opened both). MOST_OPEN becomes policy
// ACCEPT, anything else DROP. The notes say what was done, for the page's notice.
func Migrate(in MigrateInputs, now time.Time) Config {
	policy := Drop
	if in.Old.Mode == "MOST_OPEN" {
		policy = Accept
	}
	c := Config{Version: 1, Policy: Policy{V4: policy, V6: policy}, Rules: []Rule{}, Active: []string{}}
	owners := AlwaysOwners()
	for o, p := range in.Owners {
		owners[o] = p
	}
	c = Reconcile(c, owners)
	// libfirewall sent USERPORTS through its local-only chain, as it did the web server and SSH:
	// from the local networks, from everywhere with AllowExternalAccess or MOST_OPEN. An addon's
	// opened port was one of them.
	userSource := LocalNetworks
	if in.Old.AllowExternal || in.Old.Mode == "MOST_OPEN" {
		userSource = AnySource
	}
	// classic RPC switched on by the conversion (task 143): the XMLRPC service's rules become its
	// owned rules with their old sources, instead of its defaults - for the ports the service
	// listed; a port it did not list keeps classic RPC's default rule
	rpcPorts := map[int]bool{}
	if specs, ok := in.Owners[OwnerRPC]; ok {
		listed := map[int]bool{}
		for _, s := range in.Old.Services {
			if s.ID == "XMLRPC" {
				for _, p := range s.Ports {
					listed[p] = true
				}
			}
		}
		for _, p := range specs {
			if listed[p.Port] {
				rpcPorts[p.Port] = true
			}
		}
		c.Rules = slices.DeleteFunc(c.Rules, func(r Rule) bool { return r.Owner == OwnerRPC && rpcPorts[r.Port] })
	}
	var addonPorts []string
	for i, r := range c.Rules {
		if strings.HasPrefix(r.Owner, OwnerAddonPrefix) {
			c.Rules[i].Source = userSource
			addonPorts = append(addonPorts, strconv.Itoa(r.Port)+"/"+r.Proto)
		}
	}
	var notes []Note
	if in.Old.Mode == "MOST_OPEN" {
		notes = append(notes, NewNote("Mode MOST_OPEN: the policy is ACCEPT for IPv4 and IPv6."))
	} else {
		notes = append(notes, NewNote("Mode {mode}: the policy is DROP for IPv4 and IPv6.", "mode", orDefault(in.Old.Mode, "RESTRICTIVE")))
	}
	if in.Old.AllowExternal {
		for i := range c.Rules {
			if c.Rules[i].Owner == OwnerWeb || c.Rules[i].Owner == OwnerSSH {
				c.Rules[i].Source = AnySource
			}
		}
		notes = append(notes, NewNote("AllowExternalAccess was set: the web server's and SSH's rules are for every address (0/0) rather than the local networks."))
	}
	// the owners as their ids, space-separated: the page names each in its language
	names := []string{}
	for o := range owners {
		names = append(names, o)
	}
	sort.Strings(names)
	notes = append(notes, NewNote("Automatic rules: {owners}.", "owners", strings.Join(names, " ")))
	have := func(r Rule) bool {
		for _, x := range c.Rules {
			if x.Port == r.Port && x.Proto == r.Proto && x.Source == r.Source && x.Family == r.Family && x.Target == r.Target {
				return true
			}
		}
		return false
	}
	add := func(r Rule) bool {
		if have(r) {
			return false
		}
		c.Rules = append(c.Rules, r)
		return true
	}
	for _, s := range in.Old.Services {
		if DeadServices[s.ID] {
			if s.Access == "full" || s.Access == "restricted" {
				notes = append(notes, NewNote("Service {id} ({access} access): not converted - the system runs no process behind it, so its ports {ports} stay closed.", "id", s.ID, "access", s.Access, "ports", joinPorts(s.Ports)))
			}
			continue
		}
		switch s.Access {
		case "full":
			// OpenCCU's libfirewall sends a full service through the local-only chain too ("'full'
			// access is restricted to local source networks by default"; seen on .170: 2001 -j
			// local-only): the local networks, everywhere with AllowExternalAccess or MOST_OPEN
			n := 0
			for _, p := range s.Ports {
				if add(Rule{ID: NewID(), Port: p, Proto: serviceProto(p), Source: userSource, Family: FamilyBoth, Target: Accept, Comment: serviceComment(s.ID, p), Owner: rpcOwner(s.ID, p, rpcPorts)}) {
					n++
				}
			}
			if userSource == LocalNetworks {
				notes = append(notes, NewNote("Service {id} (full access): {n} rule(s) from the local networks for ports {ports}, as before.", "id", s.ID, "n", strconv.Itoa(n), "ports", joinPorts(s.Ports)))
			} else {
				notes = append(notes, NewNote("Service {id} (full access): {n} rule(s) from 0/0 for ports {ports}.", "id", s.ID, "n", strconv.Itoa(n), "ports", joinPorts(s.Ports)))
			}
		case "restricted":
			n := 0
			for _, p := range s.Ports {
				for _, ip := range in.Old.IPs {
					fam := SourceFamily(ip)
					if fam == "" {
						continue
					}
					if add(Rule{ID: NewID(), Port: p, Proto: serviceProto(p), Source: ip, Family: fam, Target: Accept, Comment: serviceComment(s.ID, p), Owner: rpcOwner(s.ID, p, rpcPorts)}) {
						n++
					}
				}
			}
			notes = append(notes, NewNote("Service {id} (restricted to the allowed addresses {ips}): {n} rule(s) for ports {ports}.", "id", s.ID, "ips", strings.Join(in.Old.IPs, " "), "n", strconv.Itoa(n), "ports", joinPorts(s.Ports)))
		}
	}
	if len(rpcPorts) > 0 {
		notes = append(notes, NewNote("Classic RPC is switched on (the plain and the TLS ports), as OpenCCU served the XMLRPC ports; their rules keep their sources and belong to Classic RPC now."))
	}
	if len(addonPorts) > 0 && userSource == LocalNetworks {
		notes = append(notes, NewNote("The addons' opened ports ({ports}) stay limited to the local networks, as before; a port switched on anew on the Addons page is open to every address.", "ports", strings.Join(addonPorts, " ")))
	}
	var user []string
	for _, up := range in.Old.UserPorts {
		n, err := strconv.Atoi(strings.TrimSpace(up))
		if err != nil || n < 1 || n > 65535 {
			notes = append(notes, NewNote("User port \"{port}\" was not a port number; libfirewall opened nothing for it, and no rule was made.", "port", up))
			continue
		}
		owned := false
		for _, r := range c.Rules {
			if r.Port == n && strings.HasPrefix(r.Owner, OwnerAddonPrefix) {
				owned = true
			}
		}
		if owned {
			continue // an addon's opened port: its owned rule is there
		}
		for _, proto := range []string{"tcp", "udp"} {
			add(Rule{ID: NewID(), Port: n, Proto: proto, Source: userSource, Family: FamilyBoth, Target: Accept, Comment: "user port from firewall.conf"})
		}
		user = append(user, up)
	}
	if len(user) > 0 {
		if userSource == LocalNetworks {
			notes = append(notes, NewNote("User ports {ports}: a rule each for tcp and udp from the local networks, as before.", "ports", strings.Join(user, " ")))
		} else {
			notes = append(notes, NewNote("User ports {ports}: a rule each for tcp and udp from 0/0.", "ports", strings.Join(user, " ")))
		}
	}
	if len(in.Old.IPs) > 0 {
		notes = append(notes, NewNote("The allowed addresses ({ips}) are no list of their own any more; a rule's source restricts it.", "ips", strings.Join(in.Old.IPs, " ")))
	}
	c.Migration = &Migration{At: now, Notes: notes}
	return c
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func joinPorts(p []int) string {
	s := make([]string, len(p))
	for i, n := range p {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, " ")
}

// serviceComment is a converted service port's comment: what is behind it, where known.
func serviceComment(id string, port int) string {
	if c, ok := XMLRPCComments[port]; ok && id == "XMLRPC" {
		return c
	}
	return id + " (from firewall.conf)"
}

// serviceProto is libfirewall's Firewall_isUdpPort: these service ports are udp, every other tcp.
func serviceProto(port int) string {
	switch port {
	case 161, 1901, 1902, 5987, 10000, 48899, 49880:
		return ProtoUDP
	}
	return ProtoTCP
}

// rpcOwner is classic RPC for an XMLRPC port it serves, "" for anything else.
func rpcOwner(service string, port int, rpc map[int]bool) string {
	if service == "XMLRPC" && rpc[port] {
		return OwnerRPC
	}
	return ""
}
