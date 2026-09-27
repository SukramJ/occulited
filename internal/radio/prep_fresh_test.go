package radio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// owners reads the recorded chown calls as the owners they leave: the last call for a path wins,
// and a path no call touched is left as its creator made it - root's, on a system.
func owners(calls []string) map[string]string {
	out := map[string]string{}
	for _, c := range calls {
		f := strings.Fields(c)
		if len(f) == 3 && f[0] == "chown" {
			out[f[1]] = f[2]
		}
	}
	return out
}

// TestPrepFreshUserfs is openccu-lite B-266: after a factory reset or a first boot /etc/config
// holds nothing of the radio daemons. The radio run and every daemon's prep have to leave each
// path the daemon writes owned by that daemon, with B-253's modes - on dev.29 hmipserver's store
// was made after the ownership pass (root's 0700: the server could not write the module's
// identity), and hmip_address.conf and ids did not exist, which the units' ReadWritePaths skip.
func TestPrepFreshUserfs(t *testing.T) {
	fakeUsers(t)
	root, rec := boxRoot(t, map[string]string{"raw-uart": "GPIO@3f201000.serial"})
	// the fresh userfs: none of the daemons' files (boxRoot's ids is a stale one)
	if err := os.Remove(filepath.Join(root, "etc/config/ids")); err != nil {
		t.Fatal(err)
	}
	calls := recordOwnership(t, root)
	d := Detector{Root: root, Run: rec.run, GPIOLimit: 0, Sleep: func(time.Duration) {}}
	r, err := Run(context.Background(), root, d, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	p := r.Render.Plan
	for _, n := range []string{"dev/mmd_bidcos", "dev/mmd_hmip"} {
		if err := os.WriteFile(filepath.Join(root, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, daemon := range []string{"multimacd", "rfd", "hmipserver", "hs485d", "hmlangw"} {
		if err := Prep(context.Background(), d, daemon, p, func(string, ...any) {}); err != nil {
			t.Fatalf("prep %s on a fresh userfs: %v", daemon, err)
		}
	}
	own := owners(*calls)
	const rfd, hmip, hs485d = "8110:8110", "8111:8111", "8113:8113"
	for _, c := range []struct {
		daemon, path, owner string
		mode                os.FileMode // 0: not checked
		dir                 bool
	}{
		{"rfd", "/etc/config/rfd", rfd, 0o755, true},
		{"rfd", "/etc/config/keys", rfd, 0o600, false},
		{"rfd", "/etc/config/ids", rfd, 0o644, false},
		{"rfd", "/etc/config/rfd.conf", "0:8110", 0o640, false},
		{"rfd", "/var/RFD.handlers", rfd, 0o644, false},
		{"hmipserver", "/etc/config/crRFD", hmip, 0o750, true},
		{"hmipserver", "/etc/config/crRFD/data", hmip, 0o700, true},
		{"hmipserver", "/etc/config/eshlight", hmip, 0, true},
		{"hmipserver", "/etc/config/hmip_address.conf", hmip, 0o644, false},
		{"hmipserver", "/etc/config/hmip_networkkey.conf", hmip, 0o600, false},
		{"hmipserver", "/etc/config/groups.gson", hmip, 0o640, false},
		{"hmipserver", "/var/LegacyService.handlers", hmip, 0o644, false},
		{"hmipserver", "/var/HMSERVER.handlers", hmip, 0o644, false},
		{"hs485d", "/etc/config/hs485d", hs485d, 0, true},
		{"hs485d", "/var/HS485D.handlers", hs485d, 0o644, false},
		{"hs485d", "/run/hs485d/run", hs485d, 0, true},
		{"hs485d", "/run/hs485d/log", hs485d, 0, true},
	} {
		st, err := os.Stat(filepath.Join(root, c.path))
		if err != nil {
			t.Errorf("%s: %s is missing: %v", c.daemon, c.path, err)
			continue
		}
		if st.IsDir() != c.dir {
			t.Errorf("%s: %s dir=%v", c.daemon, c.path, st.IsDir())
		}
		if got := own[c.path]; got != c.owner {
			t.Errorf("%s: %s owned %q, want %s", c.daemon, c.path, got, c.owner)
		}
		if c.mode != 0 && st.Mode().Perm() != c.mode {
			t.Errorf("%s: %s mode %o, want %o", c.daemon, c.path, st.Mode().Perm(), c.mode)
		}
	}
	// the module carries a BidCos address: the run wrote it, rfd's from the start; the HmIP
	// address file is empty, for the server's random address
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/ids")); !strings.HasPrefix(string(b), "BidCoS-Address=0x") || strings.HasPrefix(string(b), "BidCoS-Address=\n") {
		t.Errorf("ids on a fresh userfs: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/hmip_address.conf")); len(b) != 0 {
		t.Errorf("hmip_address.conf is made empty: %q", b)
	}

	// the next boot: the empty files are no address - nothing moved aside, the module's address
	// written into the empty ids, the HmIP address the module's, never "0x"
	if err := os.WriteFile(filepath.Join(root, "etc/config/ids"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	r, err = Run(context.Background(), root, d, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.Render.Plan.IDsInvalid || r.Render.Plan.HmIPAddressActive == "0x" || r.Render.Plan.HmIPAddressActive != r.Render.Plan.HmIP.Address {
		t.Fatalf("second boot plan: invalid %v, HmIP active %q", r.Render.Plan.IDsInvalid, r.Render.Plan.HmIPAddressActive)
	}
	aside, _ := filepath.Glob(filepath.Join(root, "etc/config/ids_old-*"))
	if len(aside) != 0 {
		t.Fatalf("an empty ids was moved aside: %v", aside)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/ids")); !strings.Contains(string(b), r.Render.Plan.HmRFAddressActive) || r.Render.Plan.HmRFAddressActive == "" {
		t.Fatalf("the module's address is not in the empty ids: %q (active %q)", b, r.Render.Plan.HmRFAddressActive)
	}
	if !has(*calls, "chown /etc/config/ids 8110:8110") {
		t.Fatalf("the run's ids is not rfd's: %v", *calls)
	}

	// a module without a BidCos address (the HM-CFG-USB-2, LAN gateways only): rfd writes its
	// random address itself, so prep leaves an empty file rfd owns, and one rfd wrote is kept
	if err := os.Remove(filepath.Join(root, "etc/config/ids")); err != nil {
		t.Fatal(err)
	}
	*calls = nil
	if err := Prep(context.Background(), d, "rfd", p, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "etc/config/ids")); err != nil || st.Size() != 0 || st.Mode().Perm() != 0o644 || !has(*calls, "chown /etc/config/ids 8110:8110") {
		t.Fatalf("prep's ids for rfd: %v %v %v", err, st, *calls)
	}
	want := "BidCoS-Address=0xFD0101\nSerialNumber=JEQ0000001\n"
	if err := os.WriteFile(filepath.Join(root, "etc/config/ids"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Prep(context.Background(), d, "rfd", p, func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "etc/config/ids")); string(b) != want {
		t.Fatalf("prep changed rfd's ids: %q", b)
	}
}
