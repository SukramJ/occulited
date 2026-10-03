package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// The radio-exchange diagnosis and what the page offers (D-106). hmipserver's ready step records
// why an adapter exchange was rejected - the key server was never reached, or it answered and
// refused this module (radio.HmIPFatal.Cause). The page offers a retry for the first case and,
// for the second, the guided fresh start: the previous module's HmIP identity is moved aside into
// a snapshot (kept, never deleted), local key mode can be switched on in the same step, and
// HmIP-RF starts on this module with an empty network. These live on the local key service
// because they touch the same things - the identity files, the snapshots, hmip_user.conf, the
// restart - and must not run beside a switch.

// SnapshotFreshStart marks a snapshot the fresh start made: the previous module's identity as it
// was, not one made before a switch to local key mode.
const SnapshotFreshStart = "fresh-start"

// SnapshotModuleMove marks the snapshot a connection change takes before it moves HmIP-RF to
// another module (openccu-lite B-285, maintainer 2026-09-30): the module's identity files,
// hmip_address.conf and every device file, with the connection choices from before the move. The
// move is an adapter exchange through eQ-3's key server, which may refuse the exchange back (it
// did on a lab system, and accepted one on another) - and hmipserver rewrites the device files for
// the new access point, so the way back without the server needs the pre-move identity and device
// files, which only this snapshot has.
const SnapshotModuleMove = "module-move"

// HmIPMoveBack is the Interfaces page's offer: a module-move snapshot is kept, and HmIP-RF can go
// back to that module from it without the key server.
type HmIPMoveBack struct {
	Previous string        `json:"previous"` // the module the snapshot belongs to
	At       time.Time     `json:"at"`
	Choices  radio.Choices `json:"choices"` // the connection choices from before the move
	Devices  int           `json:"devices"` // device files in the snapshot
	// CounterGap: the previous module's HmIP security counter is behind what the module in use
	// sent (openccu-lite B-302) - the dialog says the expected wait before the user confirms
	CounterGap *HmIPCounterGap `json:"counter_gap,omitempty"`
}

// SnapshotBeforeMove copies the module's identity files, hmip_address.conf and every device file
// of hmipserver's data directory into a module-move snapshot before a connection change moves
// HmIP-RF away from it (the change calls it before it writes anything). Nothing is removed. With
// local key mode on nothing is taken: no key server is involved then, and the switch's snapshot
// of the module must stay. A snapshot of the module kept from an earlier move or a fresh start
// is replaced - it is older than what is on the system now.
func (k *HmIPLocalKey) SnapshotBeforeMove(from string, prev radio.Choices) error {
	from = strings.ToUpper(from)
	if !sgtinRe.MatchString(from) {
		return ErrLocalKey{"not an SGTIN: " + from}
	}
	conf := k.conf()
	if radio.ReadLocalKey(conf).Enabled() {
		return nil
	}
	if s, ok := k.snapshotFor(from); ok && s.Kind == "" {
		return ErrLocalKey{"a snapshot of " + from + " from before the switch to local key mode is kept; discard it first"}
	}
	dir := k.snapshotDir(from)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Join(k.StateDir, "snapshots"), 0o700)
	_ = os.Chmod(k.StateDir, 0o700)
	choices := prev
	snap := LocalKeySnapshot{SGTIN: from, At: k.now(), Files: []string{}, Kind: SnapshotModuleMove, Choices: &choices}
	copyIn := func(src, name string) error {
		b := readFile(src)
		if b == "" {
			return fmt.Errorf("%s is empty or unreadable", name)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(b), 0o600); err != nil {
			return err
		}
		snap.Files = append(snap.Files, name)
		return nil
	}
	ids := k.identityFiles(from)
	if len(ids) == 0 {
		_ = os.RemoveAll(dir)
		return ErrLocalKey{"no identity files of " + from + " in " + crRFDDataDir}
	}
	for _, name := range ids {
		if err := copyIn(k.Root.join(filepath.Join(crRFDDataDir, name)), name); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
	}
	if readFile(k.Root.join(hmipAddressConf)) != "" {
		if err := copyIn(k.Root.join(hmipAddressConf), filepath.Base(hmipAddressConf)); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
	}
	for _, name := range k.deviceFiles() {
		if err := copyIn(k.Root.join(filepath.Join(crRFDDataDir, name)), name); err != nil {
			_ = os.RemoveAll(dir)
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hmip_user.conf"), []byte(conf), 0o600); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), b, 0o600); err != nil {
		return err
	}
	k.log().Info("module move: the previous module's identity and device files kept", "module", from, "devices", len(k.deviceFiles()), "choices", fmt.Sprintf("%+v", prev))
	return nil
}

