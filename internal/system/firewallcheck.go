package system

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// ---- whether the firewall is loaded (B-152, task 157) ----------------------------------------
//
// The rules are loaded by occu-firewall.service at boot and by occulited afterwards; loaded means
// INPUT jumps to the lite-input chain after the frame. A family whose INPUT has no such jump - the
// load failed, or something flushed the chain - accepts what its policy says, whatever the rules.

// FirewallFamilyNames are the families the check reads, in the order a warning names them.
var FirewallFamilyNames = []string{"ipv4", "ipv6"}

// ParseInputPolicy is the policy of `iptables -S INPUT`'s answer: its "-P INPUT <policy>" line,
// "" when there is none.
func ParseInputPolicy(out []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 3 && f[0] == "-P" && f[1] == "INPUT" {
			return f[2]
		}
	}
	return ""
}

// HasFirewallJump says whether `iptables -S INPUT`'s answer has the jump to lite-input.
func HasFirewallJump(out []byte) bool {
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "-A INPUT -j lite-input" {
			return true
		}
	}
	return false
}

// FirewallLoaded reads each family's INPUT chain through the privilege helper and says whether it
// jumps to lite-input. available is false on a box without iptables (the call answers
// priv.ErrNotAvailable); a family whose read fails is an error, so the check says "unknown" rather
// than "open".
func FirewallLoaded(ctx context.Context) (loaded map[string]bool, available bool, err error) {
	loaded = map[string]bool{}
	for _, f := range FirewallFamilyNames {
		r, err := Priv.FirewallInput(ctx, f)
		if errors.Is(err, priv.ErrNotAvailable) {
			return nil, false, nil
		}
		if err != nil {
			return nil, true, err
		}
		if r.Exit != 0 {
			return nil, true, fmt.Errorf("%s -S INPUT exited %d: %s", f, r.Exit, strings.TrimSpace(string(r.Stderr)))
		}
		loaded[f] = HasFirewallJump(r.Stdout)
	}
	return loaded, true, nil
}

// FirewallNotLoaded are the families whose INPUT has no jump to lite-input.
func FirewallNotLoaded(loaded map[string]bool) []string {
	var open []string
	for _, f := range FirewallFamilyNames {
		if l, ok := loaded[f]; ok && !l {
			open = append(open, f)
		}
	}
	return open
}
