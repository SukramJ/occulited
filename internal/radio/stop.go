package radio

import (
	"context"
	"os"
	"strings"
	"time"
)

// Stop is occu-init-rf-hardware.service's stop (task 129 phase 2; before it lite-rf-stop and
// S47InitRFHardware's stop()). Upstream's stop sends every coprocessor into its bootloader
// ("eq3configcmd update-coprocessor -bl", up to 120 s per module) so that a stopped stack stops
// receiving; that handover is only wanted before a firmware update - it costs up to four minutes
// on a two-module box at every reboot, and with systemd the stack is stopped by stopping its
// units. So: with a staged update (/usr/local/.firmwareUpdate, the link checkFirmwareUpdate.sh
// and occulited's update create, or /usr/local/.recoveryMode, which arms the recovery system for
// the next boot) the handover runs as upstream's; without one the modules stay in normal
// operation. In both cases an HB-RF-ETH is disconnected (its kernel module would otherwise keep
// reconnecting to a box that no longer runs the stack), and the LED driver of an RPI-RF-MOD on an
// HB-RF adapter is unloaded before a handover, as upstream's stop did.

// StagedUpdateMarkers arm the handover.
var StagedUpdateMarkers = []string{"/usr/local/.firmwareUpdate", "/usr/local/.recoveryMode"}

// Stop runs the stop step against the box's /var/hm_mode.
func Stop(ctx context.Context, d Detector, logf func(string, ...any)) {
	env := ParseKV(readFile(d.path("/var/hm_mode")))
	staged := ""
	for _, m := range StagedUpdateMarkers {
		if _, err := os.Lstat(d.path(m)); err == nil {
			staged = m
			break
		}
	}
	if staged != "" {
		logf("stop: %s is staged, handing the coprocessors to the bootloader", staged)
		nodes := []string{}
		if n := env["HM_HMRF_DEVNODE"]; n != "" {
			nodes = append(nodes, n)
		}
		if n := env["HM_HMIP_DEVNODE"]; n != "" && n != env["HM_HMRF_DEVNODE"] {
			nodes = append(nodes, n)
		}
		for _, n := range nodes {
			hctx, cancel := context.WithTimeout(ctx, 120*time.Second)
			if _, err := d.run()(hctx, d.tool("/bin/eq3configcmd"), "update-coprocessor", "-p", d.path(n), "-bl", "-l", "1"); err != nil {
				logf("stop: the handover on %s did not finish cleanly: %v", n, err)
			}
			cancel()
		}
		hbrf := false
		for _, role := range []string{"HMRF", "HMIP"} {
			if env["HM_"+role+"_DEV"] == "RPI-RF-MOD" && strings.Contains(strings.ToLower(env["HM_"+role+"_DEVTYPE"]), "hb-rf-") {
				hbrf = true
			}
		}
		if hbrf && d.moduleLoaded("rpi_rf_mod_led") {
			_, _ = d.run()(ctx, d.tool("/sbin/rmmod"), "rpi_rf_mod_led")
		}
	} else {
		logf("stop: no firmware update staged, no bootloader handover")
	}
	if exists(d.path("/etc/config/hb_rf_eth")) && exists(d.path("/sys/module/hb_rf_eth/parameters/connect")) {
		_ = os.WriteFile(d.path("/sys/module/hb_rf_eth/parameters/connect"), []byte("-"), 0)
		_, _ = d.run()(ctx, d.tool("/sbin/rmmod"), "hb-rf-eth")
		logf("stop: HB-RF-ETH disconnected")
	}
}
