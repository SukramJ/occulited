package httpapi

// openccu-lite task 251: the paired devices of a checked CCU backup (the upload /restore/check
// stored) onto this system - what the backup holds and what this system has (GET), and the import
// itself with the reboot (POST), refused while anything is paired here.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/bootexpect"
	"github.com/hobbyquaker/occulited/internal/system"
)

func (a *SystemAPI) registerRestoreDevices(mux *http.ServeMux, p string) {
	route(mux, auth.ScopePower, "GET "+p+"/restore/devices", a.restoreDevicesView)
	route(mux, auth.ScopePower, "POST "+p+"/restore/import-devices", a.restoreImportDevices)
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

// restoreTarget is this system's side: the devices paired per interface (unknown when the
// interface does not answer), whether an individual security key is set, and the verdict.
type restoreTarget struct {
	Paired map[string]system.PairedDevices `json:"paired"`
	// Devices is the sum over the interfaces that answered
	Devices int `json:"devices"`
	// Unknown names the interfaces that did not answer; the import waits for them
	Unknown []string `json:"unknown,omitempty"`
	UserKey bool     `json:"user_key"`
	// Importable: no device paired here and every interface answered
	Importable bool `json:"importable"`
}

func (a *SystemAPI) restoreTargetState(ctx context.Context) restoreTarget {
	t := restoreTarget{Paired: map[string]system.PairedDevices{}, Unknown: []string{}}
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
	if set, known, err := a.Root.SecurityKeyState(ctx); known && err == nil {
		t.UserKey = set
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
	writeJSON(w, 200, map[string]any{"backup": b, "target": a.restoreTargetState(r.Context())})
}

// restoreImportDevices takes {file, key?}: the target must have nothing paired (409 paired /
// unknown), a security key involved must match (422 key), then the radio files are put in place
// and the system reboots - 200 {ok, imported, rebooting} as /restore/apply answers.
func (a *SystemAPI) restoreImportDevices(w http.ResponseWriter, r *http.Request) {
	if s := SessionFrom(r); s == nil || !s.Has(auth.ScopePower) {
		forbiddenScope(w, auth.ScopePower)
		return
	}
	var body struct {
		File string `json:"file"`
		Key  string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
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
	if len(target.Unknown) > 0 {
		writeJSON(w, http.StatusConflict, apiError{Error: "unknown", Message: "an interface did not answer: " + strings.Join(target.Unknown, ", ") + " - try again in a moment", Detail: map[string]any{"target": target}})
		return
	}
	if target.Devices > 0 {
		writeJSON(w, http.StatusConflict, apiError{Error: "paired", Message: "this system has devices paired already; the import takes over another system's radio identity and would orphan them", Detail: map[string]any{"target": target}})
		return
	}
	// the security key: the script's own check with the key offered, when the backup or this
	// system is protected - the same rule as the restore, without a force
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Minute)
	defer cancel()
	var kc system.KeyCheck
	if b.BidCosRF.HasKey || b.KeyIndex > 0 || target.UserKey {
		kc, err = a.Root.CheckBackupKey(ctx, a.RunStdin, path, body.Key)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "check-failed", Message: err.Error()})
			return
		}
		if (kc.BackupHasKey || kc.SystemHasKey) && body.Key == "" {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "key", Message: "the backup or this system is protected by a security key: enter it"})
			return
		}
		if (kc.BackupHasKey && !kc.BackupMatches) || (kc.SystemHasKey && !kc.SystemMatches) {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "key", Message: "the security key does not match: " + kc.Output})
			return
		}
	}
	res, err := a.Root.ImportRadio(ctx, path, body.Key, kc)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "import-failed", Message: err.Error(), Detail: map[string]any{"result": res}})
		return
	}
	withCaller(r, a.trustLog()).Info("restore: paired devices imported, rebooting", "file", body.File, "bidcos_rf", res.Backup.BidCosRF.Devices, "hmip", res.Backup.HmIP.Devices, "wired", res.Backup.BidCosWired.Devices, "key_set", res.KeySet, "aside", res.Aside)
	if a.Manager == nil {
		writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "rebooting": false, "message": "imported; reboot to apply it"})
		return
	}
	a.markBoot(bootexpect.KindRestore)
	if err := a.Manager.Reboot(context.Background()); err != nil {
		a.unmarkBoot()
		writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "rebooting": false, "message": "imported, but the reboot did not start: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "imported": res, "rebooting": true})
}
