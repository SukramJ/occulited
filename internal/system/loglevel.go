package system

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hobbyquaker/occulited/internal/radio"
)

// ---- log levels (task 27.8) ------------------------------------------------------------------
//
// The daemons do not share one mechanism, and the page has to say so rather than pretend:
//
//   rfd, hs485d   -l N on the command line, from LOGLEVEL_RFD / LOGLEVEL_HS485D in
//                 /etc/config/syslog; N is 1 debug, 2 info, 4 warning, 5 error - OpenCCU's own
//                 vocabulary, kept so a user of its WebUI finds the same four choices. Both also
//                 take the level live over XML-RPC (logLevel), which the API layer does.
//
//                 B-162 asked whether this table is inverted (it reads like a syslog severity,
//                 where the number rises with what is written). It is not. Measured on
//                 ccu-pi3-1 on 2026-09-22, every level set live and the same provocation
//                 each time (register a callback to a dead port, ping, unregister), counting
//                 rfd's own journal lines:
//
//                   level 1: 20 lines   2: 12   3: 9   4: 9   5: 8   6: 4   7: 4
//
//                 with the four error lines in every one of them. 1 is the most verbose and 7
//                 the quietest - the number is a floor on what is written, in eQ-3's own scale.
//                 What made rfd look silent to the maintainer is B-161 (the running daemon never
//                 got the level) and an idle rfd, which writes nothing at any level: three
//                 listBidcosInterfaces calls produced no line at 1 either.
//   multimacd     -l N from LOGLEVEL_MULTIMACD, the same scale (task 101). Unset, the init script
//                 falls back to LOGLEVEL_RFD, which was multimacd's only level before, so a box or
//                 a restored backup without the key keeps its behaviour. Only a restart applies
//                 it, and multimacd cannot restart under rfd and hmipserver: the radio stack
//                 stops and starts in order (RestartRadioStack).
//   hmipserver    log4j2: S62HMServer copies the template to /var/etc/log4j2.xml at start and
//                 patches every level= with LOGLEVEL_HMIP (a name: TRACE DEBUG INFO WARN ERROR).
//                 Restart to apply.
//   lighttpd      no level at all, only debug.* switches in its configuration, which is on the
//                 read-only rootfs; the one include it takes from the userfs is
//                 /etc/config/lighttpd/*.conf, so the switches go there as a drop-in of ours.
//                 The access log is a second drop-in of its own (see lighttpdAccessLine).
//                 Restart to apply.
//
// occulited's own level is not in this file: it lives in occulited.json and applies live
// (internal/logctl); the API layer puts the two together.
//
// /etc/config/syslog is on the userfs and also carries LOGHOST (B-46): a level a user sets
// survives a firmware update, and LOGHOST belongs to the same page because it is the same file.
// Every other line of the file - LOGLEVEL_REGA, comments, anything an addon put there - is kept.

const (
	syslogConfig       = "/etc/config/syslog"
	lighttpdDebugFile  = "/etc/config/lighttpd/occulite-debug.conf"
	lighttpdAccessFile = "/etc/config/lighttpd/occulite-accesslog.conf"
)

// lighttpdAccessLine is the access log's drop-in: lighttpd pipes every request line to
// systemd-cat, which puts it into the journal as identifier lighttpd-access at priority info.
//
// A pipe and not accesslog.use-syslog, measured with lighttpd 1.4.82: use-syslog logs under the
// identifier lighttpd, the same as lighttpd's error log, so the Log page could not tell a request
// from an error. Only a piped logger gives the access log an identifier of its own. The image
// loads mod_accesslog without a file, which logs nothing until this drop-in names one; with the
// drop-in gone the access log is off again. It is off by default: every request is an entry, and
// the UI's polling alone would fill the RAM journal and its rate limit.
const lighttpdAccessLine = `accesslog.filename = "|/usr/bin/systemd-cat -t lighttpd-access -p info"`

