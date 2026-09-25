package httpapi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/hobbyquaker/occulited/internal/system"
)

// scriptBox is the tests' fake box: the addons' rc.d layer (system.AddonScripts: the addon list,
// the installer, the nav) plus a service manager over the fixture root's init scripts and pid
// files. Until task 187 that manager was occulited's busybox init implementation; the product is
// systemd only now (D-39), and the routes under test need a ServiceManager that does something.
type scriptBox struct{ system.AddonScripts }

var fixtureServices = []struct {
	id, script, pidfile, protocol string
	port                          int
}{
	{"rfd", "S61rfd", "rfd.pid", "BidCos-RF", 32001},
	{"hs485d", "S60hs485d", "hs485d.pid", "BidCos-Wired", 32000},
	{"hmipserver", "S62HMServer", "HMIPServer.pid", "HmIP-RF", 32010},
	{"multimacd", "S60multimacd", "multimacd.pid", "", 0},
	{"lighttpd", "S50lighttpd", "lighttpd.pid", "", 80},
}

func (b scriptBox) path(p string) string { return string(b.Root) + p }

func (b scriptBox) pidAlive(pidfile string) (int, bool) {
	raw, err := os.ReadFile(b.path("/var/run/" + pidfile))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	_, err = os.Stat(b.path(fmt.Sprintf("/proc/%d", pid)))
	return pid, err == nil
}

func (b scriptBox) List() ([]system.Service, error) {
	out := []system.Service{}
	for _, m := range fixtureServices {
		script := b.path("/etc/init.d/" + m.script)
		if _, err := os.Stat(script); err != nil {
			continue
		}
		s := system.Service{ID: m.id, Kind: "system", Script: script, Protocol: m.protocol, Port: m.port, Managed: true, Enabled: true}
		s.PID, s.Running = b.pidAlive(m.pidfile)
		out = append(out, s)
	}
	entries, _ := os.ReadDir(b.path("/usr/local/etc/config/rc.d"))
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), ".script") {
			continue
		}
		s := system.Service{ID: e.Name(), Kind: "addon", Script: b.path("/usr/local/etc/config/rc.d/" + e.Name()), Managed: true, Enabled: true, Category: "addon"}
		s.PID, s.Running = b.pidAlive(e.Name() + ".pid")
		out = append(out, s)
	}
	return out, nil
}

func (b scriptBox) Control(ctx context.Context, id, action string) (string, error) {
	switch action {
	case "start", "stop", "restart":
	default:
		return "", errors.New("action must be start, stop or restart")
	}
	list, _ := b.List()
	for _, s := range list {
		if s.ID == id {
			ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, s.Script, action).CombinedOutput()
			return string(out), err
		}
	}
	return "", errors.New("unknown service")
}
