package priv

import (
	"os"
	"strings"
)

// The files of the named operations are outside the generic ones (openccu-lite B-238).
//
// /etc/config/ and /usr/local/etc/config/ are write prefixes (Paths), and the files that have an
// operation of their own lie below them: /etc/config/shadow (SetRootPasswordHash rewrites one field
// of root's line, B-15) and /etc/config/server.pem with its two markers (WriteCertificate checks
// the chain and sets root:certs 0640, task 35). Through the prefix the generic write, touch,
// rename, chmod, chown, symlink and removeall reached them too - shadow could be replaced whole
// with any hash for root, the certificate with any bytes and any mode. A named operation exists so
// that a general one does not have to (the threat model's B4); its narrowness has to hold at the
// boundary, not by the daemon's good behaviour.
//
// So every path a named operation owns - ShadowPaths, CertPaths, AuthorizedKeys, AccountFiles, in
// both spellings of the config directory - is refused by pathAllowed, whatever the operation and
// whatever prefix would admit it. The directories that hold such a file are refused as a whole
// for the operations that act on a directory and everything in it: rename (moving the directory
// away, writing the file there, moving it back), removeall, chown and remove; and chmod there may
// only take permission bits away (a 0777 config directory would let the daemon's user replace
// shadow without the helper; cleanup.go's hardening takes the world-writable bit off it).
//
// Since openccu-lite B-293 there is no exception: the certificate and its markers go through
// RemoveCertificate, and the files root runs or obeys are named-only as well (rootobeyed.go): the
// addon policy drop-ins, start orders and early starts, everything in rc.d and in the addon web
// trees, and what their entries' links lead to.

// Config directory spellings: /etc/config is a link to /usr/local/etc/config on the image, and the
// helper checks a path both as given and as it resolves (B-235), so a named file is listed in both.
var configDirSpellings = [2]string{"/etc/config/", "/usr/local/etc/config/"}

// otherSpelling is rel under the other spelling of the config directory, "" when rel is under
// neither.
func otherSpelling(rel string) string {
	for i, d := range configDirSpellings {
		if rest, ok := strings.CutPrefix(rel, d); ok {
			return configDirSpellings[1-i] + rest
		}
	}
	return ""
}

// namedPaths is every path a named operation owns, in both spellings.
func (p Policy) namedPaths() []string {
	var out []string
	add := func(paths ...string) {
		for _, n := range paths {
			if n == "" {
				continue
			}
			out = append(out, n)
			if o := otherSpelling(n); o != "" {
				out = append(out, o)
			}
		}
	}
	add(p.ShadowPaths...)
	add(p.CertPaths...)
	add(p.AuthorizedKeys)
	add(p.AccountFiles...)
	return out
}

// namedOnly: rel (relative to the root, clean) is a file of a named operation, or one root runs or
// obeys (B-293, rootobeyed.go).
func (p Policy) namedOnly(rel string) bool {
	for _, n := range p.namedPaths() {
		if rel == n {
			return true
		}
	}
	return p.rootObeyed(rel) || p.addonConfigClosed(rel)
}

// holdsNamed: rel is a directory a file of a named operation lies in, at any depth - or one of the
// directories whose entries root runs or obeys, or one above them.
func (p Policy) holdsNamed(rel string) bool {
	if rel == "/" {
		return true
	}
	dir := strings.TrimSuffix(rel, "/") + "/"
	for _, n := range p.namedPaths() {
		if strings.HasPrefix(n, dir) {
			return true
		}
	}
	for _, d := range p.rootObeyedDirs() {
		if strings.HasPrefix(d, dir) {
			return true
		}
	}
	return false
}

// wholeAllowed is pathAllowed for an operation that acts on a directory with everything in it: a
// directory holding a named file is refused.
func (p Policy) wholeAllowed(path string) bool {
	if !p.pathAllowed(path) {
		return false
	}
	rel, ok := p.rel(path)
	return ok && !p.holdsNamed(rel)
}

// chmodAllowed is pathAllowed, and for a directory that holds a named file a mode that adds no bit
// to the one it has: taking permissions away is allowed there, granting any is not.
func (p Policy) chmodAllowed(path string, mode os.FileMode) bool {
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") {
		return false
	}
	// B-295: in a root addon's config directory a chmod that only takes bits away stays (the first
	// boot's hardening of world-writable directories, cleanup.go); nothing there gains a bit
	if p.addonConfigClosed(rel) && !p.rootObeyed(rel) && p.onPaths(rel) {
		fi, err := os.Lstat(path)
		return err == nil && fi.Mode()&os.ModeSymlink == 0 && mode.Perm()&^fi.Mode().Perm() == 0
	}
	if !p.pathAllowed(path) {
		return false
	}
	if !p.holdsNamed(rel) {
		return true
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	// the operation sets the permission bits alone (Fchmod with mode.Perm()), never a special one
	return mode.Perm()&^fi.Mode().Perm() == 0
}

// onPaths: a Paths entry admits rel, named files aside.
func (p Policy) onPaths(rel string) bool {
	for _, a := range p.Paths {
		if pathEntryAllows(a, rel) {
			return true
		}
	}
	return false
}