// LogLevels is what the page shows and what PUT carries back.
type LogLevels struct {
	RFD    int `json:"rfd"`    // LOGLEVEL_RFD: 1 debug, 2 info, 4 warning, 5 error
	HS485D int `json:"hs485d"` // LOGLEVEL_HS485D, same scale
	// MultiMACD is LOGLEVEL_MULTIMACD, same scale; nil = unset, multimacd runs with rfd's level.
	MultiMACD *int          `json:"multimacd"`
	HmIP      string        `json:"hmip"`     // LOGLEVEL_HMIP: TRACE DEBUG INFO WARN ERROR
	LogHost   string        `json:"loghost"`  // LOGHOST: an external syslog server, empty = none (B-46)
	Lighttpd  LighttpdDebug `json:"lighttpd"` // the drop-ins' switches
}

// MultiMACDLevel is the level multimacd starts with: its own, or rfd's when it has none.
func (l LogLevels) MultiMACDLevel() int {
	if l.MultiMACD != nil {
		return *l.MultiMACD
	}
	return l.RFD
}

// LighttpdDebug are lighttpd's debug.* switches, each a line of the debug drop-in when on, and
// the access log, which is its own drop-in (lighttpdAccessFile) when on.
type LighttpdDebug struct {
	RequestHandling   bool `json:"request_handling"`   // debug.log-request-handling
	ConditionHandling bool `json:"condition_handling"` // debug.log-condition-handling
	FileNotFound      bool `json:"file_not_found"`     // debug.log-file-not-found
	AccessLog         bool `json:"access_log"`         // accesslog.filename piped to systemd-cat
}

// RFDLevels are the values rfd, hs485d and multimacd take; HmIPLevels the names log4j2 takes.
var (
	RFDLevels  = []int{1, 2, 4, 5}
	HmIPLevels = []string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR"}
)

// HmIPDefaultLevel is hmipserver's level on a box whose /etc/config/syslog has no LOGLEVEL_HMIP:
// WARN since 2026-09-12 (maintainer, task 94). It was ERROR, task 83's lite default, which hid
// the warnings a user needs to see when a device or the radio misbehaves; at WARN hmipserver's
// start writes a handful of lines, far below the journal's rate limit (task 83's measurements).
// The fork's init script and log4j2 template fall back to the same level - keep them in step.
const HmIPDefaultLevel = "WARN"

// multimacdLevelRe is what the fork's S60multimacd takes from LOGLEVEL_MULTIMACD: one of eQ-3's
// levels, 0 to 6. Anything else there is read as unset, as the script falls back to rfd's then.
var multimacdLevelRe = regexp.MustCompile(`^[0-6]$`)

// ReadLogLevels reads /etc/config/syslog and the lighttpd drop-in. A missing file or key gives
// the defaults the init scripts fall back to as well: 5 and 5 for rfd and hs485d, rfd's for
// multimacd, HmIPDefaultLevel for hmipserver.
func (r Root) ReadLogLevels() LogLevels {
	l := LogLevels{RFD: 5, HS485D: 5, HmIP: HmIPDefaultLevel}
	for k, v := range parseSyslogConfig(readFile(r.join(syslogConfig))) {
		switch k {
		case "LOGLEVEL_RFD":
			if n, err := strconv.Atoi(v); err == nil {
				l.RFD = n
			}
		case "LOGLEVEL_HS485D":
			if n, err := strconv.Atoi(v); err == nil {
				l.HS485D = n
			}
		case "LOGLEVEL_MULTIMACD":
			if multimacdLevelRe.MatchString(v) {
				n, _ := strconv.Atoi(v)
				l.MultiMACD = &n
			}
		case "LOGLEVEL_HMIP":
			l.HmIP = strings.ToUpper(v)
		case "LOGHOST":
			l.LogHost = v
		}
	}
	for _, line := range strings.Split(readFile(r.join(lighttpdDebugFile)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		if strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"`)) != "enable" {
			continue
		}
		switch strings.TrimSpace(k) {
		case "debug.log-request-handling":
			l.Lighttpd.RequestHandling = true
		case "debug.log-condition-handling":
			l.Lighttpd.ConditionHandling = true
		case "debug.log-file-not-found":
			l.Lighttpd.FileNotFound = true
		}
	}
	l.Lighttpd.AccessLog = accessLogPiped(readFile(r.join(lighttpdAccessFile)))
	return l
}

