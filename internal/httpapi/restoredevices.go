package httpapi

// openccu-lite task 251: the paired devices of a checked CCU backup (the upload /restore/check
// stored) onto this system - what the backup holds and what this system has (GET), and the import
// itself with the reboot (POST), refused while anything is paired here.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/bootexpect"
	"github.com/hobbyquaker/occulited/internal/radio"
	"github.com/hobbyquaker/occulited/internal/system"
)

func (a *SystemAPI) registerRestoreDevices(mux *http.ServeMux, p string) {
	route(mux, auth.ScopePower, "GET "+p+"/restore/devices", a.restoreDevicesView)
	route(mux, auth.ScopePower, "POST "+p+"/restore/import-devices", a.restoreImportDevices)
	// openccu-lite task 275: what the last import left, and how hmipserver's move of the imported
	// identity onto this module went; the retry restarts HmIP-RF; the record is dismissed when done
	route(mux, auth.ScopeSystemRead, "GET "+p+"/radio/import", a.radioImportView)
	route(mux, auth.ScopePower, "POST "+p+"/radio/import/retry", a.radioImportRetry)
	route(mux, auth.ScopePower, "DELETE "+p+"/radio/import", a.radioImportDismiss)
}

// restoreUploadPath checks the file name the way /restore/apply does and answers its path.
func (a *SystemAPI) restoreUploadPath(w http.ResponseWriter, file string) (string, bool) {
	if filepath.Base(file) != file || !strings.HasPrefix(file, "restore-") || !strings.HasSuffix(file, ".sbk") {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "file must be the name restore/check returned"})
		return "", false
	}
	path := filepath.Join(string(a.Root), system.BackupDir, file)
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "upload not found - check it again"})
		return "", false
	}
	return path, true
}

// restoreModule names a radio module of this system for the import panel.
type restoreModule struct {
	Hardware string `json:"hardware"`
	Serial   string `json:"serial"`
	SGTIN    string `json:"sgtin,omitempty"`
	// Version is the module's application firmware (openccu-lite B-289), when the detection read it.
	Version string `json:"version,omitempty"`
}

// restoreTarget is this system's side: the devices paired per interface (unknown when the
// interface does not answer), whether an individual security key is set, the modules the
// interface processes run on, and the verdict.
type restoreTarget struct {
	Paired map[string]system.PairedDevices `json:"paired"`
	// Devices is the sum over the interfaces that answered
	Devices int `json:"devices"`
	// Unknown names the interfaces that did not answer; the import waits for them
	Unknown []string `json:"unknown,omitempty"`
	UserKey bool     `json:"user_key"`
	// Importable: no device paired here and every interface answered
	Importable bool `json:"importable"`
	// HmIPModule and BidCosModule are the plan's modules (task 275): what the imported identity
	// lands on; nil without one
	HmIPModule   *restoreModule `json:"hmip_module,omitempty"`
	BidCosModule *restoreModule `json:"bidcos_module,omitempty"`
}

// moduleOf names a plan role for the panel.
func moduleOf(r *radio.Role) *restoreModule {
	if r == nil {
		return nil
	}
	return &restoreModule{Hardware: r.Hardware, Serial: r.Serial, SGTIN: strings.ToUpper(r.SGTIN), Version: r.Version}
}

// plan is the radio plan in use: the import record's seam in the tests, else the file.
func (a *SystemAPI) importPlan() (radio.Plan, bool) {
	if a.ImportRecord != nil && a.ImportRecord.Plan != nil {
		return a.ImportRecord.Plan()
	}
	p, err := radio.LoadPlan(string(a.Root))
	return p, err == nil
}