// LocalKeySnapshotKept: a snapshot of the module from a switch to local key mode is kept (B-301).
func (k *HmIPLocalKey) LocalKeySnapshotKept(sgtin string) bool {
	s, ok := k.snapshotFor(strings.ToUpper(sgtin))
	return ok && s.Kind == ""
}

// deviceFiles names hmipserver's device files (<SGTIN>.dev) in its data directory, sorted.
func (k *HmIPLocalKey) deviceFiles() []string {
	var out []string
	for _, name := range readDir(k.Root.join(crRFDDataDir)) {
		if strings.HasSuffix(strings.ToUpper(name), ".DEV") && sgtinRe.MatchString(strings.ToUpper(strings.TrimSuffix(name, filepath.Ext(name)))) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// moveSnapshot is the newest module-move snapshot, if one is kept.
func (k *HmIPLocalKey) moveSnapshot() (LocalKeySnapshot, bool) {
	for _, s := range k.Snapshots() {
		if s.Kind == SnapshotModuleMove {
			return s, true
		}
	}
	return LocalKeySnapshot{}, false
}

// MoveBackOffer is what the Interfaces page shows while a module-move snapshot is kept; nil
// without one, and with local key mode on (no key server, no one-way move).
func (k *HmIPLocalKey) MoveBackOffer() *HmIPMoveBack {
	s, ok := k.moveSnapshot()
	if !ok || s.Choices == nil || radio.ReadLocalKey(k.conf()).Enabled() {
		return nil
	}
	n := 0
	for _, f := range s.Files {
		if strings.HasSuffix(strings.ToUpper(f), ".DEV") {
			n++
		}
	}
	return &HmIPMoveBack{Previous: s.SGTIN, At: s.At, Choices: *s.Choices, Devices: n, CounterGap: k.moveBackGap(s)}
}

// MoveBack takes HmIP-RF back to the module a module-move snapshot belongs to (openccu-lite
// B-285): a connection change to the choices from before the move, and - while the daemons are
// stopped - every other module's identity in the data directory (the one the exchange made for
// the module HmIP-RF ran on since) moved aside into a fresh-start snapshot of its own, the
// snapshot's identity files, device files and hmip_address.conf put back, a marker of a rejected
// exchange cleared. hmipserver then starts on the previous module with its own identity and the
// devices as they were: no key-server exchange. The snapshot is consumed. Refused with local key
// mode on, without a snapshot, and beside a switch, a connection change or a flash.
func (k *HmIPLocalKey) MoveBack() error {
	snap, ok := k.moveSnapshot()
	if !ok {
		return ErrLocalKey{"no snapshot from before a module move is kept"}
	}
	if snap.Choices == nil {
		return ErrLocalKey{"the snapshot of " + snap.SGTIN + " carries no connection choices"}
	}
	if radio.ReadLocalKey(k.conf()).Enabled() {
		return ErrLocalKey{"local key mode is on: the module is changed on the Interfaces page, no key server is involved"}
	}
	if k.Conn == nil {
		return ErrLocalKey{"the connection change is not available"}
	}
	conf := k.conf()
	// B-302: the counter gap, while the snapshot's access point file is still there
	gap := k.moveBackGap(snap)
	ctx, err := k.begin("move-back")
	if err != nil {
		return err
	}
	dir := k.snapshotDir(snap.SGTIN)
	restore := func() error {
		// D-120 (task 317): the identity files as they are now, copied before anything moves
		if _, err := k.backupIdentity("move-back"); err != nil {
			return fmt.Errorf("the copy of the identity files failed: %w", err)
		}
		for _, p := range k.previousIdentities(snap.SGTIN) {
			if err := k.moveIdentityAside(p, conf); err != nil {
				return fmt.Errorf("moving the identity of %s aside: %w", p, err)
			}
		}
		for _, f := range snap.Files {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				return fmt.Errorf("reading the snapshot's %s: %w", f, err)
			}
			if f == filepath.Base(hmipAddressConf) {
				// hmipserver's own (0644): it rewrites the file when the address changes
				if err := writeFileAtomic(k.Root.join(hmipAddressConf), b, 0o644); err != nil {
					return fmt.Errorf("restoring %s: %w", f, err)
				}
				if uid, gid, ok := hmipserverIDs(); ok {
					_ = Priv.Chown(k.Root.join(hmipAddressConf), uid, gid, false)
				}
				continue
			}
			// root's for a moment, as the fresh-start restore: hmipserver's prep gives the
			// directory and what is in it back to hmipserver at its start (openccu-lite B-253)
			if err := writeFileAtomic(k.Root.join(filepath.Join(crRFDDataDir, f)), b, 0o600); err != nil {
				return fmt.Errorf("restoring %s: %w", f, err)
			}
		}
		if err := k.clearFatal(); err != nil {
			return err
		}
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("removing the consumed snapshot: %w", err)
		}
		return nil
	}
	go func() {
		k.log().Warn("module move: back to the previous module from its snapshot", "module", snap.SGTIN, "choices", fmt.Sprintf("%+v", *snap.Choices), "files", len(snap.Files))
		err := k.Conn(ctx, *snap.Choices, restore)
		if err == nil && gap != nil {
			gap.At = k.now().UTC()
			k.saveCounterGap(gap)
			k.log().Warn("module move: the previous module's HmIP security counter is behind what the other module sent; devices that heard it may ignore commands until the counter has caught up",
				"module", gap.To, "from", gap.From, "behind", gap.Behind, "until", gap.Until.Format(time.RFC3339))
		}
		k.end(err)
	}()
	return nil
}

