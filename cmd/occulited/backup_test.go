package main

import "testing"

// openccu-lite task 86: the pipeline's command line takes a step and an instance of the unit's
// shape and nothing else.
func TestBackupArgs(t *testing.T) {
	for _, tc := range []struct {
		args            []string
		step, inst, dir string
		ok              bool
	}{
		{[]string{"create", "nightly"}, "create", "nightly", "/usr/local/etc/occulite", true},
		{[]string{"deliver", "tabc1234", "--state-dir", "/tmp/s"}, "deliver", "tabc1234", "/tmp/s", true},
		{[]string{"create", "all"}, "create", "all", "/usr/local/etc/occulite", true},
		{[]string{"create"}, "", "", "", false},
		{[]string{"restore", "all"}, "", "", "", false},
		{[]string{"create", "../x"}, "", "", "", false},
		{[]string{"create", "all", "extra"}, "", "", "", false},
		{[]string{"create", "all", "--force"}, "", "", "", false},
		{[]string{"create", "all", "--state-dir"}, "", "", "", false},
		{[]string{"create", "all", "--state-dir", "rel"}, "", "", "", false},
	} {
		step, inst, dir, err := backupArgs(tc.args)
		if (err == nil) != tc.ok || step != tc.step || inst != tc.inst || dir != tc.dir {
			t.Errorf("%q: %q %q %q %v", tc.args, step, inst, dir, err)
		}
	}
}
