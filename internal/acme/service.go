package acme

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/journald"
	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/runlog"
)

// Installer is the box side of the service (system.CertInstaller): the live file through the
// privilege helper, lighttpd's reload, the certs-group addons' restart.
type Installer interface {
	// Install writes the live file with the marker naming mode (acme or manual) and reloads
	// what reads it; the ids of the restarted addons come back for the attempt's record.
	Install(ctx context.Context, livePEM []byte, mode string, log func(string)) (restarted []string, err error)
	// Remove deletes the live file and reloads lighttpd, whose init script regenerates a
	// self-signed certificate when the file is missing; the addons are restarted here too.
	Remove(ctx context.Context, log func(string)) (restarted []string, err error)
	// ReadLive returns the certificate blocks of the live file (never its key) and the line of
	// the marker beside it - "<mode> <issuer>", written when occulited installed the file,
	// empty when the certificate is not occulited's (S50lighttpd's own, or one restored by
	// hand).
	ReadLive() (pem []byte, marker string, err error)
}

// The renewal rule (D-48): twice a day, renew when fewer than 30 days remain - or when less than
// half the certificate's lifetime is left, which is the rule that matters for a LAN CA: step-ca
// issues for 24 hours by default, and a 30-day threshold would call that due at every check
// anyway, while the half-lifetime rule renews a 24-hour certificate every check and a 7-day one
// every third day. S50lighttpd's check_certificate never touches an ACME-managed file (the
// marker the helper writes beside it), so the two never fight; a certificate this service fails
// to renew simply expires, and the warning on the Status page says so from 14 days out. A
// lifetime under ShortLifetime is warned about at issue: a certificate that lives less than
// twelve hours can lapse between two checks.
const (
	RenewBelow    = 30 * 24 * time.Hour
	ShortLifetime = 48 * time.Hour
	CheckInterval = 12 * time.Hour
	firstCheck    = 2 * time.Minute
	runTimeout    = 40 * time.Minute // netcup's propagation alone can be a quarter of an hour
)

// The kinds of attempt.
const (
	KindTest  = "test"
	KindIssue = "issue"
	KindRenew = "renew"
)

// Attempt is one run of the flow, in flight or finished - what the page shows as the last
// attempt. Its lines are in the journal (task 102, D-59): every entry carries OCCULITE_RUN=acme
// and OCCULITE_RUN_ID=RunID, and the page reads them through GET /log?run=.
type Attempt struct {
	Kind      string     `json:"kind"`
	Started   time.Time  `json:"started"`
	Finished  *time.Time `json:"finished,omitempty"`
	OK        bool       `json:"ok"`
	Error     string     `json:"error,omitempty"`
	Directory string     `json:"directory"`
	Names     []string   `json:"names"`
	// RunID finds the attempt's lines in the journal.
	RunID string `json:"run_id,omitempty"`
	// run writes the lines; set while the attempt runs.
	run *runlog.Run
	// Installed: the result went into the live file (never for a test).
	Installed bool `json:"installed"`
	// RestartedAddons: the confined addons of the certs group restarted after the install.
	RestartedAddons []string `json:"restarted_addons,omitempty"`
	// Certificate describes what was obtained, for a test as well.
	Certificate *certpem.Info `json:"certificate,omitempty"`
	// Warning is set when the run succeeded but something deserves the page's attention: a
	// lifetime under ShortLifetime.
	Warning string `json:"warning,omitempty"`
}

// Status is GET /certificate.
type Status struct {
	Settings View `json:"settings"`
	// Current is the live file's leaf, nil when there is none or it cannot be read (Error says why).
	Current      *certpem.Info `json:"current"`
	CurrentError string        `json:"current_error,omitempty"`
	// Managed: the marker beside the live file says occulited installed it (S50lighttpd leaves
	// it alone); ManagedMode is the mode it names, acme or manual; ManagedBy the issuer.
	Managed     bool   `json:"managed"`
	ManagedMode string `json:"managed_mode,omitempty"`
	ManagedBy   string `json:"managed_by,omitempty"`
	// Pending is the key + CSR made on the box that waits for its certificate (task 38).
	Pending *PendingKey `json:"pending"`
	// Issued is the certificate this service last obtained and installed; LiveIsIssued says
	// whether the live file is that one.
	Issued       *certpem.Info `json:"issued"`
	LiveIsIssued bool          `json:"live_is_issued"`
	Last         *Attempt      `json:"last"`
	Running      *Attempt      `json:"running"`
	// RenewBelowDays and NextCheck describe the timer.
	RenewBelowDays int        `json:"renew_below_days"`
	NextCheck      *time.Time `json:"next_check,omitempty"`
	// Warning is set when the page and the Status card should say something: an ACME box whose
	// certificate is short of days, or whose last renewal failed.
	Warning   string     `json:"warning,omitempty"`
	Providers []Provider `json:"providers"`
	// SuggestedNames are the default names - the box's FQDN, then its host name - which the page
	// proposes while none are stored and an ACME save without names takes; empty when unknown.
	SuggestedNames []string `json:"suggested_names"`
}

