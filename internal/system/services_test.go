package system

import "testing"

// openccu-lite task 243: lighttpd is a core service, shown with system services hidden, and the
// three units the web interface runs through are the ones whose stop would cut the page off.
func TestCoreAndUIServices(t *testing.T) {
	for id, want := range map[string]string{"lighttpd": "core", "occulited": "core", "occulited-helper": "core", "rfd": "core", "sshd": "system", "addon-x": "addon", "occu-persist": "occu"} {
		if got := categoryOf(id); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	for id, want := range map[string]bool{"lighttpd": true, "lighttpd.service": true, "occulited": true, "occulited-helper": true, "rfd": false, "sshd": false} {
		if CutsUI(id) != want {
			t.Errorf("CutsUI(%s) = %v", id, !want)
		}
	}
}
