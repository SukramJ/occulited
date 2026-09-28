package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// HmIPLocalKey is task 149's local key mode (D-103): the HmIP network key kept on the box, so a
// radio swap needs no eQ-3 key server. The switch writes the keys into hmip_user.conf (the only
// hmipserver keys lite writes there) with KeyServer.Mode=LOCAL, after copying the module's identity
// files and the file aside; the revert puts that snapshot back. HmIP-RF restarts at once either way,
// since hmipserver writes a configured key into the module at its start. A wrong entered key shows
// only as devices going silent, so after the switch the devices' UNREACH is watched for a while.
type HmIPLocalKey struct {
	Root     Root
	Services ServiceManager
	// StateDir holds state.json and the snapshots, one directory per module SGTIN (0700; the
	// snapshots carry key material).
	StateDir string
	// HmIP is hmipserver's interface; false while there is none.
	HmIP func() (interfaces.Interface, bool)
	// Plan is the last radio run's plan (the module's SGTIN).
	Plan func() (radio.Plan, bool)
	// Busy: a connection change or a flash is running, which a switch must not cross.
	Busy func() bool
	// Restarts is the lock shared with the device keys' apply (B-198): whoever rewrites
	// hmipserver's configuration and restarts it holds it. One of its own when nil.
	Restarts *HmIPRestartLock
	Log      *slog.Logger
	Now      func() time.Time

	// the check's timing: how long hmipserver gets to answer after the restart, how long and how
	// often the devices are watched (defaults 3 min, 10 min, 30 s)
	CheckWait, CheckFor, CheckEvery time.Duration
	// OverrideFor: the pairing override ends at the latest after this, if no install mode came (30 min)
	OverrideFor time.Duration

	mu        sync.Mutex
	stateMu   sync.Mutex // state.json's read-modify-write: the check and a switch both update it
	switching string     // "on", "off", "override" while a switch runs
	lastErr   string
	cancel    context.CancelFunc
}

// LocalKeyCheck is the watch after a switch. It watches the devices that answered before it
// (UNREACH known and false): a device hmipserver has not heard from since its start has no value
// at all, and a wrong key leaves every device like that.
type LocalKeyCheck struct {
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	// State: waiting (for hmipserver), running, ok, failed (most watched devices unreachable, or
	// none heard from again), skipped (no device to watch), error (hmipserver never answered),
	// interrupted (occulited restarted), superseded (a switch or the override cut it off, B-197)
	State  string `json:"state"`
	Before int    `json:"before"` // devices hmipserver listed before the switch, -1 when unknown
	// Total: the devices watched; Heard: of those, answering again; Unreachable: reported
	// unreachable since; Quiet: not heard from yet
	Total       int      `json:"total"`
	Heard       int      `json:"heard"`
	Unreachable []string `json:"unreachable"`
	Quiet       []string `json:"quiet"`
	Error       string   `json:"error,omitempty"`
}

type localKeyState struct {
	Source        string         `json:"source,omitempty"` // entered, generated
	SwitchedAt    time.Time      `json:"switched_at,omitzero"`
	Override      bool           `json:"override,omitempty"`
	OverrideSince time.Time      `json:"override_since,omitzero"`
	OverrideSeen  bool           `json:"override_seen,omitempty"` // the install mode was seen on
	Check         *LocalKeyCheck `json:"check,omitempty"`
}

// LocalKeySnapshot is one module's identity copied aside before a switch - or, with Kind
// fresh-start, a previous module's identity the fresh start moved aside (hmipexchange.go).
type LocalKeySnapshot struct {
	SGTIN string    `json:"sgtin"`
	At    time.Time `json:"at"`
	Files []string  `json:"files"`
	Kind  string    `json:"kind,omitempty"`
}

