package trust

// openccu-lite task 231 (the maintainer: "central truststore management page ... the system-wide
// truststore and the occulited-truststore both in the ui. both should be editable"): four named
// trust stores behind one API, so that a fifth is a new entry and not new code paths.
//
//   - StoreSystem: the image's ca-certificates bundle as the box builds it into /etc/ssl/certs at
//     boot (the fork's lite-ca-certificates), used by addons and every other program. An
//     administrator adds a CA as a file under /usr/local/share/ca-certificates and distrusts one of
//     the image's with a "!<name>" line in /usr/local/etc/ca-certificates.conf; both live on the
//     userfs and are re-applied at every boot and after an image update. Writes go through Sys
//     (the privilege helper) and end with the rebuild script.
//   - StoreOcculited: occulited's own base set - only what its outbound calls need, the CAs GitHub
//     and eQ-3 present today (BaseNames, read from the image's bundle, so the image alone updates
//     it) - plus the administrator's anchors with the purpose "occulited", minus the base
//     certificates the administrator removed (remembered in the anchors file).
//   - PurposeOIDC (task 230) and PurposeACME: the anchors with that purpose. Their pools are the
//     system's plus the anchors (OIDC) and occulited's plus the anchors (ACME).

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The store ids. PurposeOIDC is in trust.go.
const (
	StoreSystem    = "system"
	StoreOcculited = "occulited"
	PurposeACME    = "acme"
)

// StoreIDs is the order the page shows the stores in.
var StoreIDs = []string{StoreSystem, StoreOcculited, PurposeOIDC, PurposeACME}

// BaseNames is occulited's base set: the roots the servers occulited itself talks to chain to
// (read 2026-09-25 with openssl s_client): github.com, api.github.com and codeload.github.com -
// Sectigo under USERTrust ECC; ccu3-update.homematic.com and update.homematic.com (eQ-3's device
// firmware) - Sectigo under USERTrust RSA; raw.githubusercontent.com and the release assets on
// objects.githubusercontent.com - Let's Encrypt under ISRG Root X1; and Let's Encrypt's ACME
// directory itself - ISRG Root X2, cross-signed by X1. The files are the image's (buildroot's
// ca-certificates, Mozilla's names), so the image alone updates the certificates; a name the image
// lacks is logged once and skipped. When one of those servers moves to another CA the call fails
// (strict, by decision) and the Status page's warning offers the CA from the system store.
var BaseNames = []string{
	"mozilla/USERTrust_ECC_Certification_Authority.crt",
	"mozilla/USERTrust_RSA_Certification_Authority.crt",
	"mozilla/Sectigo_Public_Server_Authentication_Root_E46.crt",
	"mozilla/Sectigo_Public_Server_Authentication_Root_R46.crt",
	"mozilla/ISRG_Root_X1.crt",
	"mozilla/ISRG_Root_X2.crt",
}

// Paths are the system store's files. DefaultPaths gives the box's under root.
type Paths struct {
	// Bundle is the bundle every program reads, /etc/ssl/certs/ca-certificates.crt.
	Bundle string
	// ImageDir holds the image's certificates, /usr/share/ca-certificates (Debian's layout,
	// mozilla/<Name>.crt).
	ImageDir string
	// LocalDir holds the administrator's additions, /usr/local/share/ca-certificates/*.crt: what
	// update-ca-certificates adds to the bundle after the image's.
	LocalDir string
	// Distrust is the userfs file whose "!<relative name>" lines deselect image certificates,
	// /usr/local/etc/ca-certificates.conf (the fork's lite-ca-certificates reads it).
	Distrust string
	// Rebuild is the script that builds the bundle from the three above, run as root after a
	// change: /usr/libexec/occu/lite-ca-certificates.
	Rebuild string
}

