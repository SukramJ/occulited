package httpapi

// openccu-lite task 232 (the maintainer, 2026-10-03: "only OIDC and ACME ... per pin, the admin
// chooses whether the pin is checked in addition to the CA validation (the default) or instead of
// it"): the pins of the OAuth / OIDC and the ACME store. The page's Pin the current certificate
// fetches the chain the configured server presents (unverified, for the comparison by
// fingerprint) and pins the chosen certificate's public key; a bare public key hash makes a
// backup pin for a key rollover; a mismatch is a pending failure with the Status page's warning
// trust-pin and the page's Re-pin (a pin with replace).

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/trust"
	"github.com/hobbyquaker/occulited/internal/warnings"
)

func (a *SystemAPI) registerPins(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/trust/{store}/pins", a.pinAdd)
	route(mux, auth.ScopeSystemWrite, "POST "+p+"/trust/{store}/pins/peer", a.pinPeer)
	route(mux, auth.ScopeSystemWrite, "DELETE "+p+"/trust/{store}/pins/{id}", a.pinRemove)
}

// pinError maps the pin operations' refusals.
func pinError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, trust.ErrPinPurpose):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: err.Error()})
	case errors.Is(err, trust.ErrPinMode), errors.Is(err, trust.ErrPinSPKI):
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: err.Error()})
	case errors.Is(err, trust.ErrPinPresent):
		writeJSON(w, http.StatusConflict, apiError{Error: "present", Message: err.Error()})
	default:
		trustStoreError(w, err)
	}
}

// pinTarget is the server a purpose's connections go to: the OIDC issuer, the ACME directory.
func (a *SystemAPI) pinTarget(purpose string) string {
	if a.PinTarget == nil {
		return ""
	}
	return strings.TrimSpace(a.PinTarget(purpose))
}

type pinsAnswer struct {
	Pin  *trust.Pin  `json:"pin,omitempty"`
	Pins []trust.Pin `json:"pins"`
}

// pinAdd pins a public key: {pem, mode?, replace?} - the certificate from pins/peer whose public
// key is pinned, with its subject and fingerprint kept - or {spki, mode?} for a bare hash (a backup
// pin). replace drops the purpose's other pins first (the page's Re-pin).
func (a *SystemAPI) pinAdd(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	var body struct {
		PEM     string `json:"pem"`
		SPKI    string `json:"spki"`
		Mode    string `json:"mode"`
		Replace bool   `json:"replace"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	purpose := r.PathValue("store")
	req := trust.PinRequest{SPKI: body.SPKI, Mode: body.Mode, By: SessionFrom(r).User, Replace: body.Replace}
	if strings.TrimSpace(body.PEM) != "" {
		certs, err := trust.Parse([]byte(body.PEM))
		if err != nil {
			trustError(w, err)
			return
		}
		if len(certs) != 1 {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "one certificate at a time: the one whose public key is pinned"})
			return
		}
		req.Cert = certs[0]
	}
	pin, err := a.Trust.AddPin(purpose, req)
	if err != nil {
		pinError(w, err)
		return
	}
	withCaller(r, a.trustLog()).Info("trust: public key pinned", "purpose", purpose, "subject", pin.Subject, "spki", pin.SPKI, "mode", pin.Mode, "replace", body.Replace)
	a.trustChanged()
	pins, _ := a.Trust.Pins(purpose)
	writeJSON(w, 200, pinsAnswer{Pin: &pin, Pins: pins})
}

func (a *SystemAPI) pinRemove(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	purpose, id := r.PathValue("store"), r.PathValue("id")
	if err := a.Trust.RemovePin(purpose, id); err != nil {
		pinError(w, err)
		return
	}
	withCaller(r, a.trustLog()).Info("trust: pin removed", "purpose", purpose, "id", id)
	a.trustChanged()
	pins, _ := a.Trust.Pins(purpose)
	writeJSON(w, 200, pinsAnswer{Pins: pins})
}

// peerEntry is one certificate of the chain a server presents, for the page's pin choice.
type peerEntry struct {
	trust.Info
	PEM string `json:"pem"`
	// Pinned: its public key is one of the purpose's pins already
	Pinned bool `json:"pinned"`
}

type peerAnswer struct {
	URL   string      `json:"url"`
	Host  string      `json:"host"`
	Chain []peerEntry `json:"chain"`
	// Verified: the chain passes the purpose's verification as it stands (pool and pins); Error
	// says why not
	Verified bool   `json:"verified"`
	Error    string `json:"error,omitempty"`
	// PinOnly: a pin alone vouched
	PinOnly bool `json:"pin_only,omitempty"`
}

// pinPeer fetches the chain the purpose's server presents - {url?} for one not saved yet, else the
// configured OIDC issuer or ACME directory - without verifying it; nothing is trusted from it.
func (a *SystemAPI) pinPeer(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &body); err != nil && !strings.Contains(err.Error(), "empty body") {
		badBody(w, err)
		return
	}
	purpose := r.PathValue("store")
	pins, err := a.Trust.Pins(purpose)
	if err != nil {
		pinError(w, err)
		return
	}
	target := strings.TrimSpace(body.URL)
	if target == "" {
		target = a.pinTarget(purpose)
	}
	if target == "" {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "no server is configured for this purpose: save the OIDC issuer or the ACME directory first, or give a URL"})
		return
	}
	if !strings.HasPrefix(target, "https://") {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "the server is an https URL; a plain http one has no certificate to pin"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	host, chain, err := trust.PeerChain(ctx, target)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: "unreachable", Message: err.Error()})
		return
	}
	now := time.Now()
	out := peerAnswer{URL: target, Host: host, Chain: []peerEntry{}}
	for _, c := range chain {
		spki := trust.SPKI(c)
		pinned := false
		for _, p := range pins {
			if p.SPKI == spki {
				pinned = true
			}
		}
		out.Chain = append(out.Chain, peerEntry{Info: trust.Describe(c, now), PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})), Pinned: pinned})
	}
	if v, err := a.Trust.VerifierFor(purpose); err == nil {
		o, verr := v.Verify(host, chain)
		out.Verified, out.PinOnly = verr == nil, o.PinOnly
		if verr != nil {
			out.Error = verr.Error()
		}
	}
	writeJSON(w, 200, out)
}

// pinWarnings: the pending mismatches - one warning per purpose and host (the variant), severity
// error: the connection does not go through until an administrator re-pins or the server's key
// comes back.
func (a *SystemAPI) pinWarnings(context.Context) ([]warnings.Warning, bool) {
	if a.Trust == nil {
		return nil, true
	}
	fs := a.Trust.PinFailures()
	sort.Slice(fs, func(i, j int) bool {
		if fs[i].Purpose != fs[j].Purpose {
			return fs[i].Purpose < fs[j].Purpose
		}
		return fs[i].Host < fs[j].Host
	})
	var out []warnings.Warning
	for _, f := range fs {
		params := map[string]any{"purpose": f.Purpose, "host": f.Host}
		if len(f.Chain) > 0 {
			params["subject"], params["fingerprint"], params["spki"] = f.Chain[0].Subject, f.Chain[0].Fingerprint, f.Chain[0].SPKI
		}
		out = append(out, warnings.Warning{ID: "trust-pin", Variant: f.Purpose + ":" + f.Host, Severity: warnings.SeverityError, Href: "/system/trust#" + f.Purpose, Params: params})
	}
	return out, true
}
