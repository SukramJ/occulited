package httpapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// recordingLog is a log reader and exporter that records every query and answers with what lines
// makes of it.
type recordingLog struct {
	mu    sync.Mutex
	got   []system.LogQuery
	lines func(q system.LogQuery) []system.LogLine
}

func (f *recordingLog) record(q system.LogQuery) []system.LogLine {
	f.mu.Lock()
	f.got = append(f.got, q)
	f.mu.Unlock()
	if f.lines == nil {
		return []system.LogLine{}
	}
	return f.lines(q)
}

func (f *recordingLog) queries() []system.LogQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]system.LogQuery(nil), f.got...)
}

func (f *recordingLog) Read(q system.LogQuery) ([]system.LogLine, error) { return f.record(q), nil }

func (f *recordingLog) Export(_ context.Context, q system.LogQuery, emit func(system.LogLine) error) error {
	for _, l := range f.record(q) {
		if err := emit(l); err != nil {
			return err
		}
	}
	return nil
}

func writeRootFile(t *testing.T, r system.Root, p, content string) {
	t.Helper()
	full := filepath.Join(string(r), p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const thisBoot = "4c1d2a6b0e8f4a2b9c3d5e7f8a1b2c3d"

func TestLogKernelAndBootReachTheJournal(t *testing.T) {
	journal := &recordingLog{lines: func(system.LogQuery) []system.LogLine {
		return []system.LogLine{{Tag: "kernel", Message: "mmc0: timeout", MonotonicUS: 12_500_001}}
	}}
	// the journal's boots: an offset reaches the reader as the id listed at it (B-114)
	const previous, beforeThat = "7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d", "0f1e2d3c4b5a69788796a5b4c3d2e1f0"
	boots := `[{"index":-2,"boot_id":"` + beforeThat + `"},{"index":-1,"boot_id":"` + previous + `"},{"index":0,"boot_id":"` + thisBoot + `"}]`
	srv := logDownloadServer(t, &SystemAPI{Root: logRoot(t), Log: journal, Journal: &system.JournalLog{Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if len(args) == 0 || args[0] != "--list-boots" {
			return nil, errors.New("only the boot list is asked")
		}
		return []byte(boots), nil
	}}})

	st, out, body := do(t, srv, "GET", "/api/system/v1/log?kernel=1&boot=4C1D2A6B-0E8F-4A2B-9C3D-5E7F8A1B2C3D&severity=warning&limit=50", "", nil)
	if st != 200 || out["source"] != "journald" || !strings.Contains(body, `"monotonic_us":12500001`) {
		t.Fatalf("%d %s", st, body)
	}
	q := journal.queries()[0]
	if !q.Kernel || q.Boot != thisBoot || q.Severity != "warning" || q.Limit != 50 {
		t.Errorf("query: %+v", q)
	}
	// the journal view of an earlier boot, and no kernel flag without kernel=1
	do(t, srv, "GET", "/api/system/v1/log?boot=-1&unit=rfd", "", nil)
	if q := journal.queries()[1]; q.Kernel || q.Boot != previous || q.Unit != "rfd" {
		t.Errorf("journal query: %+v", q)
	}
	// the download takes the same two, and names the boot by its id
	res, _ := fetch(t, srv, "/api/system/v1/log/download?kernel=1&boot=-2", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "-kernel-boot-0f1e2d3c-") {
		t.Errorf("download: %d %s", res.StatusCode, res.Header.Get("Content-Disposition"))
	}
	if q := journal.queries()[2]; !q.Kernel || q.Boot != beforeThat {
		t.Errorf("download query: %+v", q)
	}
	// kernel=0 is everything but the kernel: the Log page's System source, in the file name too
	do(t, srv, "GET", "/api/system/v1/log?kernel=0", "", nil)
	if q := journal.queries()[3]; q.Kernel || !q.NoKernel {
		t.Errorf("system query: %+v", q)
	}
	res, _ = fetch(t, srv, "/api/system/v1/log/download?kernel=false", nil)
	if !strings.Contains(res.Header.Get("Content-Disposition"), `"openccu-lite-system-2`) {
		t.Errorf("system download: %s", res.Header.Get("Content-Disposition"))
	}
}

func TestLogRefusesABoot(t *testing.T) {
	journal := &recordingLog{}
	srv := logDownloadServer(t, &SystemAPI{Root: logRoot(t), Log: journal, Journal: &system.JournalLog{Run: func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("journalctl must not run")
	}}})
	for _, path := range []string{
		"/api/system/v1/log?boot=1",
		"/api/system/v1/log?boot=all",
		"/api/system/v1/log?boot=-1%20--since=today",
		"/api/system/v1/log/stream?boot=x",
		"/api/system/v1/log/download?boot=%3Brm",
	} {
		st, out, body := do(t, srv, "GET", path, "", nil)
		if st != 422 || out["error"] != "invalid" || !strings.Contains(body, "boot is a boot id") {
			t.Errorf("%s: %d %s", path, st, body)
		}
	}
	if n := len(journal.queries()); n != 0 {
		t.Errorf("a refused boot reached the reader %d times", n)
	}
}