// DefaultPaths are the box's paths under root ("/" on the box, a test's tree elsewhere).
func DefaultPaths(root string) Paths {
	j := func(p string) string { return filepath.Join(root, p) }
	return Paths{Bundle: j("/etc/ssl/certs/ca-certificates.crt"), ImageDir: j("/usr/share/ca-certificates"), LocalDir: j("/usr/local/share/ca-certificates"),
		Distrust: j("/usr/local/etc/ca-certificates.conf"), Rebuild: j("/usr/libexec/occu/lite-ca-certificates")}
}

// LocalPrefix names the files occulited writes into Paths.LocalDir: occulite-<id>.crt. A file of
// another name there was put by hand and is listed as added, but as the administrator's own.
const LocalPrefix = "occulite-"

// RunResult is what SystemWriter.Run answers.
type RunResult struct {
	Exit   int
	Output string
}

// SystemWriter writes the system store's files and runs its rebuild as root - the privilege
// helper on the box, a local implementation in the tests.
type SystemWriter interface {
	WriteFile(path string, data []byte, mode os.FileMode) error
	Remove(path string) error
	MkdirAll(path string, mode os.FileMode) error
	Run(ctx context.Context, name string, args []string) (RunResult, error)
}

var (
	ErrStore    = errors.New("no such trust store")
	ErrReadOnly = errors.New("the system trust store cannot be changed on this system")
	ErrSame     = errors.New("a certificate is copied into another store")
	ErrPresent  = errors.New("the store holds this certificate already")
	ErrNoRemove = errors.New("this certificate cannot be removed here")
)

// Failure is a TLS handshake one of occulited's own clients lost to a CA its store does not hold
// (strict, by decision): the host, the CA the server chains to, the chain it presented, and the
// system store's certificate that would verify it, when the bundle has one.
type Failure struct {
	Host  string    `json:"host"`
	Store string    `json:"store"`
	At    time.Time `json:"at"`
	Error string    `json:"error"`
	// Issuer is the subject of the CA the presented chain ends in - the one missing.
	Issuer string `json:"issuer"`
	Chain  []Info `json:"chain,omitempty"`
	// Candidate is the system store's certificate that verifies the chain, if any: the one-click
	// copy the warning offers.
	Candidate *Info `json:"candidate,omitempty"`
}

// StoreView is one store as GET /trust answers it.
type StoreView struct {
	ID string `json:"id"`
	// Editable: additions and removals work (the system store needs the helper and the rebuild)
	Editable     bool   `json:"editable"`
	Certificates []Info `json:"certificates"`
}

// TrustView is GET /trust's answer.
type TrustView struct {
	Stores  []StoreView `json:"stores"`
	Pending []Failure   `json:"pending"`
}

func (s *Store) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// OnChange registers fn to run after every change of any store's content (a pool that must be
// rebuilt, the OIDC client's roots).
func (s *Store) OnChange(fn func()) {
	s.onChangeMu.Lock()
	defer s.onChangeMu.Unlock()
	s.onChange = append(s.onChange, fn)
}

// Version counts the changes; a pool built for a version is stale once it moves.
func (s *Store) Version() uint64 { return s.version.Load() }

func (s *Store) changed() {
	s.version.Add(1)
	s.onChangeMu.Lock()
	fns := slices.Clone(s.onChange)
	s.onChangeMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// ParseAny is Parse for PEM text and for DER (a .der/.cer upload): one certificate or several
// concatenated. A DER private key is refused like a PEM one.
//
// DER is binary: its last byte may be 0x20 or 0x0a like any other, so it is never trimmed (B-237:
// a TrimSpace cut such a byte and refused about one certificate in forty as malformed). Text around
// it is forgiven by its own lengths: leading whitespace, which no DER starts with, and whatever
// whitespace follows the last complete element a tool may have appended.
func ParseAny(b []byte) ([]*x509.Certificate, error) {
	if bytes.Contains(b, []byte("-----BEGIN")) {
		return Parse(b)
	}
	der := bytes.TrimLeft(b, derSpace)
	if len(der) == 0 || der[0] != 0x30 {
		return Parse(b)
	}
	return parseDER(derElements(der))
}

const derSpace = " \t\r\n\v\f"

// derElements is der up to the end of its last complete ASN.1 element when only whitespace
// follows it; anything else is der as it is, for the parser to judge.
func derElements(der []byte) []byte {
	rest := der
	for len(rest) > 0 && len(bytes.TrimLeft(rest, derSpace)) > 0 {
		var v asn1.RawValue
		next, err := asn1.Unmarshal(rest, &v)
		if err != nil {
			return der
		}
		rest = next
	}
	return der[:len(der)-len(rest)]
}

// parseDER parses DER bytes exactly as given: certificates, or ErrPrivateKey for a key.
func parseDER(der []byte) ([]*x509.Certificate, error) {
	if _, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return nil, ErrPrivateKey
	}
	if _, err := x509.ParseECPrivateKey(der); err == nil {
		return nil, ErrPrivateKey
	}
	if _, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return nil, ErrPrivateKey
	}
	certs, err := x509.ParseCertificates(der)
	if err != nil {
		return nil, fmt.Errorf("a certificate does not parse: %w", err)
	}
	if len(certs) == 0 {
		return nil, ErrNoCertificate
	}
	return certs, nil
}

