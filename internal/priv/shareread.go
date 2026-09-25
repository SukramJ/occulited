package priv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
)

// Reading a network share as root (openccu-lite B-217). The backups on a share are written by
// root - the create unit and the write test - and on a root-squashed NFS export root is "nobody"
// there: the folders it makes are nobody's, often 0770, and occulited's own user can neither list
// nor open them. The Backup page's list, the restore check and the location picker's folders read
// them through these two operations, as the same identity that wrote them:
//
//   - sharelist: the entries of one folder under /media/net/<id> - names, directory or not, size
//     and time, never content; the folder itself may not be reached through a link (its descriptor
//     must resolve to exactly the path asked for).
//   - shareopen: one backup file (*.sbk, *.sbk.age) under /media/net/<id>, opened read-only and
//     passed back as a descriptor (SCM_RIGHTS, the openfd path of B-35).
const (
	opShareList = "sharelist"
	opShareOpen = "shareopen"
)

// ShareEntry is one entry of a share's folder.
type ShareEntry struct {
	Name  string    `json:"name"`
	Dir   bool      `json:"dir,omitempty"`
	Link  bool      `json:"link,omitempty"`
	Size  int64     `json:"size,omitempty"`
	MTime time.Time `json:"mtime,omitzero"`
}

// ShareListResult is sharelist's answer: the entries, or the errno and the error.
type ShareListResult struct {
	Entries []ShareEntry `json:"entries,omitempty"`
	Errno   string       `json:"errno,omitempty"`
	Error   string       `json:"error,omitempty"`
}

// ShareListMax bounds an answer.
const ShareListMax = 5000

// shareListTimeout bounds a listing in the helper: a share that hangs is abandoned there.
var shareListTimeout = 30 * time.Second

// ShareError is a failed share read, its errno kept so the caller can tell a folder that is not
// there from one it may not read.
type ShareError struct {
	Errno string
	Msg   string
}

func (e *ShareError) Error() string { return e.Msg }

// Unwrap answers the errno, so errors.Is(err, fs.ErrNotExist) and the classifications work.
func (e *ShareError) Unwrap() error {
	if n, ok := errnoByName[e.Errno]; ok {
		return n
	}
	return nil
}

var errnoByName = map[string]syscall.Errno{
	"EACCES": syscall.EACCES, "EPERM": syscall.EPERM, "EROFS": syscall.EROFS, "ENOSPC": syscall.ENOSPC,
	"EDQUOT": syscall.EDQUOT, "EIO": syscall.EIO, "ESTALE": syscall.ESTALE, "ENOTCONN": syscall.ENOTCONN,
	"EHOSTDOWN": syscall.EHOSTDOWN, "EHOSTUNREACH": syscall.EHOSTUNREACH, "ETIMEDOUT": syscall.ETIMEDOUT,
	"ENOENT": syscall.ENOENT, "ENODEV": syscall.ENODEV, "ECONNREFUSED": syscall.ECONNREFUSED,
	"ENOTDIR": syscall.ENOTDIR, "ELOOP": syscall.ELOOP,
}

// IsShareBackupName: the files shareopen hands out - a backup and nothing else on the share.
func IsShareBackupName(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "/\x00") &&
		(strings.HasSuffix(name, ".sbk") || strings.HasSuffix(name, ".sbk.age"))
}

// shareDir answers the share mount point a path lies in or is (/media/net/<id>, in Root).
func (p Policy) shareDir(path string) (string, bool) {
	if path == "" || filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return "", false
	}
	rel, ok := p.rel(path)
	if !ok || strings.Contains(rel, "..") {
		return "", false
	}
	rest, found := strings.CutPrefix(rel, netmount.Base+"/")
	if !found {
		return "", false
	}
	id, _, _ := strings.Cut(rest, "/")
	if !netmount.ValidID(id) {
		return "", false
	}
	return filepath.Join(p.Root, netmount.Base, id), true
}

// shareList checks and performs a listing.
func (s *Server) shareList(ctx context.Context, req request) response {
	if !reflect.DeepEqual(req, request{Op: req.Op, Path: req.Path}) {
		return refuse("sharelist takes a folder and nothing else")
	}
	if _, ok := s.Policy.shareDir(req.Path); !ok {
		s.log("helper: refused a share listing of %s", req.Path)
		return refuse("sharelist " + req.Path)
	}
	r, err := s.ops().ShareList(ctx, req.Path)
	if err != nil {
		return response{Error: err.Error()}
	}
	b, _ := json.Marshal(r)
	return response{OK: true, Stdout: b}
}

