package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testdata/boots/dmesg-raw-*.txt are the first lines of `dmesg -r` (util-linux) on the three lab
// boxes, and kernel-entries-*.jsonl two kernel entries of `journalctl -k -o json` each (the machine
// id taken out); captured 2026-09-12, fork 7006221d4.
func TestDmesgCaptures(t *testing.T) {
	for _, name := range []string{"119", "charly", "rpi4"} {
		raw, err := os.ReadFile("testdata/boots/dmesg-raw-" + name + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		lines := parseDmesg(raw, time.Time{}, "")
		if len(lines) != 12 {
			t.Fatalf("%s: %d lines", name, len(lines))
		}
		version := -1
		for i, l := range lines {
			if l.Tag != "kernel" || l.MonotonicUS != 0 || strings.HasPrefix(l.Message, "<") || strings.HasPrefix(l.Message, "[") {
				t.Errorf("%s line %d: %+v", name, i, l)
			}
			if strings.HasPrefix(l.Message, "Linux version 6.18.") {
				version = i
			}
		}
		if version < 0 || lines[version].Severity != "notice" {
			t.Errorf("%s: the version line %d", name, version)
		}

		entries, err := os.ReadFile("testdata/boots/kernel-entries-" + name + ".jsonl")
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range strings.Split(strings.TrimSpace(string(entries)), "\n") {
			l, ok := ParseJournalLine([]byte(e))
			src := regexpSourceMono.FindStringSubmatch(e)
			if !ok || src == nil || strconv.FormatInt(l.MonotonicUS, 10) != src[1] || l.Tag != "kernel" || l.Severity != "info" {
				t.Errorf("%s: %+v from %s", name, l, e)
			}
		}
	}
}

var regexpSourceMono = regexp.MustCompile(`"_SOURCE_MONOTONIC_TIMESTAMP":"(\d+)"`)

// dmesgRaw is SYNTHETIC, for what the captures above do not show: other priorities, a user-space
// message written to /dev/kmsg (<14>), a further line of a message without the prefix, and a kernel
// without CONFIG_PRINTK_TIME that prints no stamp.
const dmesgRaw = `<5>[    0.000000] Linux version 6.12.47-v8 (builder@openccu) #1 SMP PREEMPT
<6>[    0.000000] Machine model: Raspberry Pi 3 Model B Rev 1.2
<4>[    1.234567] random: crng init done
<3>[   12.500001] mmc0: timeout waiting for hardware interrupt.
<14>[   30.000100] systemd[1]: Started occulited.service.
 a further line of that message
<6>no stamp on this kernel
`

func TestParseDmesg(t *testing.T) {
	start := time.Date(2026, 9, 12, 18, 0, 0, 0, time.UTC)
	lines := parseDmesg([]byte(dmesgRaw), start, "openccu-lite")
	if len(lines) != 6 {
		t.Fatalf("%d lines: %+v", len(lines), lines)
	}
	first := lines[0]
	if first.Severity != "notice" || first.Facility != "0" || first.Tag != "kernel" || first.Host != "openccu-lite" || first.MonotonicUS != 0 ||
		first.Timestamp != "2026-09-12T18:00:00Z" || !strings.HasPrefix(first.Message, "Linux version") {
		t.Errorf("first: %+v", first)
	}
	if l := lines[2]; l.Severity != "warning" || l.MonotonicUS != 1_234_567 || l.Timestamp != "2026-09-12T18:00:01.234567Z" {
		t.Errorf("warning: %+v", l)
	}
	if l := lines[3]; l.Severity != "err" || l.MonotonicUS != 12_500_001 || l.Message != "mmc0: timeout waiting for hardware interrupt." {
		t.Errorf("err: %+v", l)
	}
	// <14> is user.info: a message written to /dev/kmsg; its further line belongs to it
	if l := lines[4]; l.Severity != "info" || l.Facility != "1" || l.Message != "systemd[1]: Started occulited.service.\n a further line of that message" {
		t.Errorf("user.info: %+v", l)
	}
	if l := lines[5]; l.MonotonicUS != 0 || l.Time != "" || l.Message != "no stamp on this kernel" {
		t.Errorf("no stamp: %+v", l)
	}
	// without a start on the wall clock the lines keep their stamps and have no time
	if l := parseDmesg([]byte(dmesgRaw), time.Time{}, "")[3]; l.Time != "" || l.MonotonicUS != 12_500_001 {
		t.Errorf("no start: %+v", l)
	}
}

func TestDmesgRead(t *testing.T) {
	root := rootWith(t, map[string]string{
		"proc/uptime":                    "100.00 180.00\n",
		"proc/sys/kernel/random/boot_id": "4c1d2a6b-0e8f-4a2b-9c3d-5e7f8a1b2c3d\n",
	})
	var calls []string
	d := Dmesg{Root: root, Now: func() time.Time { return time.Date(2026, 9, 12, 18, 1, 40, 0, time.UTC) }, Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, name+" "+strings.Join(args, " "))
		return []byte(dmesgRaw), nil
	}}
	lines, err := d.Read(LogQuery{Severity: "warning"})
	if err != nil {
		t.Fatal(err)
	}
	if calls[0] != "dmesg -r" || len(lines) != 2 || lines[0].Severity != "warning" || lines[1].Severity != "err" {
		t.Fatalf("%v %+v", calls, lines)
	}
	// the kernel started 100 s before now
	if lines[1].Timestamp != "2026-09-12T18:00:12.500001Z" {
		t.Errorf("time: %s", lines[1].Timestamp)
	}
	if lines, _ := d.Read(LogQuery{Limit: 2}); len(lines) != 2 || lines[1].Message != "no stamp on this kernel" {
		t.Errorf("limit keeps the newest: %+v", lines)
	}
	if lines, _ := d.Read(LogQuery{Contains: "MMC0"}); len(lines) != 1 {
		t.Errorf("text: %+v", lines)
	}
	if lines, _ := d.Read(LogQuery{Tag: "rfd"}); len(lines) != 0 {
		t.Errorf("tag: %+v", lines)
	}
	// this boot by its id is the ring buffer; another boot has no lines, and dmesg is not run
	if lines, _ := d.Read(LogQuery{Boot: "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d"}); len(lines) != 6 {
		t.Errorf("this boot by id: %d", len(lines))
	}
	n := len(calls)
	if lines, err := d.Read(LogQuery{Boot: "-1"}); err != nil || len(lines) != 0 || len(calls) != n {
		t.Errorf("an earlier boot: %+v %v %d", lines, err, len(calls))
	}

	var got []LogLine
	if err := d.Export(context.Background(), LogQuery{Severity: "err"}, func(l LogLine) error { got = append(got, l); return nil }); err != nil || len(got) != 1 {
		t.Errorf("export: %v %+v", err, got)
	}
	stop := errors.New("stop")
	count := 0
	if err := d.Export(context.Background(), LogQuery{}, func(LogLine) error { count++; return stop }); !errors.Is(err, stop) || count != 1 {
		t.Errorf("an emit error stops the export: %v %d", err, count)
	}

	d.Run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("operation not permitted")
	}
	if _, err := d.Read(LogQuery{}); err == nil || !strings.HasPrefix(err.Error(), "dmesg: ") {
		t.Errorf("a refused dmesg: %v", err)
	}
}

