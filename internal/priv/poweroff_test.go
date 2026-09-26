package priv

import "testing"

// The halt of the power menu: busybox's poweroff at its two paths is on the program list, and
// nothing else that stops the machine came along with it.
func TestPoweroffOnTheProgramList(t *testing.T) {
	p := DefaultPolicy("/", "/usr/local/etc/occulite")
	for _, c := range []struct {
		prog string
		args []string
		want bool
	}{
		{"/sbin/poweroff", nil, true},
		{"/bin/poweroff", nil, true},
		{"systemctl", []string{"poweroff"}, true}, // `systemctl poweroff` on a systemd box
		{"poweroff", nil, false},
		{"/usr/sbin/poweroff", nil, false},
		{"/sbin/halt", nil, false},
		{"/bin/halt", nil, false},
		{"/sbin/shutdown", nil, false},
		{"/sbin/init", nil, false},
		// B-234: the applets take nothing, and systemctl's other ways to stop the machine are not
		// verbs the daemon has
		{"/sbin/poweroff", []string{"-f"}, false},
		{"systemctl", []string{"halt"}, false},
		{"systemctl", []string{"kexec"}, false},
		{"systemctl", []string{"poweroff", "--force", "--force"}, false},
	} {
		if got := p.programAllowed(c.prog, c.args); got != c.want {
			t.Errorf("%s %v allowed = %v, want %v", c.prog, c.args, got, c.want)
		}
	}
}
