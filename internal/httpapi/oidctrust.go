package httpapi

// openccu-lite task 230 (the maintainer: "shouldnt we have possibility to upload/paste pem for
// trusting authentik server?"): the identity provider's certificate, trusted by upload or paste
// of a PEM - a private CA, a chain, or the server's own certificate pinned - for the OIDC client
// only. The anchors live in the trust store (internal/trust) with the purpose "oidc", so task
// 231's trust store page can take them over without a migration.

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/auth"
	"github.com/hobbyquaker/occulited/internal/config"
	"github.com/hobbyquaker/occulited/internal/oidc"
	"github.com/hobbyquaker/occulited/internal/trust"
)

func (a *AuthAPI) registerOIDCTrust(mux *http.ServeMux, p string) {
	route(mux, auth.ScopeAuthAdmin, "GET "+p+"/oidc/trust", a.oidcTrustList)
	route(mux, auth.ScopeAuthAdmin, "POST "+p+"/oidc/trust", a.oidcTrustAdd)
	route(mux, auth.ScopeAuthAdmin, "DELETE "+p+"/oidc/trust/{id}", a.oidcTrustRemove)
	route(mux, auth.ScopeAuthAdmin, "POST "+p+"/oidc/test", a.oidcTest)
	route(mux, auth.ScopeAuthAdmin, "POST "+p+"/oidc/peer-chain", a.oidcPeerChain)
}

// ApplyOIDCTrust hands the running provider client the system's pool plus the OIDC anchors;
// called at start and after every change of the anchors.
func (a *AuthAPI) ApplyOIDCTrust() error {
	if a.Trust == nil || a.OIDC == nil {
		return nil
	}
	pool, err := a.Trust.Pool(trust.PurposeOIDC)
	if err != nil {
		return err
	}
	a.OIDC.SetRootCAs(pool)
	return nil
}

type trustList struct {
	Anchors []trust.Info `json:"anchors"`
	Added   []trust.Info `json:"added,omitempty"`
}

func (a *AuthAPI) trustReady(w http.ResponseWriter) bool {
	if a.Trust == nil {
		writeJSON(w, http.StatusNotImplemented, apiError{Error: "no-trust-store", Message: "this daemon runs without a state directory for trust anchors"})
		return false
	}
	return true
}

func (a *AuthAPI) oidcTrustList(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	l, err := a.Trust.List(trust.PurposeOIDC)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "trust", Message: err.Error()})
		return
	}
	writeJSON(w, 200, trustList{Anchors: l})
}

