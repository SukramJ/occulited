package httpapi

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
	"github.com/hobbyquaker/occulited/internal/firmware"
	"github.com/hobbyquaker/occulited/internal/interfaces"
)

// openccu-lite task 217: the HmIP access points paired to this system - HAPs and DRAPs, which
// reach hmipserver over the LAN - for the Interfaces page, with what the firewall does with the
// ports they connect to.

// accessPointTimeout bounds each call to hmipserver; the whole answer has accessPointLimit.
var (
	accessPointTimeout = 5 * time.Second
	accessPointLimit   = 15 * time.Second
)

// AccessPointView is one entry of GET /radio/hmip/access-points.
type AccessPointView struct {
	interfaces.AccessPoint
	SGTIN string `json:"sgtin,omitempty"`
	Name  string `json:"name,omitempty"`
	// Latest and UpdateAvailable are the device firmware check's (B-195): eQ-3's version for the
	// type, and whether the device runs an older one.
	Latest          string `json:"latest,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
}

// AccessPointFirewall is what the confirmed rules do with the access point ports, and the hint
// the page shows for it (D-110's cases, task 181):
//   - keep: an access point is paired and every port is accepted;
//   - blocked: an access point is paired and a port is not accepted - it cannot reach the system;
//   - unused: none is paired and the ports are accepted - they can be set to REJECT;
//   - reopen: none is paired and a port is not accepted - accept them again before pairing one;
//   - unknown: HmIP-RF did not answer, so what is paired is not known.
type AccessPointFirewall struct {
	Ports []firewall.PortVerdict `json:"ports"`
	Hint  string                 `json:"hint"`
}

type accessPointsView struct {
	Interface    string            `json:"interface"`
	Available    bool              `json:"available"`
	Error        string            `json:"error,omitempty"`
	AccessPoints []AccessPointView `json:"access_points"`
	// Firewall is nil on a system without the rule file (before task 157).
	Firewall *AccessPointFirewall `json:"firewall"`
}

const accessPointInterface = "HmIP-RF"

// accessPoints is GET /radio/hmip/access-points.
func (a *SystemAPI) accessPoints(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), accessPointLimit)
	defer cancel()
	out := accessPointsView{Interface: accessPointInterface, AccessPoints: []AccessPointView{}}
	known := false
	var ifs []interfaces.Interface
	if a.RadioInterfaces != nil {
		ifs = a.RadioInterfaces()
	}
	if i, err := interfaces.Find(ifs, accessPointInterface); err == nil {
		out.Available = true
		aps, err := interfaces.AccessPoints(ctx, i, accessPointTimeout)
		if err != nil {
			out.Error = err.Error()
		} else {
			known = true
			sgtins := a.Root.HmIPDeviceSGTINs()
			fw := map[string]firmware.DeviceStatus{}
			if a.Firmware != nil {
				for _, d := range a.Firmware.Status().Devices {
					if d.Interface == accessPointInterface {
						fw[d.Address] = d
					}
				}
			}
			for _, ap := range aps {
				v := AccessPointView{AccessPoint: ap, SGTIN: sgtins[strings.ToUpper(ap.Address)]}
				if a.Names != nil {
					if n, _, ok := a.Names(accessPointInterface + "." + ap.Address); ok {
						v.Name = n
					}
				}
				if d, ok := fw[ap.Address]; ok {
					v.Latest, v.UpdateAvailable = d.Latest, d.UpdateAvailable
				}
				out.AccessPoints = append(out.AccessPoints, v)
			}
		}
	}
	if c, ok := a.Root.ReadRules(); ok {
		out.Firewall = &AccessPointFirewall{Ports: firewall.HmIPAPVerdicts(c)}
		out.Firewall.Hint = accessPointHint(known, len(out.AccessPoints) > 0, out.Firewall.Ports)
	}
	writeJSON(w, 200, out)
}

func accessPointHint(known, paired bool, ports []firewall.PortVerdict) string {
	open := true
	for _, p := range ports {
		if p.Target != "ACCEPT" {
			open = false
		}
	}
	switch {
	case !known:
		return "unknown"
	case paired && open:
		return "keep"
	case paired:
		return "blocked"
	case open:
		return "unused"
	}
	return "reopen"
}
