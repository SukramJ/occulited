package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/hobbyquaker/occulited/internal/acme"
	"github.com/hobbyquaker/occulited/internal/system"
)

// The certificate routes (task 35, D-48): the current certificate and the ACME settings, the
// settings' PUT, and the four actions. Test, issue and renew run in the background - the page
// polls GET /certificate and reads `running` and `last` - the switch back is synchronous.

func (a *SystemAPI) certificateUnavailable(w http.ResponseWriter) bool {
	if a.Cert == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "unsupported", Message: "the certificate service is not available on this system"})
		return true
	}
	return false
}

func (a *SystemAPI) certificate(w http.ResponseWriter, _ *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	// the names the page shows follow a domain that changed since the last look
	a.followDomain(a.Root.Hostname(), a.Root.Domain())
	a.followFQDN(a.Root.Hostname(), a.Root.Domain())
	writeJSON(w, 200, a.Cert.Status())
}

func (a *SystemAPI) certificateSettingsPut(w http.ResponseWriter, r *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	var u acme.Update
	if err := readJSON(r, &u); err != nil {
		badBody(w, err)
		return
	}
	if _, err := a.Cert.SetSettings(u); err != nil {
		if errors.Is(err, acme.ErrInvalid) {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			return
		}
		writeErr(w, err)
		return
	}
	// task 175: ACME with HTTP-01 owns a firewall rule for port 80 - it follows the settings at once
	if system.FirewallChanged != nil {
		system.FirewallChanged(r.Context())
	}
	writeJSON(w, 200, a.Cert.Status())
}

// certificateStart is test, issue and renew: 202 with the status, whose `running` is the attempt.
func (a *SystemAPI) certificateStart(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if a.certificateUnavailable(w) {
			return
		}
		if err := a.Cert.Start(kind); err != nil {
			switch {
			case errors.Is(err, acme.ErrBusy):
				writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: err.Error()})
			case errors.Is(err, acme.ErrInvalid):
				writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
			default:
				writeErr(w, err)
			}
			return
		}
		writeJSON(w, http.StatusAccepted, a.Cert.Status())
	}
}

func (a *SystemAPI) certificateSelfSigned(w http.ResponseWriter, r *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	// task 36: the installer switches HSTS off on the way back (a browser that saw the header
	// would refuse the self-signed certificate); the answer says when it did
	hstsBefore := a.Root.HTTPSSettings().HSTS
	restarted, err := a.Cert.SelfSigned(r.Context())
	if err != nil {
		if errors.Is(err, acme.ErrBusy) {
			writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: err.Error()})
			return
		}
		writeErr(w, err)
		return
	}
	if restarted == nil {
		restarted = []string{}
	}
	// task 96: HSTS goes into the clearing state, not without a header; the deadline when it runs
	after := a.Root.HTTPSSettings()
	out := map[string]any{"status": a.Cert.Status(), "restarted_addons": restarted, "hsts_disabled": hstsBefore && !after.HSTS}
	if until := clearingUntil(after); until != nil {
		out["hsts_clearing_until"] = until
	}
	writeJSON(w, 200, out)
}

// ---- task 38: mode manual --------------------------------------------------------------------

// certificateManualPut is PUT /certificate/manual: multipart (`certificate`, `chain`, `key` as
// files or fields, each PEM or DER) or JSON with the three PEM strings. Synchronous: the
// material is checked, installed, and the mode becomes manual.
func (a *SystemAPI) certificateManualPut(w http.ResponseWriter, r *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	in, err := readManualInput(w, r)
	if err != nil {
		badBody(w, err)
		return
	}
	res, err := a.Cert.InstallManual(r.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, acme.ErrBusy):
			writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: err.Error()})
		case errors.Is(err, acme.ErrInvalid):
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		default:
			writeErr(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"status": a.Cert.Status(), "result": res, "restarted_addons": res.RestartedAddons, "warning": res.Warning})
}

// readManualInput reads the three parts from a multipart form (a file part first, a field
// otherwise) or from a JSON body.
func readManualInput(w http.ResponseWriter, r *http.Request) (acme.ManualInput, error) {
	var in acme.ManualInput
	if ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); ct == "multipart/form-data" {
		r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			return in, err
		}
		part := func(name string) ([]byte, error) {
			if r.MultipartForm != nil {
				if fhs := r.MultipartForm.File[name]; len(fhs) > 0 {
					f, err := fhs[0].Open()
					if err != nil {
						return nil, err
					}
					defer f.Close()
					return io.ReadAll(io.LimitReader(f, 1<<20))
				}
			}
			return []byte(r.FormValue(name)), nil
		}
		var err error
		if in.Certificate, err = part("certificate"); err != nil {
			return in, err
		}
		if in.Chain, err = part("chain"); err != nil {
			return in, err
		}
		if in.Key, err = part("key"); err != nil {
			return in, err
		}
		return in, nil
	}
	var body struct {
		Certificate string `json:"certificate"`
		Chain       string `json:"chain"`
		Key         string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		return in, err
	}
	return acme.ManualInput{Certificate: []byte(body.Certificate), Chain: []byte(body.Chain), Key: []byte(body.Key)}, nil
}

// certificateKey is POST /certificate/key: a key and a CSR made on the box, the CSR PEM and
// the key's fingerprint in the answer; the key itself never leaves the box.
func (a *SystemAPI) certificateKey(w http.ResponseWriter, r *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	var req acme.KeyRequest
	if err := readJSON(r, &req); err != nil {
		badBody(w, err)
		return
	}
	p, err := a.Cert.GenerateKey(req)
	if err != nil {
		switch {
		case errors.Is(err, acme.ErrBusy):
			writeJSON(w, http.StatusConflict, apiError{Error: "busy", Message: err.Error()})
		case errors.Is(err, acme.ErrInvalid):
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
		default:
			writeErr(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"pending": p, "csr": p.CSR, "status": a.Cert.Status()})
}

// certificateCSR is GET /certificate/csr: the pending request as a download.
func (a *SystemAPI) certificateCSR(w http.ResponseWriter, _ *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	b, name, err := a.Cert.CSR()
	if err != nil {
		if errors.Is(err, acme.ErrNoCSR) {
			writeJSON(w, http.StatusNotFound, apiError{Error: "not-found", Message: err.Error()})
			return
		}
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/pkcs10")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// certificateInspect is POST /certificate/inspect: the parsed preview of pasted material;
// nothing is saved.
func (a *SystemAPI) certificateInspect(w http.ResponseWriter, r *http.Request) {
	if a.certificateUnavailable(w) {
		return
	}
	in, err := readManualInput(w, r)
	if err != nil {
		badBody(w, err)
		return
	}
	writeJSON(w, 200, a.Cert.Inspect(in))
}