// ErrBusy: one run at a time.
var ErrBusy = errors.New("a certificate operation is already running")

// Service is the state machine around issue, renew and the switch back.
type Service struct {
	Issuer    Issuer
	Installer Installer
	HTTP01    *TokenStore
	Log       *slog.Logger
	// Journal takes the attempts' lines (task 102); nil = they go to Log with their run id.
	Journal runlog.Sender
	Now     func() time.Time
	// UserAgent is what the CA sees.
	UserAgent string
	// TLSDir is the manual mode's state, <state>/tls; empty = the sibling of the ACME directory.
	TLSDir string
	// Suggest returns the default names (DefaultNames of the box's host name and domain); nil =
	// none are proposed.
	Suggest func() []string
	// HostDomain returns the box's host name and DNS domain; every renewal check hands them to
	// FollowDomain first, so a domain the DHCP lease changed is seen without a browser. nil = not.
	HostDomain func() (hostname, domain string)
	// Follow is handed the same host name and domain at every renewal check, after the names
	// followed them: what else keeps to the box's name (the HTTPS redirect's target) follows
	// there too. nil = nothing.
	Follow func(hostname, domain string)
	// HTTP returns the client every directory connection goes through (openccu-lite task 231:
	// the trust store's, on occulited's store plus the ACME anchors; task 232: with the ACME
	// pins); nil = lego's default client on Go's own roots.
	HTTP func() *http.Client

	st       store
	mu       sync.Mutex
	settings Settings
	domain   string // the DNS domain FollowDomain saw last (domain.json); "" = none yet
	last     *Attempt
	running  *Attempt
	next     *time.Time
	wake     chan struct{}
}

