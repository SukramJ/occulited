package bootexpect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/unitshow"
)

// Pending is the marker written before a reboot: what kind, when it was asked for on the box's
// clock, in which boot, on which product. A browser's checkpoints that arrive before the record is
// finished wait here.
type Pending struct {
	Kind          string   `json:"kind"`
	RequestWallMS int64    `json:"request_wall_ms"`
	BootID        string   `json:"boot_id"`
	Product       string   `json:"product"`
	Browser       *Browser `json:"browser,omitempty"`
}

// Record is one measured reboot. The *Mono figures are seconds after the kernel started (systemd's
// monotonic stamps): lighttpd active, occulited listening, the later of rfd and hmipserver active.
type Record struct {
	Kind              string   `json:"kind"`
	Product           string   `json:"product"`
	BootID            string   `json:"boot_id"`
	RequestWallMS     int64    `json:"request_wall_ms"`
	KernelStartWallMS int64    `json:"kernel_start_wall_ms"`
	ToKernelS         float64  `json:"to_kernel_s"`
	HTTPMono          *float64 `json:"http_mono_s,omitempty"`
	UIMono            *float64 `json:"ui_mono_s,omitempty"`
	ReadyMono         *float64 `json:"ready_mono_s,omitempty"`
	Phases            Phases   `json:"phases"`
	Browser           *Browser `json:"browser,omitempty"`
}

type timingsFile struct {
	Records map[string][]Record `json:"records"`
}

// Errors of Attach.
var (
	ErrInvalid  = errors.New("invalid checkpoints")
	ErrNoRecord = errors.New("no reboot of that kind is being recorded")
)

// Recorder writes the marker, finishes the record at the next start and answers the expectation.
type Recorder struct {
	// StateDir is occulited's state directory: the marker goes to boot-timing/pending.json, the
	// records to boot-timings.json.
	StateDir string
	// Root is the filesystem root /proc and /run are read below: "/" on a box.
	Root string
	// Product is a product of the table (ProductOf of /VERSION's PLATFORM).
	Product string
	// Systemctl runs systemctl with these arguments (through the privilege helper on a box); nil =
	// no systemd, no unit stamps.
	Systemctl func(ctx context.Context, args ...string) ([]byte, error)
	// Chrony runs `chronyc -n tracking`; nil = ExecChrony.
	Chrony func(ctx context.Context) ([]byte, error)
	// Now is the wall clock; nil = time.Now.
	Now func() time.Time
	// Interval between two looks at the clock and the units (10 s); Patience how long they are
	// waited for after the start (10 min).
	Interval, Patience time.Duration
	// Carry is a restore marker's second place, one the restore at boot does not reach; nil =
	// none (a restore's reboot is then not recorded).
	Carry *Carry
	Log   *slog.Logger

	mu         sync.Mutex
	listenMono *float64
	pending    *Pending // the marker of the previous boot while its record is being finished
	current    *Record  // the record finished in this boot
}

// Carry is a copy of a restore's marker where the restore at boot does not reach. The restore
// (S05CheckBackupRestore) deletes everything under /usr/local except tmp - the state directory
// with pending.json included - before it unpacks the backup, so a restore's reboot was never
// recorded (openccu-lite B-193). Path is the copy (system.BootMarkerCarryFile: a dotfile in
// /usr/local/tmp, which is in no backup and which the boot keeps); Place puts the marker there and
// Remove takes it away - through the privilege helper on a box, the directory is root's. The
// daemon reads the copy itself.
type Carry struct {
	Path   string
	Place  func(marker []byte) error
	Remove func() error
}

// ExecChrony asks chronyd for its tracking state; it needs no privileges.
func ExecChrony(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "chronyc", "-n", "tracking").Output()
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Recorder) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

func (r *Recorder) path(p string) string {
	root := r.Root
	if root == "" {
		root = "/"
	}
	return filepath.Join(root, p)
}

func (r *Recorder) markerPath() string {
	return filepath.Join(r.StateDir, "boot-timing", "pending.json")
}
func (r *Recorder) timingsPath() string { return filepath.Join(r.StateDir, "boot-timings.json") }

