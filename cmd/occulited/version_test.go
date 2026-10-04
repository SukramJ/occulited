package main

import "testing"

// task 16: --version and the journal's start line name the version, which is the commit; the commit
// goes beside it only where the version does not begin with it (a VERSION= override)
func TestVersionLine(t *testing.T) {
	v, c := version, commit
	t.Cleanup(func() { version, commit = v, c })
	const sha = "fa42dfde1e31fb074df53220dd573ceb92642ff0"
	for _, tc := range []struct{ version, commit, want string }{
		{sha, sha, "occulited " + sha},
		{sha + "-dirty", sha, "occulited " + sha + "-dirty"},
		{sha + "-hot", sha, "occulited " + sha + "-hot"},
		{"lab-build", sha, "occulited lab-build (" + sha + ")"},
		{"dev", "", "occulited dev"},
	} {
		version, commit = tc.version, tc.commit
		if got := versionLine(); got != tc.want {
			t.Errorf("versionLine() = %q, want %q", got, tc.want)
		}
	}
}