func validStore(id string) bool { return slices.Contains(StoreIDs, id) }

// entry is one certificate of a store with its PEM, for copies.
type entry struct {
	Info
	cert *x509.Certificate
	// file is an image certificate's name relative to ImageDir, or a local file's path
	file string
}

// --- the system store ----------------------------------------------------------------------------

// hasBundle: the box's bundle exists (a development root without one uses Go's own roots).
func (s *Store) hasBundle() bool {
	if s.Paths.Bundle == "" {
		return false
	}
	_, err := os.Stat(s.Paths.Bundle)
	return err == nil
}

type bundleCache struct {
	mod   time.Time
	size  int64
	certs []*x509.Certificate
	pool  *x509.CertPool
}

// bundleCerts reads the bundle once per change of its file.
func (s *Store) bundleCerts() ([]*x509.Certificate, *x509.CertPool) {
	s.bundleMu.Lock()
	defer s.bundleMu.Unlock()
	st, err := os.Stat(s.Paths.Bundle)
	if err != nil {
		return nil, nil
	}
	if s.bundle != nil && s.bundle.mod.Equal(st.ModTime()) && s.bundle.size == st.Size() {
		return s.bundle.certs, s.bundle.pool
	}
	b, err := os.ReadFile(s.Paths.Bundle)
	if err != nil {
		return nil, nil
	}
	certs, _ := Parse(b)
	pool := x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c)
	}
	s.bundle = &bundleCache{mod: st.ModTime(), size: st.Size(), certs: certs, pool: pool}
	return certs, pool
}

// systemPool is the box's bundle as a pool - read from the file, not x509.SystemCertPool, which Go
// loads once per process and would miss every change made here - or Go's own roots without one.
// Never nil.
func (s *Store) systemPool() *x509.CertPool {
	if s.hasBundle() {
		if _, pool := s.bundleCerts(); pool != nil {
			return pool.Clone()
		}
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		return x509.NewCertPool()
	}
	return pool
}

// imageFiles lists the image's certificate files relative to ImageDir, sorted.
func (s *Store) imageFiles() []string {
	var out []string
	_ = filepath.WalkDir(s.Paths.ImageDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".crt") {
			return nil
		}
		rel, err := filepath.Rel(s.Paths.ImageDir, p)
		if err == nil {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// readCert reads one certificate file (the first certificate of it).
func readCert(path string) *x509.Certificate {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	cs, err := Parse(b)
	if err != nil || len(cs) == 0 {
		return nil
	}
	return cs[0]
}

// distrusted reads the "!<name>" lines of the distrust file.
func (s *Store) distrusted() []string {
	b, err := os.ReadFile(s.Paths.Distrust)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "!") {
			out = append(out, strings.TrimSpace(line[1:]))
		}
	}
	return out
}

