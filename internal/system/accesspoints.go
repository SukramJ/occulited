package system

import (
	"os"
	"strings"

	"github.com/hobbyquaker/occulited/internal/interfaces"
)

// HmIPDeviceSGTINs maps the device addresses of hmipserver's paired devices to their SGTINs
// (openccu-lite task 217): hmipserver keeps one <SGTIN>.dev file per paired device in its data
// directory, and the device address is the SGTIN's last 14 digits. The SGTIN cannot be rebuilt
// from the address, since its company prefix is not always eQ-3's (the SilverCrest HmIP-HAP-B1's
// is TARGA's, 30150377DC...). An unreadable directory is an empty map.
func (r Root) HmIPDeviceSGTINs() map[string]string {
	out := map[string]string{}
	entries, _ := os.ReadDir(r.join(crRFDDataDir))
	for _, e := range entries {
		name := strings.ToUpper(e.Name())
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