// accessLogPiped says whether a drop-in has an accesslog.filename line that pipes to systemd-cat:
// that is the access log switched on. A file naming anything else is not ours to report as on.
func accessLogPiped(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(k) != "accesslog.filename" {
			continue
		}
		v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"`))
		if strings.HasPrefix(v, "|") && strings.Contains(v, "systemd-cat") {
			return true
		}
	}
	return false
}

// SetLogLevels writes /etc/config/syslog and the two lighttpd drop-ins and says which units have to
// be restarted for the change to show: multimacd when the level it starts with changed (its own,
// or rfd's while it has none), hmipserver for its level, occu-syslog-forward for LOGHOST, lighttpd
// for its debug switches and its access log. rfd and hs485d are not in the list because the caller
// applies their level live (Interface.logLevel) - if that fails they are added by the caller.
// LOGHOST alone does not restart hmipserver: it logs to the journal (task 83), and the forwarder
// reads the journal (B-96). A nil MultiMACD removes LOGLEVEL_MULTIMACD from the file.
func (r Root) SetLogLevels(l LogLevels) (LogLevels, []string, error) {
	if err := checkLogLevels(l); err != nil {
		return LogLevels{}, nil, err
	}
	old := r.ReadLogLevels()
	set := map[string]string{
		"LOGLEVEL_RFD":    strconv.Itoa(l.RFD),
		"LOGLEVEL_HS485D": strconv.Itoa(l.HS485D),
		"LOGLEVEL_HMIP":   strings.ToUpper(l.HmIP),
		"LOGHOST":         strings.TrimSpace(l.LogHost),
	}
	var unset []string
	if l.MultiMACD != nil {
		set["LOGLEVEL_MULTIMACD"] = strconv.Itoa(*l.MultiMACD)
	} else {
		unset = append(unset, "LOGLEVEL_MULTIMACD")
	}
	if err := writeFileAtomic(r.join(syslogConfig), []byte(renderSyslogConfig(readFile(r.join(syslogConfig)), set, unset...)), 0o644); err != nil {
		return LogLevels{}, nil, err
	}
	// B-161: the units take the level from /run/occulite/radio/<daemon>.env, which the radio run
	// writes at boot - so without this a level set here reached rfd and hs485d live (below) and
	// multimacd never, and a restart put the boot's level back. A failure is not fatal: the file
	// in /etc/config is what the next boot reads, and the daemons' own live call still works.
	r.writeLevelEnv(l)
	if err := r.writeDropIn(lighttpdDebugFile, renderLighttpdDebug(l.Lighttpd)); err != nil {
		return LogLevels{}, nil, err
	}
	if err := r.writeDropIn(lighttpdAccessFile, renderLighttpdAccess(l.Lighttpd)); err != nil {
		return LogLevels{}, nil, err
	}
	var restart []string
	if old.MultiMACDLevel() != l.MultiMACDLevel() {
		restart = append(restart, "multimacd")
	}
	if old.HmIP != strings.ToUpper(l.HmIP) {
		restart = append(restart, "hmipserver")
	}
	if old.LogHost != strings.TrimSpace(l.LogHost) {
		restart = append(restart, "occu-syslog-forward")
	}
	if old.Lighttpd != l.Lighttpd { // the debug switches and the access log alike
		restart = append(restart, "lighttpd")
	}
	return r.ReadLogLevels(), restart, nil
}

