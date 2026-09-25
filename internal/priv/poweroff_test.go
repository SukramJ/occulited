package priv

import "testing"

// The halt of the power menu: busybox's poweroff at its two paths is on the program list, and
// nothing else that stops the machine came along with it.
func TestPoweroffOnTheProgramList(t *testing.T) {
	p := DefaultPolicy("/", "/usr/local/etc/occulite")
	for prog, want := range map[string]bool{
		"/sbin/poweroff":     true,
		"/bin/poweroff":      true,
		"systemctl":          true, // `systemctl poweroff` on a systemd box
		"poweroff":           false,
		"/usr/sbin/poweroff": false,
		"/sbin/halt":         false,
		"/bin/halt":          false,
		"/sbin/shutdown":     false,
		"/sbin/init":         false,
	} {
		if got := p.programAllowed(prog, nil); got != want {
			t.Errorf("%s allowed = %v, want %v", prog, got, want)
		}
	}
}