func TestLogKernelWithoutAJournalIsDmesg(t *testing.T) {
	syslog := &recordingLog{lines: func(system.LogQuery) []system.LogLine { return []system.LogLine{{Tag: "rfd", Message: "syslog"}} }}
	ring := &recordingLog{lines: func(system.LogQuery) []system.LogLine { return []system.LogLine{{Tag: "kernel", Message: "ring"}} }}
	srv := logDownloadServer(t, &SystemAPI{Root: logRoot(t), Log: syslog, Dmesg: ring})

	st, out, body := do(t, srv, "GET", "/api/system/v1/log?kernel=1&q=mmc", "", nil)
	if st != 200 || out["source"] != "dmesg" || !strings.Contains(body, `"ring"`) {
		t.Fatalf("%d %s", st, body)
	}
	if q := ring.queries()[0]; !q.Kernel || q.Contains != "mmc" {
		t.Errorf("dmesg query: %+v", q)
	}
	if st, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); st != 200 || out["source"] != "syslog" || len(syslog.queries()) != 1 {
		t.Errorf("the log stays syslog: %d %v", st, out)
	}
	// the download of the kernel log is the ring buffer, and busybox names no boot
	res, text := fetch(t, srv, "/api/system/v1/log/download?kernel=1&boot=-1", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), `"openccu-lite-kernel-2`) || !strings.Contains(text, "ring") {
		t.Errorf("download: %d %s %q", res.StatusCode, res.Header.Get("Content-Disposition"), text)
	}
	// no live stream without a journal, kernel or not
	if st, _, _ := do(t, srv, "GET", "/api/system/v1/log/stream?kernel=1", "", nil); st != 501 {
		t.Errorf("stream: %d", st)
	}
}

func TestLogKernelFallsBackToDmesgInAContainer(t *testing.T) {
	r := logRoot(t)
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", "4c1d2a6b-0e8f-4a2b-9c3d-5e7f8a1b2c3d\n")
	var kernelLines []system.LogLine // what the journal holds of the kernel transport
	journal := &recordingLog{lines: func(q system.LogQuery) []system.LogLine {
		if q.Contains != "" {
			return nil
		}
		return kernelLines
	}}
	ring := &recordingLog{lines: func(system.LogQuery) []system.LogLine { return []system.LogLine{{Tag: "kernel", Message: "ring"}} }}
	// the boot list cannot be read here, so an offset stays one (B-114)
	noList := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no boot list") }
	srv := logDownloadServer(t, &SystemAPI{Root: r, Log: journal, Journal: &system.JournalLog{Run: noList}, Dmesg: ring})

	// a container: journald has no kernel line of this boot at all
	for _, path := range []string{"/api/system/v1/log?kernel=1", "/api/system/v1/log?kernel=1&boot=0", "/api/system/v1/log?kernel=1&boot=" + thisBoot} {
		if st, out, body := do(t, srv, "GET", path, "", nil); st != 200 || out["source"] != "dmesg" || !strings.Contains(body, "ring") {
			t.Errorf("%s: %d %s", path, st, body)
		}
	}
	// an earlier boot is not in the ring buffer: the journal's empty answer stands
	n := len(ring.queries())
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log?kernel=1&boot=-1", "", nil); out["source"] != "journald" || len(ring.queries()) != n {
		t.Errorf("earlier boot: %v", out)
	}
	// a box whose journal has kernel lines, and a filter that matched none of them: no fallback
	kernelLines = []system.LogLine{{Tag: "kernel", Message: "journal"}}
	if _, out, body := do(t, srv, "GET", "/api/system/v1/log?kernel=1&q=nothing", "", nil); out["source"] != "journald" || strings.Contains(body, "ring") {
		t.Errorf("filtered: %s", body)
	}
	if len(ring.queries()) != n {
		t.Error("dmesg was asked although the journal holds kernel lines")
	}
	// the journal view never falls back
	kernelLines = nil
	if _, out, _ := do(t, srv, "GET", "/api/system/v1/log", "", nil); out["source"] != "journald" {
		t.Errorf("journal view: %v", out)
	}
}