// systemEntries lists the system store: the bundle's certificates, each with its file (an image
// one, or the administrator's under LocalDir), and the image certificates deselected in the
// distrust file, marked Distrusted.
func (s *Store) systemEntries() []entry {
	now := s.now()
	certs, _ := s.bundleCerts()
	image := map[string]string{} // id -> relative name
	for _, rel := range s.imageFiles() {
		if c := readCert(filepath.Join(s.Paths.ImageDir, rel)); c != nil {
			image[idOf(c)] = rel
		}
	}
	local := map[string]string{} // id -> path
	if des, err := os.ReadDir(s.Paths.LocalDir); err == nil {
		for _, d := range des {
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".crt") {
				continue
			}
			p := filepath.Join(s.Paths.LocalDir, d.Name())
			if c := readCert(p); c != nil {
				local[idOf(c)] = p
			}
		}
	}
	var out []entry
	seen := map[string]bool{}
	for _, c := range certs {
		id := idOf(c)
		if seen[id] {
			continue
		}
		seen[id] = true
		e := entry{Info: Describe(c, now), cert: c}
		if rel, ok := image[id]; ok {
			e.Source, e.file, e.Removable = SourceImage, rel, true
		} else if p, ok := local[id]; ok {
			e.Source, e.file, e.Removable = SourceAdded, p, true
			if strings.HasPrefix(filepath.Base(p), LocalPrefix) {
				e.Origin = OriginPage
			}
			if st, err := os.Stat(p); err == nil {
				e.Added = st.ModTime().UTC()
			}
		} else {
			// in the bundle but in neither directory: the prebuilt bundle of an image whose
			// certificate directory differs, or a bundle built by hand - shown, not removable
			e.Source = SourceImage
		}
		out = append(out, e)
	}
	for _, rel := range s.distrusted() {
		c := readCert(filepath.Join(s.Paths.ImageDir, rel))
		if c == nil || seen[idOf(c)] {
			continue
		}
		seen[idOf(c)] = true
		e := entry{Info: Describe(c, now), cert: c, file: rel}
		e.Source, e.Distrusted, e.Removable = SourceImage, true, false
		out = append(out, e)
	}
	return out
}

// rebuildSystem writes nothing itself: it runs the fork's script, which builds the bundle from
// the image's certificates, the userfs additions and the distrust file.
func (s *Store) rebuildSystem(ctx context.Context) error {
	if s.Sys == nil {
		return ErrReadOnly
	}
	res, err := s.Sys.Run(ctx, s.Paths.Rebuild, nil)
	if err != nil {
		return fmt.Errorf("the CA bundle could not be rebuilt: %w", err)
	}
	if res.Exit != 0 {
		return fmt.Errorf("the CA bundle could not be rebuilt (exit %d): %s", res.Exit, strings.TrimSpace(res.Output))
	}
	return nil
}