// hmipserverIDs is hmipserver's uid and gid on this system; false where the account is unknown.
func hmipserverIDs() (uid, gid int, ok bool) {
	u, err := lookupSystemUser("hmipserver")
	if err != nil {
		return 0, 0, false
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	return uid, gid, err1 == nil && err2 == nil
}

// lookupSystemUser is user.Lookup, replaceable by a test.
var lookupSystemUser = user.Lookup

// ExchangeView is what the API answers about the adapter exchange.
type ExchangeView struct {
	// Fatal: hmipserver's last start failed on a known error; nil when HmIP-RF is not stopped so
	Fatal *radio.HmIPFatal `json:"fatal,omitempty"`
	// Module: the module HmIP-RF runs on now (the plan's SGTIN)
	Module string `json:"module,omitempty"`
	// Previous: the modules whose identity files are on the system and are not this module's -
	// the network the exchange tried to move
	Previous []string `json:"previous"`
	// ReplacesSnapshots: previous modules a snapshot is kept for already; a fresh start replaces it
	ReplacesSnapshots []string `json:"replaces_snapshots,omitempty"`
	// LocalKey: local key mode is on already, so the fresh start has no key to generate
	LocalKey bool `json:"local_key"`
	// ExchangeID: hmip_address.conf carries accesspoint.exchange.id, which rules local key mode out
	ExchangeID bool   `json:"exchange_id"`
	Switching  string `json:"switching,omitempty"`
	Error      string `json:"error,omitempty"`
	// Exchanges is the local record of the adapter exchanges this system attempted (openccu-lite
	// task 301), newest first: which module took which network over, when, whether the key server
	// took part, how it went. Kept by hmipserver's unit steps under /etc/config, in every backup.
	Exchanges []radio.HmIPExchange `json:"exchanges"`
}

// Exchange assembles the view.
func (k *HmIPLocalKey) Exchange() ExchangeView {
	k.mu.Lock()
	switching, lastErr := k.switching, k.lastErr
	k.mu.Unlock()
	sg := k.sgtin()
	out := ExchangeView{
		Fatal:      radio.ReadHmIPFatal(string(k.Root)),
		Module:     sg,
		Previous:   k.previousIdentities(sg),
		LocalKey:   radio.ReadLocalKey(k.conf()).Enabled(),
		ExchangeID: radio.ExchangeIDSet(readFile(k.Root.join(hmipAddressConf))),
		Switching:  switching,
		Error:      lastErr,
		Exchanges:  radio.ReadHmIPExchanges(string(k.Root)),
	}
	if out.Exchanges == nil {
		out.Exchanges = []radio.HmIPExchange{}
	}
	for _, p := range out.Previous {
		if _, ok := k.snapshotFor(p); ok {
			out.ReplacesSnapshots = append(out.ReplacesSnapshots, p)
		}
	}
	return out
}

// previousIdentities lists the SGTINs with an access-point file in hmipserver's data directory
// other than the module in use, sorted.
func (k *HmIPLocalKey) previousIdentities(sg string) []string {
	out := []string{}
	for _, entry := range readDir(k.Root.join(crRFDDataDir)) {
		name := strings.ToUpper(entry)
		if !strings.HasSuffix(name, ".AP") {
			continue
		}
		id := strings.TrimSuffix(name, ".AP")
		if sgtinRe.MatchString(id) && id != sg {
			out = append(out, id)
		}
	}
	return out
}

// RetryExchange tries the adapter exchange again: the marker that keeps hmipserver from starting
// goes, and HmIP-RF restarts - the exchange is attempted at every start. For a key server that
// was not reached; the page offers it once the network is back.
func (k *HmIPLocalKey) RetryExchange() error {
	if f := radio.ReadHmIPFatal(string(k.Root)); f == nil || f.Code != "adapter-exchange-rejected" {
		return ErrLocalKey{"HmIP-RF is not stopped on a rejected adapter exchange"}
	}
	ctx, err := k.begin("retry")
	if err != nil {
		return err
	}
	go func() {
		if err := k.clearFatal(); err != nil {
			k.end(err)
			return
		}
		k.log().Info("adapter exchange: tried again", "module", k.sgtin())
		k.end(k.restart(ctx))
	}()
	return nil
}

// RestartExchange restarts HmIP-RF for an imported identity whose move onto this module is still
// pending (openccu-lite task 275): hmipserver attempts the adapter exchange at every start, so a
// restart is the retry when the first attempt did not get through (the key server not reached,
// devices not yet answering). A marker of a rejected exchange goes too, as RetryExchange does.
func (k *HmIPLocalKey) RestartExchange() error {
	ctx, err := k.begin("retry")
	if err != nil {
		return err
	}
	go func() {
		if err := k.clearFatal(); err != nil {
			k.end(err)
			return
		}
		k.log().Info("adapter exchange: HmIP-RF restarted for the imported identity", "module", k.sgtin())
		k.end(k.restart(ctx))
	}()
	return nil
}

// FreshStart sets HmIP up afresh with the module in use: every other module's identity files
// (<SGTIN>.ap, .apkx, .bbkx) move into a snapshot of their own, with hmip_user.conf as it is now,
// so nothing is deleted; with localKey the two keys are generated and written as the switch does
// (unless local key mode is on already), so the new network never depends on the key server;
// then the marker goes and HmIP-RF restarts. hmipserver then makes a new identity for this module
// - an empty network, every HmIP device to be reset and paired again. Refused while a switch,
// a connection change or a flash runs, and when nothing is stopped on a rejected exchange.
func (k *HmIPLocalKey) FreshStart(localKey bool) error {
	if f := radio.ReadHmIPFatal(string(k.Root)); f == nil || f.Code != "adapter-exchange-rejected" {
		return ErrLocalKey{"HmIP-RF is not stopped on a rejected adapter exchange"}
	}
	sg := k.sgtin()
	if sg == "" {
		return ErrLocalKey{"no HmIP module is in use"}
	}
	prev := k.previousIdentities(sg)
	if len(prev) == 0 {
		return ErrLocalKey{"no other module's identity is on the system; there is nothing to move aside"}
	}
	conf := k.conf()
	var nwk, bbk string
	if localKey && !radio.ReadLocalKey(conf).Enabled() {
		if radio.ExchangeIDSet(readFile(k.Root.join(hmipAddressConf))) {
			return ErrLocalKey{"hmip_address.conf carries accesspoint.exchange.id: hmipserver would ignore a configured key"}
		}
		var err error
		if nwk, err = radio.GenerateKey(); err != nil {
			return err
		}
		if bbk, err = radio.GenerateKey(); err != nil {
			return err
		}
	}
	ctx, err := k.begin("fresh-start")
	if err != nil {
		return err
	}
	go func() {
		// D-120 (task 317): the identity files as they are now, copied before anything moves
		if _, err := k.backupIdentity("fresh-start"); err != nil {
			k.end(fmt.Errorf("the copy of the identity files failed, nothing was changed: %w", err))
			return
		}
		for _, p := range prev {
			if err := k.moveIdentityAside(p, conf); err != nil {
				k.end(fmt.Errorf("moving the identity of %s aside: %w", p, err))
				return
			}
		}
		if nwk != "" {
			if err := k.editConf(func(c string) string { return radio.SetLocalKey(c, nwk, bbk) }); err != nil {
				k.end(fmt.Errorf("writing hmip_user.conf: %w", err))
				return
			}
			k.update(func(st *localKeyState) { *st = localKeyState{Source: "generated", SwitchedAt: k.now()} })
		}
		if err := k.clearFatal(); err != nil {
			k.end(err)
			return
		}
		k.log().Info("fresh start with this module", "module", sg, "previous", strings.Join(prev, ","), "local_key", nwk != "" || localKey)
		k.end(k.restart(ctx))
	}()
	return nil
}

// moveIdentityAside puts a previous module's identity files into its snapshot directory, replacing
// a snapshot kept there, and removes them from hmipserver's data directory. hmip_user.conf as it
// is now goes with them, as the switch's snapshot keeps it.
func (k *HmIPLocalKey) moveIdentityAside(sg, conf string) error {
	dir := k.snapshotDir(sg)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Join(k.StateDir, "snapshots"), 0o700)
	_ = os.Chmod(k.StateDir, 0o700)
	snap := LocalKeySnapshot{SGTIN: sg, At: k.now(), Files: []string{}, Kind: SnapshotFreshStart}
	var moved []string
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
		moved = append(moved, src)
	}
	if len(snap.Files) == 0 {
		return errors.New("no identity files in " + crRFDDataDir)
	}
	if err := os.WriteFile(filepath.Join(dir, "hmip_user.conf"), []byte(conf), 0o600); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), b, 0o600); err != nil {
		return err
	}
	// the copies are complete: now the originals go, so a failure in between loses nothing
	for _, src := range moved {
		if err := os.Remove(src); err != nil && !errors.Is(err, fs.ErrNotExist) {
			if !errors.Is(err, fs.ErrPermission) {
				return err
			}
			if err := remove(src); err != nil {
				return err
			}
		}
	}
	return nil
}