// LocalKeyStatus is what the API answers; it never carries a key.
type LocalKeyStatus struct {
	// Available: there is an HmIP module to switch
	Available bool   `json:"available"`
	SGTIN     string `json:"sgtin,omitempty"`
	Enabled   bool   `json:"enabled"`
	// Source: entered, generated, or manual for a key found in the file that lite did not write
	Source        string `json:"source,omitempty"`
	KeyServerMode string `json:"keyserver_mode"`
	// ExchangeID: hmip_address.conf carries accesspoint.exchange.id, and hmipserver ignores a
	// configured key for this access point
	ExchangeID bool               `json:"exchange_id"`
	Snapshots  []LocalKeySnapshot `json:"snapshots"`
	// RevertBlocked says why "back to eQ-3's key server" is not possible now; empty when it is
	RevertBlocked  string         `json:"revert_blocked,omitempty"`
	OverrideActive bool           `json:"override_active"`
	OverrideSince  time.Time      `json:"override_since,omitzero"`
	Check          *LocalKeyCheck `json:"check,omitempty"`
	Switching      string         `json:"switching,omitempty"`
	Error          string         `json:"error,omitempty"`
	// Devices: the paired HmIP devices, asked for with ?devices=1 only (-1: hmipserver silent)
	Devices *int `json:"devices,omitempty"`
}

const (
	hmipAddressConf = "/etc/config/hmip_address.conf"
	crRFDDataDir    = "/etc/config/crRFD/data"
)

var sgtinRe = regexp.MustCompile(`^[0-9A-F]{24}$`)

func (k *HmIPLocalKey) log() *slog.Logger {
	if k.Log != nil {
		return k.Log
	}
	return slog.Default()
}

func (k *HmIPLocalKey) now() time.Time {
	if k.Now != nil {
		return k.Now()
	}
	return time.Now()
}

func orDur(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

func (k *HmIPLocalKey) statePath() string { return filepath.Join(k.StateDir, "state.json") }

func (k *HmIPLocalKey) readState() localKeyState {
	var st localKeyState
	if b, err := os.ReadFile(k.statePath()); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	return st
}

// update changes state.json under stateMu.
func (k *HmIPLocalKey) update(f func(*localKeyState)) {
	k.stateMu.Lock()
	defer k.stateMu.Unlock()
	st := k.readState()
	f(&st)
	k.writeState(st)
}

func (k *HmIPLocalKey) writeState(st localKeyState) {
	_ = os.MkdirAll(k.StateDir, 0o700)
	b, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(k.statePath()+".tmp", b, 0o600); err == nil {
		_ = os.Rename(k.statePath()+".tmp", k.statePath())
	}
}

func (k *HmIPLocalKey) conf() string {
	return readFile(k.Root.join(hmipUserConf))
}

// HmIPPairing (task 192) is the pairing fact the metadata API's /version answers: the key-server
// mode and how many device keys the system holds, from hmip_user.conf and sgtin.map as they are
// on disk (both are in the helper's read list, so it is read as occulite too).
func HmIPPairing(root Root) radio.Pairing {
	return radio.PairingOf(readFile(root.join(hmipUserConf)), readFile(root.join(radio.DeviceKeyMapFile)))
}

// hmipUserConfMode: the file is world-readable until it carries a key (task 149); hmipserver,
// which owns it after its start, and root read it then, occulited through the helper.
func hmipUserConfMode(conf string) os.FileMode {
	if radio.ReadLocalKey(conf).Enabled() || radio.ReadLocalKey(conf).BackboneKey != "" {
		return 0o640
	}
	return 0o644
}

// editConf rewrites hmip_user.conf through edit, from the file as it is now; a file that could
// not be read is left alone rather than rewritten from nothing (B-271).
func (k *HmIPLocalKey) editConf(edit func(string) string) error {
	conf, _, err := readFileErr(k.Root.join(hmipUserConf))
	if err != nil {
		return err
	}
	conf = edit(conf)
	return writeFileAtomic(k.Root.join(hmipUserConf), []byte(conf), hmipUserConfMode(conf))
}

func (k *HmIPLocalKey) sgtin() string {
	if k.Plan == nil {
		return ""
	}
	p, ok := k.Plan()
	if !ok || p.HmIP == nil || !sgtinRe.MatchString(strings.ToUpper(p.HmIP.SGTIN)) {
		return ""
	}
	return strings.ToUpper(p.HmIP.SGTIN)
}

func (k *HmIPLocalKey) snapshotDir(sgtin string) string {
	return filepath.Join(k.StateDir, "snapshots", sgtin)
}

// Snapshots lists the snapshots kept, newest first.
func (k *HmIPLocalKey) Snapshots() []LocalKeySnapshot {
	out := []LocalKeySnapshot{}
	dirs, _ := os.ReadDir(filepath.Join(k.StateDir, "snapshots"))
	for _, d := range dirs {
		if !d.IsDir() || !sgtinRe.MatchString(d.Name()) {
			continue
		}
		var s LocalKeySnapshot
		if b, err := os.ReadFile(filepath.Join(k.snapshotDir(d.Name()), "snapshot.json")); err != nil || json.Unmarshal(b, &s) != nil {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].At.After(out[b].At) })
	return out
}