func (a *SystemAPI) restoreTargetState(ctx context.Context) restoreTarget {
	t := restoreTarget{Paired: map[string]system.PairedDevices{}, Unknown: []string{}}
	if p, ok := a.importPlan(); ok {
		t.HmIPModule, t.BidCosModule = moduleOf(p.HmIP), moduleOf(p.HmRF)
	}
	if a.RadioInterfaces != nil {
		cctx, cancel := context.WithTimeout(ctx, system.PairedDevicesTimeout+time.Second)
		defer cancel()
		t.Paired = system.CountPaired(cctx, a.RadioInterfaces())
	}
	for name, p := range t.Paired {
		if !p.Known {
			t.Unknown = append(t.Unknown, name)
			continue
		}
		t.Devices += p.Devices
	}
	// the daemons' files on the userfs count too (task 275's lab run): an interface that is not
	// listed because its daemon is down - the module unplugged, the HB-RF-ETH off - still has its
	// pairings on the disk, and the import must not run over them
	for name, n := range a.Root.PairedFromFiles() {
		p, listed := t.Paired[name]
		if listed && p.Known {
			if n > p.Devices {
				t.Devices += n - p.Devices
				p.Devices = n
				t.Paired[name] = p
			}
			continue
		}
		if listed {
			// asked and silent: the files say there is something, which is what matters here
			t.Unknown = slices.DeleteFunc(t.Unknown, func(u string) bool { return u == name })
		}
		t.Paired[name] = system.PairedDevices{Devices: n, Known: true, Error: "counted from the device files; the interface is not running"}
		t.Devices += n
	}
	if set, known, err := a.Root.SecurityKeyState(ctx); known && err == nil {
		t.UserKey = set
	} else if !known {
		// without crypttool (a development root) rfd's persisted key store is the evidence
		if st, err := os.Stat(filepath.Join(string(a.Root), "etc/config/keys")); err == nil && st.Size() > 0 {
			t.UserKey = true
		}
	}
	t.Importable = t.Devices == 0 && len(t.Unknown) == 0
	return t
}

// restoreDevicesView: ?file=<the checked upload> → {backup, target}.
func (a *SystemAPI) restoreDevicesView(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	path, ok := a.restoreUploadPath(w, r.URL.Query().Get("file"))
	if !ok {
		return
	}
	b, err := system.InspectRadioBackup(path)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "not_a_backup", Message: err.Error()})
		return
	}
	target := a.restoreTargetState(r.Context())
	view := map[string]any{"backup": b, "target": target, "module_changed": moduleChanged(b, target), "non_default_key": b.NonDefaultKey()}
	if refused := hmipImportRefusal(b, target); refused != nil {
		view["hmip_firmware"] = refused
	}
	writeJSON(w, 200, view)
}

// hmipImportRefusal (openccu-lite B-289): the backup's HmIP identity would move onto this system's
// module, local key mode is off in the backup, and the module's application firmware cannot take
// the network key - the import and the restore are refused (the module's firmware update first).
func hmipImportRefusal(b system.RadioBackup, t restoreTarget) *system.HmIPFirmwareRefused {
	if !moduleChanged(b, t) {
		return nil
	}
	return system.HmIPFirmwareRefusal(t.HmIPModule.SGTIN, t.HmIPModule.Version, b.HmIP.LocalKey)
}

// restoreHmIPRefusal is hmipImportRefusal for the restore of a whole backup: the backup's radio
// side read from the file, this system's HmIP module from the plan. A file that is no radio
// backup, or a system without a plan, is not refused here.
func (a *SystemAPI) restoreHmIPRefusal(path string) *system.HmIPFirmwareRefused {
	b, err := system.InspectRadioBackup(path)
	if err != nil {
		return nil
	}
	var t restoreTarget
	if p, ok := a.importPlan(); ok {
		t.HmIPModule = moduleOf(p.HmIP)
	}
	return hmipImportRefusal(b, t)
}

// moduleChanged (task 275): the backup's HmIP identity is bound to another module than the one
// this system runs HmIP-RF on, so hmipserver has to take it over at the start after the import.
func moduleChanged(b system.RadioBackup, t restoreTarget) bool {
	return b.HmIP.IdentitySGTIN != "" && t.HmIPModule != nil && t.HmIPModule.SGTIN != "" && !strings.EqualFold(b.HmIP.IdentitySGTIN, t.HmIPModule.SGTIN)
}