// task 93: the kernel log and a boot go to journalctl as -k and --boot=, the latter in its
// one-word form so an offset is not read as an option
func TestJournalArgsKernelAndBoot(t *testing.T) {
	j := JournalLog{}
	for _, tc := range []struct {
		q    LogQuery
		want string
	}{
		{LogQuery{Kernel: true, Limit: 500}, "-o json --no-pager -q -n 500 -k"},
		{LogQuery{Kernel: true, Boot: "-1", Severity: "warning", Limit: 10}, "-o json --no-pager -q -n 10 -p 4 -k --boot=-1"},
		{LogQuery{Unit: "rfd", Boot: "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d", Limit: 5}, "-o json --no-pager -q -n 5 -u rfd.service --boot=4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d"},
		{LogQuery{Kernel: true, Follow: true}, "-o json --no-pager -q -n 0 -k -f"},
		// the System source: the other transports, after every option
		{LogQuery{NoKernel: true, Unit: "rfd", Severity: "err", Follow: true}, "-o json --no-pager -q -n 0 -u rfd.service -p 3 -f _TRANSPORT=journal _TRANSPORT=stdout _TRANSPORT=syslog _TRANSPORT=driver _TRANSPORT=audit"},
		// both at once is the kernel's
		{LogQuery{NoKernel: true, Kernel: true, Limit: 1}, "-o json --no-pager -q -n 1 -k"},
	} {
		if got := strings.Join(j.args(tc.q), " "); got != tc.want {
			t.Errorf("%+v:\n got %s\nwant %s", tc.q, got, tc.want)
		}
	}
}

