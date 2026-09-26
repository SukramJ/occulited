package httpapi

// openccu-lite task 228, phase 1: System → Storage - the USB sticks, Format and Safely remove.
// The listing is the daemon's (system.USBDisks); formatting and ejecting are the helper's, which
// checks the disk itself (priv.usbDiskOK) and runs the fixed steps.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/backuptarget"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/store"
	"github.com/hobbyquaker/occulited/internal/system"
)

// usbDiskHelper is what the routes need of the privilege boundary.
type usbDiskHelper interface {
	USBFormat(ctx context.Context, s priv.USBFormatSpec) error
	USBEject(ctx context.Context, dev string) error
}

// usbHelperOf is the boundary's disk operations; a test replaces it.
var usbHelperOf = func() (usbDiskHelper, bool) {
	h, ok := system.Priv.(usbDiskHelper)
	return h, ok
}

// one format or eject at a time
var usbDiskMu sync.Mutex

func (a *SystemAPI) registerStorageUSB(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemRead, "GET "+p+"/storage/usb", a.storageUSB)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/usb/{disk}/format", a.storageUSBFormat)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/storage/usb/{disk}/eject", a.storageUSBEject)
}

// usbUse is what uses a stick: the journal's target, a backup target.
type usbUse struct {
	Kind string `json:"kind"` // journal, backup
	Name string `json:"name,omitempty"`
	ID   string `json:"id,omitempty"`
}

type usbDiskView struct {
	system.USBDisk
	Uses []usbUse `json:"uses"`
}

// usbUses names what uses each partition: the journal's usb:<label>/ target by label, a backup
// directory target by the mount point its path lies on (/media/usb0 is the linked stick).
func (a *SystemAPI) usbUses(ctx context.Context, d system.USBDisk) []usbUse {
	uses := []usbUse{}
	jc := a.Root.ReadJournalSetting()
	label, _, isUSB := system.ParseJournalUSBTarget(jc.Target)
	var views []backuptarget.View
	if a.BackupTargets != nil {
		views, _ = a.BackupTargets.Views(ctx)
	}
	linked := ""
	for _, s := range a.Root.USBSticks() {
		if s.Linked {
			linked = s.Mount
		}
	}
	storeLabel := ""
	if a.DataStore != nil {
		// task 229: the database's copy on a stick
		if l, _, err := store.ParseUSB(a.DataStore.Status().Location); err == nil {
			storeLabel = l
		}
	}
	for _, p := range d.Partitions {
		if isUSB && p.LabelID != "" && p.LabelID == label {
			uses = append(uses, usbUse{Kind: "journal"})
		}
		if storeLabel != "" && p.LabelID == storeLabel {
			uses = append(uses, usbUse{Kind: "store"})
		}
		if p.Mount == "" {
			continue
		}
		for _, v := range views {
			if !v.OnUSB() {
				continue
			}
			dir := v.Dir() + "/"
			if strings.HasPrefix(dir, p.Mount+"/") || (p.Mount == linked && strings.HasPrefix(dir, "/media/usb0/")) {
				uses = append(uses, usbUse{Kind: "backup", Name: v.Name, ID: v.ID})
			}
		}
	}
	return uses
}

func (a *SystemAPI) usbView(ctx context.Context) map[string]any {
	disks := []usbDiskView{}
	for _, d := range a.Root.USBDisks() {
		disks = append(disks, usbDiskView{USBDisk: d, Uses: a.usbUses(ctx, d)})
	}
	return map[string]any{"disks": disks, "filesystems": a.Root.MkfsAvailable()}
}

func (a *SystemAPI) storageUSB(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.usbView(r.Context()))
}

func (a *SystemAPI) usbDiskFor(w http.ResponseWriter, r *http.Request) (system.USBDisk, usbDiskHelper, bool) {
	h, ok := usbHelperOf()
	if !ok {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "formatting and removing sticks needs the privilege helper"})
		return system.USBDisk{}, nil, false
	}
	name := r.PathValue("disk")
	for _, d := range a.Root.USBDisks() {
		if d.Name == name {
			// a backup being written to it is waited for by the user, not cut off
			for _, u := range a.usbUses(r.Context(), d) {
				if u.Kind == "backup" && a.backupRunning(r.Context(), u.ID) {
					writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: "a backup is being written to this stick; try again when it has finished"})
					return system.USBDisk{}, nil, false
				}
			}
			return d, h, true
		}
	}
	writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: "no USB stick " + name})
	return system.USBDisk{}, nil, false
}

func (a *SystemAPI) backupRunning(ctx context.Context, id string) bool {
	if a.BackupTargets == nil || id == "" {
		return false
	}
	v, err := a.BackupTargets.View(ctx, id)
	return err == nil && v.State.State == "running"
}

func helperError(w http.ResponseWriter, err error) {
	if errors.Is(err, priv.ErrRefused) {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "refused", Message: strings.TrimPrefix(err.Error(), "privilege helper: ")})
		return
	}
	writeJSON(w, http.StatusInternalServerError, apiError{Error: "failed", Message: err.Error()})
}

func (a *SystemAPI) storageUSBFormat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FS    string `json:"fs"`
		Label string `json:"label"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	if err := priv.ValidUSBLabel(body.FS, body.Label); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		return
	}
	usbDiskMu.Lock()
	defer usbDiskMu.Unlock()
	d, h, ok := a.usbDiskFor(w, r)
	if !ok {
		return
	}
	reqLog(r).Warn("storage: formatting a USB stick", "disk", d.Name, "vendor", d.Vendor, "model", d.Model, "serial", d.Serial, "size_bytes", d.Size, "fs", body.FS, "label", body.Label)
	if err := h.USBFormat(r.Context(), priv.USBFormatSpec{Device: d.Name, FS: body.FS, Label: body.Label}); err != nil {
		slog.Error("storage: formatting failed", "disk", d.Name, "err", err)
		helperError(w, err)
		return
	}
	reqLog(r).Info("storage: USB stick formatted", "disk", d.Name, "fs", body.FS, "label", body.Label)
	out := a.usbView(r.Context())
	out["formatted"] = d.Name
	writeJSON(w, 200, out)
}

func (a *SystemAPI) storageUSBEject(w http.ResponseWriter, r *http.Request) {
	usbDiskMu.Lock()
	defer usbDiskMu.Unlock()
	d, h, ok := a.usbDiskFor(w, r)
	if !ok {
		return
	}
	if err := h.USBEject(r.Context(), d.Name); err != nil {
		helperError(w, err)
		return
	}
	reqLog(r).Info("storage: USB stick removed safely", "disk", d.Name, "model", d.Model)
	out := a.usbView(r.Context())
	out["ejected"] = d.Name
	writeJSON(w, 200, out)
}
