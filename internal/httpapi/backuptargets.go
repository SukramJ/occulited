package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/backuptarget"
	"github.com/hobbyquaker/occulited/internal/system"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

// Backup targets (openccu-lite task 86; D-64, D-65, D-80): the routes under /backup/targets, Back
// up now, the nightly switch, the restore from a target and the Status warnings. The work is
// internal/backuptarget's; here are the parsing and the answers.

func (a *SystemAPI) registerBackupTargets(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/backup/targets", a.targetsList)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/backup/targets", a.targetsCreate)
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/backup/targets/{id}", a.targetsPut)
	route(mux, auth.ScopeSystemWrite, "DELETE "+p+"/backup/targets/{id}", a.targetsDelete)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/backup/targets/{id}/test", a.targetsTest)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/backup/targets/{id}/mount", a.targetsMount)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/backup/targets/{id}/unmount", a.targetsUnmount)
	route(mux, auth.ScopeBackup, "POST "+p+"/backup/targets/{id}/run", a.targetsRun)
	route(mux, auth.ScopeSystemRead, "GET "+p+"/backup/targets/{id}/backups", a.targetsBackups)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/backup/targets/{id}/keypair", a.targetsKeypair)
	route(mux, auth.ScopeSystemWrite, "GET "+p+"/backup/targets/{id}/hostkey", a.targetsHostKey)
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/backup/targets/{id}/hostkey", a.targetsHostKeyPut)
	route(mux, auth.ScopeBackup, "POST "+p+"/backup/run", a.backupRunAll)
	route(mux, auth.ScopeSystemWrite, "PUT "+p+"/backup/nightly", a.backupNightlyPut)
}

func (a *SystemAPI) targetsReady(w http.ResponseWriter) bool {
	if a.BackupTargets == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "backup targets are not available here"})
		return false
	}
	return true
}

// writeTargetErr maps the package's errors onto the API's answers.
func writeTargetErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backuptarget.ErrNotFound):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: err.Error()})
	case errors.Is(err, backuptarget.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: err.Error()})
	case errors.Is(err, backuptarget.ErrNotMount):
		writeJSON(w, http.StatusConflict, apiError{Error: "not-a-mount", Message: err.Error()})
	case errors.Is(err, backuptarget.ErrBusy):
		writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: err.Error()})
	case errors.Is(err, backuptarget.ErrFingerprint):
		writeJSON(w, http.StatusConflict, apiError{Error: "fingerprint", Message: err.Error()})
	case errors.Is(err, backuptarget.ErrStale):
		writeJSON(w, http.StatusServiceUnavailable, apiError{Error: "stale", Message: err.Error(), Detail: map[string]any{"state": backuptarget.StateStale}})
	default:
		state := backuptarget.Classify(err)
		if state != backuptarget.StateError {
			writeJSON(w, http.StatusBadGateway, apiError{Error: "target", Message: err.Error(), Detail: map[string]any{"state": state}})
			return
		}
		writeErr(w, err)
	}
}

// NightlyTime is when the timer runs (the fork's occu-cron-backup.timer, plus up to 30 minutes).
const NightlyTime = "00:07"

