package system

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
