package system

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// factoryResetFlag is what the firmware's S04CheckFactoryReset (occu-factory-reset.service on the
// lite image) looks for at the next boot: with it there, the userfs is made anew - mkfs on the
// partition, `rm -rf` of its content in a container - and the radio coprocessor is reset with it
// (.doCoproFactoryReset). Nothing is kept (D-106): users, tokens, metadata, addons, certificates,
// the pairings of rfd and hmipserver, the BidCos security key and the HmIP network key.
const factoryResetFlag = "/usr/local/.doFactoryReset"

// FactoryResetArmed reports whether the marker is set, so the next boot is the reset.
func (r Root) FactoryResetArmed() bool {
	_, err := os.Stat(r.join(factoryResetFlag))
	return err == nil
}

// ArmFactoryReset sets the marker (task 109). Refused while a system update is staged: the
// recovery system would install the update at that boot and the reset would follow at the next
// one, which is two surprises for one click - install or discard the update first.
func (r Root) ArmFactoryReset() error {
	if r.UpdateStaged() {
		return ErrUpdateStaged
	}
	return touch(r.join(factoryResetFlag), 0o644)
}

// DisarmFactoryReset takes the marker away again: the reboot did not start, or the user thought
// better of it.
func (r Root) DisarmFactoryReset() error { return remove(r.join(factoryResetFlag)) }

// HmIPLocalKeyEnabled says whether hmip_user.conf carries an HmIP network key (task 149's local
// key mode): the reset erases it, and the devices paired under it cannot be reached by another
// key. The key itself never leaves the file.
func (r Root) HmIPLocalKeyEnabled() bool {
	return radio.ReadLocalKey(readFile(r.join(hmipUserConf))).Enabled()
}

// PairedDevices is what one radio interface answered to listDevices, devices only: the CCU's own
// virtual entries (the BidCoS-RF central, the -RCV-50 receivers, the HmIP module itself) are not
// counted. Known is false when the interface did not answer - rfd or hmipserver down, or still
// starting - and Error says why; the count is then 0 and means nothing.
type PairedDevices struct {
	Devices int    `json:"devices"`
	Known   bool   `json:"known"`
	Error   string `json:"error,omitempty"`
}

// PairedDevicesTimeout bounds the listDevices of every interface; hmipserver's answer with many
// devices takes a few seconds.
var PairedDevicesTimeout = 10 * time.Second

// CountPaired asks every interface for its devices, in parallel, and counts them per interface
// name. It is the factory reset's warning: BidCos-RF devices paired to a system with an individual
// security key keep that key after the reset, and HmIP devices have to be reset on the device
// before they can be paired again.
func CountPaired(ctx context.Context, ifs []interfaces.Interface) map[string]PairedDevices {
	out := map[string]PairedDevices{}
	for _, i := range ifs {
		out[i.Name] = PairedDevices{}
	}
	if len(ifs) == 0 {
		return out
	}
	devices, errs := interfaces.Devices(ctx, ifs, PairedDevicesTimeout)
	for name, err := range errs {
		out[name] = PairedDevices{Error: err.Error()}
	}
	for _, i := range ifs {
		if errs[i.Name] == nil {
			out[i.Name] = PairedDevices{Known: true}
		}
	}
	for _, d := range devices {
		if ownEntry(d) {
			continue
		}
		p := out[d.Interface]
		p.Devices++
		out[d.Interface] = p
	}
	return out
}

// ownEntry is a device the CCU lists for itself: rfd's central "BidCoS-RF" and the virtual
// receivers, hmipserver's module and its receivers (the same list HmIPDeviceKeys.paired skips).
func ownEntry(d interfaces.Device) bool {
	if strings.EqualFold(d.Address, "BidCoS-RF") || strings.EqualFold(d.Address, "BidCoS-Wir") {
		return true
	}
	t := strings.ToUpper(d.Type)
	for _, own := range []string{"RCV", "RPI-RF-MOD", "RFUSB", "HMIP-CCU"} {
		if strings.Contains(t, own) {
			return true
		}
	}
	return false
}
