package shares

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// openccu-lite B-223: what root writes on a share - the journal's copies, which lite-journal-sync
// writes as root - is read by the daemon as its own user (occulite): journalctl reads the copies for
// the Log page with that identity, never as root. On an NFS export that maps root to nobody
// (root_squash, TrueNAS' default) the folders root makes belong to nobody, 0770, and occulite cannot
// enter them: the copies are written, and the Log page cannot show them. The share's Test and the
// journal's write test ask ReadBack after root's write, and answer StateUnreadable with the hint.

// StateUnreadable: root can write the share (or the folder), and the system's own user cannot read
// what root wrote there.
const StateUnreadable = "unreadable"

// UnreadableHint is the API's words for the fix, for any NFS server; the UI has its own, translated.
const UnreadableHint = "The system reads the copies as its own user, not as root: on an export that maps root to nobody (root_squash), map root to root (TrueNAS: Maproot User root) or map all users to one account (TrueNAS: Mapall User)."

// ErrUnreadable wraps the permission error ReadBack met.
var ErrUnreadable = errors.New("the system's own user cannot read what root wrote here")

// readBackFiles bounds the files ReadBack opens, readBackDirs the folders it lists: a look, not a
// walk of the share.
const (
	readBackFiles = 4
	readBackDirs  = 16
)

// ReadBack checks, as the calling process (the daemon's user), that dir and what is in it can be
// read: the folder listed, its folders one level down listed (the journal's copies are in
// <dir>/<machine-id>/), and the first few files opened and a byte read. Only a permission error is
// an answer (ErrUnreadable, wrapping it); a folder that is not there yet, an empty one or any other
// error is nil - that is the write test's to say, not this.
func ReadBack(dir string) error {
	files, dirs := 0, 0
	var look func(d string, depth int) error
	look = func(d string, depth int) error {
		entries, err := os.ReadDir(d)
		if err != nil {
			return denied(d, err)
		}
		dirs++
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".occulite-write-test-") {
				continue
			}
			p := filepath.Join(d, e.Name())
			switch {
			case e.Type().IsRegular() && files < readBackFiles:
				files++
				if err := readByte(p); err != nil {
					return denied(p, err)
				}
			case e.IsDir() && depth < 1 && dirs < readBackDirs:
				if err := look(p, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return look(dir, 0)
}

func readByte(p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [1]byte
	if _, err := f.Read(b[:]); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// denied is ErrUnreadable for a permission error, nil for anything else.
func denied(p string, err error) error {
	if errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) {
		return fmt.Errorf("%w: %s: %w", ErrUnreadable, p, syscall.EACCES)
	}
	return nil
}