func (k *HmIPLocalKey) snapshotFor(sgtin string) (LocalKeySnapshot, bool) {
	for _, s := range k.Snapshots() {
		if s.SGTIN == sgtin {
			return s, true
		}
	}
	return LocalKeySnapshot{}, false
}

// Status assembles the answer.
func (k *HmIPLocalKey) Status() LocalKeyStatus {
	k.mu.Lock()
	switching, lastErr := k.switching, k.lastErr
	k.mu.Unlock()
	conf := k.conf()
	key := radio.ReadLocalKey(conf)
	st := k.readState()
	sg := k.sgtin()
	out := LocalKeyStatus{
		Available:      sg != "",
		SGTIN:          sg,
		Enabled:        key.Enabled(),
		KeyServerMode:  key.KeyServerMode,
		ExchangeID:     radio.ExchangeIDSet(readFile(k.Root.join(hmipAddressConf))),
		Snapshots:      k.Snapshots(),
		OverrideActive: st.Override,
		OverrideSince:  st.OverrideSince,
		Check:          st.Check,
		Switching:      switching,
		Error:          lastErr,
	}
	if out.KeyServerMode == "" {
		out.KeyServerMode = radio.KeyServerLocalFallback // the shipped template's
	}
	if out.Enabled {
		out.Source = st.Source
		if out.Source == "" {
			out.Source = "manual"
		}
	}
	switch _, ok := k.snapshotFor(sg); {
	case !out.Enabled:
		out.RevertBlocked = "local key mode is off"
	case sg == "":
		out.RevertBlocked = "no HmIP module is in use"
	case !ok && len(out.Snapshots) > 0:
		out.RevertBlocked = "the snapshot belongs to another radio module (" + out.Snapshots[0].SGTIN + "); restoring it would break this one"
	case !ok:
		out.RevertBlocked = "there is no snapshot of this module from before the switch (the key was set by hand)"
	}
	return out
}

// Switching reports whether a switch is running (the page shows it; the device keys' apply is
// kept off a switch by the shared Restarts lock).
func (k *HmIPLocalKey) Switching() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.switching != ""
}

// ErrLocalKey is a switch that cannot be done now; its message says why.
type ErrLocalKey struct{ Msg string }

func (e ErrLocalKey) Error() string { return e.Msg }

// holderName is what the shared restart lock says a switch is, to the device keys' apply.
func holderName(what string) string {
	if what == "retry" || what == "fresh-start" {
		return "the adapter exchange"
	}
	return "a local key switch"
}

