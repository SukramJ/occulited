package system

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- firewall -------------------------------------------------------------------------------
//
// /etc/config/firewall.conf was OpenCCU's libfirewall configuration. Since task 157 occulited owns
// the firewall (firewall-rules.json, internal/firewall); the file is read once, to convert it.

// FirewallService is one [SERVICE] section: a named port group with an access level.
type FirewallService struct {
	ID     string
	Ports  []int
	Access string // full, restricted, none
}

// Firewall is the parsed firewall.conf - read once, by the conversion to the rule list (task 157).
type Firewall struct {
	Mode      string // MOST_OPEN or RESTRICTIVE
	IPs       []string
	UserPorts []string
	Services  []FirewallService
}

var firewallSectionRe = regexp.MustCompile(`^\[([^\]]+)\]`)

// FirewallDefaultMode is the mode of a box whose firewall.conf is missing or carries no MODE
// line. It is RESTRICTIVE for two reasons that agree: D-29 decides it ("the firewall default
// becomes RESTRICTIVE ... the CCU's MOST_OPEN default is a compatibility decision we do not have
// to inherit"), and it is what /lib/libfirewall.tcl itself falls back to, so reporting anything
// else describes a box that does not exist. occulited said MOST_OPEN until 2026-09-07 (B-43),
// which meant a freshly flashed box enforced RESTRICTIVE while the Network page displayed
// MOST_OPEN - and the first save on that page made the display true.
const FirewallDefaultMode = "RESTRICTIVE"

