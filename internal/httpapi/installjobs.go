package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// An uploaded addon's install runs as a job of its own (B-4). The installer reloads lighttpd when
// the addon brings a web fragment (B-120), and the reload ends the proxied request: an install
// that ran on the request's context lost its tail - the unit generation and the start of the new
// addon - to "context canceled", and the client got no answer. Now the request only stores the
// archive; the install runs detached, and the client polls the job.

// InstallJob is one upload install as GET /addons/install answers it.
type InstallJob struct {
	ID string `json:"id"`
	// State is "running", "done" (the installer ran and exited 0 or 10) or "failed" (it did not
	// run, or exited with anything else - Result says how)
	State    string                `json:"state"`
	Started  time.Time             `json:"started"`
	Finished *time.Time            `json:"finished,omitempty"`
	Bytes    int64                 `json:"bytes"`
	Result   *system.InstallResult `json:"result,omitempty"`
	Error    string                `json:"error,omitempty"`
	done     chan struct{}
}

// installJobs keeps the running job and the last few finished ones; one install at a time.
type installJobs struct {
	mu   sync.Mutex
	list []*InstallJob // oldest first
}

const keepInstallJobs = 8

// errInstallRunning refuses a second install while one runs: install_addon has one archive path.
var errInstallRunning = errors.New("an install is already running")

func (j *installJobs) running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.list) > 0 && j.list[len(j.list)-1].State == "running"
}

func (j *installJobs) begin(bytes int64) (*InstallJob, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.list) > 0 && j.list[len(j.list)-1].State == "running" {
		return nil, errInstallRunning
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	job := &InstallJob{ID: hex.EncodeToString(b), State: "running", Started: time.Now(), Bytes: bytes, done: make(chan struct{})}
	j.list = append(j.list, job)
	if len(j.list) > keepInstallJobs {
		j.list = j.list[len(j.list)-keepInstallJobs:]
	}
	return job, nil
}

func (j *installJobs) finish(job *InstallJob, res *system.InstallResult, err error) {
	j.mu.Lock()
	now := time.Now()
	job.Finished, job.Result = &now, res
	switch {
	case err != nil:
		job.State, job.Error = "failed", err.Error()
	case res.Exit != 0 && res.Exit != 10:
		job.State = "failed"
	default:
		job.State = "done"
	}
	j.mu.Unlock()
	close(job.done)
}

// get returns a copy of the job of that id, or of the newest one for "".
func (j *installJobs) get(id string) (InstallJob, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for i := len(j.list) - 1; i >= 0; i-- {
		if id == "" || j.list[i].ID == id {
			return *j.list[i], true
		}
	}
	return InstallJob{}, false
}

// install takes an uploaded archive (multipart "file" or a raw body), stores it and starts the
// install detached; 202 with the job. ?wait=true answers when the job ends instead (a script's
// convenience - the install runs detached either way, so a cut connection costs only the answer).
func (a *SystemAPI) install(w http.ResponseWriter, r *http.Request) {
	if a.Manager == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "no addon manager"})
		return
	}
	// refused before the upload is stored: install_addon has one archive path, and the catalogue
	// installs through the same installer
	if a.installs.running() || a.catalogInstalling() {
		writeJSON(w, http.StatusConflict, apiError{Error: "install-running", Message: errInstallRunning.Error()})
		return
	}
	var src io.Reader = r.Body
	if ct := r.Header.Get("Content-Type"); len(ct) >= 9 && ct[:9] == "multipart" {
		mr, err := r.MultipartReader()
		if err != nil {
			badBody(w, err)
			return
		}
		for {
			part, err := mr.NextPart()
			if err != nil {
				badBody(w, io.ErrUnexpectedEOF)
				return
			}
			if part.FormName() == "file" {
				src = part
				break
			}
		}
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	staged, err := system.StageAddonArchive(a.Root, "upload-"+hex.EncodeToString(b)+".tar.gz", src)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "install-failed", Message: err.Error()})
		return
	}
	job, err := a.installs.begin(staged.Size)
	if err != nil {
		staged.Remove()
		writeJSON(w, http.StatusConflict, apiError{Error: "install-running", Message: err.Error()})
		return
	}
	go a.runInstall(job, staged)
	if r.URL.Query().Get("wait") == "true" {
		select {
		case <-job.done:
		case <-r.Context().Done():
			return
		}
		out, _ := a.installs.get(job.ID)
		status := http.StatusOK
		if out.State != "done" {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, out)
		return
	}
	out, _ := a.installs.get(job.ID)
	writeJSON(w, http.StatusAccepted, out)
}

// runInstall is the job: on a context of its own, which no request ends.
func (a *SystemAPI) runInstall(job *InstallJob, staged *system.StagedArchive) {
	defer staged.Remove()
	res, err := a.Manager.Install(context.Background(), staged)
	a.installs.finish(job, res, err)
	if err != nil {
		slog.Warn("addons: an uploaded archive was not installed", "job", job.ID, "err", err)
		return
	}
	slog.Info("addons: an uploaded archive was installed", "job", job.ID, "exit", res.Exit, "seconds", res.Seconds)
	if (res.Exit == 0 || res.Exit == 10) && a.Updates != nil {
		// the addon update check's results are from before this install; an archive does not say
		// which addon it was, so the whole check runs again (in its loop, not in this job)
		a.Updates.Trigger()
	}
}

// installJob answers GET /addons/install: ?job=<id>, or the newest job without one.
func (a *SystemAPI) installJob(w http.ResponseWriter, r *http.Request) {
	job, ok := a.installs.get(r.URL.Query().Get("job"))
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "unknown-job", Message: "no such install job"})
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// catalogInstalling says whether a catalogue install runs now.
func (a *SystemAPI) catalogInstalling() bool {
	if a.Catalog == nil {
		return false
	}
	p := a.Catalog.Progress()
	return p != nil && p.Finished == nil
}
