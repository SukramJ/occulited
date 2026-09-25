package system

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// ---- own timers (task 50) ---------------------------------------------------------------------
//
// The maintainer wants to add timers of his own from the Services page. A timer is two units, a
// .timer and the .service it activates, and on a read-only rootfs they can only live in /run:
// /run/systemd/system/ is a runtime unit directory with precedence over /usr/lib. So an own timer
// is the override mechanism (unitoverride.go) once more - the two files are kept on the userfs in
// <state>/units/ and written into /run at every start, because /run does not survive a boot.
//
// Own units are named local-<name>: the prefix keeps them apart from everything the image
// ships, so no name can shadow a firmware unit, and it is what the helper admits in
// /run/systemd/system/ besides the drop-ins (priv.Policy.RunUnitDir). A name that collides with a
// unit systemd already knows is refused all the same.
//
// Enabling is `systemctl --runtime enable --now`, which links the timer into timers.target.wants
// under /run. Disabling is NOT the runtime mask the shipped units get (unitswitch.go): a mask is
// a /dev/null link at /run/systemd/system/<unit>, the very path the timer's file occupies, and
// systemctl refuses to mask over a file. So an own timer is disabled with `--runtime disable
// --now`, a marker beside its files remembers that, and the replay at start leaves such a timer
// written but not enabled.
//
// What is saved is the text of the two files, not a form: whoever knows systemd can write any
// directive the page's form does not have. The checks here are the shape (size, no NUL, the
// [Timer] and [Service] sections), `systemd-analyze verify` when the image has it (it does not
// today: BR2_PACKAGE_SYSTEMD_ANALYZE is off in every product config), and in every case systemd's
// own verdict after the reload: a unit it did not load is taken back out, so nothing broken stays.

// LocalTimer is one own timer as the API carries it.
type LocalTimer struct {
	Name        string `json:"name"`         // the short name, <name> in local-<name>.timer
	Unit        string `json:"unit"`         // local-<name>.timer
	Service     string `json:"service"`      // local-<name>.service
	TimerFile   string `json:"timer_file"`   // the .timer text
	ServiceFile string `json:"service_file"` // the .service text
	// Enabled: not switched off here (no marker); systemd's own state is on GET /timers.
	Enabled bool `json:"enabled"`
}

// LocalTimerPrefix is what every own unit's name starts with.
const LocalTimerPrefix = "local-"

// ErrLocalTimerExists and ErrLocalTimerNotFound let the routes answer 409 and 404; the message a
// person reads is the wrapping error's.
var (
	ErrLocalTimerExists   = errors.New("own timer exists")
	ErrLocalTimerNotFound = errors.New("own timer not found")
)

type localTimerErr struct {
	kind error
	msg  string
}

func (e localTimerErr) Error() string { return e.msg }
func (e localTimerErr) Unwrap() error { return e.kind }

var errLocalTimerName = errors.New("a timer name is 1 to 32 letters, digits, - and _, starting with a letter or a digit")

func localTimerUnit(name string) string    { return LocalTimerPrefix + name + ".timer" }
func localTimerService(name string) string { return LocalTimerPrefix + name + ".service" }

// ValidLocalTimerName is the name rule, the same one the helper applies to the path (a name
// becomes part of a unit name, a file name on the userfs and in /run, and a systemctl argument).
func ValidLocalTimerName(name string) bool { return priv.ValidLocalUnitName(name) }

// localTimerName accepts the short name or the timer's unit name - local-<name>.timer, which is
// what GET /timers lists - and returns the short name. A short name cannot contain a dot, so the
// two cannot be confused.
func localTimerName(id string) string {
	if strings.HasPrefix(id, LocalTimerPrefix) && strings.HasSuffix(id, ".timer") {
		return strings.TrimSuffix(strings.TrimPrefix(id, LocalTimerPrefix), ".timer")
	}
	return id
}

// unitsDir is where the stored own units live on the userfs: <state>/units/.
func (s SystemdServices) unitsDir() string {
	if s.SwitchFile == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.SwitchFile), "units")
}

