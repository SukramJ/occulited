package system

// openccu-lite tasks 275 and 278: what the device import leaves for the boots after it. The import
// (radioimport.go) writes the backup's radio identity and reboots; hmipserver then moves the HmIP
// identity onto this system's module when the backup's module is another one - the adapter
// exchange, offline in local key mode, else through eQ-3's key server - and rfd runs with the
// imported BidCos address, serial and key store. Nothing in the daemons' state says afterwards
// that an import happened, so the import records it here: which module the identity came from,
// which module this system ran, whether the backup's BidCos key store is a non-default one. The
// Interfaces page reads the record and says how the exchange went (Outcome), offers a retry
// (a restart of hmipserver, which attempts the exchange at every start), and the administrator
// dismisses the record when done.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/interfaces"
	"github.com/hobbyquaker/occulited/internal/radio"
)

// ImportedRadio is the record of one device import.
type ImportedRadio struct {
	At      time.Time `json:"at"`
	File    string    `json:"file"`
	Version string    `json:"version,omitempty"`
	HmIP    struct {
		// FromSGTIN is the module the backup's identity was bound to; ToSGTIN this system's HmIP
		// module at the import ("" without one). ModuleChanged: they differ, so hmipserver has to
		// take the identity over (the adapter exchange).
		FromSGTIN     string `json:"from_sgtin,omitempty"`
		ToSGTIN       string `json:"to_sgtin,omitempty"`
		ToModule      string `json:"to_module,omitempty"` // hardware and serial, for the page
		ModuleChanged bool   `json:"module_changed"`
		LocalKey      bool   `json:"local_key"`
		Devices       int    `json:"devices"`
	} `json:"hmip"`
	BidCosRF struct {
		Address string `json:"address,omitempty"`
		Serial  string `json:"serial,omitempty"`
		Devices int    `json:"devices"`
		// NonDefaultKey: the backup's key store is not the factory key (task 278); KeyIndex its
		// index; TargetKeyReplaced: this system had a key of its own, set aside by the import.
		NonDefaultKey     bool   `json:"non_default_key"`
		KeyIndex          int    `json:"key_index"`
		TargetKeyReplaced bool   `json:"target_key_replaced"`
		Module            string `json:"module,omitempty"` // the module carrying BidCos-RF at the import
		// KeyCheck is the verdict on the backup's passphrase at the import (task 296: KeyCheck*,
		// "" in a record from before); the passphrase itself is nowhere
		KeyCheck string `json:"key_check,omitempty"`
	} `json:"bidcos_rf"`
	// HmIPFinal is the HmIP half of the outcome once it was final (done or rejected), kept so the
	// journal is not read at every poll (openccu-lite B-281); nil while the move is pending.
	HmIPFinal *ImportHmIPOutcome `json:"hmip_final,omitempty"`
}

// The outcomes of the HmIP identity's move, as ImportOutcome.HmIP.State says them.
const (
	// ImportNoExchange: the backup's identity belongs to this module already, or there is no
	// HmIP identity in it - nothing to move.
	ImportNoExchange = "none"
	// ImportPending: the previous module's identity file is still there, no marker: hmipserver
	// has not taken it over (yet) - it is starting, or waiting for the key server.
	ImportPending = "pending"
	// ImportDone: the module in use has its identity file and the previous module's is gone -
	// hmipserver took the identity over (it rewrites the files when the exchange succeeds).
	ImportDone = "done"
	// ImportRejected: hmipserver stopped on a rejected exchange (the marker, D-102); Cause says
	// whether the key server was not reached or refused the module.
	ImportRejected = "rejected"
	// ImportNoModule: this system has no HmIP module now; the identity waits for one.
	ImportNoModule = "no-module"
	// ImportUnknown: neither identity file is there.
	ImportUnknown = "unknown"
)

// ImportHmIPOutcome is the HmIP half of the outcome: the identity's move.
type ImportHmIPOutcome struct {
	State string `json:"state"`
	// Cause: the rejection's cause (unreachable, refused; adapter-version for a move onto a module
	// whose firmware cannot take the network key, openccu-lite B-289) when State is rejected
	Cause string `json:"cause,omitempty"`
	// ModuleNow is the HmIP module in use now (the plan's), which may differ from the import's
	ModuleNow string `json:"module_now,omitempty"`
	// Line is hmipserver's own word on the exchange since the import, when the journal has one
	Line string `json:"line,omitempty"`
}