// claim marks a switch as running under mu: nothing else of local key mode's, nothing the shared
// restart lock knows of (the device keys' apply), and - unless the caller has checked it - no
// connection change or flash. Called with mu held.
func (k *HmIPLocalKey) claim(what string) error {
	if k.switching != "" {
		return ErrLocalKey{"a switch is running"}
	}
	if k.Busy != nil && k.Busy() {
		return ErrLocalKey{"a radio connection change or a firmware flash is running"}
	}
	if k.Restarts == nil {
		k.Restarts = &HmIPRestartLock{}
	}
	if held, ok := k.Restarts.Acquire(holderName(what)); !ok {
		return ErrLocalKey{held + " is running"}
	}
	k.switching, k.lastErr = what, ""
	return nil
}

func (k *HmIPLocalKey) begin(what string) (context.Context, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.claim(what); err != nil {
		return nil, err
	}
	if k.cancel != nil {
		k.cancel() // a check or an override watch of the state before ends here
	}
	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel
	// B-197: the check that was cut off ends now, with a state the page can show - not at
	// occulited's next start. Its goroutine writes nothing more (watch's save checks its context).
	k.update(func(st *localKeyState) {
		if st.Check != nil && checkOpen(st.Check.State) {
			st.Check.State, st.Check.Finished = "superseded", k.now()
		}
	})
	return ctx, nil
}

// checkOpen: the check has no final state yet.
func checkOpen(state string) bool { return state == "waiting" || state == "running" }

func (k *HmIPLocalKey) end(err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.switching = ""
	if k.Restarts != nil {
		k.Restarts.Release()
	}
	if err != nil {
		k.lastErr = err.Error()
		k.log().Warn("local key mode", "err", err)
	}
}

// Enable switches local key mode on: mode "known" with the user's network key (and optionally the
// backbone key), or "generate". The snapshot, the file and the restart run in the background; the
// page polls Status.
func (k *HmIPLocalKey) Enable(mode, networkKey, backboneKey string) error {
	sg := k.sgtin()
	if sg == "" {
		return ErrLocalKey{"no HmIP module is in use"}
	}
	conf := k.conf()
	if radio.ReadLocalKey(conf).Enabled() {
		return ErrLocalKey{"local key mode is on already"}
	}
	if radio.ExchangeIDSet(readFile(k.Root.join(hmipAddressConf))) {
		return ErrLocalKey{"hmip_address.conf carries accesspoint.exchange.id: hmipserver would ignore a configured key"}
	}
	var nwk, bbk string
	var err error
	switch mode {
	case "known":
		if nwk, err = radio.NormalizeKey(networkKey); err != nil {
			return ErrLocalKey{"network key: " + err.Error()}
		}
		if strings.TrimSpace(backboneKey) != "" {
			if bbk, err = radio.NormalizeKey(backboneKey); err != nil {
				return ErrLocalKey{"backbone key: " + err.Error()}
			}
		}
	case "generate":
		if nwk, err = radio.GenerateKey(); err != nil {
			return err
		}
		if bbk, err = radio.GenerateKey(); err != nil {
			return err
		}
	default:
		return ErrLocalKey{"mode is known or generate"}
	}
	ctx, err := k.begin("on")
	if err != nil {
		return err
	}
	source := map[string]string{"known": "entered", "generate": "generated"}[mode]
	go func() {
		before := k.states(ctx)
		if err := k.snapshot(sg, conf); err != nil {
			k.end(fmt.Errorf("the snapshot failed, nothing was changed: %w", err))
			return
		}
		if err := k.editConf(func(c string) string { return radio.SetLocalKey(c, nwk, bbk) }); err != nil {
			k.end(fmt.Errorf("writing hmip_user.conf: %w", err))
			return
		}
		k.update(func(st *localKeyState) { *st = localKeyState{Source: source, SwitchedAt: k.now()} })
		k.log().Info("local key mode on", "source", source, "sgtin", sg)
		err := k.restart(ctx)
		k.end(err)
		if err == nil {
			k.watch(ctx, before)
		}
	}()
	return nil
}