func TestLogFileNameKernelAndBoot(t *testing.T) {
	now := time.Date(2026, 9, 12, 19, 30, 0, 0, time.Local)
	for _, tc := range []struct {
		q      system.LogQuery
		ranged bool
		want   string
	}{
		{system.LogQuery{Kernel: true}, true, "box-kernel-2026-09-12T1930.txt"},
		{system.LogQuery{Kernel: true, Boot: thisBoot}, true, "box-kernel-boot-4c1d2a6b-2026-09-12T1930.txt"},
		{system.LogQuery{Boot: "-1", Unit: "rfd"}, true, "box-log-boot-minus1-2026-09-12T1930.txt"},
		{system.LogQuery{Boot: "0"}, true, "box-log-2026-09-12T1930.txt"},
		{system.LogQuery{Kernel: true, Boot: "-1"}, false, "box-kernel-2026-09-12T1930.txt"},
	} {
		if got := logFileName("box", tc.q, tc.ranged, now, "txt"); got != tc.want {
			t.Errorf("%+v: %s, want %s", tc.q, got, tc.want)
		}
	}
}

func TestBootsRoute(t *testing.T) {
	r := logRoot(t)
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", "4c1d2a6b-0e8f-4a2b-9c3d-5e7f8a1b2c3d\n")
	writeRootFile(t, r, "proc/uptime", "3600.00 7000.00\n")
	writeRootFile(t, r, "proc/self/mountinfo", "40 25 179:3 /var/log/journal /var/log/journal rw,relatime - ext4 /dev/sda3 rw\n")
	calls := 0
	answer := []byte(`[{"index":-1,"boot_id":"7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d","first_entry":1757533290000000,"last_entry":1757660400000000},{"index":0,"boot_id":"` + thisBoot + `","first_entry":1757660460000000,"last_entry":1757692800000000}]`)
	// B-114: the earlier boot's last entry says it began at 1757533200000000, before its first entry,
	// so the journal's time stays
	lastEntry := []byte(`{"__REALTIME_TIMESTAMP":"1757660400000000","__MONOTONIC_TIMESTAMP":"127200000000"}`)
	var fail error
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "journalctl" && strings.Join(args, " ") == "-o json --no-pager -q -n 1 --boot=7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d" {
			return lastEntry, nil
		}
		calls++
		if name != "journalctl" || strings.Join(args, " ") != "--list-boots -o json --no-pager -q" {
			t.Errorf("%s %v", name, args)
		}
		return answer, fail
	}
	api := &SystemAPI{Root: r, Log: &recordingLog{}, Journal: &system.JournalLog{Run: run}}
	srv := logDownloadServer(t, api)

	st, out, body := do(t, srv, "GET", "/api/system/v1/boots", "", nil)
	boots, _ := out["boots"].([]any)
	if st != 200 || out["source"] != "journald" || out["persistent"] != true || out["current"] != thisBoot || len(boots) != 2 {
		t.Fatalf("%d %s", st, body)
	}
	newest, older := boots[0].(map[string]any), boots[1].(map[string]any)
	if newest["boot_id"] != thisBoot || newest["current"] != true || newest["index"] != 0.0 || older["current"] != false || older["index"] != -1.0 || older["first"] == "" {
		t.Errorf("order and marks: %s", body)
	}
	// the running boot began an uptime ago, whatever time its first entry carries
	if began, err := time.Parse(time.RFC3339, newest["first"].(string)); err != nil || time.Since(began) < 59*time.Minute || time.Since(began) > 61*time.Minute {
		t.Errorf("this boot's start %v %v", newest["first"], err)
	}
	if older["first"] != time.UnixMicro(1757533290000000).Format(time.RFC3339) {
		t.Errorf("an earlier boot's start is the journal's: %v", older["first"])
	}
	// asked again at once: the list is kept
	do(t, srv, "GET", "/api/system/v1/boots", "", nil)
	if calls != 1 {
		t.Errorf("journalctl ran %d times", calls)
	}

	// a journal that has not written this boot yet: the running boot leads the list
	api.bootList = bootsCache{}
	answer = []byte(`[{"index":0,"boot_id":"7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d","first_entry":1757533290000000}]`)
	_, out, body = do(t, srv, "GET", "/api/system/v1/boots", "", nil)
	if boots, _ := out["boots"].([]any); len(boots) != 2 || boots[0].(map[string]any)["boot_id"] != thisBoot || boots[0].(map[string]any)["current"] != true || boots[1].(map[string]any)["current"] != false {
		t.Errorf("this boot first: %s", body)
	}

	// journalctl fails: this boot alone, and why
	api.bootList = bootsCache{}
	fail = errors.New("exit status 1")
	_, out, body = do(t, srv, "GET", "/api/system/v1/boots", "", nil)
	if boots, _ := out["boots"].([]any); len(boots) != 1 || !strings.Contains(body, "exit status 1") || boots[0].(map[string]any)["current"] != true {
		t.Errorf("failure: %s", body)
	}

	// busybox: this boot alone, no journal to hold another
	bb := logDownloadServer(t, &SystemAPI{Root: r, Log: &recordingLog{}})
	_, out, body = do(t, bb, "GET", "/api/system/v1/boots", "", nil)
	boots, _ = out["boots"].([]any)
	if out["source"] != "syslog" || out["persistent"] != false || len(boots) != 1 {
		t.Fatalf("busybox: %s", body)
	}
	if b := boots[0].(map[string]any); b["boot_id"] != thisBoot || b["current"] != true || b["first"] == nil {
		t.Errorf("busybox boot: %s", body)
	}
}