// runUnitPath is where a unit is written for systemd.
func runUnitPath(unit string) string { return filepath.Join("/run/systemd/system", unit) }

// ownTimerName answers whether unit is one of the stored own timers, and its short name.
func (s SystemdServices) ownTimerName(unit string) (string, bool) {
	if !strings.HasPrefix(unit, LocalTimerPrefix) || !strings.HasSuffix(unit, ".timer") || s.unitsDir() == "" {
		return "", false
	}
	name := localTimerName(unit)
	if !ValidLocalTimerName(name) {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(s.unitsDir(), localTimerUnit(name))); err != nil {
		return "", false
	}
	return name, true
}

// ownTimerServiceName answers whether unit is the .service of a stored own timer.
func (s SystemdServices) ownTimerServiceName(unit string) (string, bool) {
	if !strings.HasPrefix(unit, LocalTimerPrefix) || !strings.HasSuffix(unit, ".service") {
		return "", false
	}
	return s.ownTimerName(strings.TrimSuffix(unit, ".service") + ".timer")
}

// localTimerNames lists the stored own timers (a .timer file with its .service beside it).
func (s SystemdServices) localTimerNames() []string {
	dir := s.unitsDir()
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, LocalTimerPrefix) || !strings.HasSuffix(n, ".timer") {
			continue
		}
		name := localTimerName(n)
		if !ValidLocalTimerName(name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, localTimerService(name))); err != nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s SystemdServices) disabledMarker(name string) string {
	return filepath.Join(s.unitsDir(), LocalTimerPrefix+name+".disabled")
}

// ReadLocalTimer returns one stored own timer; id is its name or its timer unit.
func (s SystemdServices) ReadLocalTimer(id string) (LocalTimer, error) {
	name := localTimerName(id)
	if !ValidLocalTimerName(name) {
		return LocalTimer{}, errLocalTimerName
	}
	dir := s.unitsDir()
	if dir == "" {
		return LocalTimer{}, fmt.Errorf("no state directory: own timers cannot be kept")
	}
	tb, err := os.ReadFile(filepath.Join(dir, localTimerUnit(name)))
	if err != nil {
		return LocalTimer{}, localTimerErr{ErrLocalTimerNotFound, "no own timer named " + name}
	}
	sb, err := os.ReadFile(filepath.Join(dir, localTimerService(name)))
	if err != nil {
		return LocalTimer{}, localTimerErr{ErrLocalTimerNotFound, "the own timer " + name + " has no service file"}
	}
	_, disabled := os.Stat(s.disabledMarker(name))
	return LocalTimer{Name: name, Unit: localTimerUnit(name), Service: localTimerService(name), TimerFile: string(tb), ServiceFile: string(sb), Enabled: disabled != nil}, nil
}