// Disable goes back to eQ-3's key server: the snapshot of this module's identity and the key lines
// of hmip_user.conf from before the switch, then HmIP-RF restarts.
func (k *HmIPLocalKey) Disable() error {
	s := k.Status()
	if s.RevertBlocked != "" {
		return ErrLocalKey{s.RevertBlocked}
	}
	snap, _ := k.snapshotFor(s.SGTIN)
	ctx, err := k.begin("off")
	if err != nil {
		return err
	}
	go func() {
		dir := k.snapshotDir(snap.SGTIN)
		for _, f := range snap.Files {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				k.end(fmt.Errorf("reading the snapshot's %s: %w", f, err))
				return
			}
			// the file is root's for a moment: hmipserver's prep gives the directory and what is
			// in it back to hmipserver at its start (0600, openccu-lite B-253)
			if err := writeFileAtomic(k.Root.join(filepath.Join(crRFDDataDir, f)), b, 0o600); err != nil {
				k.end(fmt.Errorf("restoring %s: %w", f, err))
				return
			}
		}
		before, _ := os.ReadFile(filepath.Join(dir, "hmip_user.conf"))
		if err := k.editConf(func(c string) string { return radio.RestoreKeyLines(c, string(before)) }); err != nil {
			k.end(fmt.Errorf("writing hmip_user.conf: %w", err))
			return
		}
		k.update(func(st *localKeyState) { *st = localKeyState{} })
		k.log().Info("local key mode off: the snapshot is back", "sgtin", snap.SGTIN)
		k.end(k.restart(ctx))
	}()
	return nil
}

// Override allows the key server for the next pairing (KEYSERVER_LOCAL) or ends that; it ends by
// itself once an install mode has come and gone, or after OverrideFor.
func (k *HmIPLocalKey) Override(on bool) error {
	conf := k.conf()
	if !radio.ReadLocalKey(conf).Enabled() {
		return ErrLocalKey{"local key mode is off"}
	}
	st := k.readState()
	if st.Override == on {
		return nil
	}
	ctx, err := k.begin("override")
	if err != nil {
		return err
	}
	go func() {
		err := k.setOverride(ctx, on)
		k.end(err)
		if err == nil && on {
			k.watchOverride(ctx)
		}
	}()
	return nil
}

func (k *HmIPLocalKey) setOverride(ctx context.Context, on bool) error {
	mode := radio.KeyServerLocal
	if on {
		mode = radio.KeyServerLocalFallback
	}
	if err := k.editConf(func(c string) string { return radio.SetKeyServerMode(c, mode) }); err != nil {
		return fmt.Errorf("writing hmip_user.conf: %w", err)
	}
	k.update(func(st *localKeyState) {
		st.Override, st.OverrideSeen = on, false
		st.OverrideSince = time.Time{}
		if on {
			st.OverrideSince = k.now()
		}
	})
	k.log().Info("local key mode: key server for the next pairing", "on", on)
	return k.restart(ctx)
}

// watchOverride ends the override once the install mode was on and is off again, or at the latest
// after OverrideFor.
func (k *HmIPLocalKey) watchOverride(ctx context.Context) {
	limit := orDur(k.OverrideFor, 30*time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		st := k.readState()
		if !st.Override {
			return
		}
		expired := k.now().Sub(st.OverrideSince) > limit
		if !expired {
			i, ok := k.hmip()
			if !ok {
				continue
			}
			n, err := interfaces.InstallMode(ctx, i, 10*time.Second)
			if err != nil {
				continue
			}
			if n > 0 {
				if !st.OverrideSeen {
					k.update(func(st *localKeyState) { st.OverrideSeen = true })
				}
				continue
			}
			if !st.OverrideSeen {
				continue
			}
		}
		k.mu.Lock()
		err := k.claim("override")
		k.mu.Unlock()
		if err != nil {
			continue // a switch or the device keys' apply runs: try again in 10 s
		}
		k.end(k.setOverride(ctx, false))
		return
	}
}

