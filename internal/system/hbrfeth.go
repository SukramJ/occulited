package system

import (
	"errors"
	"os"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// WriteHBRFETH sets the HB-RF-ETH's address (openccu-lite task 218): OpenCCU's file, its first
// line, root's and readable; "" removes it (no board). Through the helper, as every /etc/config
// write; the caller validated the address.
func (r Root) WriteHBRFETH(addr string) error {
	p := r.join(radio.HBRFETHFile)
	if addr == "" {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return Priv.Remove(p)
	}
	return Priv.WriteFile(p, []byte(addr+"\n"), 0o644)
}