// ImportOutcome is what the page shows after the reboot.
type ImportOutcome struct {
	HmIP     ImportHmIPOutcome `json:"hmip"`
	BidCosRF struct {
		// Interface is rfd's local radio entry of listBidcosInterfaces: rfd names it by the serial
		// it runs with, so Took says whether that is the imported one. nil when rfd did not answer
		// or has no local radio.
		Took      *bool  `json:"took,omitempty"`
		Interface string `json:"interface,omitempty"`
		Connected bool   `json:"connected"`
		Error     string `json:"error,omitempty"`
	} `json:"bidcos_rf"`
}

// ImportRecord keeps the record of the last device import in one file.
type ImportRecord struct {
	Path string
	Root Root
	// Plan answers the radio plan in use (this system's modules); nil = radio.LoadPlan(Root).
	Plan func() (radio.Plan, bool)
	// Journal answers hmipserver's lines about the exchange since a time, one per line; nil =
	// journalctl -u hmipserver.service -g exchange (case-insensitive: "Adapter exchange …" and
	// B-289's "Could not exchange network key, adapter version not supported"). A test's seam.
	Journal func(ctx context.Context, since time.Time) []string
	// Interfaces answers the XML-RPC clients of the interface processes; nil = no BidCos check.
	Interfaces func() []interfaces.Interface
	// InterfacesTimeout bounds the listBidcosInterfaces round; 0 = PairedDevicesTimeout.
	InterfacesTimeout time.Duration
}

// Read answers the record, or nil without one.
func (r *ImportRecord) Read() *ImportedRadio {
	if r == nil || r.Path == "" {
		return nil
	}
	b, err := os.ReadFile(r.Path)
	if err != nil {
		return nil
	}
	var rec ImportedRadio
	if json.Unmarshal(b, &rec) != nil {
		return nil
	}
	return &rec
}

// Write keeps the record (the state directory is occulited's own).
func (r *ImportRecord) Write(rec ImportedRadio) error {
	if r == nil || r.Path == "" {
		return errors.New("no path for the import record")
	}
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o750); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rec, "", "  ")
	return os.WriteFile(r.Path, b, 0o640)
}

