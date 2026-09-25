// Package bootexpect is the reboot countdown's calibration: how long a user-initiated reboot, a
// system update's install or a backup restore takes on this box, phase by phase.
//
// The web UI shows a bar that starts full and empties until the box is back. It needs to know how
// long each phase takes: the shutdown until the box stops answering (down), then until lighttpd
// answers (http), then until occulited answers (ui), then until the radio interfaces are up
// (ready). The first figures come from defaults.json, per product; after that the box measures
// its own reboots:
//
//   - before the reboot, a marker with the request time and the boot id
//     (<state dir>/boot-timing/pending.json);
//   - at the next start, once the clock can be trusted (a Pi without an RTC starts with a wrong
//     one), the record: the kernel start on the wall clock, and lighttpd's, occulited's and the
//     interfaces' start on the monotonic clock;
//   - the browser adds what it saw (the moment the box stopped answering, lighttpd's first 503,
//     occulited's first answer), which is the only way to tell the shutdown from the boot;
//   - the last three records per kind in <state dir>/boot-timings.json, and the median per phase
//     replaces the product default.
package bootexpect

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

//go:embed defaults.json
var defaultsJSON []byte

// Expect holds the expected duration of each phase in seconds.
type Expect struct {
	Down  float64 `json:"down"`
	HTTP  float64 `json:"http"`
	UI    float64 `json:"ui"`
	Ready float64 `json:"ready"`
}

// The kinds of a user-initiated reboot. recovery and halt have no table of their own: their
// shutdown is a reboot's, and nothing after it is counted down.
const (
	KindReboot   = "reboot"
	KindRecovery = "recovery"
	KindUpdate   = "update"
	KindRestore  = "restore"
	KindHalt     = "halt"
)

// tableKinds are the kinds defaults.json lists per product.
var tableKinds = []string{KindReboot, KindUpdate, KindRestore}

// ValidKind says whether k is one of the five kinds.
func ValidKind(k string) bool {
	switch k {
	case KindReboot, KindRecovery, KindUpdate, KindRestore, KindHalt:
		return true
	}
	return false
}

// tableKind is the kind whose figures and records a kind uses.
func tableKind(k string) string {
	if k == KindUpdate || k == KindRestore {
		return k
	}
	return KindReboot
}

type defaultsFile struct {
	Default  string                       `json:"default"`
	Products map[string]map[string]Expect `json:"products"`
}

var defaults = mustParseDefaults(defaultsJSON)

// parseDefaults reads the table and checks what the code relies on: the default product exists,
// and every product has every table kind with no negative figure.
func parseDefaults(b []byte) (defaultsFile, error) {
	var d defaultsFile
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	if _, ok := d.Products[d.Default]; !ok {
		return d, fmt.Errorf("the default product %q is not in the table", d.Default)
	}
	for name, kinds := range d.Products {
		for _, k := range tableKinds {
			e, ok := kinds[k]
			if !ok {
				return d, fmt.Errorf("product %s has no %s figures", name, k)
			}
			if e.Down < 0 || e.HTTP < 0 || e.UI < 0 || e.Ready < 0 {
				return d, fmt.Errorf("product %s, %s: a negative figure", name, k)
			}
		}
	}
	return d, nil
}

func mustParseDefaults(b []byte) defaultsFile {
	d, err := parseDefaults(b)
	if err != nil {
		panic("bootexpect: defaults.json: " + err.Error())
	}
	return d
}

// ProductOf maps /VERSION's PLATFORM to a product of the table: itself when the table lists it,
// the default product otherwise (a CCU3, a generic image).
func ProductOf(platform string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	if _, ok := defaults.Products[p]; ok {
		return p
	}
	return defaults.Default
}

// Default is the table's figures for a product and kind; an unknown product gets the default one.
func Default(product, kind string) Expect {
	return defaults.Products[ProductOf(product)][tableKind(kind)]
}

// round1 rounds seconds to a tenth: nothing the bar shows is finer.
func round1(s float64) float64 {
	return math.Round(s*10) / 10
}