func (k *HmIPLocalKey) hmip() (interfaces.Interface, bool) {
	if k.HmIP == nil {
		return interfaces.Interface{}, false
	}
	return k.HmIP()
}

// DeviceCount is the HmIP devices hmipserver lists, -1 when it does not answer (the welcome page's
// step is for an empty network only).
func (k *HmIPLocalKey) DeviceCount(ctx context.Context) int {
	st := k.states(ctx)
	if st == nil {
		return -1
	}
	return len(st)
}

// states is every HmIP device's UNREACH state, nil when hmipserver does not answer.
func (k *HmIPLocalKey) states(ctx context.Context) map[string]interfaces.DeviceState {
	i, ok := k.hmip()
	if !ok {
		return nil
	}
	st, err := interfaces.Reachability(ctx, i, 10*time.Second)
	if err != nil {
		return nil
	}
	return st
}

func (k *HmIPLocalKey) restart(ctx context.Context) error {
	if _, err := k.Services.Control(ctx, "hmipserver", "restart"); err != nil {
		return fmt.Errorf("restarting hmipserver: %w", err)
	}
	return nil
}

// snapshot copies the module's identity files and hmip_user.conf aside; a snapshot of the same
// module that is kept already stays - it is the older one, from before any switch.
func (k *HmIPLocalKey) snapshot(sg, conf string) error {
	if _, ok := k.snapshotFor(sg); ok {
		return nil
	}
	dir := k.snapshotDir(sg)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Join(k.StateDir, "snapshots"), 0o700)
	_ = os.Chmod(k.StateDir, 0o700)
	snap := LocalKeySnapshot{SGTIN: sg, At: k.now(), Files: []string{}}
	for _, name := range k.identityFiles(sg) {
		src := k.Root.join(filepath.Join(crRFDDataDir, name))
		b := readFile(src)
		if b == "" {
			return fmt.Errorf("%s is empty or unreadable", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
			return err
		}
		snap.Files = append(snap.Files, name)
	}
	if len(snap.Files) == 0 {
		return errors.New("the module has no identity files in " + crRFDDataDir)
	}
	if err := os.WriteFile(filepath.Join(dir, "hmip_user.conf"), []byte(conf), 0o600); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	return os.WriteFile(filepath.Join(dir, "snapshot.json"), b, 0o600)
}

// identityFiles names the module's identity files that are in hmipserver's data directory - the
// access point file, the key exchange and the backbone key exchange - in that order. The
// directory is 0700 (openccu-lite B-253): the names come through the helper, and so do the files.
func (k *HmIPLocalKey) identityFiles(sg string) []string {
	present := map[string]bool{}
	for _, name := range readDir(k.Root.join(crRFDDataDir)) {
		present[strings.ToUpper(name)] = true
	}
	var out []string
	for _, ext := range []string{".ap", ".apkx", ".bbkx"} {
		if present[strings.ToUpper(sg+ext)] {
			out = append(out, sg+ext)
		}
	}
	return out
}

// DiscardSnapshot removes one module's snapshot.
func (k *HmIPLocalKey) DiscardSnapshot(sgtin string) error {
	sgtin = strings.ToUpper(sgtin)
	if !sgtinRe.MatchString(sgtin) {
		return ErrLocalKey{"not an SGTIN"}
	}
	if _, ok := k.snapshotFor(sgtin); !ok {
		return ErrLocalKey{"no snapshot of " + sgtin}
	}
	k.log().Info("local key mode: snapshot discarded", "sgtin", sgtin)
	return os.RemoveAll(k.snapshotDir(sgtin))
}