// bootID is the kernel's id of this boot; it changes with every boot.
func (r *Recorder) bootID() string {
	b, err := os.ReadFile(r.path("/proc/sys/kernel/random/boot_id"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// uptime is the time since the kernel started, the clock systemd's monotonic stamps count in.
func (r *Recorder) uptime() (time.Duration, bool) {
	b, err := os.ReadFile(r.path("/proc/uptime"))
	if err != nil {
		return 0, false
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, false
	}
	s, err := strconv.ParseFloat(f[0], 64)
	if err != nil || s <= 0 {
		return 0, false
	}
	return time.Duration(s * float64(time.Second)), true
}

// writeAtomic writes v as JSON through a temporary file in the same directory and a rename, so a
// power cut leaves the old file or the new one.
func writeAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o640); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Mark writes the marker right before a reboot of this kind is started.
func (r *Recorder) Mark(kind string) error {
	if !ValidKind(kind) {
		return fmt.Errorf("unknown kind %q", kind)
	}
	p := Pending{Kind: kind, RequestWallMS: r.now().UnixMilli(), BootID: r.bootID(), Product: r.Product}
	if err := writeAtomic(r.markerPath(), p); err != nil {
		return err
	}
	// a restore's boot deletes the state directory before it unpacks the backup: the copy in
	// /usr/local/tmp is what the next start finds. Its failure costs the measurement, not the marker.
	if kind == KindRestore && r.Carry != nil {
		b, _ := json.MarshalIndent(p, "", "  ")
		if err := r.Carry.Place(append(b, '\n')); err != nil {
			r.log().Warn("boot timing: the restore's marker was not carried over, its reboot is not recorded", "err", err)
		}
	}
	return nil
}

// Unmark removes the marker again, and its carried copy: the reboot did not start, or the record
// is finished.
func (r *Recorder) Unmark() {
	if err := os.Remove(r.markerPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		r.log().Warn("boot timing: the marker could not be removed", "err", err)
	}
	if r.Carry == nil {
		return
	}
	if _, err := os.Lstat(r.Carry.Path); errors.Is(err, fs.ErrNotExist) {
		return // nothing carried: no helper call
	}
	if err := r.Carry.Remove(); err != nil {
		r.log().Warn("boot timing: the carried marker could not be removed", "err", err)
	}
}

// MarkListening notes that occulited listens now, on the monotonic clock.
func (r *Recorder) MarkListening() {
	up, ok := r.uptime()
	if !ok {
		return
	}
	s := up.Seconds()
	r.mu.Lock()
	r.listenMono = &s
	if r.current != nil && r.current.UIMono == nil {
		r.current.UIMono = ptr(s)
	}
	r.mu.Unlock()
}

func (r *Recorder) readPending() (*Pending, error) {
	b, err := os.ReadFile(r.markerPath())
	if errors.Is(err, fs.ErrNotExist) && r.Carry != nil {
		// the restore at boot took the state directory's marker with it; the copy survived
		b, err = os.ReadFile(r.Carry.Path)
	}
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	if !ValidKind(p.Kind) || p.RequestWallMS <= 0 {
		return nil, fmt.Errorf("marker: kind %q, request %d", p.Kind, p.RequestWallMS)
	}
	return &p, nil
}

var leapNormal = regexp.MustCompile(`(?m)^Leap status\s*:\s*Normal\s*$`)

func (r *Recorder) clockTrusted(ctx context.Context) bool {
	return ClockTrusted(ctx, r.Root, r.Chrony)
}

// ClockTrusted says whether the wall clock is right: the clock gate found an RTC or an NTP sync
// (/run/occulite/clock-state below root), or chronyd says it is synchronised (chrony nil =
// ExecChrony). A box without an RTC starts with a clock that is months off until NTP has answered.
// The boot timeline's snapshots (task 93) wait for it as well.
func ClockTrusted(ctx context.Context, root string, chrony func(ctx context.Context) ([]byte, error)) bool {
	if root == "" {
		root = "/"
	}
	if b, err := os.ReadFile(filepath.Join(root, "/run/occulite/clock-state")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "rtc", "ntp":
			return true
		}
	}
	if chrony == nil {
		chrony = ExecChrony
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := chrony(cctx)
	return err == nil && leapNormal.Match(out)
}

// Run finishes the record of the reboot a marker announces, when this is the boot after it. It
// waits for a trustworthy clock and for the interfaces (every Interval, for up to Patience), and
// returns when the record is complete, when it gives up, or when ctx ends.
func (r *Recorder) Run(ctx context.Context) {
	p, err := r.readPending()
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			r.log().Warn("boot timing: unreadable marker dropped", "err", err)
			r.Unmark()
		}
		return
	}
	bootID := r.bootID()
	if p.BootID == bootID {
		// the reboot has not happened: occulited restarted in the boot that wrote the marker. An
		// old one belongs to a reboot that never came.
		if r.now().UnixMilli()-p.RequestWallMS > maxSpan.Milliseconds() {
			r.Unmark()
		}
		return
	}
	r.mu.Lock()
	r.pending = p
	r.mu.Unlock()
	interval, patience := r.Interval, r.Patience
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if patience <= 0 {
		patience = 10 * time.Minute
	}
	// the patience runs on Go's monotonic clock: the wall clock of a box without an RTC jumps by
	// months when NTP answers, which is exactly what is waited for here
	since := time.Now()
	started := false
	for {
		if !started && r.clockTrusted(ctx) {
			if !r.begin(bootID) {
				r.log().Warn("boot timing: the reboot's times are implausible, not recorded", "kind", p.Kind)
				r.finish()
				return
			}
			started = true
		}
		if started && r.readUnits(ctx) {
			break
		}
		if time.Since(since) >= patience {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
	if !started {
		r.log().Warn("boot timing: the clock was never trustworthy, the reboot is not recorded", "kind", p.Kind)
	}
	r.finish()
}

// finish removes the marker; the record, if any, stays where it was saved.
func (r *Recorder) finish() {
	r.Unmark()
	r.mu.Lock()
	r.pending = nil
	r.mu.Unlock()
}

// begin starts the record from the marker once the clock is right: the kernel's start on the wall
// clock, and how long it took from the request. false when that is not plausible.
func (r *Recorder) begin(bootID string) bool {
	up, ok := r.uptime()
	if !ok {
		return false
	}
	kernel := r.now().Add(-up).UnixMilli()
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.pending
	toKernel := kernel - p.RequestWallMS
	if toKernel < 0 || toKernel > maxSpan.Milliseconds() {
		return false
	}
	rec := &Record{Kind: p.Kind, Product: p.Product, BootID: bootID, RequestWallMS: p.RequestWallMS, KernelStartWallMS: kernel, ToKernelS: round1(msToS(toKernel)), Browser: p.Browser}
	// an occulited restarted while it waited keeps what the first one saw
	if old := r.findLocked(p.Kind, p.RequestWallMS); old != nil {
		rec.UIMono, rec.HTTPMono, rec.ReadyMono = old.UIMono, old.HTTPMono, old.ReadyMono
		if rec.Browser == nil {
			rec.Browser = old.Browser
		}
	}
	if rec.UIMono == nil && r.listenMono != nil {
		rec.UIMono = ptr(*r.listenMono)
	}
	r.current = rec
	return r.saveLocked(rec) == nil
}

// unitStamp is what one unit says: its active stamp, or that it will not get one.
type unitStamp int

const (
	unitWaiting unitStamp = iota
	unitActive
	unitAbsent
)

func stampOf(props map[string]string) (unitStamp, float64) {
	if props == nil || props["LoadState"] != "loaded" || props["ConditionResult"] == "no" {
		return unitAbsent, 0
	}
	switch props["ActiveState"] {
	case "active":
		if n, err := strconv.ParseInt(props["ActiveEnterTimestampMonotonic"], 10, 64); err == nil && n > 0 {
			return unitActive, float64(n) / 1e6
		}
		return unitAbsent, 0
	case "failed":
		// it does not come up by itself: nothing to wait for, and nothing to count
		return unitAbsent, 0
	case "activating", "reloading":
		return unitWaiting, 0
	}
	if s := props["UnitFileState"]; s == "disabled" || s == "masked" || s == "masked-runtime" {
		return unitAbsent, 0
	}
	return unitWaiting, 0
}

// parseShow reads `systemctl show` blocks, keyed by the Id property (internal/unitshow, which the
// boot timeline of task 93 reads the same output with).
func parseShow(out []byte) map[string]map[string]string {
	return unitshow.ByID(out)
}

// readUnits fills in lighttpd's and the interfaces' stamps and saves the record; true when
// nothing is left to wait for.
func (r *Recorder) readUnits(ctx context.Context) bool {
	var units map[string]map[string]string
	if r.Systemctl != nil {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		out, err := r.Systemctl(cctx, "show", "-p", "Id,LoadState,ActiveState,UnitFileState,ConditionResult,ActiveEnterTimestampMonotonic", "--", "lighttpd.service", "rfd.service", "hmipserver.service")
		cancel()
		if err == nil {
			units = parseShow(out)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.current
	if rec == nil {
		return true
	}
	done := true
	if units != nil {
		switch st, v := stampOf(units["lighttpd.service"]); st {
		case unitActive:
			rec.HTTPMono = ptr(v)
		case unitWaiting:
			done = false
		}
		ready, waiting := -1.0, false
		for _, u := range []string{"rfd.service", "hmipserver.service"} {
			switch st, v := stampOf(units[u]); st {
			case unitActive:
				ready = max(ready, v)
			case unitWaiting:
				waiting = true
			}
		}
		if waiting {
			done = false
		} else if ready >= 0 {
			rec.ReadyMono = ptr(ready)
		}
	}
	if rec.UIMono == nil && r.listenMono != nil {
		rec.UIMono = ptr(*r.listenMono)
	}
	if err := r.saveLocked(rec); err != nil {
		r.log().Warn("boot timing: the record could not be saved", "err", err)
	}
	return done
}

func (r *Recorder) loadLocked() timingsFile {
	var f timingsFile
	if b, err := os.ReadFile(r.timingsPath()); err == nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.Records == nil {
		f.Records = map[string][]Record{}
	}
	return f
}

func (r *Recorder) findLocked(kind string, request int64) *Record {
	for _, rec := range r.loadLocked().Records[kind] {
		if rec.RequestWallMS == request {
			return &rec
		}
	}
	return nil
}

// saveLocked puts the record into the file: in place of one with the same request, otherwise as
// the newest, keeping the last keepPerKind of its kind.
func (r *Recorder) saveLocked(rec *Record) error {
	rec.Phases = derive(*rec)
	f := r.loadLocked()
	list := f.Records[rec.Kind]
	replaced := false
	for i := range list {
		if list[i].RequestWallMS == rec.RequestWallMS {
			list[i] = *rec
			replaced = true
		}
	}
	if !replaced {
		list = append(list, *rec)
	}
	if len(list) > keepPerKind {
		list = list[len(list)-keepPerKind:]
	}
	f.Records[rec.Kind] = list
	return writeAtomic(r.timingsPath(), f)
}

// Attach adds a browser's checkpoints to the reboot being recorded or recorded in this boot, of
// the same kind and requested within an hour of the browser's start. It says where they went:
// "pending" (the record waits for the clock) or "record".
func (r *Recorder) Attach(kind string, b Browser) (string, error) {
	if err := b.Validate(); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	near := func(request int64) bool {
		d := b.Started - request
		return d < maxSpan.Milliseconds() && d > -maxSpan.Milliseconds()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if rec := r.current; rec != nil && rec.Kind == kind && near(rec.RequestWallMS) {
		rec.Browser = &b
		return "record", r.saveLocked(rec)
	}
	if p := r.pending; p != nil && p.Kind == kind && near(p.RequestWallMS) {
		p.Browser = &b
		return "pending", writeAtomic(r.markerPath(), p)
	}
	// finished before this occulited started (it was restarted since)
	boot := r.bootID()
	list := r.loadLocked().Records[kind]
	for i := len(list) - 1; i >= 0; i-- {
		if rec := list[i]; rec.BootID == boot && near(rec.RequestWallMS) {
			rec.Browser = &b
			return "record", r.saveLocked(&rec)
		}
	}
	return "", ErrNoRecord
}

// Expect is the answer for a kind: the product default, overridden per phase by the median of
// this product's records of that kind.
func (r *Recorder) Expect(kind string) Answer {
	a := DefaultAnswer(r.Product, kind)
	r.mu.Lock()
	all := r.loadLocked().Records[tableKind(kind)]
	r.mu.Unlock()
	var recs []Record
	for _, rec := range all {
		if rec.Product == a.Product {
			recs = append(recs, rec)
		}
	}
	a.Expect, a.Measured = Estimate(a.Default, recs)
	return a
}
