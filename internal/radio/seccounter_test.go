package radio

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// openccu-lite task 299: the two journal lines and the verdicts, with the numbers of the public
// issue (eq-3/occu#134) and of a lab system.
func TestSecurityCounterLinesAndVerdicts(t *testing.T) {
	const prefix = "de.eq3.cbcs.server.local.base.internal.HMIPTRXInitialResponseListener [RXTXPortMonitor(/dev/mmd_hmip)] "
	lab := prefix + "Current Security Counter: 7717888\n" + prefix + "Update security counter to calculation: 7718390\n"
	cur, calc, okC, okU := ParseSecurityCounterLines("noise\n" + lab + "more noise\n")
	if !okC || !okU || cur != 7717888 || calc != 7718390 {
		t.Fatalf("lab lines: %d %d %v %v", cur, calc, okC, okU)
	}
	// a start that logs no update line (the module's counter is already higher)
	if cur, _, okC, okU := ParseSecurityCounterLines(prefix + "Current Security Counter: 42\n"); !okC || okU || cur != 42 {
		t.Fatalf("current alone: %d %v %v", cur, okC, okU)
	}
	if _, _, okC, okU := ParseSecurityCounterLines("nothing here"); okC || okU {
		t.Fatal("no lines")
	}
	// two starts in one output: the latest counts
	if cur, calc, _, _ := ParseSecurityCounterLines(lab + prefix + "Current Security Counter: 8000000\n" + prefix + "Update security counter to calculation: 8000100\n"); cur != 8000000 || calc != 8000100 {
		t.Fatalf("latest start: %d %d", cur, calc)
	}
	for _, tc := range []struct {
		name          string
		current, calc uint64
		okCalc        bool
		seen          uint64
		verdict       string
		written       uint64
	}{
		{"lab", 7717888, 7718390, true, 0, CounterFine, 7718390},
		{"no update", 7718390, 0, false, 0, CounterFine, 7718390},
		{"near", 100, CounterNear + 5, true, 0, CounterNearWrap, CounterNear + 5},
		{"module near", CounterNear + 5, 0, false, 0, CounterNearWrap, CounterNear + 5},
		// the issue's numbers: the computed value passed 2^32 and the module gets its lower 32
		// bits - above the module's own counter, so from these two lines alone the counter is
		// wrapped; with the earlier start's value (the devices saw about 4.03e9) it is backwards
		{"issue, alone", 1024874459, 5319842009, true, 0, CounterWrapped, 1024874713},
		{"issue, after the 1970 start", 1024874459, 5319842009, true, 4031374849, CounterBackwards, 1024874713},
		// the module itself goes down: the truncated value is below the module's counter
		{"below the module", 4000000000, 4294967296 + 5, true, 0, CounterBackwards, 5},
		{"exactly 2^32", 10, CounterWrap, true, 0, CounterBackwards, 0},
	} {
		if got := CounterVerdict(tc.current, tc.calc, tc.okCalc, tc.seen); got != tc.verdict {
			t.Errorf("%s: verdict %s, want %s", tc.name, got, tc.verdict)
		}
		if got := CounterWritten(tc.current, tc.calc, tc.okCalc); got != tc.written {
			t.Errorf("%s: written %d, want %d", tc.name, got, tc.written)
		}
	}
	if WorstCounterVerdict(CounterNearWrap, CounterWrapped) != CounterWrapped || WorstCounterVerdict(CounterBackwards, CounterFine) != CounterBackwards {
		t.Fatal("worst")
	}
}