// New opens dir (<state>/acme), reading what is there.
func New(dir string, log *slog.Logger) (*Service, error) {
	s := &Service{st: store{dir: dir}, Log: log, Now: time.Now, HTTP01: &TokenStore{}, wake: make(chan struct{}, 1)}
	s.settings = Default()
	if _, err := s.st.readJSON(fileSettings, &s.settings); err != nil {
		return nil, fmt.Errorf("%s: %w", fileSettings, err)
	}
	if s.settings.Names == nil {
		s.settings.Names = []string{}
	}
	// a missing or unreadable domain.json is a first sight: the domain is only remembered
	var ds domainState
	if ok, err := s.st.readJSON(fileDomain, &ds); err == nil && ok {
		s.domain = ds.Domain
	}
	var last Attempt
	if ok, err := s.st.readJSON(fileLast, &last); err == nil && ok {
		s.last = &last
	}
	return s, nil
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Settings is a copy of the stored settings (secrets included; for the service's own use).
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// suggested is what Suggest proposes, never nil.
func (s *Service) suggested() []string {
	if s.Suggest == nil {
		return []string{}
	}
	if names := s.Suggest(); len(names) > 0 {
		return names
	}
	return []string{}
}

// SetSettings applies u over the stored settings, validates and saves. An ACME save without names
// takes the suggested ones, as long as there are some.
func (s *Service) SetSettings(u Update) (Settings, error) {
	if u.Mode == ModeACME && blankNames(u.Names) {
		if names := s.suggested(); len(names) > 0 {
			u.Names = names
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.settings.Apply(u)
	if err != nil {
		return s.settings, err
	}
	if err := s.st.writeJSON(fileSettings, n); err != nil {
		return s.settings, err
	}
	s.settings = n
	s.poke()
	return n, nil
}

// TakeCARoot hands over a CA root stored in the settings before openccu-lite task 231 and forgets
// it: the caller puts it into the ACME trust store, where the Trust stores page lists it. "" when
// there is none.
func (s *Service) TakeCARoot() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pem := s.settings.CARoot
	if pem == "" {
		return ""
	}
	n := s.settings
	n.CARoot = ""
	if err := s.st.writeJSON(fileSettings, n); err != nil {
		return ""
	}
	s.settings = n
	return pem
}

func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Status assembles the answer.
func (s *Service) Status() Status {
	// the suggestion reads the box's files: not under the lock
	suggested := s.suggested()
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	st := Status{Settings: s.settings.View(), RenewBelowDays: int(RenewBelow.Hours() / 24), Providers: Providers, NextCheck: s.next, SuggestedNames: suggested}
	if s.last != nil {
		st.Last = copyAttempt(s.last)
	}
	if s.running != nil {
		st.Running = copyAttempt(s.running)
	}
	if s.Installer != nil {
		if b, marker, err := s.Installer.ReadLive(); err != nil {
			st.CurrentError = err.Error()
		} else if info, err := certpem.ParseInfo(b, now); err != nil {
			st.CurrentError = err.Error()
		} else {
			st.Current = info
			st.Managed = marker != ""
			st.ManagedMode, st.ManagedBy, _ = strings.Cut(marker, " ")
		}
	}
	st.Pending = s.pending()
	st.Issued = s.issuedInfo(now)
	st.LiveIsIssued = st.Current != nil && st.Issued != nil && st.Current.Fingerprint == st.Issued.Fingerprint
	st.Warning = warning(s.settings.Mode, st.Current, st.Last)
	return st
}

// warning is the rule the Status card shows: under ACME, fewer than 14 days left or a failed
// last renewal; under manual, fewer than 14 days left (nobody renews it) or the box back on a
// self-signed certificate.
func warning(mode string, current *certpem.Info, last *Attempt) string {
	if mode != ModeACME && mode != ModeManual {
		return ""
	}
	if mode == ModeACME && last != nil && last.Finished != nil && !last.OK && last.Kind != KindTest {
		return "last-attempt-failed"
	}
	if current != nil && current.DaysLeft < 14 {
		return "expiring"
	}
	if current != nil && current.SelfSigned {
		return "self-signed"
	}
	return ""
}

func (s *Service) issuedInfo(now time.Time) *certpem.Info {
	b, err := s.st.read(fileCert)
	if err != nil {
		return nil
	}
	info, err := certpem.ParseInfo(b, now)
	if err != nil {
		return nil
	}
	return info
}

func copyAttempt(a *Attempt) *Attempt {
	c := *a
	c.Names = append([]string{}, a.Names...)
	return &c
}

// newRun is the journal writer of an attempt's run.
func (s *Service) newRun(a *Attempt, set Settings) *runlog.Run {
	return runlog.New(runlog.KindACME, a.RunID, runlog.Options{
		Sender: s.Journal,
		Log:    s.log(),
		Redact: Redactor(set),
		Fields: []journald.Field{{Key: "OCCULITE_RUN_KIND", Value: a.Kind}, {Key: "OCCULITE_RUN_NAMES", Value: strings.Join(a.Names, ",")}},
	})
}

// MigrateLog carries the lines a last.json from before task 102 kept into the journal, under a
// run id of the attempt's start, and writes the file again without them. A file without lines is
// left alone. Called once the Journal is set.
func (s *Service) MigrateLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.st.read(fileLast)
	if err != nil || s.last == nil {
		return
	}
	var old struct {
		Lines []string `json:"lines"`
	}
	if json.Unmarshal(b, &old) != nil || old.Lines == nil {
		return
	}
	if s.last.RunID == "" {
		s.last.RunID = runlog.NewID(s.last.Started)
	}
	r := s.newRun(s.last, s.settings)
	for _, l := range old.Lines {
		r.Line(runlog.KeptPriority(l), l)
	}
	if err := s.st.writeJSON(fileLast, s.last); err != nil {
		s.log().Warn("certificate: last.json could not be written without its lines", "err", err)
		return
	}
	s.log().Info("certificate: the last attempt's lines moved from last.json into the journal", "run_id", s.last.RunID, "lines", len(old.Lines))
}

// Start begins a run of kind in the background; the page polls Status. Test runs against the
// staging directory (or the custom one) and installs nothing; Issue and Renew run for real.
func (s *Service) Start(kind string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running != nil {
		return ErrBusy
	}
	if s.Issuer == nil {
		return errors.New("no issuer configured")
	}
	set := s.settings
	switch kind {
	case KindTest:
		set.Mode = ModeACME // validate as if switched on: names, provider fields
	case KindIssue, KindRenew:
		if set.Mode != ModeACME {
			return invalid("the mode is self-signed - switch to ACME and save first")
		}
	default:
		return invalid("unknown operation %q", kind)
	}
	if err := set.Validate(); err != nil {
		return err
	}
	dir := set.DirectoryURLFor()
	if kind == KindTest {
		dir = set.TestDirectoryURL()
	}
	now := s.now()
	a := &Attempt{Kind: kind, Started: now, Directory: dir, Names: append([]string{}, set.Names...), RunID: runlog.NewID(now)}
	a.run = s.newRun(a, set)
	s.running = a
	go s.run(set, a)
	return nil
}

// run is the background half. It ends by moving the attempt from running to last, saved.
func (s *Service) run(set Settings, a *Attempt) {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	err := s.attempt(ctx, set, a)
	// the error can carry what a provider's client put into it: redacted in the journal, in the
	// attempt the page shows and in the log line alike
	redact := Redactor(set)
	if err != nil {
		a.run.Err("failed: " + err.Error())
	}
	s.mu.Lock()
	now := s.now()
	a.Finished = &now
	a.OK = err == nil
	if err != nil {
		a.Error = redact(err.Error())
	}
	s.running, s.last = nil, a
	if werr := s.st.writeJSON(fileLast, a); werr != nil {
		s.log().Warn("certificate: the attempt could not be recorded", "err", werr)
	}
	s.mu.Unlock()
	if err != nil {
		s.log().Warn("certificate: "+a.Kind+" failed", "names", strings.Join(a.Names, ","), "directory", a.Directory, "run_id", a.RunID, "err", redact(err.Error()))
	} else {
		s.log().Info("certificate: "+a.Kind+" done", "names", strings.Join(a.Names, ","), "directory", a.Directory, "installed", a.Installed, "restarted", strings.Join(a.RestartedAddons, ","), "run_id", a.RunID)
	}
}

// line writes one of the attempt's own lines to the journal; a "warning: " line as a warning.
func (s *Service) line(a *Attempt, l string) {
	if strings.HasPrefix(l, "warning: ") {
		a.run.Warn(l)
		return
	}
	a.run.Info(l)
}

// legoPriority reads lego's level prefix: a [WARN] line is a warning, every other one information.
func legoPriority(l string) int {
	if strings.Contains(l, "[WARN]") {
		return runlog.PriorityWarning
	}
	return runlog.PriorityInfo
}

func (s *Service) attempt(ctx context.Context, set Settings, a *Attempt) error {
	logLine := func(l string) { s.line(a, l) }
	key, err := s.st.accountKey()
	if err != nil {
		return fmt.Errorf("account key: %w", err)
	}
	regs := s.st.registrations()
	req := Request{DirectoryURL: a.Directory, Email: set.Email, EABKID: set.EABKID, EABHMAC: set.EABHMAC, Names: a.Names, Challenge: set.Challenge,
		DNSProvider: set.DNSProvider, DNSFields: set.DNSCredentials, AccountKey: key, Registration: regs[a.Directory], HTTP01: s.HTTP01, Log: logLine,
		LibLog: func(l string) { a.run.Output("lego", l, legoPriority) }}
	if s.HTTP != nil {
		req.HTTP = s.HTTP()
	}
	logLine(fmt.Sprintf("%s: %s via %s", a.Kind, strings.Join(a.Names, ", "), set.Challenge))
	res, err := s.Issuer.Issue(ctx, req)
	if err != nil {
		return err
	}
	if len(res.Registration) > 0 {
		regs[a.Directory] = res.Registration
		if err := s.st.writeJSON(fileRegs, regs); err != nil {
			logLine("warning: the account could not be saved: " + err.Error())
		}
	}
	live, err := certpem.AssemblePEM(res.Certificate, res.PrivateKey)
	if err != nil {
		return fmt.Errorf("the CA's answer: %w", err)
	}
	info, err := certpem.ParseInfo(res.Certificate, s.now())
	if err != nil {
		return err
	}
	s.mu.Lock()
	a.Certificate = info
	s.mu.Unlock()
	logLine(fmt.Sprintf("issuer %s, valid until %s (%d days)", info.Issuer, info.NotAfter.Format("2006-01-02"), info.DaysLeft))
	if w := ShortLifetimeWarning(info); w != "" {
		s.mu.Lock()
		a.Warning = w
		s.mu.Unlock()
		logLine("warning: " + w)
	}
	if a.Kind == KindTest {
		logLine("test only: nothing installed")
		return nil
	}
	if err := s.st.write(fileCert, res.Certificate); err != nil {
		return err
	}
	if err := s.st.write(fileKey, res.PrivateKey); err != nil {
		return err
	}
	if s.Installer == nil {
		return errors.New("no installer: the certificate was stored but not installed")
	}
	restarted, err := s.Installer.Install(ctx, live, priv.CertModeACME, logLine)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	s.mu.Lock()
	a.Installed = true
	a.RestartedAddons = restarted
	s.mu.Unlock()
	logLine("installed as /etc/config/server.pem, lighttpd reloaded")
	if len(restarted) > 0 {
		logLine("restarted (certs group): " + strings.Join(restarted, ", "))
	}
	return nil
}

// SelfSigned switches back from ACME or manual: mode self-signed saved, the live file removed
// and lighttpd reloaded - S50lighttpd's check_certificate regenerates a self-signed certificate
// on that reload - and the certs-group addons restarted. The issued certificate stays under
// the state directory until the next issue overwrites it; so do the manual key and chain.
func (s *Service) SelfSigned(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	if s.running != nil {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	n := s.settings
	n.Mode = ModeSelfSigned
	if err := s.st.writeJSON(fileSettings, n); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.settings = n
	s.mu.Unlock()
	if s.Installer == nil {
		return nil, nil
	}
	restarted, err := s.Installer.Remove(ctx, func(l string) { s.log().Info("certificate: " + l) })
	if err != nil {
		return restarted, err
	}
	s.log().Info("certificate: back to self-signed", "restarted", strings.Join(restarted, ","))
	return restarted, nil
}

// ShortLifetimeWarning is the line for a certificate that lives under ShortLifetime; empty
// otherwise.
func ShortLifetimeWarning(info *certpem.Info) string {
	life := info.NotAfter.Sub(info.NotBefore)
	if life >= ShortLifetime {
		return ""
	}
	return fmt.Sprintf("the certificate's lifetime is only %s; renewal runs twice a day, so it is renewed at every check, and a lifetime under 12 hours can lapse between two checks - raise the CA's certificate duration (step-ca: the provisioner's max-tls-cert-duration)", life.Round(time.Minute))
}

// ShouldRenew is the decision: under ACME, with an issued certificate, when fewer than RenewBelow
// remain or less than half of the certificate's lifetime is left. No certificate is never
// renewed - issuing is the user's explicit act.
func ShouldRenew(mode string, issued *certpem.Info, now time.Time) bool {
	if mode != ModeACME || issued == nil {
		return false
	}
	left := issued.NotAfter.Sub(now)
	if left < RenewBelow {
		return true
	}
	return left < issued.NotAfter.Sub(issued.NotBefore)/2
}

// Run is the renewal timer: a first look shortly after the start, then every CheckInterval, and
// again whenever the settings change.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTimer(firstCheck)
	defer t.Stop()
	s.setNext(s.now().Add(firstCheck))
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		s.check()
		t.Reset(CheckInterval)
		s.setNext(s.now().Add(CheckInterval))
	}
}

func (s *Service) setNext(at time.Time) {
	s.mu.Lock()
	s.next = &at
	s.mu.Unlock()
}

// check is one tick of the timer: a renewal when the certificate is short of time, or when the
// names it was issued for are no longer the settings' (a rename adapted them).
func (s *Service) check() {
	// a domain another DHCP lease brought: following names follow it before the names are compared
	s.followDomainNow()
	s.mu.Lock()
	mode := s.settings.Mode
	names := append([]string{}, s.settings.Names...)
	issued := s.issuedInfo(s.now())
	var last *Attempt
	if s.last != nil {
		last = copyAttempt(s.last)
	}
	busy := s.running != nil
	s.mu.Unlock()
	if busy {
		return
	}
	switch {
	case ShouldRenew(mode, issued, s.now()):
		s.log().Info("certificate: renewing", "days_left", issued.DaysLeft)
	case NamesChanged(mode, names, issued, last):
		s.log().Info("certificate: renewing for the changed names", "names", strings.Join(names, ","), "issued_for", strings.Join(issued.Names, ","))
	default:
		return
	}
	if err := s.Start(KindRenew); err != nil {
		s.log().Warn("certificate: renewal could not start", "err", err)
	}
}
