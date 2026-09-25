package acme

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/priv"
)

// Task 38: the third mode, manual - the user brings the certificate: uploaded or pasted, PEM or
// DER, with a key of his own or for the key the box generated with a CSR. Everything here goes
// through the same installer as an ACME certificate (the helper's write with the marker,
// lighttpd's reload, the certs-group addons' restart); what differs is where the material comes
// from and that nobody renews it - the timer runs in mode acme only (ShouldRenew).

// ModeManual is the mode a manual install sets.
const ModeManual = "manual"

// The files under <state>/tls/ (docs/config.md): the key of the last CSR or the last manual
// install (never handed out), the pending request, and the chain last installed by hand.
const (
	fileTLSKey  = "key.pem"  // PKCS#8 PEM, 0600
	fileTLSCSR  = "csr.pem"  // the pending request
	fileTLSMeta = "csr.json" // PendingKey
	fileTLSCert = "cert.pem" // the last manually installed chain
)

// PendingKey describes the key + CSR made on the box and not yet answered by a certificate.
type PendingKey struct {
	Algorithm   string    `json:"algorithm"`
	Fingerprint string    `json:"fingerprint"`
	CN          string    `json:"cn"`
	SANs        []string  `json:"sans"`
	Org         string    `json:"org,omitempty"`
	Created     time.Time `json:"created"`
	// CSR is the request's PEM, for the page's text to copy (GET /certificate/csr downloads it).
	CSR string `json:"csr,omitempty"`
}

// KeyRequest is POST /certificate/key.
type KeyRequest struct {
	Algorithm string   `json:"algorithm"`
	CN        string   `json:"cn"`
	SANs      []string `json:"sans"`
	Org       string   `json:"org"`
}

// ManualInput is PUT /certificate/manual: each part PEM or DER, the chain optional (or
// appended to the certificate), the key optional when a pending key is waiting for exactly
// this certificate.
type ManualInput struct {
	Certificate []byte
	Chain       []byte
	Key         []byte
}

// ManualResult is what the install answers beside the status.
type ManualResult struct {
	Certificate     *certpem.Info `json:"certificate"`
	RestartedAddons []string      `json:"restarted_addons"`
	// Warning: installed, but the chain does not carry the leaf's issuer.
	Warning string `json:"warning,omitempty"`
	// KeyFrom says which key was used: "upload" or "pending".
	KeyFrom string `json:"key_from"`
}

// InspectResult is POST /certificate/inspect: what the page shows under a field as soon as its
// text parses, before anything is saved.
type InspectResult struct {
	Certificate      *certpem.Info    `json:"certificate,omitempty"`
	CertificateError string           `json:"certificate_error,omitempty"`
	Chain            int              `json:"chain,omitempty"`
	ChainWarning     string           `json:"chain_warning,omitempty"`
	Key              *certpem.KeyInfo `json:"key,omitempty"`
	KeyError         string           `json:"key_error,omitempty"`
	// Matches: the key given belongs to the certificate given; PendingMatches: the box's
	// pending key does. Absent when the pair to compare is not there.
	Matches        *bool `json:"matches,omitempty"`
	PendingMatches *bool `json:"pending_matches,omitempty"`
	// Validity is CheckValidity's refusal, for the page to show in red before Save.
	Validity string `json:"validity,omitempty"`
}

// tlsDir is <state>/tls, the sibling of the ACME directory unless set.
func (s *Service) tls() store {
	if s.TLSDir != "" {
		return store{dir: s.TLSDir}
	}
	return store{dir: filepath.Join(filepath.Dir(s.st.dir), "tls")}
}

// pendingKey reads the stored key, nil when there is none.
func (s *Service) pendingKey() (crypto.Signer, error) {
	b, err := s.tls().read(fileTLSKey)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return certpem.ParsePrivateKey(b)
}

// pending is the request waiting for its certificate, nil when none.
func (s *Service) pending() *PendingKey {
	var p PendingKey
	if ok, err := s.tls().readJSON(fileTLSMeta, &p); err != nil || !ok {
		return nil
	}
	if csr, err := s.tls().read(fileTLSCSR); err == nil {
		p.CSR = string(csr)
	}
	if p.SANs == nil {
		p.SANs = []string{}
	}
	return &p
}