// The access point file: an anonymised lab file (the SGTIN and the radio address replaced, the two
// numbers kept), a truncated one and a foreign one, and the computation against the journal.
func TestAccessPointFile(t *testing.T) {
	b, err := os.ReadFile("testdata/accesspoint.ap")
	if err != nil {
		t.Fatal(err)
	}
	ap, err := ParseAccessPoint(b)
	if err != nil {
		t.Fatal(err)
	}
	if ap.SGTIN != "3014F711A000040000000A01" || ap.Address != "ABCDE1" || ap.State != "BACKUP_DONE" || ap.Offset != 2829 {
		t.Fatalf("access point: %+v", ap)
	}
	if want := time.Date(2026, 9, 3, 18, 52, 45, 412e6, time.UTC); !ap.FirstConnect.Equal(want) {
		t.Fatalf("first connect %s, want %s", ap.FirstConnect, want)
	}
	// the lab system's own journal line: at 2026-09-30 09:05:08 UTC hmipserver computed 7661307
	calc, behind := ap.Calc(time.Date(2026, 9, 30, 9, 5, 8, 0, time.UTC))
	if behind || calc < 7661307-3 || calc > 7661307+3 {
		t.Fatalf("calc %d (behind %v), the journal said 7661307", calc, behind)
	}
	// a clock before the first connection: the offset alone
	if calc, behind := ap.Calc(time.Date(2026, 3, 13, 17, 8, 28, 0, time.UTC)); !behind || calc != 2830 {
		t.Fatalf("behind: %d %v", calc, behind)
	}
	// openccu-lite B-302: when the computed value reaches a given one - the offset alone at first
	if at := ap.ReachesAt(7661307); !at.Before(time.Date(2026, 9, 30, 9, 5, 8, 0, time.UTC).Add(time.Second)) || at.Before(time.Date(2026, 9, 30, 9, 5, 7, 0, time.UTC)) {
		t.Fatalf("reaches the journal's value at %s", at)
	}
	if c, _ := ap.Calc(ap.ReachesAt(7661307)); c != 7661307 {
		t.Fatalf("calc at ReachesAt: %d", c)
	}
	if !ap.ReachesAt(100).Equal(ap.FirstConnect) {
		t.Fatal("below the offset: the first connection")
	}
	// the wrap with this offset is about 40.8 years after the first connection
	if y := ap.WrapsAt().Year(); y != 2067 {
		t.Fatalf("wraps in %d", y)
	}
	// the issue's access point: first connected 2014-07-01 00:00 CEST with an offset of
	// 4031374848 computes the issue's number at its clock (2026-09-30 ... 1790705748050 ms)
	hasko := AccessPoint{FirstConnect: time.UnixMilli(1404165600000).UTC(), Offset: 4031374848}
	if calc, _ := hasko.Calc(time.UnixMilli(1790705748050).UTC()); calc != 5319842009 {
		t.Fatalf("the issue's computation: %d", calc)
	}
	if !hasko.WrapsAt().Before(hasko.FirstConnect.Add(24 * time.Hour * 365 * 3)) {
		t.Fatalf("the issue's access point wraps at %s", hasko.WrapsAt())
	}
	for name, data := range map[string][]byte{
		"truncated":   b[:100],
		"foreign":     []byte("de.eq3.cbcs.server.core.devicemanagement.SomethingElse"),
		"empty":       nil,
		"no sgtin":    b[:strings.Index(string(b), "3014")],
		"no numbers":  b[:strings.Index(string(b), "BACKUP")+4],
		"bad time":    badTime(b),
		"device file": []byte("de.eq3.cbcs.server.core.devicemanagement.HMIPDevice" + string(b[60:])),
	} {
		if _, err := ParseAccessPoint(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// the directory reader skips what it cannot parse
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "3014F711A000040000000A01.ap"), b, 0o600)
	_ = os.WriteFile(filepath.Join(dir, "3014F711A000040000000A02.ap"), []byte("junk"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "3014F711A000040000000A01.dev"), b, 0o600)
	if aps, err := AccessPointFiles(dir); err != nil || len(aps) != 1 || aps[0].SGTIN != ap.SGTIN {
		t.Fatalf("files: %v %v", aps, err)
	}
}

// badTime is the fixture with its first connection set to the year 3000.
func badTime(b []byte) []byte {
	out := append([]byte{}, b...)
	i := strings.Index(string(out), "BACKUP_DON") - 8
	binary.BigEndian.PutUint64(out[i:i+8], uint64(time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()))
	return out
}