// ListLocalTimers returns every stored own timer, never nil.
func (s SystemdServices) ListLocalTimers() []LocalTimer {
	out := []LocalTimer{}
	for _, name := range s.localTimerNames() {
		if t, err := s.ReadLocalTimer(name); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// checkLocalTimerFiles is what the two texts must look like before systemd sees them: not empty,
// at most 64 KiB (a pasted log does not belong in /run), no NUL, and the section that makes each
// file what it is - a .timer without [Timer] or a .service without [Service] is the two pasted
// crosswise, every time.
func checkLocalTimerFiles(timer, service string) error {
	for _, f := range []struct{ what, text, section string }{{"timer", timer, "Timer"}, {"service", service, "Service"}} {
		switch {
		case strings.TrimSpace(f.text) == "":
			return fmt.Errorf("the %s file is empty", f.what)
		case len(f.text) > 64*1024:
			return fmt.Errorf("the %s file is larger than 64 KiB", f.what)
		case strings.ContainsRune(f.text, 0):
			return fmt.Errorf("the %s file contains a NUL byte", f.what)
		case !hasSection(f.text, f.section):
			return fmt.Errorf("the %s file has no [%s] section", f.what, f.section)
		}
	}
	return nil
}

func hasSection(text, name string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "["+name+"]" {
			return true
		}
	}
	return false
}

// AnalyzeAvailable reports whether systemd-analyze is on the box. Both of its uses - verifying the
// files and checking a calendar expression - are optional: run when it is there, skipped with a
// note when it is not.
func (s SystemdServices) AnalyzeAvailable() bool {
	look := s.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look("systemd-analyze")
	return err == nil
}

// analyze runs systemd-analyze. It needs no privilege - it reads unit files and computes calendar
// times - so it runs as the daemon itself, not through the helper; a test's Runner stands in.
func (s SystemdServices) analyze(ctx context.Context, args ...string) ([]byte, error) {
	if s.Run != nil {
		return s.Run(ctx, "systemd-analyze", args...)
	}
	return exec.CommandContext(ctx, "systemd-analyze", args...).CombinedOutput()
}

// verifyLocalTimer runs `systemd-analyze verify` over the two files in a scratch directory (the
// directory of a given file joins the unit search path, so the .timer finds its .service) and
// returns its complaints. Nothing when systemd-analyze is not installed.
func (s SystemdServices) verifyLocalTimer(ctx context.Context, name, timer, service string) error {
	if !s.AnalyzeAvailable() {
		slog.Info("timers: systemd-analyze is not installed, the unit files are checked by systemd when they are loaded", "timer", localTimerUnit(name))
		return nil
	}
	dir, err := os.MkdirTemp("", "occulite-timer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tp, sp := filepath.Join(dir, localTimerUnit(name)), filepath.Join(dir, localTimerService(name))
	if err := os.WriteFile(tp, []byte(timer), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(sp, []byte(service), 0o644); err != nil {
		return err
	}
	out, err := s.analyze(ctx, "verify", "--", tp, sp)
	if err != nil {
		msg := strings.ReplaceAll(strings.TrimSpace(string(out)), dir+"/", "")
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("systemd-analyze verify: %s", msg)
	}
	return nil
}

// CalendarCheck is what the calendar route answers: the next run times of an OnCalendar=
// expression from `systemd-analyze calendar --iterations=3`, or that the box cannot tell
// (Available false).
type CalendarCheck struct {
	Available  bool     `json:"available"`
	Expression string   `json:"expression"`
	Normalized string   `json:"normalized,omitempty"`
	Next       []string `json:"next,omitempty"`
	Output     string   `json:"output,omitempty"`
}

// CheckCalendar runs the expression through systemd-analyze. An error is what systemd said about
// an expression it could not parse (the result still carries its output).
func (s SystemdServices) CheckCalendar(ctx context.Context, expr string) (CalendarCheck, error) {
	res := CalendarCheck{Expression: expr}
	if strings.TrimSpace(expr) == "" || len(expr) > 256 || strings.ContainsAny(expr, "\x00\n\r") || strings.HasPrefix(strings.TrimSpace(expr), "-") {
		return res, fmt.Errorf("not a calendar expression")
	}
	if !s.AnalyzeAvailable() {
		return res, nil
	}
	res.Available = true
	out, err := s.analyze(ctx, "calendar", "--iterations=3", "--", expr)
	res.Output = strings.TrimSpace(string(out))
	if err != nil {
		msg := res.Output
		if msg == "" {
			msg = err.Error()
		}
		return res, fmt.Errorf("systemd-analyze calendar: %s", msg)
	}
	// the answer is "  Original form: ...", "Normalized form: ...", "    Next elapse: <time>",
	// then "       Iter. #2: <time>" and so on, with "(in UTC)" and "From now" lines between
	sc := bufio.NewScanner(strings.NewReader(res.Output))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case k == "Normalized form":
			res.Normalized = v
		case k == "Next elapse", strings.HasPrefix(k, "Iter. #"):
			res.Next = append(res.Next, v)
		}
	}
	return res, nil
}

// knownUnit returns the first of units systemd can load already - a shipped one, a generated one,
// an addon's - so an own timer never shadows it; "" when none. One `systemctl show` for all of
// them; LoadState=not-found is systemd's answer for a name it knows nothing about.
func (s SystemdServices) knownUnit(ctx context.Context, units ...string) string {
	shown := s.showAll(ctx, units, "LoadState")
	for _, u := range units {
		if st := shown[u]["LoadState"]; st != "" && st != "not-found" {
			return u
		}
	}
	return ""
}

// loadProblem asks systemd, after the reload, whether it loaded both units. A file it could not
// use (a broken OnCalendar=, a timer with nothing to trigger on, an unknown section) is
// LoadState=bad-setting or error, with the details in the journal. No answer at all is not a
// verdict and passes.
func (s SystemdServices) loadProblem(ctx context.Context, name string) error {
	units := []string{localTimerUnit(name), localTimerService(name)}
	shown := s.showAll(ctx, units, "LoadState", "LoadError")
	for _, u := range units {
		st := shown[u]["LoadState"]
		if st == "" || st == "loaded" {
			continue
		}
		// LoadError=org.freedesktop.systemd1.BadSetting "Unit x has a bad unit file setting."
		why := shown[u]["LoadError"]
		if _, text, ok := strings.Cut(why, " "); ok {
			why = strings.Trim(text, `"`)
		}
		if why != "" {
			why = ": " + why
		}
		return fmt.Errorf("systemd did not load %s (%s)%s - the journal of the reload has the details", u, st, why)
	}
	return nil
}

// CreateLocalTimer stores a new own timer, writes it into /run, reloads, and enables and starts
// it. Anything that fails on the way takes back what was written: a refused timer leaves nothing
// behind, neither on the userfs nor in /run.
func (s SystemdServices) CreateLocalTimer(ctx context.Context, id, timer, service string) (LocalTimer, error) {
	name := localTimerName(id)
	if !ValidLocalTimerName(name) {
		return LocalTimer{}, errLocalTimerName
	}
	dir := s.unitsDir()
	if dir == "" {
		return LocalTimer{}, fmt.Errorf("no state directory: own timers cannot be kept")
	}
	if _, err := os.Stat(filepath.Join(dir, localTimerUnit(name))); err == nil {
		return LocalTimer{}, localTimerErr{ErrLocalTimerExists, "an own timer named " + name + " exists already"}
	}
	if err := checkLocalTimerFiles(timer, service); err != nil {
		return LocalTimer{}, err
	}
	if u := s.knownUnit(ctx, localTimerUnit(name), localTimerService(name)); u != "" {
		return LocalTimer{}, localTimerErr{ErrLocalTimerExists, "a unit named " + u + " exists on this system already"}
	}
	if err := s.verifyLocalTimer(ctx, name, timer, service); err != nil {
		return LocalTimer{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return LocalTimer{}, err
	}
	fail := func(err error) (LocalTimer, error) {
		s.dropLocalTimer(ctx, name)
		return LocalTimer{}, err
	}
	if err := s.storeLocalTimer(name, timer, service); err != nil {
		return fail(err)
	}
	if err := s.applyLocalTimer(name, timer, service); err != nil {
		return fail(err)
	}
	if out, err := s.run(ctx, "daemon-reload"); err != nil {
		return fail(fmt.Errorf("daemon-reload: %w: %s", err, strings.TrimSpace(string(out))))
	}
	if err := s.loadProblem(ctx, name); err != nil {
		return fail(err)
	}
	if out, err := s.run(ctx, "enable", "--runtime", "--now", "--no-pager", "--", localTimerUnit(name)); err != nil {
		_, _ = s.run(ctx, "disable", "--runtime", "--now", "--no-pager", "--", localTimerUnit(name))
		return fail(fmt.Errorf("systemctl enable %s: %w: %s", localTimerUnit(name), err, strings.TrimSpace(string(out))))
	}
	return s.ReadLocalTimer(name)
}

// UpdateLocalTimer replaces the two files of an own timer and, when the timer is enabled,
// restarts it so a changed schedule is armed at once. Files systemd does not load are taken back:
// the previous text goes back to the userfs and /run, and the timer runs on as it was.
func (s SystemdServices) UpdateLocalTimer(ctx context.Context, id, timer, service string) (LocalTimer, error) {
	cur, err := s.ReadLocalTimer(id)
	if err != nil {
		return LocalTimer{}, err
	}
	name := cur.Name
	if err := checkLocalTimerFiles(timer, service); err != nil {
		return LocalTimer{}, err
	}
	if err := s.verifyLocalTimer(ctx, name, timer, service); err != nil {
		return LocalTimer{}, err
	}
	fail := func(err error) (LocalTimer, error) {
		_ = s.storeLocalTimer(name, cur.TimerFile, cur.ServiceFile)
		_ = s.applyLocalTimer(name, cur.TimerFile, cur.ServiceFile)
		_, _ = s.run(ctx, "daemon-reload")
		return LocalTimer{}, err
	}
	if err := s.storeLocalTimer(name, timer, service); err != nil {
		return fail(err)
	}
	if err := s.applyLocalTimer(name, timer, service); err != nil {
		return fail(err)
	}
	if out, err := s.run(ctx, "daemon-reload"); err != nil {
		return fail(fmt.Errorf("daemon-reload: %w: %s", err, strings.TrimSpace(string(out))))
	}
	if err := s.loadProblem(ctx, name); err != nil {
		return fail(err)
	}
	if cur.Enabled {
		if out, err := s.run(ctx, "restart", "--no-pager", "--", localTimerUnit(name)); err != nil {
			return LocalTimer{}, fmt.Errorf("the files were saved, but systemctl restart %s failed: %w: %s", localTimerUnit(name), err, strings.TrimSpace(string(out)))
		}
	}
	return s.ReadLocalTimer(name)
}

// DeleteLocalTimer stops and disables the timer, stops its service if it runs, removes both units
// from /run and the userfs and reloads.
func (s SystemdServices) DeleteLocalTimer(ctx context.Context, id string) error {
	t, err := s.ReadLocalTimer(id)
	if err != nil {
		return err
	}
	// a failure here is not a reason to keep the files: a unit systemd does not know cannot be
	// disabled, and that is the state after a boot whose replay did not run
	_, _ = s.run(ctx, "disable", "--runtime", "--now", "--no-pager", "--", t.Unit)
	_, _ = s.run(ctx, "stop", "--no-pager", "--", t.Service)
	for _, unit := range []string{t.Unit, t.Service} {
		if err := s.removeRunUnit(unit); err != nil {
			return err
		}
	}
	if err := s.unstoreLocalTimer(t.Name); err != nil {
		return err
	}
	if out, err := s.run(ctx, "daemon-reload"); err != nil {
		return fmt.Errorf("daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RunLocalTimer starts the timer's service once, now, without waiting for it: a job may take
// minutes, and its outcome is in the log under the service's name.
func (s SystemdServices) RunLocalTimer(ctx context.Context, id string) (string, error) {
	t, err := s.ReadLocalTimer(id)
	if err != nil {
		return "", err
	}
	out, err := s.run(ctx, "start", "--no-block", "--no-pager", "--", t.Service)
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("systemctl start %s: %w", t.Service, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// SetLocalTimerEnabled is the Services page's switch for an own timer (see the top of the file
// for why it is not the runtime mask): a runtime enable or disable, and then the marker that
// makes the replay at start do the same - systemctl first, so a failure leaves the stored state
// describing what the box does.
func (s SystemdServices) SetLocalTimerEnabled(ctx context.Context, id string, on bool) (string, error) {
	t, err := s.ReadLocalTimer(id)
	if err != nil {
		return "", err
	}
	if on {
		out, err := s.run(ctx, "enable", "--runtime", "--now", "--no-pager", "--", t.Unit)
		if err != nil {
			return strings.TrimSpace(string(out)), fmt.Errorf("systemctl enable %s: %w", t.Unit, err)
		}
		if err := os.Remove(s.disabledMarker(t.Name)); err != nil && !os.IsNotExist(err) {
			return strings.TrimSpace(string(out)), fmt.Errorf("the timer is on, but that could not be stored: %w", err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	out, err := s.run(ctx, "disable", "--runtime", "--now", "--no-pager", "--", t.Unit)
	if err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("systemctl disable %s: %w", t.Unit, err)
	}
	if err := os.WriteFile(s.disabledMarker(t.Name), []byte("switched off on the Services page\n"), 0o640); err != nil {
		return strings.TrimSpace(string(out)), fmt.Errorf("the timer is off, but that could not be stored: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// storeLocalTimer writes the two texts to the userfs (occulited's own state directory: no
// privilege needed, and it survives a firmware update with the rest of /usr/local).
func (s SystemdServices) storeLocalTimer(name, timer, service string) error {
	dir := s.unitsDir()
	if err := os.WriteFile(filepath.Join(dir, localTimerUnit(name)), []byte(timer), 0o640); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, localTimerService(name)), []byte(service), 0o640)
}

// unstoreLocalTimer removes the two texts and the marker from the userfs.
func (s SystemdServices) unstoreLocalTimer(name string) error {
	dir := s.unitsDir()
	for _, f := range []string{filepath.Join(dir, localTimerUnit(name)), filepath.Join(dir, localTimerService(name)), s.disabledMarker(name)} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// dropLocalTimer takes back a create that did not go through: the files in /run and on the
// userfs, and a reload so systemd forgets the units again.
func (s SystemdServices) dropLocalTimer(ctx context.Context, name string) {
	for _, unit := range []string{localTimerUnit(name), localTimerService(name)} {
		if err := s.removeRunUnit(unit); err != nil {
			slog.Warn("timers: a refused timer could not be removed from /run", "unit", unit, "err", err)
		}
	}
	if err := s.unstoreLocalTimer(name); err != nil {
		slog.Warn("timers: a refused timer could not be removed from the state directory", "timer", localTimerUnit(name), "err", err)
	}
	_, _ = s.run(ctx, "daemon-reload")
}

// applyLocalTimer writes the two units into /run through the privilege helper (the daemon is
// unprivileged and /run/systemd/system is root's; the helper admits local-<name>.timer and
// local-<name>.service there, and the drop-ins, and nothing else).
func (s SystemdServices) applyLocalTimer(name, timer, service string) error {
	if Priv == nil {
		return fmt.Errorf("no privilege helper")
	}
	for _, f := range []struct{ unit, text string }{{localTimerUnit(name), timer}, {localTimerService(name), service}} {
		if err := Priv.WriteFile(runUnitPath(f.unit), []byte(f.text), 0o644); err != nil {
			return fmt.Errorf("%s: %w", runUnitPath(f.unit), err)
		}
	}
	return nil
}

func (s SystemdServices) removeRunUnit(unit string) error {
	if Priv == nil {
		return nil
	}
	if err := Priv.Remove(runUnitPath(unit)); err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
		return err
	}
	return nil
}

// ReplayLocalTimers writes every stored own timer into /run at start and enables the ones that
// are not switched off - before the switch replay, so a runtime mask on something else still
// applies on top. Loud about what it could not do, like the other two replays.
func (s SystemdServices) ReplayLocalTimers(ctx context.Context) (applied []string, problems []string) {
	names := s.localTimerNames()
	if len(names) == 0 {
		return nil, nil
	}
	var enable []string
	for _, name := range names {
		t, err := s.ReadLocalTimer(name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if err := checkLocalTimerFiles(t.TimerFile, t.ServiceFile); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", t.Unit, err))
			continue
		}
		if err := s.applyLocalTimer(name, t.TimerFile, t.ServiceFile); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", t.Unit, err))
			continue
		}
		applied = append(applied, t.Unit)
		if t.Enabled {
			enable = append(enable, t.Unit)
		}
	}
	if len(applied) == 0 {
		return applied, problems
	}
	if out, err := s.run(ctx, "daemon-reload"); err != nil {
		problems = append(problems, fmt.Sprintf("daemon-reload: %v %s", err, strings.TrimSpace(string(out))))
		return applied, problems
	}
	for _, unit := range enable {
		if out, err := s.run(ctx, "enable", "--runtime", "--now", "--no-pager", "--", unit); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v %s", unit, err, strings.TrimSpace(string(out))))
		}
	}
	return applied, problems
}