func (s *Store) writeDistrust(names []string) error {
	if len(names) == 0 {
		return s.Sys.Remove(s.Paths.Distrust)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# openccu-lite: certificates of the image's CA bundle this system does not trust, one\n# \"!<name>\" line each (written by the Trust stores page; the bundle is rebuilt at boot).\n")
	for _, n := range names {
		b.WriteString("!" + n + "\n")
	}
	// the file's directory is /usr/local/etc, which exists on every box and is not the helper's
	// to create; only a bare test tree lacks it
	if _, err := os.Stat(filepath.Dir(s.Paths.Distrust)); err != nil {
		if err := s.Sys.MkdirAll(filepath.Dir(s.Paths.Distrust), 0o755); err != nil {
			return err
		}
	}
	return s.Sys.WriteFile(s.Paths.Distrust, []byte(b.String()), 0o644)
}

func (s *Store) addSystem(ctx context.Context, certs []*x509.Certificate) ([]Info, error) {
	if s.Sys == nil {
		return nil, ErrReadOnly
	}
	have := map[string]entry{}
	for _, e := range s.systemEntries() {
		have[e.ID] = e
	}
	var written []string
	var undistrust []string
	for _, c := range certs {
		e, ok := have[idOf(c)]
		if ok && !e.Distrusted {
			continue
		}
		if ok && e.Distrusted {
			undistrust = append(undistrust, e.file)
			continue
		}
		written = append(written, idOf(c))
		if err := s.Sys.MkdirAll(s.Paths.LocalDir, 0o755); err != nil {
			return nil, err
		}
		if err := s.Sys.WriteFile(filepath.Join(s.Paths.LocalDir, LocalPrefix+idOf(c)+".crt"), []byte(encode(c)), 0o644); err != nil {
			return nil, err
		}
	}
	if len(undistrust) > 0 {
		keep := slices.DeleteFunc(s.distrusted(), func(n string) bool { return slices.Contains(undistrust, n) })
		if err := s.writeDistrust(keep); err != nil {
			return nil, err
		}
	}
	if len(written) == 0 && len(undistrust) == 0 {
		return nil, ErrPresent
	}
	if err := s.rebuildSystem(ctx); err != nil {
		return nil, err
	}
	s.changed()
	var out []Info
	for _, e := range s.systemEntries() {
		if slices.Contains(written, e.ID) || slices.Contains(undistrust, e.file) {
			out = append(out, e.Info)
		}
	}
	return out, nil
}

func (s *Store) removeSystem(ctx context.Context, id string) error {
	if s.Sys == nil {
		return ErrReadOnly
	}
	for _, e := range s.systemEntries() {
		if e.ID != id {
			continue
		}
		switch {
		case e.Distrusted:
			return ErrNoRemove
		case e.Source == SourceAdded:
			if err := s.Sys.Remove(e.file); err != nil {
				return err
			}
		case e.file != "":
			names := s.distrusted()
			if !slices.Contains(names, e.file) {
				names = append(names, e.file)
			}
			if err := s.writeDistrust(names); err != nil {
				return err
			}
		default:
			return ErrNoRemove
		}
		if err := s.rebuildSystem(ctx); err != nil {
			return err
		}
		s.changed()
		return nil
	}
	return ErrUnknown
}

// --- occulited's store ---------------------------------------------------------------------------

// baseEntries reads occulited's base set from the image; a missing file is logged once per process.
func (s *Store) baseEntries(removed []string) []entry {
	now := s.now()
	var out []entry
	for _, name := range s.Base {
		if s.Paths.ImageDir == "" {
			break
		}
		c := readCert(filepath.Join(s.Paths.ImageDir, name))
		if c == nil {
			s.warnMissing(name)
			continue
		}
		e := entry{Info: Describe(c, now), cert: c, file: name}
		e.Source = SourceImage
		e.Distrusted = slices.Contains(removed, e.ID)
		e.Removable = !e.Distrusted
		out = append(out, e)
	}
	return out
}

var missingOnce sync.Map

func (s *Store) warnMissing(name string) {
	if _, loaded := missingOnce.LoadOrStore(name, true); !loaded {
		s.log().Warn("trust: a certificate of occulited's base set is not in the image's bundle", "file", name)
	}
}

// occulitedEntries: the base set (removed ones marked), then the anchors with the purpose.
func (s *Store) occulitedEntries(f file) []entry {
	out := s.baseEntries(f.Removed[StoreOcculited])
	seen := map[string]bool{}
	for _, e := range out {
		seen[e.ID] = true
	}
	now := s.now()
	for _, a := range f.Anchors {
		if !slices.Contains(a.Purposes, StoreOcculited) || seen[a.ID] {
			continue
		}
		cs, err := Parse([]byte(a.PEM))
		if err != nil || len(cs) == 0 {
			continue
		}
		out = append(out, entry{Info: anchorInfo(a, cs[0], now), cert: cs[0]})
	}
	return out
}

func (s *Store) anchorEntries(f file, purpose string) []entry {
	now := s.now()
	var out []entry
	for _, a := range f.Anchors {
		if !slices.Contains(a.Purposes, purpose) {
			continue
		}
		cs, err := Parse([]byte(a.PEM))
		if err != nil || len(cs) == 0 {
			continue
		}
		out = append(out, entry{Info: anchorInfo(a, cs[0], now), cert: cs[0]})
	}
	return out
}

// entries lists one store; the caller holds mu for the anchors file.
func (s *Store) entries(f file, store string) ([]entry, error) {
	switch store {
	case StoreSystem:
		return s.systemEntries(), nil
	case StoreOcculited:
		return s.occulitedEntries(f), nil
	case PurposeOIDC, PurposeACME:
		return s.anchorEntries(f, store), nil
	}
	return nil, ErrStore
}

// --- the API's operations ------------------------------------------------------------------------

// ListStore lists one store's certificates.
func (s *Store) ListStore(store string) ([]Info, error) {
	if !validStore(store) {
		return nil, ErrStore
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	es, err := s.entries(f, store)
	if err != nil {
		return nil, err
	}
	out := make([]Info, 0, len(es))
	for _, e := range es {
		out = append(out, e.Info)
	}
	return out, nil
}

// View is GET /trust: every store and the pending failures.
func (s *Store) View() (TrustView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return TrustView{}, err
	}
	v := TrustView{Stores: []StoreView{}, Pending: []Failure{}}
	for _, id := range StoreIDs {
		es, _ := s.entries(f, id)
		sv := StoreView{ID: id, Editable: id != StoreSystem || (s.Sys != nil && s.hasBundle()), Certificates: []Info{}}
		for _, e := range es {
			sv.Certificates = append(sv.Certificates, e.Info)
		}
		v.Stores = append(v.Stores, sv)
	}
	v.Pending = append(v.Pending, f.Failures...)
	return v, nil
}

// AddTo adds the certificates of text (PEM or DER) to a store. by and origin are recorded on
// anchors; the system store's files carry neither. A certificate the store holds already is not
// an error for the anchors (it gains the purpose) and ErrPresent for the system store when nothing
// at all was new. A failure whose candidate was just added is cleared.
func (s *Store) AddTo(ctx context.Context, store string, text []byte, by, origin string) ([]Info, error) {
	if !validStore(store) {
		return nil, ErrStore
	}
	var added []Info
	var err error
	switch store {
	case StoreSystem:
		var certs []*x509.Certificate
		certs, err = ParseAny(text)
		if err != nil {
			return nil, err
		}
		if err = s.checkValidity(certs); err != nil {
			return nil, err
		}
		added, err = s.addSystem(ctx, certs)
	case StoreOcculited:
		added, err = s.addOcculited(text, by, origin)
	default:
		added, err = s.addAnchors(text, store, by, origin)
	}
	if err != nil {
		return nil, err
	}
	s.clearFailuresFor(store, added)
	return added, nil
}

func (s *Store) checkValidity(certs []*x509.Certificate) error {
	now := s.now()
	for _, c := range certs {
		if now.After(c.NotAfter) {
			return fmt.Errorf("%w: %s (until %s)", ErrExpired, c.Subject, c.NotAfter.Format(time.DateOnly))
		}
		if now.Before(c.NotBefore) {
			return fmt.Errorf("%w: %s (from %s)", ErrNotYetValid, c.Subject, c.NotBefore.Format(time.DateOnly))
		}
	}
	return nil
}

// addOcculited: a removed base certificate comes back; anything else is an anchor.
func (s *Store) addOcculited(text []byte, by, origin string) ([]Info, error) {
	certs, err := ParseAny(text)
	if err != nil {
		return nil, err
	}
	if err := s.checkValidity(certs); err != nil {
		return nil, err
	}
	s.mu.Lock()
	f, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	base := s.baseEntries(nil)
	var restored []Info
	var rest []*x509.Certificate
	changed := false
	for _, c := range certs {
		k := slices.IndexFunc(base, func(e entry) bool { return e.ID == idOf(c) })
		if k < 0 {
			rest = append(rest, c)
			continue
		}
		if slices.Contains(f.Removed[StoreOcculited], idOf(c)) {
			f.Removed[StoreOcculited] = slices.DeleteFunc(f.Removed[StoreOcculited], func(id string) bool { return id == idOf(c) })
			changed = true
		}
		restored = append(restored, base[k].Info)
	}
	if changed {
		if err := s.save(f); err != nil {
			s.mu.Unlock()
			return nil, err
		}
	}
	s.mu.Unlock()
	if changed {
		s.changed()
	}
	if len(rest) == 0 {
		return restored, nil
	}
	var pem bytes.Buffer
	for _, c := range rest {
		pem.WriteString(encode(c))
	}
	added, err := s.addAnchors(pem.Bytes(), StoreOcculited, by, origin)
	if err != nil {
		return nil, err
	}
	return append(restored, added...), nil
}

// RemoveFrom removes one certificate from a store: a system or base certificate is distrusted (and
// stays listed, so it can be restored), an added one goes.
func (s *Store) RemoveFrom(ctx context.Context, store, id string) error {
	if !validStore(store) {
		return ErrStore
	}
	switch store {
	case StoreSystem:
		return s.removeSystem(ctx, id)
	case StoreOcculited:
		s.mu.Lock()
		f, err := s.load()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if k := slices.IndexFunc(s.baseEntries(f.Removed[StoreOcculited]), func(e entry) bool { return e.ID == id }); k >= 0 {
			if f.Removed == nil {
				f.Removed = map[string][]string{}
			}
			if slices.Contains(f.Removed[StoreOcculited], id) {
				s.mu.Unlock()
				return ErrNoRemove
			}
			f.Removed[StoreOcculited] = append(f.Removed[StoreOcculited], id)
			err = s.save(f)
			s.mu.Unlock()
			if err == nil {
				s.changed()
			}
			return err
		}
		s.mu.Unlock()
		return s.Remove(id, StoreOcculited)
	default:
		return s.Remove(id, store)
	}
}

// Restore trusts a removed base certificate again: the system store's distrust line goes, or
// occulited's removal is forgotten.
func (s *Store) Restore(ctx context.Context, store, id string) error {
	switch store {
	case StoreSystem:
		if s.Sys == nil {
			return ErrReadOnly
		}
		for _, e := range s.systemEntries() {
			if e.ID == id && e.Distrusted {
				keep := slices.DeleteFunc(s.distrusted(), func(n string) bool { return n == e.file })
				if err := s.writeDistrust(keep); err != nil {
					return err
				}
				if err := s.rebuildSystem(ctx); err != nil {
					return err
				}
				s.changed()
				return nil
			}
		}
		return ErrUnknown
	case StoreOcculited:
		s.mu.Lock()
		f, err := s.load()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if !slices.Contains(f.Removed[StoreOcculited], id) {
			s.mu.Unlock()
			return ErrUnknown
		}
		f.Removed[StoreOcculited] = slices.DeleteFunc(f.Removed[StoreOcculited], func(x string) bool { return x == id })
		err = s.save(f)
		s.mu.Unlock()
		if err == nil {
			s.changed()
		}
		return err
	}
	if !validStore(store) {
		return ErrStore
	}
	return ErrUnknown
}

// Copy puts one certificate of store from into store to - a copy, the stores independent
// afterwards (the maintainer: "a copy", not a link).
func (s *Store) Copy(ctx context.Context, from, to, id, by string) ([]Info, error) {
	if !validStore(from) || !validStore(to) {
		return nil, ErrStore
	}
	if from == to {
		return nil, ErrSame
	}
	s.mu.Lock()
	f, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	es, err := s.entries(f, from)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	k := slices.IndexFunc(es, func(e entry) bool { return e.ID == id })
	if k < 0 {
		return nil, ErrUnknown
	}
	return s.AddTo(ctx, to, []byte(encode(es[k].cert)), by, OriginCopy)
}

// PEMOf answers one certificate of a store as PEM (a download).
func (s *Store) PEMOf(store, id string) (string, error) {
	if !validStore(store) {
		return "", ErrStore
	}
	s.mu.Lock()
	f, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return "", err
	}
	es, err := s.entries(f, store)
	s.mu.Unlock()
	if err != nil {
		return "", err
	}
	k := slices.IndexFunc(es, func(e entry) bool { return e.ID == id })
	if k < 0 {
		return "", ErrUnknown
	}
	return encode(es[k].cert), nil
}

