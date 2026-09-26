package priv

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Symlinks at the boundary (openccu-lite B-235).
//
// pathAllowed looks at the string it is given. A write to /usr/local/tmp/x is allowed by prefix
// - and if /usr/local/tmp/x is a symlink to /etc/passwd, WriteFile used to write /etc/passwd as
// root (it resolves links first, for B-53's /etc/hostname -> /var/etc/hostname). The daemon
// could plant that link itself (the symlink operation checked the link's path, not its target),
// and an addon can plant one anywhere it owns a directory the daemon writes into.
//
// So the helper resolves every path it is about to act on itself (Policy.resolve), component by
// component with Lstat, and follows a link only when the link is one the image put there: owned
// by root, in a directory only root writes, outside removable media. A link an addon or the
// daemon planted is refused by name. The resolved path is checked against the policy again, and
// the operation then runs on the resolved path without following anything (Local.noFollow: every
// component opened O_NOFOLLOW, the file created or renamed through the directory's descriptor), so
// nothing swapped in between the check and the write is followed either.

// UntrustedLinkDirs are trees whose symlinks are never followed, whoever owns them: removable
// media carry whatever owner and link their filesystem says.
var UntrustedLinkDirs = []string{"/media/", "/mnt/"}

// resolve walks path and follows only trusted links. With final false the last component is left
// as it is - for remove, rename and symlink, which act on the entry itself and never follow it.
// The result is absolute and free of links at the time of the walk; it may not exist yet.
func (p Policy) resolve(path string, final bool) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s: not an absolute path", path)
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	cur := "/"
	for i, part := range parts {
		if part == "" {
			continue
		}
		next := filepath.Join(cur, part)
		last := i == len(parts)-1
		if last && !final {
			cur = next
			break
		}
		for hops := 0; ; hops++ {
			fi, err := os.Lstat(next)
			if err != nil || fi.Mode()&os.ModeSymlink == 0 {
				break
			}
			if hops >= 40 {
				return "", fmt.Errorf("%s: too many links", path)
			}
			if err := p.trustedLink(next, fi); err != nil {
				return "", err
			}
			target, err := os.Readlink(next)
			if err != nil {
				return "", err
			}
			// an absolute target is taken as it is - under a test root the links carry the
			// root's absolute paths, on a box the root is /
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(next), target)
			}
			next = filepath.Clean(target)
		}
		cur = next
	}
	return cur, nil
}

// trustedLink says whether the symlink at link may be followed: not under UntrustedLinkDirs, owned
// by the helper's own user (root on a box), and in a directory that user owns and nobody else
// writes (group-writable only for root's group). The image's links - /etc/config, /etc/hostname,
// /etc/hosts, /var/run - pass; a link an addon made in its tree, a link the daemon made in a
// directory of its own, and a link on a USB stick do not.
func (p Policy) trustedLink(link string, fi os.FileInfo) error {
	rel, ok := p.rel(link)
	if !ok {
		return fmt.Errorf("%s: a link outside the root", link)
	}
	for _, d := range UntrustedLinkDirs {
		if strings.HasPrefix(rel, d) {
			return fmt.Errorf("%s: a link on removable media is not followed", link)
		}
	}
	me := uint32(os.Geteuid())
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != me {
		return fmt.Errorf("%s: a link not owned by the helper's user is not followed", link)
	}
	dir, err := os.Lstat(filepath.Dir(link))
	if err != nil {
		return err
	}
	dst, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || dst.Uid != me {
		return fmt.Errorf("%s: a link in a directory not owned by the helper's user is not followed", link)
	}
	mode := dir.Mode().Perm()
	if mode&0o002 != 0 || (mode&0o020 != 0 && dst.Gid != 0 && dst.Gid != uint32(os.Getegid())) {
		return fmt.Errorf("%s: a link in a directory others may write is not followed", link)
	}
	return nil
}

// symlinkTargetAllowed: the target of a link the daemon asks for, taken absolute against the
// link's directory, must lie where the daemon may write anyway - the one legitimate link is
// /usr/local/.firmwareUpdate -> /usr/local/tmp/<image> (sysupdate.go).
func (p Policy) symlinkTargetAllowed(link, target string) bool {
	if target == "" || strings.ContainsRune(target, 0) {
		return false
	}
	abs := target
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(filepath.Dir(link), target)
	}
	if p.pathAllowed(abs) {
		return true
	}
	// the daemon writes the target as the recovery system will read it on the mounted userfs -
	// an absolute path without a development root in front; under such a root it is taken as
	// relative to it
	root := filepath.Clean(p.Root)
	return root != "/" && root != "" && filepath.IsAbs(target) && p.pathAllowed(filepath.Join(root, target))
}

// isSymlink: the entry itself is a link (rename's source must not be one - a link moved into an
// allowed place would point wherever the daemon chose).
func isSymlink(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
