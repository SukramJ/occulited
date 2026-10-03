package main

import "testing"

// task 9: --version and the journal's start line name the version and the commit beside it
func TestVersionLine(t *testing.T) {
	v, c := version, commit
	t.Cleanup(func() { version, commit = v, c })
	for _, tc := range []struct{ version, commit, want string }{
		{"1.0.0-dev.38", "fa42dfde1e31fb074df53220dd573ceb92642ff0", "occulited 1.0.0-dev.38 (fa42dfde1e31fb074df53220dd573ceb92642ff0)"},
		{"1.0.0-dev.38-5-gfa42dfd-dirty", "fa42dfde1e31fb074df53220dd573ceb92642ff0", "occulited 1.0.0-dev.38-5-gfa42dfd-dirty (fa42dfde1e31fb074df53220dd573ceb92642ff0)"},
		{"dev", "", "occulited dev"},
	} {
		version, commit = tc.version, tc.commit
		if got := versionLine(); got != tc.want {
			t.Errorf("versionLine() = %q, want %q", got, tc.want)
		}
	}
}