// The state file: the journal readings and the computations per access point, the history's
// bound, and the verdict that follows from the sequence of the issue.
func TestSecurityCounterState(t *testing.T) {
	root := t.TempDir()
	const sgtin = "3014F711A000040000000A01"
	at := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	line := func(cur, calc uint64) string {
		return fmt.Sprintf("Current Security Counter: %d\nUpdate security counter to calculation: %d\n", cur, calc)
	}
	ap, err := RecordCounterLines(root, sgtin, line(7717888, 7718390), at, "rtc")
	if err != nil || ap == nil || ap.Verdict != CounterFine || ap.Seen != 7718390 || len(ap.Starts) != 1 || ap.Starts[0].ClockState != "rtc" {
		t.Fatalf("lab start: %+v %v", ap, err)
	}
	if ap, err := RecordCounterLines(root, sgtin, "nothing", at, "rtc"); err != nil || ap != nil {
		t.Fatalf("no lines: %+v %v", ap, err)
	}
	// the issue's sequence on another access point: the 1970 start writes the offset, the next
	// start's wrapped value lands below it
	const hasko = "3014F711A000040000000A02"
	if ap, _ := RecordCounterLines(root, hasko, line(1024874459, 4031374849), at, "timeout"); ap.Verdict != CounterWrapped && ap.Verdict != CounterNearWrap {
		t.Fatalf("the 1970 start: %s", ap.Verdict)
	}
	ap, _ = RecordCounterLines(root, hasko, line(1024874459, 5319842009), at.Add(time.Hour), "ntp")
	if ap.Verdict != CounterBackwards || ap.Seen != 4031374849 || ap.Starts[1].Written != 1024874713 {
		t.Fatalf("the next start: %+v", ap)
	}
	st := ReadCounterState(root)
	if len(st.AccessPoints) != 2 || st.AccessPoints[hasko].Verdict != CounterBackwards || st.AccessPoints[sgtin].Verdict != CounterFine {
		t.Fatalf("state: %+v", st.AccessPoints)
	}
	// the history is bounded
	for i := 0; i < counterHistory+5; i++ {
		_, _ = RecordCounterLines(root, sgtin, line(7718390+uint64(i), 7718400+uint64(i)), at.Add(time.Duration(i)*time.Minute), "rtc")
	}
	if n := len(ReadCounterState(root).AccessPoints[sgtin].Starts); n != counterHistory {
		t.Fatalf("history %d, want %d", n, counterHistory)
	}
	// a computation against a value the devices saw: below it and truncated is backwards
	st = ReadCounterState(root)
	rec := st.record(hasko, CounterStart{At: at, Calc: CounterWrap + 1000, Source: "computed"})
	if rec.Starts[len(rec.Starts)-1].Verdict != CounterBackwards {
		t.Fatalf("computed below the seen value: %+v", rec.Starts[len(rec.Starts)-1])
	}
	for state, ok := range map[string]bool{"": true, "rtc": true, "ntp": true, "manual": true, "timeout": false, "rtc-implausible": false, "ntp-implausible": false} {
		if ClockTrusted(state) != ok {
			t.Errorf("ClockTrusted(%q) != %v", state, ok)
		}
	}
}

