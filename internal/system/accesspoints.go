package system

import (
	"strings"

	"github.com/hobbyquaker/occulited/internal/interfaces"
)

// HmIPDeviceSGTINs maps the device addresses of hmipserver's paired devices to their SGTINs
// (openccu-lite task 217): hmipserver keeps one <SGTIN>.dev file per paired device in its data
// directory, and the device address is the SGTIN's last 14 digits. The SGTIN cannot be rebuilt
// from the address, since its company prefix is not always eQ-3's (the SilverCrest HmIP-HAP-B1's
// is TARGA's, 30150377DC...). The directory is hmipserver's alone (0700, openccu-lite B-253): the
// names come through the helper (readDir), and a directory it does not list is an empty map.
func (r Root) HmIPDeviceSGTINs() map[string]string {
	out := map[string]string{}
	for _, entry := range readDir(r.join(crRFDDataDir)) {
		name := strings.ToUpper(entry)
		if !strings.HasSuffix(name, ".DEV") {
			continue
		}
		id := strings.TrimSuffix(name, ".DEV")
		if addr := interfaces.SGTINDevice(id); addr != "" && sgtinRe.MatchString(id) {
			out[addr] = id
		}
	}
	return out
}