// writeLevelEnv rewrites the environment files the units read for their level (B-161). The names
// and the content are radio.LogLevelEnvFiles', so the boot's render and this one cannot drift.
func (r Root) writeLevelEnv(l LogLevels) {
	for name, content := range radio.LogLevelEnvFiles(strconv.Itoa(l.RFD), strconv.Itoa(l.HS485D), strconv.Itoa(l.MultiMACDLevel())) {
		path := r.join(filepath.Join(radio.RunDir, name+".env"))
		if err := writeFileAtomic(path, []byte(content), 0o644); err != nil {
			slog.Warn("log levels: the environment file was not written; the level applies at the next boot", "file", path, "err", err)
		}
	}
}

func checkLogLevels(l LogLevels) error {
	ok := func(n int) bool {
		for _, v := range RFDLevels {
			if v == n {
				return true
			}
		}
		return false
	}
	if !ok(l.RFD) || !ok(l.HS485D) {
		return fmt.Errorf("rfd and hs485d levels are 1 (debug), 2 (info), 4 (warning) or 5 (error)")
	}
	if l.MultiMACD != nil && !ok(*l.MultiMACD) {
		return fmt.Errorf("multimacd's level is 1 (debug), 2 (info), 4 (warning), 5 (error), or null for rfd's")
	}
	hm := strings.ToUpper(l.HmIP)
	found := false
	for _, v := range HmIPLevels {
		if v == hm {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("hmipserver level is one of %s", strings.Join(HmIPLevels, ", "))
	}
	if h := strings.TrimSpace(l.LogHost); h != "" && !validLogHost(h) {
		return fmt.Errorf("syslog server must be a host name or an address, optionally with :port")
	}
	return nil
}

// logHostRe: a host name or IPv4 address, or an IPv6 address in brackets, each with an optional
// port - what busybox syslogd's -R took and occu-syslog-forward takes (B-96).
var logHostRe = regexp.MustCompile(`^([A-Za-z0-9.-]{1,253}|\[[0-9A-Fa-f:.]{2,45}\])(:[0-9]{1,5})?$`)

// validLogHost: LOGHOST goes into a shell file the init scripts source, and on the busybox
// products into a sed expression in S62HMServer, so nothing that could be more than a host and a
// port - no quote, no space, no $. A bare IPv6 address (as an OpenCCU file may have it) is hex
// digits, colons and dots.
func validLogHost(h string) bool {
	if m := logHostRe.FindStringSubmatch(h); m != nil {
		if m[2] == "" {
			return true
		}
		n, err := strconv.Atoi(m[2][1:])
		return err == nil && n >= 1 && n <= 65535
	}
	return strings.Count(h, ":") > 1 && !strings.ContainsAny(h, "[]") && net.ParseIP(h) != nil
}

// parseSyslogConfig reads KEY=VALUE lines; quotes around the value are stripped.
func parseSyslogConfig(s string) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return m
}

// renderSyslogConfig rewrites the known keys where they stand and appends the ones the file did
// not have; the keys in unset are dropped wherever they stand (only as an assignment, never a
// comment); every other line stays as it was. An empty LOGHOST is written as LOGHOST= rather
// than dropped, which is how the WebUI's own dialog leaves it.
func renderSyslogConfig(s string, set map[string]string, unset ...string) string {
	var out []string
	seen := map[string]bool{}
	drop := map[string]bool{}
	for _, k := range unset {
		drop[k] = true
	}
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if s == "" {
			break
		}
		k, _, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		comment := strings.HasPrefix(strings.TrimSpace(line), "#")
		if ok && !comment && drop[k] {
			continue
		}
		if v, want := set[k]; ok && want && !comment {
			out = append(out, k+"="+v)
			seen[k] = true
			continue
		}
		out = append(out, line)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+set[k])
	}
	return strings.Join(out, "\n") + "\n"
}