func TestParseJournalLineMonotonic(t *testing.T) {
	// a kernel line: the kernel's own stamp, not the moment journald read it
	l, ok := ParseJournalLine([]byte(`{"MESSAGE":"mmc0: timeout","PRIORITY":"3","SYSLOG_IDENTIFIER":"kernel","_TRANSPORT":"kernel","__REALTIME_TIMESTAMP":"1789236000000000","__MONOTONIC_TIMESTAMP":"15123456","_SOURCE_MONOTONIC_TIMESTAMP":"12500001"}`))
	if !ok || l.MonotonicUS != 12_500_001 || l.Tag != "kernel" || l.Severity != "err" {
		t.Errorf("kernel: %+v", l)
	}
	l, _ = ParseJournalLine([]byte(`{"MESSAGE":"started","__MONOTONIC_TIMESTAMP":"88608376"}`))
	if l.MonotonicUS != 88_608_376 {
		t.Errorf("journald's stamp: %+v", l)
	}
	l, _ = ParseJournalLine([]byte(`{"MESSAGE":"old entry"}`))
	if l.MonotonicUS != 0 {
		t.Errorf("none: %+v", l)
	}
	// B-116: the kernel's first messages carry a source stamp of 0, and 0 is their stamp - not
	// journald's receive time
	l, _ = ParseJournalLine([]byte(`{"MESSAGE":"Booting Linux","_TRANSPORT":"kernel","__MONOTONIC_TIMESTAMP":"8803370","_SOURCE_MONOTONIC_TIMESTAMP":"0"}`))
	if l.MonotonicUS != 0 {
		t.Errorf("a source stamp of 0: %+v", l)
	}
}