// watch is the check after a switch: hmipserver gets CheckWait to answer, then the devices that
// answered before the switch are read every CheckEvery for CheckFor. One of them heard from again
// is a working key (maintainer, 2026-09-18: a wrong key brings none back); none heard, or most
// reporting unreachable, fails the check. Access points are not watched: on the Charly a DRAP kept
// answering over the LAN under a wrong key. Without a reading from before (hmipserver was silent)
// every radio device listed after the restart is watched.
func (k *HmIPLocalKey) watch(ctx context.Context, before map[string]interfaces.DeviceState) {
	c := &LocalKeyCheck{Started: k.now(), State: "waiting", Before: -1, Unreachable: []string{}, Quiet: []string{}}
	save := func() {
		cp := *c
		cp.Unreachable = append([]string{}, c.Unreachable...)
		cp.Quiet = append([]string{}, c.Quiet...)
		k.update(func(st *localKeyState) {
			// cut off by a switch (B-197): begin has marked the check superseded under the same
			// lock, and nothing of this check may overwrite that
			if ctx.Err() != nil {
				return
			}
			st.Check = &cp
		})
	}
	var watched []string
	if before != nil {
		c.Before = len(before)
		for a, s := range before {
			if s.State == "ok" && !s.AccessPoint {
				watched = append(watched, a)
			}
		}
		sort.Strings(watched)
		if len(watched) == 0 {
			c.State, c.Finished = "skipped", k.now()
			save()
			return
		}
	}
	save()
	read := func(now map[string]interfaces.DeviceState) {
		if before == nil && watched == nil {
			for a, s := range now {
				if !s.AccessPoint {
					watched = append(watched, a)
				}
			}
			sort.Strings(watched)
		}
		c.Total, c.Heard, c.Unreachable, c.Quiet = len(watched), 0, []string{}, []string{}
		for _, a := range watched {
			switch now[a].State {
			case "ok":
				c.Heard++
			case "unreach":
				c.Unreachable = append(c.Unreachable, a)
			default:
				c.Quiet = append(c.Quiet, a)
			}
		}
	}
	deadline := k.now().Add(orDur(k.CheckWait, 3*time.Minute))
	for {
		if now := k.states(ctx); now != nil {
			read(now)
			c.State = "running"
			break
		}
		if k.now().After(deadline) {
			c.State, c.Error, c.Finished = "error", "hmipserver did not answer after the restart", k.now()
			save()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(min(orDur(k.CheckEvery, 5*time.Second), 5*time.Second)):
		}
	}
	save()
	end := k.now().Add(orDur(k.CheckFor, 10*time.Minute))
	for k.now().Before(end) {
		select {
		case <-ctx.Done():
			return
		case <-time.After(orDur(k.CheckEvery, 30*time.Second)):
		}
		if now := k.states(ctx); now != nil {
			read(now)
			save()
		}
	}
	c.Finished = k.now()
	switch {
	case c.Total == 0:
		c.State = "skipped"
	case len(c.Unreachable)*2 > c.Total || c.Heard == 0:
		c.State = "failed"
	default:
		c.State = "ok"
	}
	save()
	k.log().Info("local key mode: the check after the switch", "state", c.State, "watched", c.Total, "heard", c.Heard, "unreachable", len(c.Unreachable), "quiet", len(c.Quiet))
}

// Load is run at start: a check cut off by a restart of occulited is marked so, and a pairing
// override is watched again.
func (k *HmIPLocalKey) Load() {
	k.update(func(st *localKeyState) {
		if st.Check != nil && (st.Check.State == "waiting" || st.Check.State == "running") {
			st.Check.State, st.Check.Finished = "interrupted", k.now()
		}
	})
	st := k.readState()
	if st.Override {
		ctx, cancel := context.WithCancel(context.Background())
		k.mu.Lock()
		k.cancel = cancel
		k.mu.Unlock()
		go k.watchOverride(ctx)
	}
}

// CheckFailed is the warning's condition: the last switch's check found most devices unreachable.
func (k *HmIPLocalKey) CheckFailed() *LocalKeyCheck {
	st := k.readState()
	if st.Check != nil && st.Check.State == "failed" && radio.ReadLocalKey(k.conf()).Enabled() {
		return st.Check
	}
	return nil
}
