package radio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The LAN gateway steps (task 129 phase 4, D-99): upstream's S58LGWFirmwareUpdate, S59SetLGWKey
// and setlgwkey.sh in Go, run as root oneshots before rfd and hs485d start. They do what the
// scripts did - eq3configcmd does the work - with these differences:
//   - a failure fails the unit (the scripts exited 0 whatever happened), each with its reason;
//   - a key change file is parsed by its keys: setlgwkey.sh's `grep KEY` also took the CURKEY=
//     line occulited writes, so the key it passed was two lines and the change could not work;
//   - a file of an unknown class is left alone with a journal line, and the others are still
//     applied (the script's `exit 1` gave up on every file after it).
// Neither has been run against a real gateway yet (the lab has none; measurement 5).

var (
	lgwRFRe    = regexp.MustCompile(`(?m)^Type = (HMLGW2|Lan Interface)`)
	lgwWiredRe = regexp.MustCompile(`(?m)^Type = HMWLGW`)
)

// LGWStep is the environment of the two steps.
type LGWStep struct {
	Root string
	Run  Runner
	// Sleep pauses between the network checks; time.Sleep when nil.
	Sleep func(time.Duration)
	// Timeout bounds each eq3configcmd call (upstream: timeout 120).
	Timeout time.Duration
}

func (s LGWStep) path(p string) string { return Detector{Root: s.Root}.path(p) }
func (s LGWStep) tool(p string) string { return Detector{Root: s.Root}.tool(p) }

func (s LGWStep) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	t := s.Timeout
	if t == 0 {
		t = 120 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, t)
	defer cancel()
	r := s.Run
	if r == nil {
		r = ExecRunner
	}
	out, err := r(cctx, name, args...)
	if cctx.Err() != nil && errors.Is(cctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("no answer within %s", t)
	}
	return out, err
}

func (s LGWStep) normal() bool {
	return ParseKV(readFile(s.path("/var/hm_mode")))["HM_MODE"] == "NORMAL"
}

// LGWConfigured: an RF LAN gateway in rfd.conf, a wired one in hs485d.conf (S58's test).
func LGWConfigured(root string) (rf, wired bool) {
	d := Detector{Root: root}
	return lgwRFRe.MatchString(readFile(d.path("/etc/config/rfd.conf"))), lgwWiredRe.MatchString(readFile(d.path("/etc/config/hs485d.conf")))
}

// waitForNetwork is S58's waitForIP: the default route's gateway answers a ping, five tries.
func (s LGWStep) waitForNetwork(ctx context.Context) error {
	for i := 0; i < 5; i++ {
		if out, err := s.run(ctx, "ip", "-4", "route", "get", "1"); err == nil {
			f := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
			// "1.0.0.0 via 198.51.100.1 dev eth0 src …": the third field, as cut -f3
			if len(f) >= 3 && f[1] == "via" {
				if _, err := s.run(ctx, "ping", "-q", "-W", "5", "-c", "1", f[2]); err == nil {
					return nil
				}
			}
		}
		if s.Sleep != nil {
			s.Sleep(2 * time.Second)
		} else {
			time.Sleep(2 * time.Second)
		}
	}
	return errors.New("the default gateway did not answer: no network to reach the LAN gateways")
}

// LGWFirmware is S58LGWFirmwareUpdate: the RF gateways' coprocessor and firmware and the wired
// gateways' firmware, from /firmware/fwmap, once the network answers.
func LGWFirmware(ctx context.Context, s LGWStep, logf func(string, ...any)) error {
	if !s.normal() {
		logf("lgw: HM_MODE is not NORMAL, no LAN gateway update")
		return nil
	}
	rf, wired := LGWConfigured(s.Root)
	if !rf && !wired {
		logf("lgw: no LAN gateway in rfd.conf or hs485d.conf")
		return nil
	}
	if err := s.waitForNetwork(ctx); err != nil {
		return err
	}
	eq3 := s.tool("/bin/eq3configcmd")
	var failed []string
	step := func(what string, args ...string) {
		out, err := s.run(ctx, eq3, args...)
		if err != nil {
			logf("lgw: %s failed: %v: %s", what, err, strings.TrimSpace(string(out)))
			failed = append(failed, what)
			return
		}
		logf("lgw: %s done", what)
	}
	if rf {
		step("the RF LAN gateways' coprocessor", "update-coprocessor", "-lgw", "-u", "-rfdconf", "/etc/config/rfd.conf", "-l", "1")
		step("the RF LAN gateways' firmware", "update-lgw-firmware", "-m", "/firmware/fwmap", "-c", "/etc/config/rfd.conf", "-l", "1")
	}
	if wired {
		step("the wired LAN gateways' firmware", "update-lgw-firmware", "-m", "/firmware/fwmap", "-c", "/etc/config/hs485d.conf", "-l", "1")
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s failed (see the lines above)", strings.Join(failed, ", "))
	}
	return nil
}

// KeyChange is one /etc/config/<serial>.keychange as occulited (and the CCU WebUI) writes it.
type KeyChange struct {
	File, Class, Serial, IP, Key, CurKey string
}

// ParseKeyChange reads the file by its keys, not by substrings.
func ParseKeyChange(file, text string) KeyChange {
	k := KeyChange{File: file}
	for _, l := range strings.Split(text, "\n") {
		name, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch name {
		case "Class":
			k.Class = v
		case "Serial":
			k.Serial = v
		case "IP":
			k.IP = v
		case "KEY":
			k.Key = v
		case "CURKEY":
			k.CurKey = v
		}
	}
	return k
}

// LGWKeys is S59SetLGWKey with setlgwkey.sh: every queued key change is sent to its gateway
// (eq3configcmd setlgwkey, which also writes the key into the daemon's file), and the file is
// removed when that worked.
func LGWKeys(ctx context.Context, s LGWStep, logf func(string, ...any)) error {
	if !s.normal() {
		logf("lgw: HM_MODE is not NORMAL, no key change")
		return nil
	}
	files, _ := filepath.Glob(s.path("/etc/config/*.keychange"))
	sort.Strings(files)
	if len(files) == 0 {
		logf("lgw: no key change queued")
		return nil
	}
	var failed []string
	for _, f := range files {
		k := ParseKeyChange(f, readFile(f))
		conf := map[string]string{"RF": "/etc/config/rfd.conf", "Wired": "/etc/config/hs485d.conf"}[k.Class]
		switch {
		case conf == "":
			logf("lgw: %s: unknown class %q, left alone", filepath.Base(f), k.Class)
			failed = append(failed, filepath.Base(f))
			continue
		case k.Serial == "" || k.Key == "":
			logf("lgw: %s: no serial or no new key, left alone", filepath.Base(f))
			failed = append(failed, filepath.Base(f))
			continue
		}
		args := []string{"setlgwkey", "-s", k.Serial}
		if k.IP != "" {
			args = append(args, "-h", k.IP)
		}
		args = append(args, "-c", k.CurKey, "-n", k.Key, "-f", conf, "-l", "1")
		out, err := s.run(ctx, s.tool("/bin/eq3configcmd"), args...)
		if err != nil {
			logf("lgw: the key change of %s failed: %v: %s", k.Serial, err, strings.TrimSpace(string(out)))
			failed = append(failed, filepath.Base(f))
			continue
		}
		if err := os.Remove(f); err != nil {
			logf("lgw: %s: the key is changed, the file could not be removed: %v", filepath.Base(f), err)
		}
		logf("lgw: the key of %s (%s) is changed", k.Serial, k.Class)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s not applied (see the lines above); the files stay for the next boot", strings.Join(failed, ", "))
	}
	return nil
}