func (a *SystemAPI) targetsList(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	m := a.BackupTargets
	views, err := m.Views(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	host := ""
	if m.Hostname != nil {
		host = m.Hostname()
	}
	enc := a.BackupCrypt != nil && a.BackupCrypt.Enabled()
	writeJSON(w, 200, map[string]any{
		"nightly":      map[string]any{"enabled": m.Store.Nightly(), "time": NightlyTime},
		"container":    a.Root.Container(),
		"kinds":        m.Kinds(),
		"encryption":   enc,
		"hostname":     host,
		"needed_bytes": m.NeededBytes(),
		"targets":      views,
	})
}

// errLegacyKind answers a new or changed task-86 NFS/SMB target.
var errLegacyKind = errors.New("NFS and SMB shares are added on System → Storage now; a backup target of kind share picks one and a folder")

// targetBody reads a target; the id comes from the path, never from the body.
func targetBody(r *http.Request) (backuptarget.Target, error) {
	var t backuptarget.Target
	if err := readJSON(r, &t); err != nil {
		return t, err
	}
	t.ID = ""
	t.Created = time.Time{}
	if t.Kind == backuptarget.KindNFS || t.Kind == backuptarget.KindCIFS {
		// task 228: shares are defined once, on System → Storage; a target picks one
		return t, errLegacyKind
	}
	if t.SFTP != nil {
		t.SFTP.PublicKey, t.SFTP.HostKey = "", nil
	}
	if t.CIFS != nil {
		t.CIFS.HasPassword = false
	}
	return t, nil
}

func (a *SystemAPI) targetsCreate(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	t, err := targetBody(r)
	if errors.Is(err, errLegacyKind) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	if err != nil {
		badBody(w, err)
		return
	}
	if t.Kind == backuptarget.KindDirectory {
		if _, ok, _ := a.BackupTargets.Store.Get(backuptarget.DirectoryID); ok {
			writeJSON(w, http.StatusConflict, apiError{Error: "exists", Message: "there is one directory target; change it instead"})
			return
		}
	}
	a.saveTarget(w, r, t, true)
}

func (a *SystemAPI) targetsPut(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	id := r.PathValue("id")
	old, ok, err := a.BackupTargets.Store.Get(id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if !ok {
		writeTargetErr(w, backuptarget.ErrNotFound)
		return
	}
	t, err := targetBody(r)
	if errors.Is(err, errLegacyKind) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	if err != nil {
		badBody(w, err)
		return
	}
	if t.Kind != old.Kind {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid", Message: "the kind of a target cannot change"})
		return
	}
	t.ID = id
	a.saveTarget(w, r, t, false)
}

func (a *SystemAPI) saveTarget(w http.ResponseWriter, r *http.Request, t backuptarget.Target, created bool) {
	m := a.BackupTargets
	saved, err := m.Store.Put(t)
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	if created && saved.Kind == backuptarget.KindSFTP {
		if _, err := m.Keypair(saved.ID); err != nil {
			slog.Warn("backup targets: no key made for the new target", "target", saved.ID, "err", err)
		}
	}
	applyErr := ""
	if saved.IsMount() {
		if err := m.Apply(r.Context(), saved); err != nil {
			applyErr = err.Error()
			slog.Warn("backup targets: the mount units were not written", "target", saved.ID, "err", err)
		}
	}
	slog.Info("backup targets: saved", "by", who(r), "target", saved.ID, "kind", saved.Kind, "created", created)
	v, err := m.View(r.Context(), saved.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"target": v}
	if applyErr != "" {
		out["apply_error"] = applyErr
	}
	status := 200
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, out)
}

func (a *SystemAPI) targetsDelete(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	id := r.PathValue("id")
	if err := a.BackupTargets.Remove(r.Context(), id); err != nil {
		writeTargetErr(w, err)
		return
	}
	slog.Info("backup targets: removed (the files on the target stay)", "by", who(r), "target", id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *SystemAPI) targetsTest(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	res, err := a.BackupTargets.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	writeJSON(w, 200, res)
}

func (a *SystemAPI) targetsMount(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	v, err := a.BackupTargets.Mount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *SystemAPI) targetsUnmount(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	v, err := a.BackupTargets.Unmount(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *SystemAPI) startRun(w http.ResponseWriter, r *http.Request, instance string) {
	if err := a.BackupTargets.Start(r.Context(), instance); err != nil {
		writeTargetErr(w, err)
		return
	}
	slog.Info("backup: back up now", "by", who(r), "instance", instance)
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "instance": instance})
}

func (a *SystemAPI) targetsRun(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	a.startRun(w, r, r.PathValue("id"))
}

func (a *SystemAPI) backupRunAll(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	var b struct {
		Target string `json:"target"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &b); err != nil {
			badBody(w, err)
			return
		}
	}
	inst := "all"
	if b.Target != "" {
		inst = b.Target
	}
	a.startRun(w, r, inst)
}

func (a *SystemAPI) targetsBackups(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	files, err := a.BackupTargets.Backups(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"backups": files})
}

func (a *SystemAPI) targetsKeypair(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	line, err := a.BackupTargets.Keypair(r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	slog.Info("backup targets: a new SSH key", "by", who(r), "target", r.PathValue("id"))
	writeJSON(w, 200, map[string]any{"public_key": line})
}

func (a *SystemAPI) targetsHostKey(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	v, err := a.BackupTargets.ScanHostKey(r.Context(), r.PathValue("id"))
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *SystemAPI) targetsHostKeyPut(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	var b struct {
		Fingerprint string `json:"fingerprint"`
	}
	if err := readJSON(r, &b); err != nil || !strings.HasPrefix(b.Fingerprint, "SHA256:") {
		badBody(w, errors.New("fingerprint: SHA256:… as GET …/hostkey answered it"))
		return
	}
	v, err := a.BackupTargets.TrustHostKey(r.Context(), r.PathValue("id"), b.Fingerprint)
	if err != nil {
		writeTargetErr(w, err)
		return
	}
	slog.Info("backup targets: host key trusted", "by", who(r), "target", r.PathValue("id"), "fingerprint", v.Fingerprint)
	writeJSON(w, 200, v)
}

func (a *SystemAPI) backupNightlyPut(w http.ResponseWriter, r *http.Request) {
	if !a.targetsReady(w) {
		return
	}
	var b struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return
	}
	if err := a.BackupTargets.Store.SetNightly(b.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	slog.Info("backup: nightly run switched", "by", who(r), "enabled", b.Enabled)
	writeJSON(w, 200, map[string]any{"enabled": a.BackupTargets.Store.Nightly(), "time": NightlyTime})
}

// openTargetBackup is POST /restore/check's {target, name}: the backup opened on its target.
func (a *SystemAPI) openTargetBackup(w http.ResponseWriter, r *http.Request) (io.ReadCloser, string, bool) {
	if !a.targetsReady(w) {
		return nil, "", false
	}
	var b struct {
		Target string `json:"target"`
		Name   string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		badBody(w, err)
		return nil, "", false
	}
	// the whole file comes over the network: a long deadline, not the request's alone
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	rc, err := a.BackupTargets.Open(ctx, b.Target, b.Name)
	if err != nil {
		cancel()
		writeTargetErr(w, err)
		return nil, "", false
	}
	slog.Info("restore: a backup from a target", "by", who(r), "target", b.Target, "file", b.Name)
	return &cancelCloser{ReadCloser: rc, cancel: cancel}, b.Name, true
}

type cancelCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// backup-delivery: an enabled target whose last test or delivery failed (the cause is the state:
// unreachable, auth-failed, host-key-changed, read-only, full, stale, last-failed), or that has
// had no successful backup for 26 hours while the nightly run is on (too-old, the backstop for
// every cause). The variant is <id>:<cause>, so another cause is a new warning.
func (a *SystemAPI) backupDeliveryWarnings(ctx context.Context) ([]warnings.Warning, bool) {
	if a.BackupTargets == nil {
		return nil, true
	}
	probs, err := a.BackupTargets.Problems(ctx)
	if err != nil {
		return nil, false
	}
	var out []warnings.Warning
	for _, p := range probs {
		sev := warnings.SeverityError
		if p.Cause == "too-old" {
			sev = warnings.SeverityWarning
		}
		out = append(out, warnings.Warning{ID: "backup-delivery", Variant: p.ID + ":" + p.Cause, Severity: sev, Href: "/backup#targets",
			Params: map[string]any{"name": p.Name, "cause": p.Cause, "detail": p.Detail}})
	}
	return out, true
}

// readCronBackup is ReadCronBackup with a deadline: the directory target's listing must not hang a
// handler or the warnings on a share that does not answer. ok false = it did not answer in time.
func (a *SystemAPI) readCronBackup() (system.CronBackup, bool) {
	var c system.CronBackup
	err := cronProber.Do("directory", func() error {
		c = a.Root.ReadCronBackup()
		return nil
	})
	return c, err == nil
}

// cronProber keeps at most one listing of the directory target in flight.
var cronProber backuptarget.Prober