// B-116: the Pi 4's first kernel lines, captured 2026-09-13 00:27 with 1.0.0-alpha.0-snapshot.69e14ddaa
// (testdata/boots/kernel-entries-rpi4-early.jsonl, the machine id taken out): two at [0.000000] and
// [0.000239] as dmesg prints them, and PID 1's message to /dev/kmsg at [7.207404]. journald read
// them 8.8 s into the boot, while the clock still said 13 March; the box's uptime was 3532.42 s at
// 00:27:19.
func TestJournalKernelWallClock(t *testing.T) {
	entries, err := os.ReadFile("testdata/boots/kernel-entries-rpi4-early.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	const pi4Boot = "49d593c957f24a0cb6f3d89e2f2c137e"
	cest := time.FixedZone("CEST", 2*3600)
	now := time.Date(2026, 9, 13, 0, 27, 19, 0, cest)
	root := func(bootID string) Root {
		d := t.TempDir()
		for p, s := range map[string]string{"proc/uptime": "3532.42 13804.51\n", "proc/sys/kernel/random/boot_id": bootID + "\n"} {
			if err := os.MkdirAll(filepath.Join(d, filepath.Dir(p)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(d, p), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return Root(d)
	}
	// the answer to the kernel query, and to the last entry of a boot by its id
	var calls []string
	run := func(last string, lastErr error) Runner {
		return func(_ context.Context, name string, args ...string) ([]byte, error) {
			joined := strings.Join(args, " ")
			calls = append(calls, joined)
			if strings.Contains(joined, "--boot="+pi4Boot) && strings.Contains(joined, "-n 1 ") {
				return []byte(last), lastErr
			}
			return entries, nil
		}
	}
	wantMono := []int64{0, 239, 7_207_404}
	stamps := func(j JournalLog) []LogLine {
		t.Helper()
		calls = nil
		lines, err := j.Read(LogQuery{Kernel: true})
		if err != nil || len(lines) != 3 {
			t.Fatalf("%v %d", err, len(lines))
		}
		for i, l := range lines {
			if l.MonotonicUS != wantMono[i] {
				t.Errorf("line %d: monotonic %d, want %d", i, l.MonotonicUS, wantMono[i])
			}
		}
		return lines
	}
	at := func(l LogLine) time.Time {
		t.Helper()
		ts, err := time.Parse(time.RFC3339Nano, l.Timestamp)
		if err != nil {
			t.Fatalf("%q: %v", l.Timestamp, err)
		}
		return ts
	}

	// this boot: it began an uptime ago, and no further journalctl is asked
	lines := stamps(JournalLog{Root: root("49d593c9-57f2-4a0c-b6f3-d89e2f2c137e"), Now: func() time.Time { return now }, Run: run("", nil)})
	start := now.Add(-time.Duration(3532.42 * float64(time.Second)))
	for i, l := range lines {
		if want := start.Add(time.Duration(wantMono[i]) * time.Microsecond); !at(l).Equal(want) {
			t.Errorf("this boot, line %d: %s, want %s", i, l.Timestamp, want.Format(time.RFC3339Nano))
		}
	}
	if at(lines[0]).Year() != 2026 || at(lines[0]).Month() != time.September || len(calls) != 1 {
		t.Errorf("this boot: %s, %d calls %q", lines[0].Timestamp, len(calls), calls)
	}

	// an earlier boot: its last entry's time less its monotonic stamp, looked up once for the three
	// lines by the boot's id (23:26:05.742975 at 3531 s into that boot)
	last := `{"__REALTIME_TIMESTAMP":"1789248365742975","__MONOTONIC_TIMESTAMP":"3531000000","MESSAGE":"x"}`
	lines = stamps(JournalLog{Root: root("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }, Run: run(last, nil)})
	earlier := time.UnixMicro(1789248365742975 - 3531000000)
	for i, l := range lines {
		if want := earlier.Add(time.Duration(wantMono[i]) * time.Microsecond); !at(l).Equal(want) {
			t.Errorf("earlier boot, line %d: %s, want %s", i, l.Timestamp, want.Format(time.RFC3339Nano))
		}
	}
	if len(calls) != 2 || calls[1] != "-o json --no-pager -q -n 1 --boot="+pi4Boot {
		t.Errorf("earlier boot's lookup: %q", calls)
	}

	// the lookup fails, or no Root: journald's time stays, the stamps are still the kernel's
	journald := time.UnixMicro(1773421710084650)
	for name, j := range map[string]JournalLog{
		"lookup failed": {Root: root("0123456789abcdef0123456789abcdef"), Now: func() time.Time { return now }, Run: run("", errors.New("exit status 1"))},
		"no Root":       {Run: run("", nil)},
	} {
		lines = stamps(j)
		if !at(lines[0]).Equal(journald) {
			t.Errorf("%s: %s", name, lines[0].Timestamp)
		}
	}
}
