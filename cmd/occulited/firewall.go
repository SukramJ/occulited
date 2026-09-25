package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/system"
)

// firewallCmd is `occulited firewall load`, occu-firewall.service's command (task 157, D-105): at
// boot, before the network, the rule file is loaded - made first when there is none (converted
// from firewall.conf, or a fresh box's defaults: the web server's rules, policy DROP), so a box is
// never open while it comes up. It runs as root, without the daemon and its helper. The local
// networks are what the interfaces carry now (the static ranges before the network is up);
// occulited reloads them once it runs.
func firewallCmd(args []string) error {
	if len(args) == 0 || args[0] != "load" {
		return errors.New("usage: occulited firewall load [--root DIR] [--state-dir DIR]")
	}
	fs := flag.NewFlagSet("firewall load", flag.ContinueOnError)
	root := fs.String("root", "/", "filesystem root")
	stateDir := fs.String("state-dir", "/usr/local/etc/occulite", "occulited's state directory (where the default's marker is)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	r := system.Root(*root)
	// the addons' opened ports as their policies declare them (the catalogue is not reachable
	// before the network): the conversion makes them owned rules, not user ports
	system.Priv = priv.Local{}
	owners := system.FirewallManager{Root: r}.AddonOwners()
	if p := r.ClassicRPCOwner(); p != nil {
		owners[firewall.OwnerRPC] = p
	}
	if p := r.SSHOwner(); p != nil {
		owners[firewall.OwnerSSH] = p
	}
	c, err := r.EnsureRules(owners, *stateDir)
	if err != nil {
		// openccu-lite B-188: the rules built here (the defaults, or the converted ones) are
		// loaded whatever became of the file - a box is never open, and the unit does not fail
		// before an iptables call; occulited writes the file again at its start (EnsureRules).
		fmt.Fprintln(os.Stderr, "occulited firewall: the rule file could not be written, the rules are loaded from memory:", err)
	}
	var nets []*net.IPNet
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				nets = append(nets, n)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return system.LoadRules(ctx, c, firewall.LocalFor(nets))
}