// B-114: the Pi 4 in ram-sync after a trim, 2026-09-12 23:26 (the ids completed from the report's
// prefixes): --list-boots named the previous boot at -1, yet `journalctl -b -1` found none - the
// running boot's first entry carried the image's date, 13 March, older than both boots before it; by
// id the lines were there. /log and the download name a boot by the id the list gives for an offset;
// an offset the list does not hold, or a list that cannot be read, stays as it came. GET /boots dates
// an earlier boot whose first entry carries the image's date by its last entry, once per boot.
func TestLogEarlierBootByID(t *testing.T) {
	const (
		oldest   = "f2f55505a1b2c3d4e5f60718293a4b5c"
		previous = "4ca2723bd4e5f60718293a4b5c6d7e8f"
		running  = "de7258d5c3d4e5f60718293a4b5c6d7e"
	)
	cest := time.FixedZone("CEST", 2*3600)
	at := func(h, m, s int) time.Time { return time.Date(2026, 9, 12, h, m, s, 0, cest) }
	image := time.Date(2026, 3, 13, 18, 8, 30, 0, time.FixedZone("CET", 3600))
	entry := func(index int, id string, first, last time.Time) string {
		return `{"index":` + strconv.Itoa(index) + `,"boot_id":"` + id + `","first_entry":` + strconv.FormatInt(first.UnixMicro(), 10) + `,"last_entry":` + strconv.FormatInt(last.UnixMicro(), 10) + `}`
	}
	list := "[" + entry(-2, oldest, at(23, 13, 39), at(23, 18, 1)) + "," + entry(-1, previous, at(23, 18, 46), at(23, 22, 56)) + "," + entry(0, running, image, at(23, 26, 5)) + "]"
	var listErr error
	lookups := map[string]int{}
	lastEntries := map[string]string{} // a boot's last entry, by id
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if joined == "--list-boots -o json --no-pager -q" {
			return []byte(list), listErr
		}
		if id, ok := strings.CutPrefix(joined, "-o json --no-pager -q -n 1 --boot="); ok {
			lookups[id]++
			if e, ok := lastEntries[id]; ok {
				return []byte(e), nil
			}
			return nil, errors.New("exit status 1")
		}
		t.Errorf("journalctl %s", joined)
		return nil, errors.New("unexpected")
	}
	r := logRoot(t)
	writeRootFile(t, r, "proc/sys/kernel/random/boot_id", running+"\n")
	journal := &recordingLog{}
	api := &SystemAPI{Root: r, Log: journal, Journal: &system.JournalLog{Run: run}}
	srv := logDownloadServer(t, api)

	for _, tc := range []struct{ path, want string }{
		{"/api/system/v1/log?boot=-1", previous},
		{"/api/system/v1/log?boot=-2&kernel=1", oldest},
		{"/api/system/v1/log?boot=0", "0"},
		{"/api/system/v1/log?boot=" + previous, previous},
		// not in the list: journalctl answers for itself
		{"/api/system/v1/log?boot=-3", "-3"},
	} {
		n := len(journal.queries())
		if st, _, body := do(t, srv, "GET", tc.path, "", nil); st != 200 {
			t.Fatalf("%s: %d %s", tc.path, st, body)
		}
		if q := journal.queries()[n]; q.Boot != tc.want {
			t.Errorf("%s: boot %q, want %q", tc.path, q.Boot, tc.want)
		}
	}
	res, _ := fetch(t, srv, "/api/system/v1/log/download?boot=-1", nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "-log-boot-4ca2723b-") {
		t.Errorf("download: %d %s", res.StatusCode, res.Header.Get("Content-Disposition"))
	}

	// a list that cannot be read: the offset stays
	api.bootList = bootsCache{}
	listErr = errors.New("exit status 1")
	n := len(journal.queries())
	do(t, srv, "GET", "/api/system/v1/log?boot=-1", "", nil)
	if q := journal.queries()[n]; q.Boot != "-1" {
		t.Errorf("without a list: boot %q", q.Boot)
	}

	// GET /boots: the oldest boot's first entry carries the image's date too; its last entry, 262 s
	// into it at 23:18:01, says it began at 23:13:39. The previous boot's lookup fails and its time
	// stays. Asked twice, each boot is looked up once.
	api.bootList = bootsCache{}
	listErr = nil
	list = "[" + entry(-2, oldest, image, at(23, 18, 1)) + "," + entry(-1, previous, at(23, 18, 46), at(23, 22, 56)) + "," + entry(0, running, image, at(23, 26, 5)) + "]"
	lastEntries[oldest] = `{"__REALTIME_TIMESTAMP":"` + strconv.FormatInt(at(23, 18, 1).UnixMicro(), 10) + `","__MONOTONIC_TIMESTAMP":"262000000"}`
	for range 2 {
		_, out, body := do(t, srv, "GET", "/api/system/v1/boots", "", nil)
		boots, _ := out["boots"].([]any)
		if len(boots) != 3 {
			t.Fatalf("%s", body)
		}
		first := map[string]string{}
		for _, b := range boots {
			m := b.(map[string]any)
			f, _ := m["first"].(string)
			first[m["boot_id"].(string)] = f
		}
		for id, want := range map[string]time.Time{oldest: at(23, 13, 39), previous: at(23, 18, 46)} {
			if got, err := time.Parse(time.RFC3339, first[id]); err != nil || !got.Equal(want) {
				t.Errorf("%s began %q, want %s", id[:8], first[id], want)
			}
		}
	}
	if lookups[oldest] != 1 || lookups[previous] != 1 || lookups[running] != 0 {
		t.Errorf("lookups: %v", lookups)
	}
}