// ReadFirewall parses firewall.conf: KEY = value lines, then [SERVICE X] sections with Id, Ports
// and Access.
func (r Root) ReadFirewall() Firewall {
	fw := Firewall{Mode: FirewallDefaultMode, IPs: []string{}, UserPorts: []string{}, Services: []FirewallService{}}
	var cur *FirewallService
	sc := bufio.NewScanner(strings.NewReader(readFile(r.join("/etc/config/firewall.conf"))))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := firewallSectionRe.FindStringSubmatch(line); m != nil {
			id := ""
			if f := strings.Fields(m[1]); len(f) == 2 {
				id = f[1]
			}
			fw.Services = append(fw.Services, FirewallService{Ports: []int{}, ID: id})
			cur = &fw.Services[len(fw.Services)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if cur != nil {
			switch k {
			case "Id":
				cur.ID = v
			case "Ports":
				for _, p := range strings.Fields(v) {
					if n, err := strconv.Atoi(p); err == nil {
						cur.Ports = append(cur.Ports, n)
					}
				}
			case "Access":
				cur.Access = v
			}
			continue
		}
		switch strings.ToUpper(k) {
		case "MODE":
			fw.Mode = v
		case "USERPORTS":
			fw.UserPorts = strings.Fields(v)
		case "IPS":
			fw.IPs = strings.Fields(v)
		}
	}
	return fw
}

// ---- time -----------------------------------------------------------------------------------

// TimeConfig is timezone, NTP and the clock as the box has them.
type TimeConfig struct {
	TZ         string    `json:"tz"`   // /etc/config/TZ: a POSIX string or a zone name
	Zone       string    `json:"zone"` // /etc/config/timezone: the zone name updateTZ.sh derived
	NTPServers []string  `json:"ntp_servers"`
	HasNTP     bool      `json:"has_ntp"` // /var/status/hasNTP: chrony accepted at least one server
	Now        time.Time `json:"now"`
}

var ntpServersRe = regexp.MustCompile(`(?m)^\s*NTPSERVERS\s*=\s*'?([^'\n]*)'?\s*$`)

// ReadTime reads /etc/config/TZ, /etc/config/timezone and NTPSERVERS from /etc/config/ntpclient.
func (r Root) ReadTime() TimeConfig {
	t := TimeConfig{TZ: strings.TrimSpace(readFile(r.join("/etc/config/TZ"))), Zone: strings.TrimSpace(readFile(r.join("/etc/config/timezone"))), NTPServers: []string{}, Now: time.Now()}
	if m := ntpServersRe.FindStringSubmatch(readFile(r.join("/etc/config/ntpclient"))); m != nil {
		t.NTPServers = strings.Fields(m[1])
	}
	if _, err := os.Stat(r.join("/var/status/hasNTP")); err == nil {
		t.HasNTP = true
	}
	return t
}

// Zone is one line of zone.tab.
type Zone struct {
	Name    string `json:"name"` // Europe/Berlin
	Country string `json:"country"`
	Code    string `json:"code"`
	Comment string `json:"comment,omitempty"`
}

// Zones lists the IANA zones the box knows, from zone.tab with country names from iso3166.tab.
func (r Root) Zones() []Zone {
	countries := map[string]string{}
	for _, line := range strings.Split(readFile(r.join("/usr/share/zoneinfo/iso3166.tab")), "\n") {
		if f := strings.SplitN(line, "\t", 2); len(f) == 2 && !strings.HasPrefix(line, "#") {
			countries[f[0]] = strings.TrimSpace(f[1])
		}
	}
	var zones []Zone
	for _, line := range strings.Split(readFile(r.join("/usr/share/zoneinfo/zone.tab")), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		z := Zone{Code: f[0], Country: countries[f[0]], Name: f[2]}
		if len(f) > 3 {
			z.Comment = f[3]
		}
		zones = append(zones, z)
	}
	sort.Slice(zones, func(i, j int) bool { return zones[i].Name < zones[j].Name })
	if zones == nil {
		zones = []Zone{}
	}
	return zones
}

var zoneNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z][A-Za-z0-9_+-]*){0,2}$`)

// SetTimezone writes the zone name into /etc/config/TZ (what cp_time.cgi does for zone.tab
// entries), keeps time.conf's TIMEZONE in step, and runs /bin/updateTZ.sh, hwclock and
// SetInterfaceClock as the WebUI does.
func (r Root) SetTimezone(ctx context.Context, run Runner, zone string) error {
	if run == nil {
		run = ExecRunner
	}
	if !zoneNameRe.MatchString(zone) || path.Clean(zone) != zone {
		return errors.New("zone: not a zone name")
	}
	if _, err := os.Stat(r.join("/usr/share/zoneinfo/" + zone)); err != nil {
		return fmt.Errorf("zone: %s is not installed", zone)
	}
	if err := writeFileAtomic(r.join("/etc/config/TZ"), []byte(zone+"\n"), 0o644); err != nil {
		return err
	}
	conf := setProperty(readFile(r.join("/etc/config/time.conf")), "TIMEZONE", zone)
	if err := writeFileAtomic(r.join("/etc/config/time.conf"), []byte(conf), 0o644); err != nil {
		return err
	}
	if out, err := run(ctx, r.join("/bin/updateTZ.sh")); err != nil {
		return fmt.Errorf("updateTZ.sh: %w: %s", err, strings.TrimSpace(string(out)))
	}
	r.pushClock(ctx, run)
	return nil
}

// pushClock is what the WebUI does after every clock-related change: the RTC gets the system
// time and rfd is told (it timestamps radio traffic).
func (r Root) pushClock(ctx context.Context, run Runner) {
	_, _ = run(ctx, "/sbin/hwclock", "-wu")
	port := "2001"
	if m := regexp.MustCompile(`set EQ3_SERVICE_RFD_PORT (\d+)`).FindStringSubmatch(readFile(r.join("/etc/eq3services.ports.tcl"))); m != nil {
		port = m[1]
	}
	_, _ = run(ctx, "/bin/SetInterfaceClock", "127.0.0.1:"+port)
}

var ntpHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)

// SetNTPServers writes /etc/config/ntpclient and restarts chrony.
//
// The restart goes through the service manager, as SetSSH's does (openccu-lite task 115): on the
// systemd product /etc/init.d/S46chronyd is the init-script wrapper, which turned the call into
// `systemctl restart chrony.service` anyway; asking the manager directly is the same restart
// without the detour. svc == nil keeps the init-script call for development and the busybox tests.
func (r Root) SetNTPServers(ctx context.Context, run Runner, svc ServiceManager, servers []string) error {
	if run == nil {
		run = ExecRunner
	}
	for _, s := range servers {
		if !ntpHostRe.MatchString(s) && net.ParseIP(s) == nil {
			return fmt.Errorf("ntp_servers: %q is not a host name or address", s)
		}
	}
	if err := writeFileAtomic(r.join("/etc/config/ntpclient"), []byte("NTPSERVERS='"+strings.Join(servers, " ")+"'\n"), 0o644); err != nil {
		return err
	}
	if svc != nil {
		if out, err := svc.Control(ctx, "chrony", "restart"); err != nil {
			return fmt.Errorf("chronyd restart: %w: %s", err, strings.TrimSpace(out))
		}
		return nil
	}
	if out, err := run(ctx, r.join("/etc/init.d/S46chronyd"), "restart"); err != nil {
		return fmt.Errorf("chronyd restart: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SetClock sets the system time by hand (a box without NTP), then pushes it to the RTC and rfd.
func (r Root) SetClock(ctx context.Context, run Runner, t time.Time) error {
	if run == nil {
		run = ExecRunner
	}
	if t.Year() < 2020 || t.Year() > 2100 {
		return errors.New("time: implausible")
	}
	if out, err := run(ctx, "date", "-u", "-s", t.UTC().Format("2006-01-02 15:04:05")); err != nil {
		return fmt.Errorf("date: %w: %s", err, strings.TrimSpace(string(out)))
	}
	r.pushClock(ctx, run)
	return nil
}

// ---- LEDs -----------------------------------------------------------------------------------

// LED is one onboard LED as /var/hm_mode names it.
type LED struct {
	Name    string `json:"name"` // green, red, yellow
	Path    string `json:"path"`
	Trigger string `json:"trigger"` // the active trigger from sysfs
	Normal  string `json:"normal"`  // HM_LED_*_MODE2: what "on" means for this box
}

// LEDs is the LED state: the list and the disableOnboardLED marker.
type LEDs struct {
	Disabled bool  `json:"disabled"`
	LEDs     []LED `json:"leds"`
}

var triggerActiveRe = regexp.MustCompile(`\[([^\]]+)\]`)

// ReadLEDs lists the onboard LEDs the box knows about (none on a VM).
func (r Root) ReadLEDs() LEDs {
	out := LEDs{LEDs: []LED{}}
	if _, err := os.Stat(r.join("/etc/config/disableOnboardLED")); err == nil {
		out.Disabled = true
	}
	kv := r.HMMode()
	for _, c := range []string{"GREEN", "RED", "YELLOW"} {
		p := kv["HM_LED_"+c]
		if p == "" {
			continue
		}
		l := LED{Name: strings.ToLower(c), Path: p, Normal: kv["HM_LED_"+c+"_MODE2"]}
		if m := triggerActiveRe.FindStringSubmatch(readFile(r.join(p + "/trigger"))); m != nil {
			l.Trigger = m[1]
		}
		if l.Trigger == "" && !strings.HasPrefix(p, "/sys") {
			continue
		}
		out.LEDs = append(out.LEDs, l)
	}
	return out
}

// SetLEDsDisabled writes the marker S99SetupLEDs honours at boot and applies it now.
func (r Root) SetLEDsDisabled(disabled bool) error {
	marker := r.join("/etc/config/disableOnboardLED")
	if disabled {
		if err := touch(marker, 0o644); err != nil {
			return err
		}
	} else if err := remove(marker); err != nil {
		return err
	}
	var failed error
	for _, l := range r.ReadLEDs().LEDs {
		trigger := l.Normal
		if disabled || trigger == "" {
			trigger = "none"
		}
		// sysfs attributes are not renamed into place: a plain write through the helper's Touch
		// would be wrong too, so the LED trigger goes through Run with the shell's redirect. The
		// helper's policy knows this exact shape (B-14); the error is reported rather than
		// dropped, because for a while it was refused and the switch silently did nothing.
		if _, err := run(context.Background(), "sh", "-c", "echo "+shellQuote(trigger)+" > "+shellQuote(r.join(l.Path+"/trigger"))); err != nil && failed == nil {
			failed = fmt.Errorf("led %s: %w", l.Name, err)
		}
	}
	return failed
}
