package system

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// openccu-lite B-302 step 1 (maintainer, 2026-10-03): the way back after a module move puts the
// previous module's identity back, and that module goes on with its own HmIP security counter
// (task 299: the module's own counter, or what hmipserver computes from the access point file's
// first connection and offset, whichever is higher). The module HmIP-RF leaves may have sent with
// a higher one - its access point started from its own module's counter at the exchange, and a
// module used in another network brings a higher one along (rpi4-2, 2026-10-03: 8 529 664 on the
// HmIP-RFUSB against 8 284 417 on the TK) - and a device drops frames whose counter is below the
// highest it accepted. Until the previous module's computed counter has passed that value, devices
// that heard the other module may ignore this system's commands. The dialog says the expected wait
// before the user confirms, and a notice after the way back says until when. Nothing is written to
// the identity files for it (step 2, an opt-in counter write, waits for the maintainer).

// HmIPCounterGap is the security counter gap of a way back.
type HmIPCounterGap struct {
	// From is the module HmIP-RF leaves (or left), To the one it goes back to.
	From string `json:"from"`
	To   string `json:"to"`
	// Highest is what devices may have heard from From: its module's counter as hmipserver last
	// logged it, or its computed counter when none was logged. Back is To's counter at the way
	// back: its module's last logged counter or its computed one, whichever is higher.
	Highest uint64 `json:"highest"`
	Back    uint64 `json:"back"`
	Behind  uint64 `json:"behind"`
	// Until is when To's computed counter reaches Highest; hmipserver sets the counter at a start,
	// so the gap closes at the first start of HmIP-RF after that time.
	Until time.Time `json:"until"`
	// At is when the way back ran; empty in the dialog's offer.
	At time.Time `json:"at,omitzero"`
}

// counterGapFile keeps the gap of the last way back under the state directory.
const counterGapFile = "counter-gap.json"

// accessPointOf parses a module's access point file, nil when it cannot be read or parsed.
func accessPointOf(path string) *radio.AccessPoint {
	b := readFile(path)
	if b == "" {
		return nil
	}
	ap, err := radio.ParseAccessPoint([]byte(b))
	if err != nil {
		return nil
	}
	return &ap
}

// counterGap computes the gap of going from the module in use (its access point file in
// hmipserver's data directory) back to the one whose access point file is toAP; nil without a gap
// or when a value is not known.
func (k *HmIPLocalKey) counterGap(from string, to string, toAP *radio.AccessPoint, now time.Time) *HmIPCounterGap {
	if from == "" || to == "" || toAP == nil || strings.EqualFold(from, to) {
		return nil
	}
	from, to = strings.ToUpper(from), strings.ToUpper(to)
	st := radio.ReadCounterState(string(k.Root))
	highest := uint64(0)
	if a := st.AccessPoints[from]; a != nil {
		highest = a.Seen
	}
	if highest == 0 {
		if ap := accessPointOf(k.Root.join(filepath.Join(crRFDDataDir, from+".ap"))); ap != nil {
			highest, _ = ap.Calc(now)
		}
	}
	back, _ := toAP.Calc(now)
	if a := st.AccessPoints[to]; a != nil && a.Seen > back {
		back = a.Seen
	}
	if highest == 0 || highest <= back {
		return nil
	}
	return &HmIPCounterGap{From: from, To: to, Highest: highest, Back: back, Behind: highest - back, Until: toAP.ReachesAt(highest).UTC()}
}

// moveBackGap is the gap the way back from the module in use to the snapshot's module would open.
func (k *HmIPLocalKey) moveBackGap(s LocalKeySnapshot) *HmIPCounterGap {
	for _, f := range s.Files {
		if strings.EqualFold(f, s.SGTIN+".ap") {
			return k.counterGap(k.sgtin(), s.SGTIN, accessPointOf(filepath.Join(k.snapshotDir(s.SGTIN), f)), k.now())
		}
	}
	return nil
}

func (k *HmIPLocalKey) saveCounterGap(g *HmIPCounterGap) {
	p := filepath.Join(k.StateDir, counterGapFile)
	if g == nil {
		_ = os.Remove(p)
		return
	}
	b, _ := json.MarshalIndent(g, "", "  ")
	_ = os.MkdirAll(k.StateDir, 0o700)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		k.log().Warn("the HmIP security counter gap was not kept", "err", err)
	}
}

// CounterGap is the notice after a way back: the gap while it is open - HmIP-RF still runs on the
// module it went back to, and hmipserver has not logged a start with a counter at or above the
// highest since. A closed gap is forgotten.
func (k *HmIPLocalKey) CounterGap() *HmIPCounterGap {
	b, err := os.ReadFile(filepath.Join(k.StateDir, counterGapFile))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			k.log().Warn("the HmIP security counter gap could not be read", "err", err)
		}
		return nil
	}
	var g HmIPCounterGap
	if json.Unmarshal(b, &g) != nil || g.To == "" {
		return nil
	}
	if !strings.EqualFold(k.sgtin(), g.To) {
		k.saveCounterGap(nil)
		return nil
	}
	if a := radio.ReadCounterState(string(k.Root)).AccessPoints[g.To]; a != nil {
		for _, s := range a.Starts {
			if s.Source == "journal" && !s.At.Before(g.At) && s.Written >= g.Highest {
				k.saveCounterGap(nil)
				return nil
			}
		}
	}
	return &g
}
