package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// openccu-lite B-218: after the kernel has an HB-RF-ETH back, the daemons on it are asked whether
// they have their module (listBidcosInterfaces: rfd names it by its serial, hmipserver by its
// SGTIN, CONNECTED 1); a board that lost power reset its module, and the daemons may need a
// restart to take it. Without a plan nothing is asked (ok).

// boardHealth checks one listBidcosInterfaces answer against the plan's modules on the board.
func boardHealth(p radio.Plan, list []interfaces.RadioInterface, errs map[string]error) (bool, string, error) {
	hmrf, hmip, _ := p.OnHBRFETH()
	type want struct {
		iface string
		ids   []string
	}
	var ws []want
	if hmrf != nil {
		ws = append(ws, want{"BidCos-RF", []string{hmrf.Serial, hmrf.SGTIN}})
	}
	if hmip != nil {
		ws = append(ws, want{"HmIP-RF", []string{hmip.SGTIN, hmip.Serial}})
	}
	for _, w := range ws {
		if err := errs[w.iface]; err != nil {
			return false, "", fmt.Errorf("%s: %w", w.iface, err)
		}
		found := false
		for _, r := range list {
			if r.Interface != w.iface {
				continue
			}
			for _, id := range w.ids {
				if id != "" && strings.EqualFold(r.Address, id) {
					found = true
					if !r.Connected {
						return false, w.iface + ": " + r.Address + " not connected", nil
					}
				}
			}
		}
		if !found {
			return false, w.iface + ": the module " + w.ids[0] + " is not listed", nil
		}
	}
	return true, "", nil
}

// hbrfethHealthy is the watch's Healthy.
func hbrfethHealthy(root string, ifs func() []interfaces.Interface) func(context.Context) (bool, string, error) {
	return func(ctx context.Context) (bool, string, error) {
		p, err := radio.LoadPlan(root)
		if err != nil {
			return true, "", nil
		}
		list, errs := interfaces.ListInterfaces(ctx, ifs(), 5*time.Second)
		return boardHealth(p, list, errs)
	}
}

// serviceControl is the part of the service manager the restart needs.
type serviceControl interface {
	Control(ctx context.Context, id, action string) (string, error)
}

// hbrfethRestart is the watch's Restart: the daemons on the board stopped in reverse start order
// and started again, the way the hotplug restarts what a re-plan touches.
func hbrfethRestart(root string, svc serviceControl) func(context.Context) error {
	return func(ctx context.Context) error {
		p, err := radio.LoadPlan(root)
		if err != nil {
			return err
		}
		_, _, daemons := p.OnHBRFETH()
		if len(daemons) == 0 {
			return nil
		}
		for i := len(daemons) - 1; i >= 0; i-- {
			if out, err := svc.Control(ctx, daemons[i], "stop"); err != nil {
				return fmt.Errorf("stop %s: %v %s", daemons[i], err, out)
			}
		}
		var errs []string
		for _, d := range daemons {
			if out, err := svc.Control(ctx, d, "start"); err != nil {
				errs = append(errs, fmt.Sprintf("start %s: %v %s", d, err, out))
			}
		}
		if len(errs) > 0 {
			return fmt.Errorf("%s", strings.Join(errs, "; "))
		}
		return nil
	}
}