// ShareList lists one folder of a share as root, in its own goroutine and bounded: the folder is
// opened without following a link in its last part, and its descriptor must resolve to the path
// asked for - no link anywhere on the way leads the listing elsewhere.
func (Local) ShareList(ctx context.Context, dir string) (ShareListResult, error) {
	done := make(chan ShareListResult, 1)
	go func() { done <- shareList(dir) }()
	t := time.NewTimer(shareListTimeout)
	defer t.Stop()
	select {
	case r := <-done:
		return r, nil
	case <-t.C:
		return ShareListResult{Errno: "ETIMEDOUT", Error: "no answer within " + shareListTimeout.String()}, nil
	case <-ctx.Done():
		return ShareListResult{Errno: "ETIMEDOUT", Error: ctx.Err().Error()}, nil
	}
}

func shareList(dir string) ShareListResult {
	failed := func(err error) ShareListResult {
		return ShareListResult{Errno: ErrnoName(err), Error: err.Error()}
	}
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return failed(err)
	}
	defer f.Close()
	if err := fdIs(f, dir); err != nil {
		return ShareListResult{Errno: "ELOOP", Error: err.Error()}
	}
	entries, err := f.ReadDir(ShareListMax)
	if err != nil && !errors.Is(err, io.EOF) {
		return failed(err)
	}
	out := ShareListResult{Entries: []ShareEntry{}}
	for _, e := range entries {
		se := ShareEntry{Name: e.Name(), Dir: e.IsDir(), Link: e.Type()&os.ModeSymlink != 0}
		if e.Type().IsRegular() {
			if info, err := e.Info(); err == nil {
				se.Size, se.MTime = info.Size(), info.ModTime()
			}
		}
		out.Entries = append(out.Entries, se)
	}
	return out
}

// fdIs reports that the open descriptor names exactly path (the root resolved: a development
// root or a test's temp dir may sit behind a link, the share's folders may not).
func fdIs(f *os.File, path string) error {
	real, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(int(f.Fd())))
	if err != nil {
		return fmt.Errorf("the descriptor cannot be resolved: %w", err)
	}
	want := filepath.Clean(path)
	if real == want {
		return nil
	}
	// the part above /media resolved (a temp root in a test); the rest must match as asked
	i := strings.Index(want, netmount.Base+"/")
	if i > 0 {
		if base, err := filepath.EvalSymlinks(want[:i]); err == nil && real == filepath.Join(base, want[i:]) {
			return nil
		}
	}
	return errors.New("the folder is reached through a link")
}

// shareOpenAllowed: a backup file under a share's mount point.
func (p Policy) shareOpenAllowed(path string) (dir string, ok bool) {
	dir, ok = p.shareDir(path)
	if !ok || !IsShareBackupName(filepath.Base(path)) || filepath.Dir(path) == filepath.Join(p.Root, netmount.Base) {
		return "", false
	}
	return dir, true
}

// ShareList is the client side of sharelist; a failure is a *ShareError carrying the errno.
func (c Client) ShareList(ctx context.Context, dir string) (ShareListResult, error) {
	res, err := c.call(ctx, request{Op: opShareList, Path: dir})
	if err != nil {
		return ShareListResult{}, err
	}
	var out ShareListResult
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return ShareListResult{}, fmt.Errorf("privilege helper: %w", err)
	}
	return out, nil
}

// ShareOpen asks the helper to open a backup file on a share as root and to pass the descriptor
// back (the openfd path: nothing of the file travels through the socket).
func (c Client) ShareOpen(path string) (*os.File, error) { return c.openOp(opShareOpen, path) }

// ShareOpen opens a backup file on a share (the in-process side; the Server checks the path).
func (l Local) ShareOpen(path string) (*os.File, error) { return l.Open(path) }

// ListShare runs a listing through ops and answers the entries, or a *ShareError.
func ListShare(ctx context.Context, ops interface {
	ShareList(ctx context.Context, dir string) (ShareListResult, error)
}, dir string) ([]ShareEntry, error) {
	r, err := ops.ShareList(ctx, dir)
	if err != nil {
		return nil, err
	}
	if r.Error != "" || r.Errno != "" {
		return nil, &ShareError{Errno: r.Errno, Msg: r.Error}
	}
	return r.Entries, nil
}
