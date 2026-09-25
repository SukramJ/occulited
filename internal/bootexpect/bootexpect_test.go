package bootexpect

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func TestDefaultsTable(t *testing.T) {
	if _, err := parseDefaults(defaultsJSON); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ platform, want string }{
		{"rpi4", "rpi4"}, {"RPI3", "rpi3"}, {" ova ", "ova"}, {"rpi5", "rpi5"},
		{"ccu3", "rpi4"}, {"generic-x86_64", "rpi4"}, {"", "rpi4"},
	} {
		if got := ProductOf(c.platform); got != c.want {
			t.Errorf("ProductOf(%q) = %q, want %q", c.platform, got, c.want)
		}
	}
	// the Pi 5's reboot is a guess: 0.6 of the Pi 4, rounded
	p4, p5 := Default("rpi4", KindReboot), Default("rpi5", KindReboot)
	for name, pair := range map[string][2]float64{"down": {p4.Down, p5.Down}, "http": {p4.HTTP, p5.HTTP}, "ui": {p4.UI, p5.UI}, "ready": {p4.Ready, p5.Ready}} {
		if want := math.Round(pair[0] * 0.6); pair[1] != want {
			t.Errorf("rpi5 reboot %s = %v, want %v", name, pair[1], want)
		}
	}
	// an update and a restore take the install time on top of the reboot's http, and are the same
	// as the reboot otherwise
	for product, extra := range map[string]float64{"rpi3": 620, "rpi4": 340, "ova": 85, "rpi5": 205} {
		if Default(product, KindUpdate) != Default(product, KindRestore) {
			t.Errorf("%s: update and restore differ", product)
		}
		if u, r := Default(product, KindUpdate), Default(product, KindReboot); u.Down != r.Down || u.UI != r.UI || u.Ready != r.Ready {
			t.Errorf("%s: an update differs from the reboot beyond http", product)
		}
		for _, k := range []string{KindUpdate, KindRestore} {
			if got, want := Default(product, k).HTTP, Default(product, KindReboot).HTTP+extra; got != want {
				t.Errorf("%s %s http = %v, want %v", product, k, got, want)
			}
		}
	}
	// recovery and halt count with the reboot's figures
	if Default("ova", KindRecovery) != Default("ova", KindReboot) || Default("ova", KindHalt) != Default("ova", KindReboot) {
		t.Error("recovery and halt must use the reboot's figures")
	}
	for _, bad := range []string{
		`{"default":"x","products":{"rpi4":{}}}`,
		`{"default":"rpi4","products":{"rpi4":{"reboot":{},"update":{}}}}`,
		`{"default":"rpi4","products":{"rpi4":{"reboot":{"down":-1},"update":{},"restore":{}}}}`,
		`not json`,
	} {
		if _, err := parseDefaults([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestMedian(t *testing.T) {
	for _, c := range []struct {
		in   []float64
		want float64
	}{
		{[]float64{5}, 5},
		{[]float64{9, 5}, 7},
		{[]float64{9, 1, 5}, 5},
		{[]float64{30, 31, 90}, 31},
	} {
		if got := median(c.in); got != c.want {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBrowserValidate(t *testing.T) {
	const s = 1_800_000_000_000
	for _, c := range []struct {
		name string
		b    Browser
		ok   bool
	}{
		{"all four", Browser{Started: s, Down: s + 8000, HTTP: s + 38000, UI: s + 40000, Ready: s + 90000}, true},
		{"only ui", Browser{Started: s, UI: s + 40000}, true},
		{"equal times", Browser{Started: s, Down: s, HTTP: s, UI: s}, true},
		{"no start", Browser{Down: s}, false},
		{"no checkpoint", Browser{Started: s, Ready: s + 1}, false},
		{"http before down", Browser{Started: s, Down: s + 9000, HTTP: s + 8000}, false},
		{"down before the start", Browser{Started: s, Down: s - 1}, false},
		{"negative", Browser{Started: s, Down: -5, UI: s + 1}, false},
		{"over an hour", Browser{Started: s, UI: s + time.Hour.Milliseconds() + 1}, false},
		{"ready over an hour", Browser{Started: s, UI: s + 1000, Ready: s + 2*time.Hour.Milliseconds()}, false},
	} {
		if err := c.b.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func TestEstimate(t *testing.T) {
	def := Expect{Down: 8, HTTP: 30, UI: 2, Ready: 50}
	const s = 1_800_000_000_000
	boxOnly := func(toKernel, http, ui, ready float64) Record {
		return Record{ToKernelS: toKernel, HTTPMono: f(http), UIMono: f(ui), ReadyMono: f(ready)}
	}
	withBrowser := func(r Record, down int64) Record {
		r.Browser = &Browser{Started: s, Down: s + down, UI: s + 60000}
		return r
	}
	cases := []struct {
		name     string
		recs     []Record
		want     Expect
		measured Measured
	}{
		{"nothing recorded: the default", nil, def, Measured{}},
		// 12 s to the kernel and lighttpd 20 s after it: 32 s, of which the default 8 are the shutdown
		{"the box alone", []Record{boxOnly(12, 20, 21, 60)}, Expect{Down: 8, HTTP: 24, UI: 1, Ready: 39}, Measured{HTTP: 1, UI: 1, Ready: 1}},
		// the browser saw the box go after 5 s, so 27 of the 32 s were the boot
		{"with the browser's down", []Record{withBrowser(boxOnly(12, 20, 21, 60), 5000)}, Expect{Down: 5, HTTP: 27, UI: 1, Ready: 39}, Measured{Down: 1, HTTP: 1, UI: 1, Ready: 1}},
		{"occulited before lighttpd: ui is 0, ready counts from lighttpd", []Record{boxOnly(10, 25, 22, 55)}, Expect{Down: 8, HTTP: 27, UI: 0, Ready: 30}, Measured{HTTP: 1, UI: 1, Ready: 1}},
		{"the median of three", []Record{boxOnly(10, 20, 22, 60), boxOnly(10, 30, 31, 90), boxOnly(10, 22, 23, 70)}, Expect{Down: 8, HTTP: 24, UI: 1, Ready: 47}, Measured{HTTP: 3, UI: 3, Ready: 3}},
		{"only the newest three count", []Record{boxOnly(100, 100, 101, 200), boxOnly(10, 20, 22, 60), boxOnly(10, 30, 31, 90), boxOnly(10, 22, 23, 70)}, Expect{Down: 8, HTTP: 24, UI: 1, Ready: 47}, Measured{HTTP: 3, UI: 3, Ready: 3}},
		{"two records: the mean of both", []Record{boxOnly(10, 20, 22, 60), boxOnly(10, 30, 31, 90)}, Expect{Down: 8, HTTP: 27, UI: 1.5, Ready: 48.5}, Measured{HTTP: 2, UI: 2, Ready: 2}},
		{"no interfaces stamp: ready stays the default", []Record{{ToKernelS: 10, HTTPMono: f(20), UIMono: f(21)}}, Expect{Down: 8, HTTP: 22, UI: 1, Ready: 50}, Measured{HTTP: 1, UI: 1}},
		{"a box without systemd: the browser's checkpoints", []Record{{ToKernelS: 10, UIMono: f(30), Browser: &Browser{Started: s, Down: s + 6000, HTTP: s + 40000, UI: s + 43000, Ready: s + 83000}}}, Expect{Down: 6, HTTP: 34, UI: 3, Ready: 40}, Measured{Down: 1, HTTP: 1, UI: 1, Ready: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, m := Estimate(def, c.recs)
			if got != c.want || m != c.measured {
				t.Fatalf("got %+v %+v, want %+v %+v", got, m, c.want, c.measured)
			}
		})
	}
}

// rig is a box: a root with /proc and /run, a state directory, a clock and systemctl's answers.
type rig struct {
	t     *testing.T
	root  string
	state string
	now   time.Time
	mu    sync.Mutex
	units string // systemctl show's output
	leap  string // chronyc's Leap status
	rec   *Recorder
}

func newRig(t *testing.T) *rig {
	t.Helper()
	g := &rig{t: t, root: t.TempDir(), state: t.TempDir(), now: time.UnixMilli(1_800_000_000_000), leap: "Not synchronised"}
	g.write("proc/sys/kernel/random/boot_id", "boot-1\n")
	g.write("proc/uptime", "300.00 900.00\n")
	g.rec = &Recorder{
		StateDir: g.state, Root: g.root, Product: "rpi4",
		Now: func() time.Time { g.mu.Lock(); defer g.mu.Unlock(); return g.now },
		Systemctl: func(_ context.Context, args ...string) ([]byte, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			if args[0] != "show" {
				t.Errorf("systemctl %v", args)
			}
			return []byte(g.units), nil
		},
		Chrony: func(context.Context) ([]byte, error) {
			g.mu.Lock()
			defer g.mu.Unlock()
			return []byte("Reference ID    : 7F7F0101 ()\nLeap status     : " + g.leap + "\n"), nil
		},
		Interval: time.Millisecond, Patience: 200 * time.Millisecond,
	}
	return g
}

func (g *rig) write(p, content string) {
	g.t.Helper()
	full := filepath.Join(g.root, p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		g.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		g.t.Fatal(err)
	}
}

func (g *rig) pending() *Pending {
	p, err := g.rec.readPending()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		g.t.Fatal(err)
	}
	return p
}

func (g *rig) records(kind string) []Record {
	b, err := os.ReadFile(filepath.Join(g.state, "boot-timings.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var file timingsFile
	if err := json.Unmarshal(b, &file); err != nil {
		g.t.Fatal(err)
	}
	return file.Records[kind]
}

// the box after the reboot: another boot id, 40 s up, the wall clock 50 s after the request
func (g *rig) reboot() {
	g.write("proc/sys/kernel/random/boot_id", "boot-2\n")
	g.write("proc/uptime", "40.00 100.00\n")
	g.mu.Lock()
	g.now = g.now.Add(50 * time.Second)
	g.mu.Unlock()
}

func unitBlock(id, active string, mono int64, extra ...string) string {
	return strings.Join(append([]string{"Id=" + id, "LoadState=loaded", "ActiveState=" + active, "UnitFileState=enabled", "ConditionResult=yes", "ActiveEnterTimestampMonotonic=" + itoa(mono)}, extra...), "\n") + "\n"
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

const readyUnits = "Id=lighttpd.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\nConditionResult=yes\nActiveEnterTimestampMonotonic=23000000\n\n" +
	"Id=rfd.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\nConditionResult=yes\nActiveEnterTimestampMonotonic=28000000\n\n" +
	"Id=hmipserver.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\nConditionResult=yes\nActiveEnterTimestampMonotonic=65500000\n"

func TestMarkerLifecycle(t *testing.T) {
	g := newRig(t)
	if err := g.rec.Mark("sideways"); err == nil {
		t.Fatal("an unknown kind was marked")
	}
	if err := g.rec.Mark(KindReboot); err != nil {
		t.Fatal(err)
	}
	p := g.pending()
	if p == nil || *p != (Pending{Kind: KindReboot, RequestWallMS: 1_800_000_000_000, BootID: "boot-1", Product: "rpi4"}) {
		t.Fatalf("marker %+v", p)
	}
	if st, err := os.Stat(filepath.Join(g.state, "boot-timing", "pending.json")); err != nil || st.Mode().Perm() != 0o640 {
		t.Fatalf("marker file: %v %v", st, err)
	}

	// occulited restarts in the same boot: the marker stays, nothing is recorded
	g.rec.Run(context.Background())
	if g.pending() == nil || g.records(KindReboot) != nil {
		t.Fatal("the same boot finished the marker")
	}

	// the reboot failed: Unmark
	g.rec.Unmark()
	if g.pending() != nil {
		t.Fatal("Unmark left the marker")
	}
	g.rec.Unmark() // twice is fine

	// the next boot
	if err := g.rec.Mark(KindReboot); err != nil {
		t.Fatal(err)
	}
	g.reboot()
	g.write("run/occulite/clock-state", "ntp\n")
	g.units = readyUnits
	g.write("proc/uptime", "24.50 50.00\n")
	g.rec.MarkListening()
	g.write("proc/uptime", "40.00 100.00\n")
	g.rec.Run(context.Background())
	if g.pending() != nil {
		t.Fatal("the marker survived its record")
	}
	recs := g.records(KindReboot)
	if len(recs) != 1 {
		t.Fatalf("records %+v", recs)
	}
	r := recs[0]
	// 50 s after the request with 40 s of uptime: the kernel started 10 s after the request
	if r.BootID != "boot-2" || r.ToKernelS != 10 || r.KernelStartWallMS != 1_800_000_010_000 || *r.HTTPMono != 23 || *r.UIMono != 24.5 || *r.ReadyMono != 65.5 {
		t.Fatalf("record %+v", r)
	}
	if r.Phases.DownHTTP == nil || *r.Phases.DownHTTP != 33 || *r.Phases.UI != 1.5 || *r.Phases.Ready != 41 || r.Phases.Down != nil {
		t.Fatalf("phases %+v", r.Phases)
	}
	a := g.rec.Expect(KindReboot)
	// the rpi4's default shutdown of 5 s taken off the 33 s to lighttpd
	if a.Product != "rpi4" || a.Expect != (Expect{Down: 5, HTTP: 28, UI: 1.5, Ready: 41}) || a.Measured != (Measured{HTTP: 1, UI: 1, Ready: 1}) || a.Default != Default("rpi4", KindReboot) {
		t.Fatalf("expect %+v", a)
	}
	// a halt counts with the reboot's records, an update has none
	if g.rec.Expect(KindHalt).Expect != a.Expect || g.rec.Expect(KindUpdate).Expect != Default("rpi4", KindUpdate) {
		t.Fatal("halt or update used the wrong records")
	}
	// another product's records do not count
	g.rec.Product = "ova"
	if g.rec.Expect(KindReboot).Expect != Default("ova", KindReboot) {
		t.Fatal("the rpi4's records counted on the ova")
	}
}

// A restore's marker gets a copy where the restore at boot does not reach (openccu-lite B-193):
// the restore deletes the state directory before it unpacks the backup, so the copy is what the
// next start finds - also after a restart inside the wait for the clock, which starts months
// behind on a box without an RTC.
func TestRestoreMarkerCarried(t *testing.T) {
	g := newRig(t)
	carry := filepath.Join(g.root, "usr/local/tmp/.occulite-boot-timing")
	removes := 0
	g.rec.Carry = &Carry{
		Path: carry,
		Place: func(b []byte) error {
			if err := os.MkdirAll(filepath.Dir(carry), 0o755); err != nil {
				return err
			}
			return os.WriteFile(carry, b, 0o600)
		},
		Remove: func() error {
			removes++
			if err := os.Remove(carry); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			return nil
		},
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	marker := filepath.Join(g.state, "boot-timing", "pending.json")

	// a reboot is not carried, and its Unmark asks the helper for nothing
	if err := g.rec.Mark(KindReboot); err != nil {
		t.Fatal(err)
	}
	if exists(carry) {
		t.Fatal("a reboot's marker was carried")
	}
	g.rec.Unmark()
	if removes != 0 {
		t.Fatal("Unmark asked for a copy that was never made")
	}

	// a restore is: the same marker in both places; the reboot that does not start takes both back
	if err := g.rec.Mark(KindRestore); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(marker)
	b, err := os.ReadFile(carry)
	if err != nil || string(a) != string(b) || len(b) == 0 {
		t.Fatalf("copy %q, marker %q (%v)", b, a, err)
	}
	g.rec.Unmark()
	if exists(marker) || exists(carry) || removes != 1 {
		t.Fatalf("after Unmark: marker %v copy %v removes %d", exists(marker), exists(carry), removes)
	}

	// the restore: the box reboots, S05CheckBackupRestore deletes the state directory
	if err := g.rec.Mark(KindRestore); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(g.state); err != nil {
		t.Fatal(err)
	}
	g.reboot()
	g.units = readyUnits
	// the clock is months behind, NTP has not answered, and occulited is restarted inside the wait
	g.mu.Lock()
	g.now = time.UnixMilli(1_780_000_000_000)
	g.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	g.rec.Chrony = func(context.Context) ([]byte, error) {
		cancel()
		return []byte("Leap status     : Not synchronised\n"), nil
	}
	g.rec.Patience = time.Hour
	g.rec.Run(ctx)
	if g.records(KindRestore) != nil || !exists(carry) || exists(marker) {
		t.Fatalf("the restart inside the wait: records %+v, copy %v, marker %v", g.records(KindRestore), exists(carry), exists(marker))
	}
	if p := g.pending(); p == nil || p.Kind != KindRestore || p.BootID != "boot-1" {
		t.Fatalf("the copy is not read as the marker: %+v", p)
	}

	// the next start, NTP answered: the record, from the copy
	prev := g.rec
	g.rec = &Recorder{StateDir: prev.StateDir, Root: prev.Root, Product: prev.Product, Now: prev.Now, Systemctl: prev.Systemctl, Interval: prev.Interval, Patience: prev.Patience, Carry: prev.Carry}
	g.mu.Lock()
	g.now = time.UnixMilli(1_800_000_050_000)
	g.leap = "Normal"
	g.mu.Unlock()
	g.rec.Chrony = func(context.Context) ([]byte, error) { return []byte("Leap status     : Normal\n"), nil }
	g.rec.MarkListening()
	g.rec.Run(context.Background())
	recs := g.records(KindRestore)
	if len(recs) != 1 {
		t.Fatalf("records %+v", recs)
	}
	if r := recs[0]; r.Kind != KindRestore || r.BootID != "boot-2" || r.ToKernelS != 10 || *r.HTTPMono != 23 || *r.UIMono != 40 || *r.ReadyMono != 65.5 {
		t.Fatalf("record %+v", r)
	}
	if exists(marker) || exists(carry) || removes != 2 {
		t.Fatalf("after the record: marker %v copy %v removes %d", exists(marker), exists(carry), removes)
	}

	// a copy that cannot be placed costs the measurement, not the marker
	g.rec.Carry.Place = func([]byte) error { return errors.New("helper down") }
	if err := g.rec.Mark(KindRestore); err != nil {
		t.Fatal(err)
	}
	if !exists(marker) || exists(carry) {
		t.Fatalf("marker %v copy %v", exists(marker), exists(carry))
	}
	g.rec.Unmark()
}

func TestOldMarkerOfTheSameBoot(t *testing.T) {
	g := newRig(t)
	if err := g.rec.Mark(KindReboot); err != nil {
		t.Fatal(err)
	}
	g.now = g.now.Add(61 * time.Minute)
	g.rec.Run(context.Background())
	if g.pending() != nil {
		t.Fatal("a marker of a reboot that never came survived")
	}
}

func TestKeepsThreePerKind(t *testing.T) {
	g := newRig(t)
	g.write("run/occulite/clock-state", "rtc")
	g.write("proc/uptime", "30.00 60.00\n")
	g.units = readyUnits
	for i := 0; i < 5; i++ {
		g.write("proc/sys/kernel/random/boot_id", "boot-a\n")
		if err := g.rec.Mark(KindUpdate); err != nil {
			t.Fatal(err)
		}
		g.write("proc/sys/kernel/random/boot_id", "boot-b\n")
		g.now = g.now.Add(time.Duration(60+i) * time.Second)
		g.rec.Run(context.Background())
	}
	recs := g.records(KindUpdate)
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	// the newest three, in order: 30 s of uptime after 62, 63 and 64 s
	for i, want := range []float64{32, 33, 34} {
		if recs[i].ToKernelS != want {
			t.Fatalf("record %d: %+v", i, recs[i])
		}
	}
	if g.records(KindReboot) != nil {
		t.Fatal("an update was recorded as a reboot")
	}
}

func TestClockTrust(t *testing.T) {
	for _, c := range []struct {
		name, state, leap string
		chronyErr         bool
		want              bool
	}{
		{"an RTC", "rtc\n", "Not synchronised", false, true},
		{"NTP", "ntp", "Not synchronised", false, true},
		{"the gate timed out, chrony has synced since", "timeout\n", "Normal", false, true},
		{"the gate timed out, chrony has not", "timeout\n", "Not synchronised", false, false},
		{"no gate, chrony synced", "", "Normal", false, true},
		{"no gate, no chrony", "", "", true, false},
		{"a leap second is announced: still synchronised", "", "Insert second", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := newRig(t)
			if c.state != "" {
				g.write("run/occulite/clock-state", c.state)
			}
			g.leap = c.leap
			if c.chronyErr {
				g.rec.Chrony = func(context.Context) ([]byte, error) { return nil, errors.New("chronyc: not found") }
			}
			if got := g.rec.clockTrusted(context.Background()); got != c.want {
				t.Fatalf("got %v", got)
			}
		})
	}
}

// The Pi 4 without an RTC: the record waits for chrony, and the kernel start is taken from the
// clock once it is right.
func TestRunWaitsForTheClock(t *testing.T) {
	g := newRig(t)
	if err := g.rec.Mark(KindReboot); err != nil {
		t.Fatal(err)
	}
	g.reboot()
	g.units = readyUnits
	calls := 0
	g.rec.Chrony = func(context.Context) ([]byte, error) {
		calls++
		if calls < 3 {
			// a clock months behind while NTP has not answered
			g.mu.Lock()
			g.now = time.UnixMilli(1_780_000_000_000)
			g.mu.Unlock()
			return []byte("Leap status     : Not synchronised\n"), nil
		}
		g.mu.Lock()
		g.now = time.UnixMilli(1_800_000_050_000)
		g.mu.Unlock()
		return []byte("Leap status     : Normal\n"), nil
	}
	g.rec.Patience = time.Hour // the clock jumps; the deadline is taken before
	g.rec.Run(context.Background())
	recs := g.records(KindReboot)
	if calls != 3 || len(recs) != 1 || recs[0].ToKernelS != 10 {
		t.Fatalf("calls %d, records %+v", calls, recs)
	}
}

func TestRunGivesUp(t *testing.T) {
	t.Run("the clock is never right", func(t *testing.T) {
		g := newRig(t)
		if err := g.rec.Mark(KindReboot); err != nil {
			t.Fatal(err)
		}
		g.reboot()
		g.rec.Run(context.Background())
		if g.pending() != nil || g.records(KindReboot) != nil {
			t.Fatal("recorded without a trustworthy clock, or the marker stayed")
		}
	})
	t.Run("an implausible gap: the clock went backwards", func(t *testing.T) {
		g := newRig(t)
		if err := g.rec.Mark(KindReboot); err != nil {
			t.Fatal(err)
		}
		g.reboot()
		g.write("run/occulite/clock-state", "ntp")
		g.write("proc/uptime", "90.00 100.00\n") // the kernel "started" 40 s before the request
		g.rec.Run(context.Background())
		if g.pending() != nil || g.records(KindReboot) != nil {
			t.Fatal("an implausible reboot was recorded")
		}
	})
	t.Run("hmipserver never comes: the record without ready", func(t *testing.T) {
		g := newRig(t)
		if err := g.rec.Mark(KindReboot); err != nil {
			t.Fatal(err)
		}
		g.reboot()
		g.write("run/occulite/clock-state", "ntp")
		g.units = unitBlock("lighttpd.service", "active", 23_000_000) + "\n" + unitBlock("rfd.service", "active", 28_000_000) + "\n" + unitBlock("hmipserver.service", "activating", 0)
		g.rec.Patience = 20 * time.Millisecond
		g.rec.Run(context.Background())
		recs := g.records(KindReboot)
		if g.pending() != nil || len(recs) != 1 || recs[0].ReadyMono != nil || *recs[0].HTTPMono != 23 {
			t.Fatalf("records %+v", recs)
		}
	})
}

func TestUnitStamps(t *testing.T) {
	for _, c := range []struct {
		name  string
		props map[string]string
		want  unitStamp
		mono  float64
	}{
		{"active", map[string]string{"LoadState": "loaded", "ActiveState": "active", "ActiveEnterTimestampMonotonic": "65500000"}, unitActive, 65.5},
		{"activating", map[string]string{"LoadState": "loaded", "ActiveState": "activating", "ActiveEnterTimestampMonotonic": "0"}, unitWaiting, 0},
		{"inactive, enabled: not started yet", map[string]string{"LoadState": "loaded", "ActiveState": "inactive", "UnitFileState": "enabled"}, unitWaiting, 0},
		{"not installed", map[string]string{"LoadState": "not-found", "ActiveState": "inactive"}, unitAbsent, 0},
		{"condition unmet (no wired module)", map[string]string{"LoadState": "loaded", "ActiveState": "inactive", "ConditionResult": "no"}, unitAbsent, 0},
		{"switched off", map[string]string{"LoadState": "loaded", "ActiveState": "inactive", "UnitFileState": "masked-runtime"}, unitAbsent, 0},
		{"failed", map[string]string{"LoadState": "loaded", "ActiveState": "failed"}, unitAbsent, 0},
		{"no answer for it", nil, unitAbsent, 0},
	} {
		if st, mono := stampOf(c.props); st != c.want || mono != c.mono {
			t.Errorf("%s: %v %v", c.name, st, mono)
		}
	}
	got := parseShow([]byte("Id=a.service\nLoadState=loaded\n\nId=b.service\nActiveState=active\n"))
	if len(got) != 2 || got["a.service"]["LoadState"] != "loaded" || got["b.service"]["ActiveState"] != "active" {
		t.Fatalf("parseShow %v", got)
	}
}

func TestAttach(t *testing.T) {
	const s = 1_800_000_000_000
	good := Browser{Started: s - 30, Down: s + 7000, HTTP: s + 36000, UI: s + 38000, Ready: s + 90000}

	t.Run("while the record waits for the clock", func(t *testing.T) {
		g := newRig(t)
		if err := g.rec.Mark(KindReboot); err != nil {
			t.Fatal(err)
		}
		g.reboot()
		g.units = readyUnits
		// Run in the background, waiting for chrony, which is not synchronised yet
		g.rec.Patience = time.Hour
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { g.rec.Run(ctx); close(done) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			g.rec.mu.Lock()
			waiting := g.rec.pending != nil
			g.rec.mu.Unlock()
			if waiting || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if _, err := g.rec.Attach(KindUpdate, good); !errors.Is(err, ErrNoRecord) {
			t.Fatalf("another kind: %v", err)
		}
		where, err := g.rec.Attach(KindReboot, good)
		if err != nil || where != "pending" {
			t.Fatalf("attach: %q %v", where, err)
		}
		if p := g.pending(); p == nil || p.Browser == nil || *p.Browser != good {
			t.Fatalf("the marker does not carry them: %+v", p)
		}
		g.mu.Lock()
		g.leap = "Normal"
		g.mu.Unlock()
		<-done
		cancel()
		recs := g.records(KindReboot)
		if len(recs) != 1 || recs[0].Browser == nil || recs[0].Phases.Down == nil || *recs[0].Phases.Down != 7 {
			t.Fatalf("records %+v", recs)
		}
		// again, now to the finished record
		later := good
		later.Down = s + 6000
		if where, err := g.rec.Attach(KindReboot, later); err != nil || where != "record" {
			t.Fatalf("attach to the record: %q %v", where, err)
		}
		if recs := g.records(KindReboot); *recs[0].Phases.Down != 6 {
			t.Fatalf("records %+v", recs)
		}
	})

	t.Run("after occulited restarted in the same boot", func(t *testing.T) {
		g := newRig(t)
		if err := g.rec.Mark(KindRestore); err != nil {
			t.Fatal(err)
		}
		g.reboot()
		g.write("run/occulite/clock-state", "rtc")
		g.units = readyUnits
		g.rec.Run(context.Background())
		fresh := &Recorder{StateDir: g.state, Root: g.root, Product: "rpi4"}
		if where, err := fresh.Attach(KindRestore, good); err != nil || where != "record" {
			t.Fatalf("attach: %q %v", where, err)
		}
		// a browser whose start is hours away from the request belongs to another reboot
		far := good
		far.Started, far.Down, far.HTTP, far.UI, far.Ready = s+2*time.Hour.Milliseconds(), 0, 0, s+2*time.Hour.Milliseconds()+1000, 0
		if _, err := fresh.Attach(KindRestore, far); !errors.Is(err, ErrNoRecord) {
			t.Fatalf("a far start: %v", err)
		}
	})

	t.Run("nothing to attach to, and nonsense", func(t *testing.T) {
		g := newRig(t)
		if _, err := g.rec.Attach(KindReboot, good); !errors.Is(err, ErrNoRecord) {
			t.Fatalf("no record: %v", err)
		}
		if _, err := g.rec.Attach(KindReboot, Browser{Started: s, Down: s + 5, HTTP: s + 1}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("not monotone: %v", err)
		}
	})
}

// A marker that is not JSON is dropped, not retried forever.
func TestUnreadableMarker(t *testing.T) {
	g := newRig(t)
	path := filepath.Join(g.state, "boot-timing", "pending.json")
	_ = os.MkdirAll(filepath.Dir(path), 0o750)
	_ = os.WriteFile(path, []byte("{"), 0o640)
	g.rec.Run(context.Background())
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the unreadable marker stayed")
	}
}
