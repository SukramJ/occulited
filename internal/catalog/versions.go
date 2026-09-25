package catalog

import (
	"regexp"
	"strconv"
	"strings"
)

// The version comparisons the catalogue and the shell use: whether a release is an update of an
// installed version, and whether an installed version is at least a given one.

var dottedRe = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// UpdateAvailable compares an installed version (the `version` line of the addon's rc.d `info`)
// with the latest release the resolver picked. The comparison is deliberately loose: a leading
// "v" is ignored, two plain dotted numbers are compared numerically (so 1.10 is newer than 1.9
// and an installed build that is *ahead* of the release does not ask for a downgrade), and
// anything else — "2.1.2+1", "3.5.2-beta", a date, a git hash — counts as an update when the two
// strings differ. An unknown version on either side is never an update.
//
// The one case the plain "they differ" rule got wrong is a prerelease on the *release* side
// (B-22): installed "3.6.0", latest "3.6.0-beta" offered a downgrade to a beta of what the box
// already runs. When both sides share a dotted core and only the release carries a prerelease
// suffix, there is no update. The other direction — installed "3.6.0-beta", latest "3.6.0" — is
// an update and stays one.
func UpdateAvailable(installed, latest string) bool {
	a, b := strings.TrimPrefix(strings.TrimSpace(installed), "v"), strings.TrimPrefix(strings.TrimSpace(latest), "v")
	if a == "" || b == "" || a == b {
		return false
	}
	if dottedRe.MatchString(a) && dottedRe.MatchString(b) {
		return compareDotted(b, a) > 0
	}
	ac, apre := splitPrerelease(a)
	bc, bpre := splitPrerelease(b)
	if dottedRe.MatchString(ac) && dottedRe.MatchString(bc) {
		if c := compareDotted(bc, ac); c != 0 {
			return c > 0
		}
		// same core: a prerelease is older than the release it precedes
		if apre == "" && bpre != "" {
			return false
		}
	}
	return true
}

// splitPrerelease cuts a version at the first "-": "3.6.0-beta.1" → "3.6.0", "beta.1". A "+build"
// suffix is left where it is; it is not an ordering, and mosquitto's "2.1.2+3" is a real version.
func splitPrerelease(v string) (core, pre string) {
	if c, p, ok := strings.Cut(v, "-"); ok {
		return c, p
	}
	return v, ""
}

// compareDotted compares two dotted number strings segment by segment; a missing segment is 0,
// so 1.2 and 1.2.0 are equal.
func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// VersionAtLeast says whether an installed version is since or newer (task 88: from which version on
// an addon reads the gate's session header). It orders by the rules UpdateAvailable compares with - a
// leading "v" ignored, the dotted core numerically, a prerelease older than the release it precedes -
// and, where UpdateAvailable only needs "the release differs", it also orders prereleases of one core:
// their dot-separated identifiers compare as numbers when both are digits and as text otherwise, digits
// before text, the shorter list first (3.0.0-beta.9 < 3.0.0-beta.16 < 3.0.0-rc.1 < 3.0.0). A "+build"
// suffix counts only between two otherwise equal versions (2.1.2 < 2.1.2+2 < 2.1.2+3). What it cannot
// order - an empty version, a date, a git hash, two different builds that are no numbers - is not at
// least: the caller stays on the safe side.
func VersionAtLeast(installed, since string) bool {
	c, ok := compareVersions(installed, since)
	return ok && c >= 0
}

var digitsRe = regexp.MustCompile(`^[0-9]+$`)

// compareVersions orders two versions for VersionAtLeast; ok is false when they cannot be ordered.
func compareVersions(a, b string) (c int, ok bool) {
	a, b = strings.TrimPrefix(strings.TrimSpace(a), "v"), strings.TrimPrefix(strings.TrimSpace(b), "v")
	if a == "" || b == "" {
		return 0, false
	}
	a, abuild, _ := strings.Cut(a, "+")
	b, bbuild, _ := strings.Cut(b, "+")
	ac, apre := splitPrerelease(a)
	bc, bpre := splitPrerelease(b)
	if !dottedRe.MatchString(ac) || !dottedRe.MatchString(bc) {
		return 0, false
	}
	if c := compareDotted(ac, bc); c != 0 {
		return c, true
	}
	switch {
	case apre == bpre:
	case apre == "":
		return 1, true
	case bpre == "":
		return -1, true
	default:
		if c := comparePrerelease(apre, bpre); c != 0 {
			return c, true
		}
	}
	switch {
	case abuild == bbuild:
		return 0, true
	case bbuild == "":
		return 1, true
	case abuild == "":
		return -1, true
	case dottedRe.MatchString(abuild) && dottedRe.MatchString(bbuild):
		return compareDotted(abuild, bbuild), true
	}
	return 0, false
}

// comparePrerelease orders two prerelease suffixes ("beta.15", "beta.16", "rc.1") identifier by identifier.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, y := as[i], bs[i]
		xd, yd := digitsRe.MatchString(x), digitsRe.MatchString(y)
		switch {
		case xd && yd:
			if c := compareDotted(strings.TrimLeft(x, "0")+"0", strings.TrimLeft(y, "0")+"0"); c != 0 {
				return c
			}
		case xd:
			return -1
		case yd:
			return 1
		default:
			if c := strings.Compare(x, y); c != 0 {
				return c
			}
		}
	}
	return cmpInt(len(as), len(bs))
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