// writeDropIn writes a lighttpd drop-in, or removes it when body is "" (a missing file is fine).
func (r Root) writeDropIn(path, body string) error {
	if body == "" {
		if err := remove(r.join(path)); err != nil && !os.IsNotExist(err) && !strings.Contains(err.Error(), "no such file") {
			return err
		}
		return nil
	}
	if err := Priv.MkdirAll(filepath.Dir(r.join(path)), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(r.join(path), []byte(body), 0o644)
}

// renderLighttpdAccess is the access log's drop-in, or "" when it is off (then there is no file).
func renderLighttpdAccess(d LighttpdDebug) string {
	if !d.AccessLog {
		return ""
	}
	return "# written by occulited (Log page): lighttpd's access log into the journal, identifier lighttpd-access. Remove there, not here.\n" + lighttpdAccessLine + "\n"
}

// renderLighttpdDebug is the drop-in, or "" when no switch is on (then there is no file).
func renderLighttpdDebug(d LighttpdDebug) string {
	var b strings.Builder
	if d.RequestHandling || d.ConditionHandling || d.FileNotFound {
		b.WriteString("# written by occulited (Log page): lighttpd debug switches. Remove there, not here.\n")
	}
	if d.RequestHandling {
		b.WriteString(`debug.log-request-handling = "enable"` + "\n")
	}
	if d.ConditionHandling {
		b.WriteString(`debug.log-condition-handling = "enable"` + "\n")
	}
	if d.FileNotFound {
		b.WriteString(`debug.log-file-not-found = "enable"` + "\n")
	}
	return b.String()
}

// ---- restarting the radio stack (task 101) ---------------------------------------------------

// RadioStackRestart is what a restart of the radio stack did, in order.
type RadioStackRestart struct {
	Stopped []string          `json:"stopped"`
	Started []string          `json:"started"`
	Errors  map[string]string `json:"errors,omitempty"`
}

// RestartRadioStack restarts multimacd the only way it can take a new level: hmipserver and rfd
// hold multimacd's /dev/mmd_* nodes, so the units that run stop in the reverse of the boot order
// (hmipserver, rfd, multimacd) and start again in boot order - the sequence of the radio firmware
// flash (radiofw.go), without the flash. A unit that did not run is left alone, and a stop that
// fails ends the stop phase; whatever was stopped is started again either way.
func RestartRadioStack(ctx context.Context, svc ServiceManager) (RadioStackRestart, error) {
	res := RadioStackRestart{Stopped: []string{}, Started: []string{}}
	if svc == nil {
		return res, fmt.Errorf("no service manager")
	}
	running := map[string]bool{}
	if list, err := svc.List(); err != nil {
		for _, u := range radioUnits { // unknown: a stop of a stopped unit is harmless
			running[u] = true
		}
	} else {
		for _, sv := range list {
			if sv.Running && containsUnit(radioUnits, sv.ID) {
				running[sv.ID] = true
			}
		}
	}
	fail := func(u string, err error) {
		if res.Errors == nil {
			res.Errors = map[string]string{}
		}
		res.Errors[u] = err.Error()
	}
	var stopErr error
	for i := len(radioUnits) - 1; i >= 0; i-- {
		u := radioUnits[i]
		if !running[u] {
			continue
		}
		if _, err := svc.Control(ctx, u, "stop"); err != nil {
			fail(u, err)
			stopErr = fmt.Errorf("stopping %s: %w", u, err)
			break
		}
		res.Stopped = append(res.Stopped, u)
	}
	var startErr error
	for _, u := range radioUnits {
		if !containsUnit(res.Stopped, u) {
			continue
		}
		if _, err := svc.Control(ctx, u, "start"); err != nil {
			fail(u, err)
			if startErr == nil {
				startErr = fmt.Errorf("starting %s: %w", u, err)
			}
			continue
		}
		res.Started = append(res.Started, u)
	}
	if stopErr != nil {
		return res, stopErr
	}
	return res, startErr
}