// restoreImportDevices takes {file, replace_key?, key?, confirm}: confirm is required (400 confirm,
// D-120: the backup's HmIP identity files are written); the target must have nothing paired (409
// paired / unknown); a system with a security key store of its own needs replace_key (422
// key_replace) - the backup's store replaces it, and there is nothing paired here that it could
// orphan; then the radio files are put in place, the import is recorded for the boots after it
// (task 275), and the system reboots - 200 {ok, imported, rebooting} as /restore/apply answers.
// The key store comes as it is (task 278, option B); key is the backup's passphrase when the user
// gave it (task 296): checked against the backup's signature, the verdict in the answer
// (key_check) and the record, never a reason to refuse, never kept.
func (a *SystemAPI) restoreImportDevices(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	var body struct {
		File       string `json:"file"`
		ReplaceKey bool   `json:"replace_key"`
		Key        string `json:"key"`
		Confirm    bool   `json:"confirm"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	// openccu-lite task 317 (D-120): the import writes the backup's HmIP identity files - on the
	// user's word after the warning
	if !a.identityConfirmed(w, body.Confirm, "", false) {
		return
	}
	path, ok := a.restoreUploadPath(w, body.File)
	if !ok {
		return
	}
	b, err := system.InspectRadioBackup(path)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "not_a_backup", Message: err.Error()})
		return
	}
	if b.Empty() {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "nothing_to_import", Message: "the backup holds no paired device and no radio identity"})
		return
	}
	target := a.restoreTargetState(r.Context())
	if refused := hmipImportRefusal(b, target); refused != nil {
		hmipFirmwareError(w, refused)
		return
	}
	if len(target.Unknown) > 0 {
		writeJSON(w, http.StatusConflict, apiError{Error: "unknown", Message: "an interface did not answer: " + strings.Join(target.Unknown, ", ") + " - try again in a moment", Detail: map[string]any{"target": target}})
		return
	}
	if target.Devices > 0 {
		writeJSON(w, http.StatusConflict, apiError{Error: "paired", Message: "this system has devices paired already; the import takes over another system's radio identity and would orphan them", Detail: map[string]any{"target": target}})
		return
	}
	// this system's own security key store: replaced by the backup's, on the user's word only
	if target.UserKey && !body.ReplaceKey {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "key_replace", Message: "this system has a security key of its own; confirm that the backup's key store replaces it (replace_key)", Detail: map[string]any{"target": target}})
		return
	}
	// task 296: the passphrase's verdict (the backup's side: this system's key store is replaced)
	keyCheck := system.KeyCheckNone
	if sig, err := a.backupSignature(path); err == nil {
		keyCheck = sig.KeyCheck(body.Key)
	} else if b.NonDefaultKey() {
		keyCheck = system.KeyCheckSkipped // no signature to check against: as good as not known
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	// the names first (task 281): the checked file is gone after the reboot (/usr/local/tmp is
	// emptied at boot), so the ReGa names, rooms and functions of the same backup are imported
	// now; a failure is reported and does not stop the devices
	var names *NamesImportResult
	if a.NamesImport != nil {
		n := a.NamesImport(path)
		names = &n
		if n.OK {
			withCaller(r, a.trustLog()).Info("restore: names imported from the backup before the device import", "file", body.File, "objects", n.Objects, "rooms", n.Rooms, "functions", n.Functions)
		} else {
			withCaller(r, a.trustLog()).Warn("restore: the names import from the backup failed; the device import goes on", "file", body.File, "err", n.Error)
		}
	}
	res, err := a.Root.ImportRadio(ctx, path)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "import-failed", Message: err.Error(), Detail: map[string]any{"result": res, "names": names}})
		return
	}
	// the record for the boots after the reboot (task 275): the module the identity came from,
	// the modules here, the key store's provenance
	p, hasPlan := a.importPlan()
	rec := system.NewImportedRadio(time.Now().UTC(), body.File, res.Backup, p, hasPlan, target.UserKey || res.TargetKeyReplaced)
	rec.BidCosRF.KeyCheck = keyCheck
	if a.ImportRecord != nil {
		if err := a.ImportRecord.Write(rec); err != nil {
			a.trustLog().Warn("restore: the import record was not written", "err", err)
		}
	}
	withCaller(r, a.trustLog()).Info("restore: paired devices imported, rebooting", "file", body.File, "bidcos_rf", res.Backup.BidCosRF.Devices, "hmip", res.Backup.HmIP.Devices, "wired", res.Backup.BidCosWired.Devices, "non_default_key", res.NonDefaultKey, "key_check", keyCheck, "target_key_replaced", res.TargetKeyReplaced, "hmip_module_changed", rec.HmIP.ModuleChanged, "hmip_from", rec.HmIP.FromSGTIN, "hmip_to", rec.HmIP.ToSGTIN, "aside", res.Aside)
	if a.Manager == nil {
		writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "record": rec, "names": names, "key_check": keyCheck, "rebooting": false, "message": "imported; reboot to apply it"})
		return
	}
	a.markBoot(bootexpect.KindRestore)
	if err := a.Manager.Reboot(context.Background()); err != nil {
		a.unmarkBoot()
		writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "record": rec, "names": names, "key_check": keyCheck, "rebooting": false, "message": "imported, but the reboot did not start: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "record": rec, "names": names, "key_check": keyCheck, "rebooting": true})
}

// radioImportView: {imported: false} without a record, else {imported: true, record, outcome} -
// the record of the last import and how the HmIP identity's move and the BidCos identity stand now
// (system.ImportRecord.Outcome), plus whether a restart runs (switching).
func (a *SystemAPI) radioImportView(w http.ResponseWriter, r *http.Request) {
	rec := a.ImportRecord.Read()
	if rec == nil {
		writeJSON(w, 200, map[string]any{"imported": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), system.PairedDevicesTimeout+time.Second)
	defer cancel()
	out := map[string]any{"imported": true, "record": rec, "outcome": a.ImportRecord.Outcome(ctx, *rec)}
	if a.HmIPLocalKey != nil {
		v := a.HmIPLocalKey.Exchange()
		out["switching"], out["error"] = v.Switching, v.Error
	}
	writeJSON(w, 200, out)
}

// radioImportRetry restarts HmIP-RF so that hmipserver attempts the exchange again (a marker of a
// rejected one goes with it). The import's own scope (power), as the dismissal. 202 with the
// view; 409 local-key while a switch, a connection change or a flash runs; 404 without a record;
// 501 without the local key service.
func (a *SystemAPI) radioImportRetry(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	if a.ImportRecord.Read() == nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "no device import is recorded"})
		return
	}
	if !a.localKeyReady(w) {
		return
	}
	// openccu-lite B-281: the kept final outcome goes; the next read looks at the files again
	if err := a.ImportRecord.ClearFinal(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "record", Message: err.Error()})
		return
	}
	if err := a.HmIPLocalKey.RestartExchange(); err != nil {
		localKeyError(w, err)
		return
	}
	withCaller(r, a.trustLog()).Info("device import: HmIP-RF restarted to try the adapter exchange again")
	w.WriteHeader(http.StatusAccepted)
	a.radioImportView(w, r)
}

// radioImportDismiss removes the record; 204.
func (a *SystemAPI) radioImportDismiss(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	if err := a.ImportRecord.Clear(); err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "io", Message: err.Error()})
		return
	}
	withCaller(r, a.trustLog()).Info("device import: the record was dismissed")
	w.WriteHeader(http.StatusNoContent)
}
