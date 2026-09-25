package priv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// The database's snapshot on a USB stick (openccu-lite task 229). occulited may write only its own
// directory; the stick is root's. The daemon writes a consistent copy of its bbolt file into RAM
// (its own private /tmp) and hands the helper the open file as a descriptor with the request - the
// helper never opens a path the daemon names - to put it into a folder on a stick as
// StoreSnapshotName: a partial file created exclusively, synced, renamed, the folder synced. Only a folder of up to four plain levels on a stick mounted at
// /media/usb1…8, never through a link, and only while a filesystem other than /media's own is
// mounted there - a pulled stick leaves the tmpfs, which would take the copy and lose it.
const opStoreCopy = "storecopy"

// StoreSnapshotName is the file on the stick.
var StoreSnapshotName = "occulited-store.db"

// storeCopyMountCheck is off in tests only: their stick is a directory, not a mount.
var storeCopyMountCheck = true

var storeCopyDirRe = regexp.MustCompile(`^/media/usb[1-8](/[A-Za-z0-9_-][A-Za-z0-9._-]{0,63}){1,4}$`)

// storeCopyAllowed checks the folder: its shape, no link on the way, a stick mounted there.
func (p Policy) storeCopyAllowed(dir string) error {
	if dir == "" || filepath.Clean(dir) != dir || !filepath.IsAbs(dir) {
		return errors.New("not a clean absolute path")
	}
	rel, ok := p.rel(dir)
	if !ok || !storeCopyDirRe.MatchString(rel) {
		return errors.New("not a folder on a USB stick (/media/usb1…8)")
	}
	parts := strings.Split(strings.TrimPrefix(rel, "/"), "/")
	mount := filepath.Join(p.Root, "/"+parts[0]+"/"+parts[1])
	st, err := os.Lstat(mount)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", mount)
	}
	if storeCopyMountCheck {
		var a, b syscall.Stat_t
		if syscall.Stat(mount, &a) != nil || syscall.Stat(filepath.Dir(mount), &b) != nil || a.Dev == b.Dev {
			return fmt.Errorf("no USB stick is mounted at %s", "/"+parts[0]+"/"+parts[1])
		}
	}
	// the folders below the mount that exist: directories, not links
	p2 := mount
	for _, seg := range parts[2:] {
		p2 = filepath.Join(p2, seg)
		st, err := os.Lstat(p2)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("%s is not a directory", p2)
		}
	}
	return nil
}

func (s *Server) storeCopy(req request, in *os.File) response {
	if !reflect.DeepEqual(req, request{Op: req.Op, Path: req.Path}) {
		return refuse("storecopy takes a folder and nothing else")
	}
	if in == nil {
		return refuse("storecopy takes the snapshot as a descriptor")
	}
	if err := s.Policy.storeCopyAllowed(req.Path); err != nil {
		s.log("helper: refused a store copy to %s: %v", req.Path, err)
		return refuse("storecopy: " + err.Error())
	}
	if err := s.ops().StoreCopy(in, req.Path); err != nil {
		return response{Error: err.Error()}
	}
	return response{OK: true}
}

// StoreCopy copies the snapshot in (read from its start) into dir as StoreSnapshotName (the folders
// made as needed).
func (Local) StoreCopy(in *os.File, dir string) error {
	if st, err := in.Stat(); err != nil || !st.Mode().IsRegular() {
		return fmt.Errorf("the snapshot is not a file")
	}
	if _, err := in.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	partial := filepath.Join(dir, "."+StoreSnapshotName+".partial")
	_ = os.Remove(partial)
	out, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(partial)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(partial)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		return err
	}
	if err := os.Rename(partial, filepath.Join(dir, StoreSnapshotName)); err != nil {
		os.Remove(partial)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// StoreCopy is the client side: the open snapshot travels as a descriptor with the request.
func (c Client) StoreCopy(in *os.File, dir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := c.callWith(ctx, request{Op: opStoreCopy, Path: dir}, in)
	return err
}