// PoolFor is a store's pool as its consumers use it: the system store's bundle; occulited's base
// set and anchors; OIDC = the system's plus its anchors (task 230); ACME = occulited's plus its
// anchors. Never nil.
func (s *Store) PoolFor(store string) (*x509.CertPool, error) {
	switch store {
	case StoreSystem:
		return s.systemPool(), nil
	case PurposeOIDC:
		pool, err := s.Pool(PurposeOIDC)
		if pool == nil && err == nil {
			pool = s.systemPool()
		}
		return pool, err
	case StoreOcculited, PurposeACME:
		s.mu.Lock()
		f, err := s.load()
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		pool := x509.NewCertPool()
		for _, e := range s.occulitedEntries(f) {
			if !e.Distrusted {
				pool.AddCert(e.cert)
			}
		}
		if store == PurposeACME {
			for _, e := range s.anchorEntries(f, PurposeACME) {
				pool.AddCert(e.cert)
			}
		}
		return pool, nil
	}
	return nil, ErrStore
}

// --- the strict failures -------------------------------------------------------------------------

// Failures lists the pending failures.
func (s *Store) Failures() []Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil
	}
	return slices.Clone(f.Failures)
}

// recordFailure keeps one failure per host and store, and looks the missing CA up in the system
// store for the one-click copy.
func (s *Store) recordFailure(store, host string, chain []*x509.Certificate, err error) {
	now := s.now()
	fl := Failure{Host: host, Store: store, At: now.UTC(), Error: err.Error()}
	for _, c := range chain {
		fl.Chain = append(fl.Chain, Describe(c, now))
	}
	if len(chain) > 0 {
		last := chain[len(chain)-1]
		fl.Issuer = last.Issuer.String()
		if bytes.Equal(last.RawIssuer, last.RawSubject) {
			fl.Issuer = last.Subject.String()
		}
		inter := x509.NewCertPool()
		for _, c := range chain[1:] {
			inter.AddCert(c)
		}
		if chains, verr := chain[0].Verify(x509.VerifyOptions{Roots: s.systemPool(), Intermediates: inter, DNSName: host}); verr == nil && len(chains) > 0 && len(chains[0]) > 0 {
			root := chains[0][len(chains[0])-1]
			info := Describe(root, now)
			for _, e := range s.systemEntries() {
				if e.ID == info.ID {
					info = e.Info
				}
			}
			fl.Candidate = &info
			fl.Issuer = root.Subject.String()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, lerr := s.load()
	if lerr != nil {
		return
	}
	k := slices.IndexFunc(f.Failures, func(x Failure) bool { return x.Host == host && x.Store == store })
	if k >= 0 {
		f.Failures[k] = fl
	} else {
		f.Failures = append(f.Failures, fl)
		s.log().Warn("trust: a server presents a certificate its store does not trust", "store", store, "host", host, "issuer", fl.Issuer, "in_system_store", fl.Candidate != nil)
	}
	_ = s.save(f)
}

// clearFailure forgets a host's failure once it answers.
func (s *Store) clearFailure(store, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return
	}
	n := len(f.Failures)
	f.Failures = slices.DeleteFunc(f.Failures, func(x Failure) bool { return x.Host == host && x.Store == store })
	if len(f.Failures) != n {
		_ = s.save(f)
	}
}

// clearFailuresFor forgets the failures whose candidate was just added to their store.
func (s *Store) clearFailuresFor(store string, added []Info) {
	if len(added) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return
	}
	n := len(f.Failures)
	f.Failures = slices.DeleteFunc(f.Failures, func(x Failure) bool {
		return x.Store == store && x.Candidate != nil && slices.ContainsFunc(added, func(i Info) bool { return i.ID == x.Candidate.ID })
	})
	if len(f.Failures) != n {
		_ = s.save(f)
	}
}