// GenerateKey makes the key and the request and stores both; an earlier pending key is
// replaced (the page asks first). The key never leaves the box.
func (s *Service) GenerateKey(req KeyRequest) (*PendingKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil {
		return nil, ErrBusy
	}
	cn := strings.ToLower(strings.TrimSpace(req.CN))
	if cn == "" {
		return nil, invalid("the common name is empty - the name this system is reached by")
	}
	var sans []string
	for _, raw := range req.SANs {
		if n := strings.ToLower(strings.TrimSpace(raw)); n != "" && n != cn {
			sans = append(sans, n)
		}
	}
	if sans == nil {
		sans = []string{}
	}
	key, err := certpem.GenerateKey(req.Algorithm)
	if err != nil {
		return nil, invalid("%v", err)
	}
	csr, err := certpem.CSR(key, cn, sans, req.Org)
	if err != nil {
		return nil, invalid("%v", err)
	}
	keyPEM, err := certpem.EncodeKey(key)
	if err != nil {
		return nil, err
	}
	info := certpem.DescribeKey(key)
	p := &PendingKey{Algorithm: info.Algorithm, Fingerprint: info.Fingerprint, CN: cn, SANs: sans, Org: strings.TrimSpace(req.Org), Created: s.now()}
	st := s.tls()
	if err := st.write(fileTLSKey, keyPEM); err != nil {
		return nil, err
	}
	if err := st.write(fileTLSCSR, csr); err != nil {
		return nil, err
	}
	if err := st.writeJSON(fileTLSMeta, p); err != nil {
		return nil, err
	}
	p.CSR = string(csr)
	s.log().Info("certificate: key and CSR generated", "cn", cn, "algorithm", info.Algorithm, "fingerprint", info.Fingerprint)
	return p, nil
}

// CSR returns the pending request's PEM and a file name for the download; ErrNoCSR when none.
func (s *Service) CSR() ([]byte, string, error) {
	p := s.pending()
	if p == nil || p.CSR == "" {
		return nil, "", ErrNoCSR
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			return r
		}
		return '_'
	}, p.CN)
	if name == "" {
		name = "certificate"
	}
	return []byte(p.CSR), name + ".csr", nil
}

// ErrNoCSR: there is no pending request to download.
var ErrNoCSR = errors.New("no certificate request has been generated")

// Inspect parses what the page holds without saving anything.
func (s *Service) Inspect(in ManualInput) InspectResult {
	var res InspectResult
	now := s.now()
	var certs []byte
	if len(strings.TrimSpace(string(in.Certificate))) > 0 {
		pemBytes, err := certpem.NormalizeCertificates(in.Certificate)
		if err != nil {
			res.CertificateError = err.Error()
		} else {
			certs = pemBytes
		}
	}
	if len(strings.TrimSpace(string(in.Chain))) > 0 {
		pemBytes, err := certpem.NormalizeCertificates(in.Chain)
		if err != nil {
			res.CertificateError = strings.TrimSpace(res.CertificateError + " chain: " + err.Error())
		} else {
			certs = append(certs, pemBytes...)
		}
	}
	var leaf *certpem.Info
	if certs != nil {
		parsed, err := certpem.ParseCertificates(certs)
		if err != nil {
			res.CertificateError = err.Error()
		} else {
			ordered := certpem.Order(parsed)
			info, _ := certpem.ParseInfo(certpem.EncodeCertificates(ordered), now)
			leaf = info
			res.Certificate = info
			res.Chain = len(ordered)
			res.ChainWarning = certpem.ChainWarning(ordered)
			if err := certpem.CheckValidity(ordered[0], now); err != nil {
				res.Validity = err.Error()
			}
			if pk, err := s.pendingKey(); err == nil && pk != nil {
				m := certpem.KeyMatches(ordered[0], pk)
				res.PendingMatches = &m
			}
			if len(strings.TrimSpace(string(in.Key))) > 0 {
				if keyPEM, err := certpem.NormalizeKey(in.Key); err == nil {
					if k, err := certpem.ParsePrivateKey(keyPEM); err == nil {
						m := certpem.KeyMatches(ordered[0], k)
						res.Matches = &m
					}
				}
			}
		}
	}
	_ = leaf
	if len(strings.TrimSpace(string(in.Key))) > 0 {
		keyPEM, err := certpem.NormalizeKey(in.Key)
		if err != nil {
			res.KeyError = err.Error()
		} else if k, err := certpem.ParsePrivateKey(keyPEM); err != nil {
			res.KeyError = err.Error()
		} else {
			info := certpem.DescribeKey(k)
			res.Key = &info
		}
	}
	return res
}

