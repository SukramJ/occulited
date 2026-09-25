package interfaces

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mdzio/go-hmccu/v2/itf/xmlrpc"
)

// IsLANAccessPoint: an HmIP access point that reaches hmipserver over the LAN (openccu-lite task
// 217) - the HmIP-HAP family (HmIP-HAP, HmIP-HAP-B1, HmIP-HAP JS1, HmIP-HAP2) and the wired
// HmIPW-DRAP. The radio module itself and the CCU's own entries are not: they are the system's.
func IsLANAccessPoint(typ string) bool {
	t := strings.ToUpper(typ)
	return strings.HasPrefix(t, "HMIP-HAP") || strings.Contains(t, "DRAP")
}

// AccessPoint is one paired LAN access point as hmipserver reports it: the device description and
// the values of its channel 0.
type AccessPoint struct {
	Address             string `json:"address"`
	Type                string `json:"type"`
	Firmware            string `json:"firmware"`
	AvailableFirmware   string `json:"available_firmware,omitempty"`
	FirmwareUpdateState string `json:"firmware_update_state,omitempty"`
	// Reachable is UNREACH negated; nil when hmipserver has no value yet (fault -5 for a device it
	// has not heard from since its start) or channel 0 did not answer.
	Reachable *bool `json:"reachable"`
	// IPAddress is :0's IP_ADDRESS: the address the access point reports it has.
	IPAddress string `json:"ip_address,omitempty"`
	// DutyCycle is the share of the hourly airtime budget used, in percent: :0's DUTY_CYCLE_LEVEL,
	// or the DUTY_CYCLE of the access point's listBidcosInterfaces entry. nil for a wired one.
	DutyCycle *float64 `json:"duty_cycle,omitempty"`
	// DutyCycleLimit is :0's DUTY_CYCLE: the budget is used up and the access point stops sending.
	DutyCycleLimit bool `json:"duty_cycle_limit,omitempty"`
	CarrierSense   *int `json:"carrier_sense,omitempty"`
	ConfigPending  bool `json:"config_pending,omitempty"`
	UpdatePending  bool `json:"update_pending,omitempty"`
	// Connected is the CONNECTED of the listBidcosInterfaces entry, when there is one for it.
	Connected *bool `json:"connected,omitempty"`
	// Error: channel 0 did not answer; the device's own fields still stand.
	Error string `json:"error,omitempty"`
}

// AccessPoints lists the LAN access points paired on one interface (HmIP-RF) with their channel 0
// values. The interface not answering listDevices is the error; a channel that does not answer
// leaves that entry with Error set.
func AccessPoints(ctx context.Context, i Interface, timeout time.Duration) ([]AccessPoint, error) {
	devices, errs := Devices(ctx, []Interface{i}, timeout)
	if e := errs[i.Name]; e != nil {
		return nil, e
	}
	var entries []RadioInterface
	out := []AccessPoint{}
	for _, d := range devices {
		if !IsLANAccessPoint(d.Type) {
			continue
		}
		if entries == nil {
			// an access point with a radio may have an entry of its own in listBidcosInterfaces,
			// under its SGTIN; asked once, and only when there is an access point at all
			entries, _, _ = SampleInterfaces(ctx, []Interface{i}, timeout)
			if entries == nil {
				entries = []RadioInterface{}
			}
		}
		ap := AccessPoint{Address: d.Address, Type: d.Type, Firmware: d.Firmware, AvailableFirmware: d.AvailableFirmware}
		if v, err := callWithin(ctx, i.caller, timeout, "getDeviceDescription", xmlrpc.NewString(d.Address)); err == nil {
			q := xmlrpc.Q(v)
			ap.FirmwareUpdateState = q.TryKey("FIRMWARE_UPDATE_STATE").String()
			q.SetErr(nil)
		}
		v, err := callWithin(ctx, i.caller, timeout, "getParamset", xmlrpc.NewString(d.Address+":0"), xmlrpc.NewString("VALUES"))
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			ap.Error = err.Error()
		} else {
			fillAccessPoint(&ap, xmlrpc.Q(v))
		}
		for _, e := range entries {
			if deviceAddress(e.Address) != d.Address {
				continue
			}
			c := e.Connected
			ap.Connected = &c
			if ap.DutyCycle == nil && e.DutyCycle >= 0 {
				f := float64(e.DutyCycle)
				ap.DutyCycle = &f
			}
		}
		out = append(out, ap)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Address < out[b].Address })
	return out, nil
}

// fillAccessPoint reads channel 0's VALUES into the entry. UNREACH missing from the paramset is
// "not known": hmipserver leaves out a value it has not had yet.
func fillAccessPoint(ap *AccessPoint, q *xmlrpc.Query) {
	m := q.Map()
	if u, ok := m["UNREACH"]; ok && u.IsNotEmpty() {
		r := !u.Bool()
		if u.Err() == nil {
			ap.Reachable = &r
		}
		u.SetErr(nil)
	}
	if ip, ok := m["IP_ADDRESS"]; ok {
		ap.IPAddress = ip.String()
		ip.SetErr(nil)
	}
	if dc, ok := m["DUTY_CYCLE_LEVEL"]; ok {
		if f, ok := number(dc); ok {
			ap.DutyCycle = &f
		}
	}
	if cs, ok := m["CARRIER_SENSE_LEVEL"]; ok {
		if f, ok := number(cs); ok {
			ap.CarrierSense = percent(f)
		}
	}
	ap.DutyCycleLimit = flag(m["DUTY_CYCLE"])
	ap.ConfigPending = flag(m["CONFIG_PENDING"])
	ap.UpdatePending = flag(m["UPDATE_PENDING"])
	q.SetErr(nil)
}

func flag(q *xmlrpc.Query) bool {
	if q == nil || !q.IsNotEmpty() {
		return false
	}
	b := q.Bool()
	if q.Err() != nil {
		q.SetErr(nil)
		return false
	}
	return b
}

// ErrNoInterface: the interface asked for is not in InterfacesList.xml.
var ErrNoInterface = errors.New("no such interface")

// Find returns the interface of that name.
func Find(ifs []Interface, name string) (Interface, error) {
	for _, i := range ifs {
		if i.Name == name {
			return i, nil
		}
	}
	return Interface{}, ErrNoInterface
}