// The prep step: the computation goes into the state, a wrapped access point with an untrusted
// clock holds hmipserver (an error, a marker), a trusted clock releases it; the ready step reads
// the lines of the start.
func TestSecurityCounterPrepAndReady(t *testing.T) {
	fakeUsers(t)
	// hmipserver's unit runs the steps with UMask=0077
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })
	root, rec := boxRoot(t, map[string]string{"raw-uart": "GPIO@3f201000.serial"})
	recordOwnership(t, root)
	now := time.Date(2026, 9, 30, 9, 5, 8, 0, time.UTC)
	d := Detector{Root: root, Run: rec.run, GPIOLimit: 0, Sleep: func(time.Duration) {}, Now: func() time.Time { return now }}
	r, err := Run(context.Background(), root, d, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	p := r.Render.Plan
	var lines []string
	logf := func(f string, a ...any) { lines = append(lines, fmt.Sprintf(f, a...)) }
	write := func(rel string, content []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("dev/mmd_hmip", nil)
	write("dev/mmd_bidcos", nil)
	fixture, _ := os.ReadFile("testdata/accesspoint.ap")
	// the lab access point, an untrusted clock: fine, no hold
	write("etc/config/crRFD/data/3014F711A000040000000A01.ap", fixture)
	write("run/occulite/clock-state", []byte("timeout\n"))
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err != nil {
		t.Fatalf("fine access point: %v", err)
	}
	st := ReadCounterState(root)
	ap := st.AccessPoints["3014F711A000040000000A01"]
	if ap == nil || ap.Verdict != CounterFine || ap.Offset != 2829 || len(ap.Starts) != 1 || ap.Starts[0].Source != "computed" || ap.Starts[0].Calc < 7661300 || ap.Starts[0].ClockState != "timeout" {
		t.Fatalf("computed start: %+v", ap)
	}
	if ReadCounterHold(root) != nil {
		t.Fatal("a hold on a fine access point")
	}
	// the daemon reads the state as its own user: world-readable whatever the umask (the unit's is 0077)
	if st, err := os.Stat(filepath.Join(root, CounterStateFile)); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("state file mode: %v %v", err, st)
	}
	if st, err := os.Stat(filepath.Dir(filepath.Join(root, CounterStateFile))); err != nil || st.Mode().Perm() != 0o755 {
		t.Fatalf("state dir mode: %v %v", err, st)
	}
	// an offset that puts the computed value past 2^32 at this clock (the issue's 4031374848 with a
	// first connection of 2026 is still below it: near): wrapped - held while the clock is untrusted
	near := withOffset(fixture, 4031374848)
	write("etc/config/crRFD/data/3014F711A000040000000A01.ap", near)
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err != nil {
		t.Fatalf("near: %v", err)
	}
	if v := ReadCounterState(root).AccessPoints["3014F711A000040000000A01"].Verdict; v != CounterNearWrap {
		t.Fatalf("near verdict %s", v)
	}
	wrapped := withOffset(fixture, CounterWrap)
	write("etc/config/crRFD/data/3014F711A000040000000A01.ap", wrapped)
	err = Prep(context.Background(), d, "hmipserver", p, logf)
	if err == nil || !strings.Contains(err.Error(), "held back") || !strings.Contains(err.Error(), "2^32") {
		t.Fatalf("wrapped and untrusted: %v", err)
	}
	h := ReadCounterHold(root)
	if h == nil || h.SGTIN != "3014F711A000040000000A01" || h.ClockState != "timeout" || h.Calc < CounterWrap || !h.Since.Equal(now) {
		t.Fatalf("hold: %+v", h)
	}
	if st, err := os.Stat(shadowPath(root, CounterHoldFile)); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("hold marker mode: %v %v", err, st)
	}
	if v := ReadCounterState(root).AccessPoints["3014F711A000040000000A01"].Verdict; v != CounterWrapped {
		t.Fatalf("verdict %s", v)
	}
	// the hold keeps its start time across retries
	now = now.Add(time.Minute)
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err == nil {
		t.Fatal("still held")
	}
	if h := ReadCounterHold(root); h == nil || !h.Since.Equal(now.Add(-time.Minute)) {
		t.Fatalf("hold since: %+v", h)
	}
	// the clock trusted: released, the marker gone
	write("run/occulite/clock-state", []byte("ntp\n"))
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err != nil {
		t.Fatalf("trusted clock: %v", err)
	}
	if ReadCounterHold(root) != nil {
		t.Fatal("the marker stays after the release")
	}
	// a clock before the first connection with a small offset: at risk, held while untrusted
	write("etc/config/crRFD/data/3014F711A000040000000A01.ap", fixture)
	write("run/occulite/clock-state", []byte("timeout\n"))
	now = time.Date(2026, 3, 13, 17, 8, 28, 0, time.UTC)
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err == nil || !strings.Contains(err.Error(), "first connection") {
		t.Fatalf("behind: %v", err)
	}
	// no gate on the image (no state file): trusted, as before
	_ = os.Remove(filepath.Join(root, "run/occulite/clock-state"))
	if err := Prep(context.Background(), d, "hmipserver", p, logf); err != nil {
		t.Fatalf("no gate: %v", err)
	}
	// the ready step reads the start's two lines and records the verdict
	now = time.Date(2026, 9, 30, 9, 5, 20, 0, time.UTC)
	write("var/status/HMServerStarted", nil)
	rec.answers["journalctl"] = "de.eq3.cbcs.server.local.base.internal.HMIPTRXInitialResponseListener [RXTXPortMonitor(/dev/mmd_hmip)] Current Security Counter: 7439872\nde.eq3.cbcs.server.local.base.internal.HMIPTRXInitialResponseListener [RXTXPortMonitor(/dev/mmd_hmip)] Update security counter to calculation: 7661307\n"
	lines = nil
	if err := Ready(context.Background(), d, "hmipserver", 4242, logf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "HmIP security counter 7439872, computed 7661307 (fine)") {
		t.Fatalf("ready lines: %q", lines)
	}
	ap = ReadCounterState(root).AccessPoints["3014F711A0001F0000000A03"]
	if ap == nil || len(ap.Starts) != 1 || ap.Starts[0].Source != "journal" || ap.Starts[0].Current != 7439872 || ap.Seen != 7661307 {
		t.Fatalf("recorded: %+v", ap)
	}
	// a wrapped start is a warning line
	rec.answers["journalctl"] = "x Current Security Counter: 1024874459\nx Update security counter to calculation: 5319842009\n"
	lines = nil
	if err := Ready(context.Background(), d, "hmipserver", 4243, logf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "<4>ready hmipserver: HmIP security counter 1024874459, computed 5319842009, the module now holds 1024874713: wrapped") {
		t.Fatalf("ready lines: %q", lines)
	}
}

// withOffset is the fixture with another offset.
func withOffset(b []byte, offset uint64) []byte {
	out := append([]byte{}, b...)
	i := strings.Index(string(out), "BACKUP_DON") + 11
	binary.BigEndian.PutUint64(out[i:i+8], offset)
	return out
}
