package catalog

import "testing"

func TestUpdateAvailable(t *testing.T) {
	for _, c := range []struct {
		installed, latest string
		want              bool
	}{
		{"1.2.0", "1.2.0", false},
		{"v1.2.0", "1.2.0", false},   // the leading v is noise
		{"1.2", "1.2.0", false},      // a missing segment is zero
		{"1.9", "1.10", true},        // dotted numbers compare numerically, not as strings
		{"1.10", "1.9", false},       // never offer a downgrade when both parse
		{"", "1.2.0", false},         // unknown installed version: no claim
		{"1.2.0", "", false},         // unknown release: no claim
		{"2.1.2+1", "2.1.2+2", true}, // not plain dotted numbers: different means update
		{"2.1.2+1", "2.1.2+1", false},
		{"3.5.2-beta", "3.5.2", true},
		{"2026-09-05", "2026-09-06", true},
		// a prerelease on the release side is not an update (B-22)
		{"3.6.0", "3.6.0-beta", false},
		{"3.8.0", "3.7.0-rc1", false}, // an older core, prerelease or not, is a downgrade
		{"3.7.0", "3.8.0-rc1", true},  // a newer core is an update even as a prerelease
		{"3.6.0-beta", "3.6.0", true}, // and the other direction still is one
	} {
		if got := UpdateAvailable(c.installed, c.latest); got != c.want {
			t.Errorf("UpdateAvailable(%q, %q) = %v, want %v", c.installed, c.latest, got, c.want)
		}
	}
}

// task 88 (D-67): from which installed version on an addon reads the gate's session header - kept
// for the policies the old catalogue wrote (runtime.session.header_since)
func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		installed, since string
		want             bool
	}{
		{"9.7.3", "9.7.3", true},
		{"9.7.2", "9.7.3", false},
		{"9.7.4", "9.7.3", true},
		{"9.10.0", "9.7.3", true}, // numerically, not as text
		{"10.0.0", "9.7.3", true},
		{"v9.7.3", "9.7.3", true}, // the leading v is noise, as for the update check
		{"9.7", "9.7.0", true},    // a missing segment is zero
		{"3.0.0-beta.16", "3.0.0-beta.16", true},
		{"3.0.0-beta.15", "3.0.0-beta.16", false},
		{"3.0.0-beta.17", "3.0.0-beta.16", true},
		{"3.0.0-beta.9", "3.0.0-beta.16", false}, // prerelease numbers numerically
		{"3.0.0-rc.1", "3.0.0-beta.16", true},    // text in order
		{"3.0.0", "3.0.0-beta.16", true},         // the release is after its prereleases
		{"3.0.0-beta.16", "3.0.0", false},
		{"3.0.1-beta.1", "3.0.0", true},       // a newer core wins, prerelease or not
		{"2.9.9", "3.0.0-beta.16", false},     // an older core loses
		{"3.0.0-beta", "3.0.0-beta.1", false}, // the shorter list first
		{"3.0.0-beta.1", "3.0.0-beta", true},
		{"3.0.0-beta.1", "3.0.0-1", true}, // digits before text
		{"2.1.2+3", "2.1.2", true},        // a build counts only between equal versions
		{"2.1.2", "2.1.2+3", false},
		{"2.1.2+3", "2.1.2+2", true},
		{"2.1.2+2", "2.1.2+3", false},
		{"2.1.3", "2.1.2+3", true},
		{"2.1.2+abc", "2.1.2+def", false}, // builds that are no numbers cannot be ordered
		{"", "9.7.3", false},              // an unknown version keeps ?sid=
		{"9.7.3", "", false},
		{"unknown", "9.7.3", false},
		{"a1b2c3d", "9.7.3", false},
	} {
		if got := VersionAtLeast(c.installed, c.since); got != c.want {
			t.Errorf("VersionAtLeast(%q, %q) = %v, want %v", c.installed, c.since, got, c.want)
		}
	}
}