// trustError maps the store's refusals to 422 codes the page words.
func trustError(w http.ResponseWriter, err error) {
	code := ""
	switch {
	case errors.Is(err, trust.ErrPrivateKey):
		code = "private_key"
	case errors.Is(err, trust.ErrNoCertificate):
		code = "no_certificate"
	case errors.Is(err, trust.ErrExpired):
		code = "expired"
	case errors.Is(err, trust.ErrNotYetValid):
		code = "not_yet_valid"
	case errors.Is(err, trust.ErrUnknown):
		writeJSON(w, http.StatusNotFound, apiError{Error: "not_found", Message: err.Error()})
		return
	}
	if code == "" {
		if strings.HasPrefix(err.Error(), "a certificate does not parse") {
			code = "bad_certificate"
		} else {
			writeJSON(w, http.StatusInternalServerError, apiError{Error: "trust", Message: err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: code, Message: err.Error()})
}

// oidcTrustAdd takes {pem} - pasted or the content of an uploaded .pem/.crt - and, from the
// page's fetch of the issuer's chain, {pem, fingerprint}: then the certificate must be the one
// whose fingerprint the administrator confirmed.
func (a *AuthAPI) oidcTrustAdd(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	var body struct {
		PEM         string `json:"pem"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := readJSON(r, &body); err != nil {
		badBody(w, err)
		return
	}
	if body.Fingerprint != "" {
		certs, err := trust.Parse([]byte(body.PEM))
		if err != nil {
			trustError(w, err)
			return
		}
		if len(certs) != 1 || trust.NormalFingerprint(trust.Fingerprint(certs[0])) != trust.NormalFingerprint(body.Fingerprint) {
			writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "fingerprint_mismatch", Message: "the certificate is not the one whose fingerprint was confirmed"})
			return
		}
	}
	added, err := a.Trust.Add([]byte(body.PEM), trust.PurposeOIDC, SessionFrom(r).User)
	if err != nil {
		trustError(w, err)
		return
	}
	if err := a.ApplyOIDCTrust(); err != nil {
		a.logger().Warn("auth.oidc: the trust anchors could not be applied", "err", err)
	}
	for _, i := range added {
		a.logger().Info("auth.oidc: certificate trusted for the identity provider", "subject", i.Subject, "fingerprint", i.Fingerprint, "by", SessionFrom(r).User)
	}
	l, _ := a.Trust.List(trust.PurposeOIDC)
	writeJSON(w, 200, trustList{Anchors: l, Added: added})
}

func (a *AuthAPI) oidcTrustRemove(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	id := r.PathValue("id")
	if err := a.Trust.Remove(id, trust.PurposeOIDC); err != nil {
		trustError(w, err)
		return
	}
	if err := a.ApplyOIDCTrust(); err != nil {
		a.logger().Warn("auth.oidc: the trust anchors could not be applied", "err", err)
	}
	a.logger().Info("auth.oidc: a trusted certificate removed", "id", id, "by", SessionFrom(r).User)
	l, _ := a.Trust.List(trust.PurposeOIDC)
	writeJSON(w, 200, trustList{Anchors: l})
}

// the issuer of a test: the body's (the form, maybe not saved yet), else the stored one
func (a *AuthAPI) testIssuer(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Issuer string `json:"issuer"`
	}
	if err := readJSON(r, &body); err != nil && !strings.Contains(err.Error(), "empty body") {
		badBody(w, err)
		return "", false
	}
	issuer := strings.TrimSpace(body.Issuer)
	if issuer == "" && a.ConfigFile != "" {
		if cfg, err := config.Load(a.ConfigFile); err == nil {
			issuer = cfg.Auth.OIDC.Issuer
		}
	}
	if !strings.HasPrefix(issuer, "https://") && !strings.HasPrefix(issuer, "http://") {
		writeJSON(w, http.StatusUnprocessableEntity, apiError{Error: "invalid", Message: "the issuer is a URL (https://…)"})
		return "", false
	}
	return issuer, true
}

// verifiedBy is the chain's anchor as the page shows it: the last certificate of the verified
// chain, and whether it is one added here or the system's own
type verifiedBy struct {
	trust.Info
	// TrustedHere: one of the anchors added here, not the system's own
	TrustedHere bool `json:"trusted_here"`
}

type oidcTestAnswer struct {
	oidc.CheckResult
	TLS        bool        `json:"tls"`
	VerifiedBy *verifiedBy `json:"verified_by,omitempty"`
}

func (a *AuthAPI) oidcTest(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	issuer, ok := a.testIssuer(w, r)
	if !ok {
		return
	}
	pool, err := a.Trust.Pool(trust.PurposeOIDC)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiError{Error: "trust", Message: err.Error()})
		return
	}
	res := oidc.Check(r.Context(), issuer, pool)
	out := oidcTestAnswer{CheckResult: res, TLS: strings.HasPrefix(issuer, "https://")}
	if n := len(res.Verified); n > 0 {
		root := res.Verified[n-1]
		ours, _ := a.Trust.Certificates(trust.PurposeOIDC)
		// a pinned server certificate is its own one-element chain
		added := slices.ContainsFunc(ours, func(c *x509.Certificate) bool { return c.Equal(root) })
		out.VerifiedBy = &verifiedBy{Info: trust.Describe(root, time.Now()), TrustedHere: added}
	}
	writeJSON(w, 200, out)
}

// peerCert is one certificate of the chain the issuer's server presents.
type peerCert struct {
	trust.Info
	PEM     string `json:"pem"`
	Trusted bool   `json:"trusted"` // already one of the OIDC anchors
}

func (a *AuthAPI) oidcPeerChain(w http.ResponseWriter, r *http.Request) {
	if !a.trustReady(w) {
		return
	}
	issuer, ok := a.testIssuer(w, r)
	if !ok {
		return
	}
	chain, err := oidc.PeerChain(r.Context(), issuer)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: "unreachable", Message: err.Error()})
		return
	}
	ours, _ := a.Trust.Certificates(trust.PurposeOIDC)
	now := time.Now()
	out := []peerCert{}
	for _, c := range chain {
		out = append(out, peerCert{Info: trust.Describe(c, now), PEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})),
			Trusted: slices.ContainsFunc(ours, func(o *x509.Certificate) bool { return o.Equal(c) })})
	}
	// whether the chain verifies now, with the system's pool and the anchors
	pool, _ := a.Trust.Pool(trust.PurposeOIDC)
	verr := ""
	if len(chain) > 0 {
		inter := x509.NewCertPool()
		for _, c := range chain[1:] {
			inter.AddCert(c)
		}
		host := issuer
		if u := hostOf(issuer); u != "" {
			host = u
		}
		if _, err := chain[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: inter, DNSName: host}); err != nil {
			verr = err.Error()
		}
	}
	writeJSON(w, 200, map[string]any{"chain": out, "verified": verr == "", "error": verr})
}

func hostOf(issuer string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(issuer, "https://"), "http://")
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
	}
	if i := strings.LastIndex(s, ":"); i >= 0 && !strings.Contains(s[:i], ":") {
		s = s[:i]
	}
	return s
}