// InstallManual validates the material and installs it: the certificate (with the chain given
// or appended), the key uploaded or the pending one, and the mode becomes manual. Refused with
// the reason: no certificate, no key at all, a key that is not the certificate's (both
// fingerprints named), an encrypted key, an expired or not-yet-valid certificate. A missing
// intermediate is a warning in the result.
func (s *Service) InstallManual(ctx context.Context, in ManualInput) (*ManualResult, error) {
	s.mu.Lock()
	if s.running != nil {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	s.mu.Unlock()
	now := s.now()
	if len(strings.TrimSpace(string(in.Certificate))) == 0 {
		return nil, invalid("no certificate: upload or paste the certificate the CA issued")
	}
	certPEM, err := certpem.NormalizeCertificates(in.Certificate)
	if err != nil {
		return nil, invalid("the certificate does not parse (PEM or DER): %v", err)
	}
	if len(strings.TrimSpace(string(in.Chain))) > 0 {
		chainPEM, err := certpem.NormalizeCertificates(in.Chain)
		if err != nil {
			return nil, invalid("the chain does not parse (PEM or DER): %v", err)
		}
		certPEM = append(certPEM, chainPEM...)
	}
	parsed, err := certpem.ParseCertificates(certPEM)
	if err != nil {
		return nil, invalid("%v", err)
	}
	ordered := certpem.Order(parsed)
	leaf := ordered[0]
	if err := certpem.CheckValidity(leaf, now); err != nil {
		return nil, invalid("%v", err)
	}
	// the key: uploaded, or the pending one
	var key crypto.Signer
	from := "upload"
	if len(strings.TrimSpace(string(in.Key))) > 0 {
		keyPEM, err := certpem.NormalizeKey(in.Key)
		if err != nil {
			if errors.Is(err, certpem.ErrEncrypted) {
				return nil, invalid("%v", err)
			}
			return nil, invalid("the private key does not parse (PEM or DER; PKCS#8, PKCS#1 or SEC 1): %v", err)
		}
		if key, err = certpem.ParsePrivateKey(keyPEM); err != nil {
			return nil, invalid("the private key: %v", err)
		}
	} else {
		pk, err := s.pendingKey()
		if err != nil {
			return nil, fmt.Errorf("the pending key: %w", err)
		}
		if pk == nil {
			return nil, invalid("no private key: upload or paste the certificate's key, or generate a key and request on this page first and have the CA sign that request")
		}
		key, from = pk, "pending"
	}
	if !certpem.KeyMatches(leaf, key) {
		certFP, _ := certpem.PublicKeyFingerprint(leaf.PublicKey)
		keyFP := certpem.DescribeKey(key).Fingerprint
		which := "the key given"
		if from == "pending" {
			which = "the key generated on this system"
		}
		return nil, invalid("the private key does not belong to the certificate: the certificate was issued for the key %s, %s is %s", short(certFP), which, short(keyFP))
	}
	chainPEM := certpem.EncodeCertificates(ordered)
	keyPEM, err := certpem.EncodeKey(key)
	if err != nil {
		return nil, err
	}
	live, err := certpem.AssemblePEM(chainPEM, keyPEM)
	if err != nil {
		return nil, invalid("%v", err)
	}
	info, err := certpem.ParseInfo(chainPEM, now)
	if err != nil {
		return nil, err
	}
	warning := certpem.ChainWarning(ordered)
	if s.Installer == nil {
		return nil, errors.New("no installer: the certificate cannot be installed on this system")
	}
	var lines []string
	restarted, err := s.Installer.Install(ctx, live, priv.CertModeManual, func(l string) { lines = append(lines, l) })
	if err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}
	// the state: the mode, the key (for a later request with the same key), the chain, and
	// the request is fulfilled
	s.mu.Lock()
	n := s.settings
	n.Mode = ModeManual
	if werr := s.st.writeJSON(fileSettings, n); werr != nil {
		s.mu.Unlock()
		return nil, werr
	}
	s.settings = n
	s.mu.Unlock()
	st := s.tls()
	if err := st.write(fileTLSKey, keyPEM); err != nil {
		return nil, err
	}
	if err := st.write(fileTLSCert, chainPEM); err != nil {
		return nil, err
	}
	if p := s.pending(); p != nil && p.Fingerprint == certpem.DescribeKey(key).Fingerprint {
		_ = os.Remove(st.path(fileTLSCSR))
		_ = os.Remove(st.path(fileTLSMeta))
	}
	if restarted == nil {
		restarted = []string{}
	}
	s.log().Info("certificate: installed by hand", "subject", info.Subject, "issuer", info.Issuer, "not_after", info.NotAfter.Format("2006-01-02"), "key_from", from, "restarted", strings.Join(restarted, ","), "lines", strings.Join(lines, "; "))
	if warning != "" {
		s.log().Warn("certificate: " + warning)
	}
	return &ManualResult{Certificate: info, RestartedAddons: restarted, Warning: warning, KeyFrom: from}, nil
}

// short is the first six bytes of a colon fingerprint, for a message.
func short(fp string) string {
	parts := strings.Split(fp, ":")
	if len(parts) > 6 {
		return strings.Join(parts[:6], ":") + "…"
	}
	return fp
}
