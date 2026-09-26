package priv

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// The helper's file operations follow no link at all (openccu-lite B-235). The Server has
// resolved the path through the image's own links already (Policy.resolve) and checked the
// result; what Local does with it must not be redirected by a link that appeared in between.
// So a path is opened component by component with O_NOFOLLOW on a directory descriptor (the
// same guarantee as openat2's RESOLVE_NO_SYMLINKS, on any kernel), and the file is created,
// renamed, unlinked or changed relative to that descriptor. A link anywhere in the path is an
// error, never a detour. As root without a helper (development, an old image) Local keeps
// following links, as before: there is no boundary to hold then.

// openDir opens dir without following a link at any component, creating the missing
// directories with mode when create is set. The caller closes the descriptor.
func openDir(dir string, create bool, mode os.FileMode) (int, error) {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		return -1, fmt.Errorf("%s: not an absolute path", dir)
	}
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, &os.PathError{Op: "open", Path: "/", Err: err}
	}
	for _, part := range strings.Split(strings.TrimPrefix(dir, "/"), "/") {
		if part == "" {
			continue
		}
		next, err := unix.Openat(fd, part, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) && create {
			if merr := unix.Mkdirat(fd, part, uint32(mode.Perm())); merr != nil && !errors.Is(merr, unix.EEXIST) {
				unix.Close(fd)
				return -1, &os.PathError{Op: "mkdir", Path: dir, Err: merr}
			}
			next, err = unix.Openat(fd, part, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		unix.Close(fd)
		if err != nil {
			if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
				err = fmt.Errorf("a symlink in the path: %w", err)
			}
			return -1, &os.PathError{Op: "open", Path: dir, Err: err}
		}
		fd = next
	}
	return fd, nil
}

// splitPath is dir and base of a cleaned absolute path.
func splitPath(path string) (string, string) {
	path = filepath.Clean(path)
	return filepath.Dir(path), filepath.Base(path)
}

func (Local) writeFileNoFollow(path string, data []byte, mode os.FileMode) error {
	dir, base := splitPath(path)
	if base == "/" || base == "." {
		return fmt.Errorf("%s: not a file", path)
	}
	dfd, err := openDir(dir, true, 0o755)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	var rnd [4]byte
	_, _ = rand.Read(rnd[:])
	tmp := "." + base + "." + hex.EncodeToString(rnd[:])
	fd, err := unix.Openat(dfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return &os.PathError{Op: "create", Path: filepath.Join(dir, tmp), Err: err}
	}
	f := os.NewFile(uintptr(fd), filepath.Join(dir, tmp))
	fail := func(err error) error {
		f.Close()
		_ = unix.Unlinkat(dfd, tmp, 0)
		return err
	}
	if _, err := f.Write(data); err != nil {
		return fail(err)
	}
	if err := f.Chmod(mode); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return err
	}
	if err := unix.Renameat(dfd, tmp, dfd, base); err != nil {
		_ = unix.Unlinkat(dfd, tmp, 0)
		return &os.LinkError{Op: "rename", Old: tmp, New: path, Err: err}
	}
	return nil
}

func (Local) touchNoFollow(path string, mode os.FileMode) error {
	dir, base := splitPath(path)
	dfd, err := openDir(dir, true, 0o755)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	fd, err := unix.Openat(dfd, base, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, uint32(mode.Perm()))
	if err != nil {
		return &os.PathError{Op: "open", Path: path, Err: err}
	}
	return unix.Close(fd)
}

func (Local) removeNoFollow(path string) error {
	dir, base := splitPath(path)
	dfd, err := openDir(dir, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	err = unix.Unlinkat(dfd, base, 0)
	if errors.Is(err, unix.EISDIR) {
		err = unix.Unlinkat(dfd, base, unix.AT_REMOVEDIR)
	}
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return &os.PathError{Op: "remove", Path: path, Err: err}
	}
	return nil
}

// removeAllNoFollow: the parent opened without following anything, then os.RemoveAll through
// the descriptor's /proc entry - that path names exactly the directory held open, and RemoveAll
// itself descends with O_NOFOLLOW.
func (Local) removeAllNoFollow(path string) error {
	dir, base := splitPath(path)
	dfd, err := openDir(dir, false, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	return os.RemoveAll(fmt.Sprintf("/proc/self/fd/%d/%s", dfd, base))
}

func (Local) mkdirAllNoFollow(path string, mode os.FileMode) error {
	fd, err := openDir(path, true, mode)
	if err != nil {
		return err
	}
	return unix.Close(fd)
}

func (Local) symlinkNoFollow(target, link string) error {
	dir, base := splitPath(link)
	dfd, err := openDir(dir, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	if err := unix.Unlinkat(dfd, base, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return &os.PathError{Op: "remove", Path: link, Err: err}
	}
	if err := unix.Symlinkat(target, dfd, base); err != nil {
		return &os.LinkError{Op: "symlink", Old: target, New: link, Err: err}
	}
	return nil
}

func (Local) renameNoFollow(src, dst string) error {
	sdir, sbase := splitPath(src)
	ddir, dbase := splitPath(dst)
	sfd, err := openDir(sdir, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(sfd)
	dfd, err := openDir(ddir, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	// the entry itself is never a link: a link moved into place would point wherever the
	// daemon chose (the Server checks this too; here against the descriptor)
	var st unix.Stat_t
	if err := unix.Fstatat(sfd, sbase, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "rename", Path: src, Err: err}
	}
	if st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: errors.New("the source is a symlink")}
	}
	if err := unix.Renameat(sfd, sbase, dfd, dbase); err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}

func (Local) chmodNoFollow(path string, mode os.FileMode) error {
	dir, base := splitPath(path)
	dfd, err := openDir(dir, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	// there is no fchmodat without following on Linux: open the entry itself (a link is refused
	// by O_NOFOLLOW) and change the mode through the descriptor
	fd, err := unix.Openat(dfd, base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return &os.PathError{Op: "chmod", Path: path, Err: err}
	}
	defer unix.Close(fd)
	if err := unix.Fchmod(fd, uint32(mode.Perm())); err != nil {
		return &os.PathError{Op: "chmod", Path: path, Err: err}
	}
	return nil
}

func (Local) lchownNoFollow(path string, uid, gid int) error {
	dir, base := splitPath(path)
	dfd, err := openDir(dir, false, 0)
	if err != nil {
		return err
	}
	defer unix.Close(dfd)
	if err := unix.Fchownat(dfd, base, uid, gid, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return &os.PathError{Op: "chown", Path: path, Err: err}
	}
	return nil
}
