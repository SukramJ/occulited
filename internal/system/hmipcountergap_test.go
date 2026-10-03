package system

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// apFile builds an access point file in the layout radio.ParseAccessPoint reads.
func apFile(sgtin, address string, first time.Time, offset uint64) []byte {
	str := func(s string) []byte { b := []byte(s); b[len(b)-1] |= 0x80; return b }
	b := append([]byte{0xAC, 0xED, 0x00}, []byte("de.eq3.ShareableHMIPAccessPoint")...)
	b = append(b, str(sgtin)...)
	b = append(b, str(address)...)
	b = binary.BigEndian.AppendUint64(b, uint64(first.UnixMilli()))
	b = append(b, str("BACKUP_DONE")...)
	b = binary.BigEndian.AppendUint64(b, offset)
	return append(b, 0, 0, 0, 0)
}

// openccu-lite B-302 step 1, with the numbers of rpi4-2's way back on 2026-10-03: the module left
// (an HmIP-RFUSB used in another network before) had logged 8 529 664; the TK went back with its
// computed counter, about 8.29 M - a gap of about 0.24 M counts, about 20 hours at hmipserver's
// 300 ms tick. The offer names it before the way back, the notice after it until a start of
// HmIP-RF has logged the counter at or above the highest.
func TestCounterGapOfTheWayBack(t *testing.T) {
	root := t.TempDir()
	const tk, stick = "3014F5AC9400040000000A09", "3014F711A000040000000A01"
	first := time.Date(2026, 10, 1, 7, 27, 15, 912e6, time.UTC)
	stickFirst := time.Date(2026, 10, 1, 15, 21, 6, 857e6, time.UTC)
	now := time.Date(2026, 10, 3, 10, 12, 30, 0, time.UTC)
	data := filepath.Join(root, "etc/config/crRFD/data")
	_ = os.MkdirAll(data, 0o755)
	_ = os.WriteFile(filepath.Join(data, stick+".ap"), apFile(stick, "ABCDE1", stickFirst, 7770112), 0o600)
	state := radio.CounterState{AccessPoints: map[string]*radio.CounterAccessPoint{
		stick: {SGTIN: stick, Seen: 8529664},
		tk:    {SGTIN: tk, Seen: 8284417},
	}}
	writeState := func() {
		b, _ := json.Marshal(state)
		_ = os.MkdirAll(filepath.Dir(filepath.Join(root, radio.CounterStateFile)), 0o755)
		_ = os.WriteFile(filepath.Join(root, radio.CounterStateFile), b, 0o644)
	}
	writeState()
	inUse := stick
	k := &HmIPLocalKey{Root: Root(root), StateDir: filepath.Join(root, "state"), Now: func() time.Time { return now },
		Plan: func() (radio.Plan, bool) { return radio.Plan{HmIP: &radio.Role{SGTIN: inUse}}, true }}
	snapDir := k.snapshotDir(tk)
	_ = os.MkdirAll(snapDir, 0o700)
	_ = os.WriteFile(filepath.Join(snapDir, tk+".ap"), apFile(tk, "ABCDE2", first, 7675328), 0o600)
	snap := LocalKeySnapshot{SGTIN: tk, Files: []string{tk + ".ap", tk + ".apkx"}, Kind: SnapshotModuleMove, Choices: &radio.Choices{}}

	g := k.moveBackGap(snap)
	calcTK := uint64(now.Sub(first)/(300*time.Millisecond)) + 1 + 7675328
	if calcTK < 8284417 {
		calcTK = 8284417 // the TK's own counter as hmipserver last logged it is the higher here
	}
	if g == nil || g.From != stick || g.To != tk || g.Highest != 8529664 || g.Back != calcTK || g.Behind != 8529664-calcTK {
		t.Fatalf("gap: %+v (computed %d)", g, calcTK)
	}
	if want := first.Add(time.Duration(8529664-7675328-1) * 300 * time.Millisecond); !g.Until.Equal(want) {
		t.Fatalf("until %s, want %s", g.Until, want)
	}
	if h := g.Until.Sub(now).Hours(); h < 20 || h > 21 {
		t.Fatalf("the wait is %.1f h, the lab's was about 20", h)
	}
	// no gap: the previous module is ahead, or its counter is not known
	state.AccessPoints[tk].Seen = 9000000
	writeState()
	if g := k.moveBackGap(snap); g != nil {
		t.Fatalf("the previous module ahead: %+v", g)
	}
	state.AccessPoints[tk].Seen = 8284417
	writeState()
	if g := k.moveBackGap(LocalKeySnapshot{SGTIN: tk, Files: []string{tk + ".apkx"}}); g != nil {
		t.Fatalf("no access point file in the snapshot: %+v", g)
	}
	// nothing logged for the module left: its computed counter (7.77 M + the time) is below the TK's
	delete(state.AccessPoints, stick)
	writeState()
	if g := k.moveBackGap(snap); g != nil {
		t.Fatalf("computed only: %+v", g)
	}

	// after the way back: the notice while HmIP-RF runs on the TK and no start reached the highest
	g = &HmIPCounterGap{From: stick, To: tk, Highest: 8529664, Back: calcTK, Behind: 8529664 - calcTK, Until: g0Until(first), At: now}
	k.saveCounterGap(g)
	inUse = tk
	if got := k.CounterGap(); got == nil || got.Highest != 8529664 {
		t.Fatalf("the notice: %+v", got)
	}
	state.AccessPoints[tk] = &radio.CounterAccessPoint{SGTIN: tk, Starts: []radio.CounterStart{
		{At: now.Add(-time.Hour), Source: "journal", Written: 9000000}, // before the way back: does not count
		{At: now.Add(time.Hour), Source: "journal", Written: 8400000},
	}}
	writeState()
	if k.CounterGap() == nil {
		t.Fatal("closed by a start below the highest or one from before")
	}
	state.AccessPoints[tk].Starts = append(state.AccessPoints[tk].Starts, radio.CounterStart{At: now.Add(21 * time.Hour), Source: "journal", Written: 8529700})
	writeState()
	if k.CounterGap() != nil {
		t.Fatal("still open after the counter caught up")
	}
	if _, err := os.Stat(filepath.Join(k.StateDir, counterGapFile)); err == nil {
		t.Fatal("a closed gap is kept")
	}
	// HmIP-RF on another module: forgotten
	k.saveCounterGap(g)
	inUse = stick
	if k.CounterGap() != nil {
		t.Fatal("the notice on another module")
	}
}

func g0Until(first time.Time) time.Time {
	return first.Add(time.Duration(8529664-7675328-1) * 300 * time.Millisecond)
}