// clearFatal removes the marker that keeps hmipserver from starting; the marker is root's, so the
// privilege boundary does it where the daemon may not.
func (k *HmIPLocalKey) clearFatal() error {
	path := radio.FatalPath(string(k.Root))
	if err := os.Remove(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := remove(path); err != nil {
		return fmt.Errorf("clearing the marker %s: %w", path, err)
	}
	return nil
}

// RestoreSnapshot puts a fresh-start snapshot back (openccu-lite task 212): the previous module is
// in the system again and local key mode is off - with it on, Disable is the way back, key lines
// and all. Its identity files return to hmipserver's data directory, so the network and the
// devices paired under it are known again without eQ-3's key server; every other module's
// identity in the directory - the one the fresh start made for the module that was refused -
// moves into a fresh-start snapshot of its own first, so hmipserver attempts no exchange onto the
// restored network; a marker of a rejected exchange goes; HmIP-RF restarts. The snapshot is
// consumed: its files are the identity in use now, and restoring them again later would roll the
// network back behind the devices paired since. Refused for a snapshot the switch made (Disable's),
// for a module that is not in use, with local key mode on, and beside a switch, a connection
// change or a flash.
func (k *HmIPLocalKey) RestoreSnapshot(sgtin string) error {
	sgtin = strings.ToUpper(sgtin)
	if !sgtinRe.MatchString(sgtin) {
		return ErrLocalKey{"not an SGTIN"}
	}
	snap, ok := k.snapshotFor(sgtin)
	if !ok {
		return ErrLocalKey{"no snapshot of " + sgtin}
	}
	if snap.Kind != SnapshotFreshStart {
		return ErrLocalKey{"the snapshot of " + sgtin + " is from a switch to local key mode; \"Back to eQ-3's key server\" restores it"}
	}
	conf := k.conf()
	if radio.ReadLocalKey(conf).Enabled() {
		return ErrLocalKey{"local key mode is on: \"Back to eQ-3's key server\" is the way back to a kept identity"}
	}
	sg := k.sgtin()
	if sg == "" {
		return ErrLocalKey{"no HmIP module is in use"}
	}
	if sg != sgtin {
		return ErrLocalKey{"the module " + sgtin + " is not in use (" + sg + " is); put it back first"}
	}
	if len(snap.Files) == 0 {
		return ErrLocalKey{"the snapshot of " + sgtin + " holds no identity files"}
	}
	others := k.previousIdentities(sgtin)
	ctx, err := k.begin("restore")
	if err != nil {
		return err
	}
	go func() {
		// D-120 (task 317): the identity files as they are now, copied before anything moves
		if _, err := k.backupIdentity("restore"); err != nil {
			k.end(fmt.Errorf("the copy of the identity files failed, nothing was changed: %w", err))
			return
		}
		for _, p := range others {
			if err := k.moveIdentityAside(p, conf); err != nil {
				k.end(fmt.Errorf("moving the identity of %s aside: %w", p, err))
				return
			}
		}
		dir := k.snapshotDir(sgtin)
		for _, f := range snap.Files {
			b, err := os.ReadFile(filepath.Join(dir, f))
			if err != nil {
				k.end(fmt.Errorf("reading the snapshot's %s: %w", f, err))
				return
			}
			// root's for a moment, as Disable's restore: hmipserver's prep gives the directory and
			// what is in it back to hmipserver at its start (0600, openccu-lite B-253)
			if err := writeFileAtomic(k.Root.join(filepath.Join(crRFDDataDir, f)), b, 0o600); err != nil {
				k.end(fmt.Errorf("restoring %s: %w", f, err))
				return
			}
		}
		if err := k.clearFatal(); err != nil {
			k.end(err)
			return
		}
		// the files are in place: the snapshot has served
		if err := os.RemoveAll(dir); err != nil {
			k.end(fmt.Errorf("removing the restored snapshot: %w", err))
			return
		}
		k.log().Info("fresh-start snapshot restored: the previous module is back", "module", sgtin, "moved_aside", strings.Join(others, ","))
		k.end(k.restart(ctx))
	}()
	return nil
}