// Clear removes the record; a missing one is fine.
func (r *ImportRecord) Clear() error {
	if r == nil || r.Path == "" {
		return nil
	}
	if err := os.Remove(r.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// NewImportedRadio makes the record of an import from what the backup holds and this system's
// plan (the modules in use at the import).
func NewImportedRadio(now time.Time, file string, b RadioBackup, p radio.Plan, hasPlan bool, targetUserKey bool) ImportedRadio {
	rec := ImportedRadio{At: now, File: file, Version: b.Version}
	rec.HmIP.FromSGTIN = strings.ToUpper(b.HmIP.IdentitySGTIN)
	rec.HmIP.LocalKey = b.HmIP.LocalKey
	rec.HmIP.Devices = b.HmIP.Devices
	if hasPlan && p.HmIP != nil {
		rec.HmIP.ToSGTIN = strings.ToUpper(p.HmIP.SGTIN)
		rec.HmIP.ToModule = strings.TrimSpace(p.HmIP.Hardware + " " + p.HmIP.Serial)
	}
	rec.HmIP.ModuleChanged = rec.HmIP.FromSGTIN != "" && rec.HmIP.ToSGTIN != "" && rec.HmIP.FromSGTIN != rec.HmIP.ToSGTIN
	rec.BidCosRF.Address, rec.BidCosRF.Serial, rec.BidCosRF.Devices = b.BidCosRF.Address, b.BidCosRF.Serial, b.BidCosRF.Devices
	rec.BidCosRF.NonDefaultKey, rec.BidCosRF.KeyIndex = b.NonDefaultKey(), b.KeyIndex
	rec.BidCosRF.TargetKeyReplaced = targetUserKey
	if hasPlan && p.HmRF != nil {
		rec.BidCosRF.Module = strings.TrimSpace(p.HmRF.Hardware + " " + p.HmRF.Serial)
	}
	return rec
}

// Outcome reads how the import went so far: the HmIP identity's move from hmipserver's files, the
// marker and its journal lines, and rfd's local radio entry for the BidCos side.
//
// openccu-lite B-281: once the move is final - done, or rejected - its half is kept in the record
// (HmIPFinal) and not read again: the pages and the Status warnings poll the outcome every few
// minutes, and each poll was a journal read through the privilege helper. The retry (a restart of
// hmipserver, which attempts the exchange again) clears the kept half. The BidCos half is rfd's
// own answer and is read every time.
func (r *ImportRecord) Outcome(ctx context.Context, rec ImportedRadio) ImportOutcome {
	var out ImportOutcome
	root := ""
	if r != nil {
		root = string(r.Root)
	}
	p, ok := r.plan()
	now := ""
	if ok && p.HmIP != nil {
		now = strings.ToUpper(p.HmIP.SGTIN)
	}
	if rec.HmIPFinal != nil {
		out.HmIP = *rec.HmIPFinal
		r.bidcosOutcome(ctx, rec, p, ok, &out)
		return out
	}
	out.HmIP.ModuleNow = now
	// hmipserver's data directory is its own (0700, B-253): the names come through readDir, which
	// asks the privilege helper when the directory is closed to occulited - a plain stat as
	// occulited's user saw neither file and answered "unknown" on every system (seen on a lab
	// system after a real import onto another module, 2026-09-27)
	var aps map[string]bool
	apExists := func(sg string) bool {
		if sg == "" {
			return false
		}
		if aps == nil {
			aps = map[string]bool{}
			for _, n := range readDir(filepath.Join(root, crRFDDataDir)) {
				aps[strings.ToUpper(n)] = true
			}
		}
		return aps[strings.ToUpper(sg)+".AP"]
	}
	switch {
	case rec.HmIP.FromSGTIN == "":
		out.HmIP.State = ImportNoExchange
	case now == "":
		out.HmIP.State = ImportNoModule
	case rec.HmIP.FromSGTIN == now:
		out.HmIP.State = ImportNoExchange
	default:
		if f := radio.ReadHmIPFatal(root); f != nil && f.Code == "adapter-exchange-rejected" && !f.At.Before(rec.At) {
			out.HmIP.State, out.HmIP.Cause = ImportRejected, f.Cause
		} else if apExists(now) && !apExists(rec.HmIP.FromSGTIN) {
			out.HmIP.State = ImportDone
		} else if apExists(rec.HmIP.FromSGTIN) {
			out.HmIP.State = ImportPending
		} else {
			out.HmIP.State = ImportUnknown
		}
		swap := false
		for _, l := range r.journal(ctx, rec.At) {
			if strings.Contains(l, "Adapter exchange") && !swap {
				out.HmIP.Line = strings.TrimSpace(l)
			}
			if strings.Contains(l, "adapter version not supported") {
				swap, out.HmIP.Line = true, strings.TrimSpace(l)
			}
		}
		// openccu-lite B-289: the files moved, but onto a module that could not take the network
		// key (hmipserver's line, or the local record of exchanges) - a failed move, not a success
		if out.HmIP.State == ImportDone && !rec.HmIP.LocalKey && (swap || localSwapSince(root, now, rec.At) != nil) {
			out.HmIP.State, out.HmIP.Cause = ImportRejected, radio.ExchangeCauseAdapterVersion
		}
		if (out.HmIP.State == ImportDone || out.HmIP.State == ImportRejected) && r != nil && r.Path != "" {
			final := out.HmIP
			rec.HmIPFinal = &final
			if err := r.Write(rec); err != nil {
				slog.Warn("device import: the final outcome was not kept", "err", err)
			}
		}
	}
	r.bidcosOutcome(ctx, rec, p, ok, &out)
	return out
}

// bidcosOutcome fills the BidCos-RF half: rfd names its local radio entry by the serial it runs
// with (the plan's transmitter mapping: a CCU2 entry, or the HM-CFG-USB-2's serial), so the
// imported serial there is the imported identity in use.
func (r *ImportRecord) bidcosOutcome(ctx context.Context, rec ImportedRadio, p radio.Plan, ok bool, out *ImportOutcome) {
	if r != nil && r.Interfaces != nil && rec.BidCosRF.Serial != "" {
		timeout := r.InterfacesTimeout
		if timeout <= 0 {
			timeout = PairedDevicesTimeout
		}
		var rfd []interfaces.Interface
		for _, i := range r.Interfaces() {
			if i.Name == "BidCos-RF" {
				rfd = append(rfd, i)
			}
		}
		if len(rfd) > 0 {
			list, errs := interfaces.ListInterfaces(ctx, rfd, timeout)
			if err := errs["BidCos-RF"]; err != nil {
				out.BidCosRF.Error = err.Error()
			} else {
				out.BidCosRF.Took = bidcosTook(list, rec, p, ok, out)
			}
		}
	}
}

// ClearFinal forgets the kept HmIP half of the outcome, so that the next read looks again (the
// retry restarts hmipserver, which attempts the exchange once more).
func (r *ImportRecord) ClearFinal() error {
	rec := r.Read()
	if rec == nil || rec.HmIPFinal == nil {
		return nil
	}
	rec.HmIPFinal = nil
	return r.Write(*rec)
}

// bidcosTook says whether rfd runs with the imported BidCos identity, from its
// listBidcosInterfaces entries. A module rfd reaches through multimacd is its "CCU2" entry, named
// by the serial rfd runs with - the imported one when the address took. A USB adapter (the
// HM-CFG-USB-2) or a LAN gateway is named by its own serial whatever address rfd sends with, so
// there the entry of the plan's BidCos module counts, and the address rfd runs with is the one the
// boot's radio run read from the ids file (the plan's active address), which rfd would have
// rewritten had it not taken it. nil when there is no local entry at all.
func bidcosTook(list []interfaces.RadioInterface, rec ImportedRadio, p radio.Plan, hasPlan bool, out *ImportOutcome) *bool {
	module := ""
	if hasPlan && p.HmRF != nil {
		module = p.HmRF.Serial
	}
	var found *interfaces.RadioInterface
	for i := range list {
		e := &list[i]
		if strings.EqualFold(e.Address, rec.BidCosRF.Serial) {
			found = e
			break
		}
		if found == nil && (strings.EqualFold(e.Type, "CCU2") || (module != "" && strings.EqualFold(e.Address, module))) {
			found = e
		}
	}
	took := false
	if found == nil {
		return &took
	}
	out.BidCosRF.Interface, out.BidCosRF.Connected = strings.TrimSpace(found.Type+" "+found.Address), found.Connected
	switch {
	case strings.EqualFold(found.Address, rec.BidCosRF.Serial):
		took = true
	case !strings.EqualFold(found.Type, "CCU2") && rec.BidCosRF.Address != "" && hasPlan:
		took = strings.EqualFold(p.HmRFAddressActive, rec.BidCosRF.Address)
	}
	return &took
}

func (r *ImportRecord) plan() (radio.Plan, bool) {
	if r == nil {
		return radio.Plan{}, false
	}
	if r.Plan != nil {
		return r.Plan()
	}
	p, err := radio.LoadPlan(string(r.Root))
	return p, err == nil
}

// localSwapSince is the record's entry of a local swap onto module since a time (B-289), nil
// without one: the newest entry onto that module decides.
func localSwapSince(root, module string, since time.Time) *radio.HmIPExchange {
	for _, x := range radio.ReadHmIPExchanges(root) {
		if !strings.EqualFold(x.To, module) {
			continue
		}
		if x.At.Before(since) || x.Outcome != radio.ExchangeRejected || x.Cause != radio.ExchangeCauseAdapterVersion {
			return nil
		}
		return &x
	}
	return nil
}

func (r *ImportRecord) journal(ctx context.Context, since time.Time) []string {
	if r == nil {
		return nil
	}
	if r.Journal != nil {
		return r.Journal(ctx, since)
	}
	if string(r.Root) != "/" {
		return nil
	}
	out, err := run(ctx, "journalctl", "-u", "hmipserver.service", "-o", "cat", "-q", "--no-pager", "-g", "exchange", "--since", since.Local().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// String is the record's one-line summary for the journal.
func (rec ImportedRadio) String() string {
	return fmt.Sprintf("import of %s at %s: hmip %s -> %s (changed %v), bidcos %s/%s (non-default key %v, passphrase %s)", rec.File, rec.At.Format(time.RFC3339), rec.HmIP.FromSGTIN, rec.HmIP.ToSGTIN, rec.HmIP.ModuleChanged, rec.BidCosRF.Address, rec.BidCosRF.Serial, rec.BidCosRF.NonDefaultKey, rec.BidCosRF.KeyCheck)
}
