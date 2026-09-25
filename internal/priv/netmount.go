package priv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
)

// Backup targets (openccu-lite task 86): a network share's mount units and the write test. The
// mount is PID 1's - the helper runs in a mount namespace of its own (ProtectHome=), so a mount it
// made itself would stay invisible to the rest of the system - and the units are rendered here
// from checked fields (netmount.Spec), never taken as text from the daemon.
const (
	opNetMount       = "netmount"
	opNetUnmount     = "netunmount"
	opNetMountRemove = "netmount-remove"
	opWriteTest      = "writetest"
)

// NetMountCredDir is where a CIFS target's credentials file lives, <dir>/<id>/cifs.cred: the
// daemon's state directory, which the daemon writes and root reads. The path in the unit is this
// one, fixed by the id - the daemon can change how the configured server is authenticated, never
// which file a mount reads.
var NetMountCredDir = "/usr/local/etc/occulite/backup-targets"

// ShareCredDir is the same for a share of the Storage page (task 228; netmount.Spec.Share):
// <dir>/<id>/cifs.cred.
var ShareCredDir = "/usr/local/etc/occulite/shares"

// credFile is the one file a CIFS mount of spec reads its credentials from.
func credFile(s netmount.Spec) string {
	dir := NetMountCredDir
	if s.Share {
		dir = ShareCredDir
	}
	return filepath.Join(dir, s.ID, "cifs.cred")
}

// NetMountGroup is the group a CIFS mount's files get (gid=), so the daemon can list and read what
// root wrote.
var NetMountGroup = "occulite"

// runUnitDir is PID 1's runtime unit directory; a variable for tests.
var runUnitDir = "/run/systemd/system"

// WriteTestResult is the write test's answer: the step it reached, and what it measured.
type WriteTestResult struct {
	OK bool `json:"ok"`
	// Step is where it stopped: mkdir, statfs, create, write, fsync, read, compare, delete,
	// timeout - or done.
	Step  string `json:"step"`
	Error string `json:"error,omitempty"`
	// Errno is the error's name (EACCES, EROFS, ENOSPC, EIO, ESTALE, ...) when there was one.
	Errno      string  `json:"errno,omitempty"`
	FSType     string  `json:"fs_type,omitempty"`
	FreeBytes  int64   `json:"free_bytes"`
	TotalBytes int64   `json:"total_bytes"`
	WriteMBps  float64 `json:"write_mbps,omitempty"`
}

// WriteTestSize is what the test writes, reads back and deletes.
const WriteTestSize = 1 << 20

// writeTestTimeout bounds the whole test: a mount attempt (30 s) plus the file.
const writeTestTimeout = 60 * time.Second

// WriteTestPrefixes are where the write test may run: under /media (USB sticks, the network
// targets), and - for a path there that is a symlink - where such a link may lead: the SD card
// directory upstream links /media/usb0 to, and /usr/local/backup.
var WriteTestPrefixes = []string{"/media/", "/usr/local/sdcard/", "/usr/local/backup/"}

// Client side.

func (c Client) NetMount(ctx context.Context, s netmount.Spec) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = c.call(ctx, request{Op: opNetMount, Data: b})
	return err
}

func (c Client) NetUnmount(ctx context.Context, id string) error {
	_, err := c.call(ctx, request{Op: opNetUnmount, Name: id})
	return err
}

func (c Client) NetMountRemove(ctx context.Context, id string) error {
	_, err := c.call(ctx, request{Op: opNetMountRemove, Name: id})
	return err
}

func (c Client) WriteTest(ctx context.Context, dir string) (WriteTestResult, error) {
	res, err := c.call(ctx, request{Op: opWriteTest, Path: dir})
	if err != nil {
		return WriteTestResult{}, err
	}
	var out WriteTestResult
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return WriteTestResult{}, fmt.Errorf("privilege helper: %w", err)
	}
	return out, nil
}

// Server side.

// netMount checks and performs the four operations.
func (s *Server) netMount(ctx context.Context, req request) response {
	ops := s.ops()
	fail := func(err error) response {
		if err != nil {
			return response{Error: err.Error()}
		}
		return response{OK: true}
	}
	switch req.Op {
	case opNetMount:
		if !reflect.DeepEqual(req, request{Op: req.Op, Data: req.Data}) {
			return refuse("netmount takes a target and nothing else")
		}
		var spec netmount.Spec
		dec := json.NewDecoder(strings.NewReader(string(req.Data)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return refuse("netmount: " + err.Error())
		}
		if err := spec.Validate(); err != nil {
			s.log("helper: refused a mount target: %v", err)
			return refuse("netmount: " + err.Error())
		}
		if err := s.Policy.netMountPointOK(spec.ID); err != nil {
			s.log("helper: refused a mount target: %v", err)
			return refuse("netmount: " + err.Error())
		}
		return fail(ops.NetMount(ctx, spec))
	case opNetUnmount, opNetMountRemove:
		if !reflect.DeepEqual(req, request{Op: req.Op, Name: req.Name}) || !netmount.ValidID(req.Name) {
			return refuse(req.Op + " takes a target id and nothing else")
		}
		// no mount-point check here: both act on the units by name (a link planted at the mount
		// point cannot redirect a systemctl stop, and the removal of the empty directory removes a
		// link itself), and an lstat of a share whose server is gone answers EHOSTDOWN after
		// seconds - the stale mount the watchdog must unmount would be refused
		if req.Op == opNetUnmount {
			return fail(ops.NetUnmount(ctx, req.Name))
		}
		return fail(ops.NetMountRemove(ctx, req.Name))
	case opWriteTest:
		if !reflect.DeepEqual(req, request{Op: req.Op, Path: req.Path}) {
			return refuse("writetest takes a directory and nothing else")
		}
		if err := s.Policy.writeTestAllowed(req.Path); err != nil {
			s.log("helper: refused a write test in %s: %v", req.Path, err)
			return refuse("writetest: " + err.Error())
		}
		r, err := ops.WriteTest(ctx, req.Path)
		if err != nil {
			return response{Error: err.Error()}
		}
		b, _ := json.Marshal(r)
		return response{OK: true, Stdout: b}
	}
	return refuse("op " + req.Op)
}

// netMountPointOK: /media/net and /media/net/<id>, where they exist, are directories and not
// symlinks - the daemon may write under /media/ and could otherwise point the mount point
// somewhere else before PID 1 mounts on it.
func (p Policy) netMountPointOK(id string) error {
	for _, d := range []string{netmount.Base, netmount.Base + "/" + id} {
		st, err := os.Lstat(filepath.Join(p.Root, d))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil && d != netmount.Base && p.networkMountAt(d) {
			// the share is mounted there and its server does not answer (EHOSTDOWN, EIO, a
			// timeout): a mount, not a planted link - the new units replace it
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 || !st.IsDir() {
			return fmt.Errorf("%s is not a directory", d)
		}
	}
	return nil
}

// networkMountAt says whether a network filesystem is mounted at where (the helper's mountinfo,
// which follows the host's mounts). Only the mount table is read, never the path.
func (p Policy) networkMountAt(where string) bool {
	f, err := os.Open(filepath.Join(p.Root, "/proc/self/mountinfo"))
	if err != nil {
		return false
	}
	defer f.Close()
	_, ok := netmount.ParseMountinfo(f, where)
	return ok
}

// writeTestAllowed: a clean absolute path under /media/, and - resolved as far as it exists -
// still under one of WriteTestPrefixes.
func (p Policy) writeTestAllowed(path string) error {
	if path == "" || filepath.Clean(path) != path || !filepath.IsAbs(path) {
		return errors.New("not a clean absolute path")
	}
	rel, ok := p.rel(path)
	// under /media (sticks, shares), or - task 228's userfs location for backups - under
	// /usr/local/backup itself
	if !ok || strings.Contains(rel, "..") || !(strings.HasPrefix(rel, "/media/") || strings.HasPrefix(rel+"/", "/usr/local/backup/")) {
		return errors.New("not under /media or /usr/local/backup")
	}
	// the deepest part that exists, resolved: a link planted under /media must not lead elsewhere
	existing := path
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	root, _ := filepath.EvalSymlinks(p.Root)
	if root == "" {
		root = p.Root
	}
	realRel := real
	if root != "/" {
		realRel = strings.TrimPrefix(real, strings.TrimSuffix(root, "/"))
	}
	for _, pre := range WriteTestPrefixes {
		if strings.HasPrefix(realRel+"/", pre) {
			return nil
		}
	}
	if realRel == "/usr/local" && strings.HasPrefix(rel+"/", "/usr/local/backup/") {
		// /usr/local/backup does not exist yet: the test makes it, below the real userfs
		return nil
	}
	return fmt.Errorf("%s leads to %s", existing, realRel)
}

// Local side: what the helper does.

func (l Local) systemctl(ctx context.Context, args ...string) error {
	r, err := l.Run(ctx, "systemctl", args, nil)
	if err != nil {
		return err
	}
	if r.Exit != 0 {
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(string(r.Stderr)))
	}
	return nil
}

// NetMount writes the target's two units into PID 1's runtime unit directory and (re)starts its
// automount; a mount with the old options is stopped first, so the next access mounts anew.
func (l Local) NetMount(ctx context.Context, s netmount.Spec) error {
	gid := 0
	if s.Kind == netmount.KindCIFS {
		g, err := user.LookupGroup(NetMountGroup)
		if err != nil {
			return fmt.Errorf("group %s: %w", NetMountGroup, err)
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	mount, automount, err := s.Render(credFile(s), gid)
	if err != nil {
		return err
	}
	name := netmount.UnitName(s.ID)
	if err := os.MkdirAll(netmount.Base, 0o755); err != nil {
		return err
	}
	if err := l.WriteFile(filepath.Join(runUnitDir, name+".mount"), mount, 0o644); err != nil {
		return err
	}
	if err := l.WriteFile(filepath.Join(runUnitDir, name+".automount"), automount, 0o644); err != nil {
		return err
	}
	if err := l.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	_ = l.unmount(ctx, s.ID)
	return l.systemctl(ctx, "restart", name+".automount")
}

// unmount stops the mount unit. The unit carries LazyUnmount= and ForceUnmount=, so PID 1 detaches
// a share whose server does not answer instead of waiting for it; an umount run here would only
// reach the helper's own mount namespace.
func (l Local) unmount(ctx context.Context, id string) error {
	name := netmount.UnitName(id)
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := l.systemctl(sctx, "stop", name+".mount")
	_ = l.systemctl(ctx, "reset-failed", name+".mount")
	return err
}

// NetUnmount unmounts now; the automount stays armed.
func (l Local) NetUnmount(ctx context.Context, id string) error { return l.unmount(ctx, id) }

// NetMountRemove stops both units and removes them - never anything on the share.
func (l Local) NetMountRemove(ctx context.Context, id string) error {
	name := netmount.UnitName(id)
	_ = l.systemctl(ctx, "stop", name+".automount")
	_ = l.unmount(ctx, id)
	for _, suffix := range []string{".automount", ".mount"} {
		if err := os.Remove(filepath.Join(runUnitDir, name+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := l.systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	_ = os.Remove(netmount.Base + "/" + id) // the empty mount point; never recursive
	return nil
}

// WriteTest runs the test in its own goroutine and gives up after writeTestTimeout: a server that
// hangs in an uninterruptible call is abandoned there, not waited for.
func (Local) WriteTest(ctx context.Context, dir string) (WriteTestResult, error) {
	done := make(chan WriteTestResult, 1)
	go func() { done <- writeTest(dir) }()
	t := time.NewTimer(writeTestTimeout)
	defer t.Stop()
	select {
	case r := <-done:
		return r, nil
	case <-t.C:
		return WriteTestResult{Step: "timeout", Error: "no answer within " + writeTestTimeout.String()}, nil
	case <-ctx.Done():
		return WriteTestResult{Step: "timeout", Error: ctx.Err().Error()}, nil
	}
}

// writeTest: the directory made when missing, the filesystem's space, then a 1 MiB file created
// exclusively, written, synced, read back, compared and deleted.
func writeTest(dir string) WriteTestResult {
	r := WriteTestResult{}
	failed := func(step string, err error) WriteTestResult {
		r.Step, r.Error, r.Errno = step, err.Error(), ErrnoName(err)
		return r
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return failed("mkdir", err)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return failed("statfs", err)
	}
	r.FSType = FSTypeName(int64(st.Type))
	r.FreeBytes = int64(st.Bavail) * int64(st.Bsize)
	r.TotalBytes = int64(st.Blocks) * int64(st.Bsize)
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	path := filepath.Join(dir, ".occulite-write-test-"+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return failed("create", err)
	}
	data := make([]byte, WriteTestSize)
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)
	start := time.Now()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return failed("write", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return failed("fsync", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return failed("fsync", err)
	}
	if s := time.Since(start).Seconds(); s > 0 {
		r.WriteMBps = float64(WriteTestSize) / 1e6 / s
	}
	rf, err := os.Open(path)
	if err != nil {
		os.Remove(path)
		return failed("read", err)
	}
	h := sha256.New()
	_, err = io.Copy(h, rf)
	rf.Close()
	if err != nil {
		os.Remove(path)
		return failed("read", err)
	}
	if !reflect.DeepEqual(h.Sum(nil), sum[:]) {
		os.Remove(path)
		return failed("compare", errors.New("the file read back differs from what was written"))
	}
	if err := os.Remove(path); err != nil {
		return failed("delete", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return failed("delete", errors.New("the test file is still there after the delete"))
	}
	r.OK, r.Step = true, "done"
	return r
}

// ErrnoName is the errno's symbolic name for the errors a target produces, "" otherwise.
func ErrnoName(err error) string {
	var e syscall.Errno
	if !errors.As(err, &e) {
		return ""
	}
	switch e {
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.EROFS:
		return "EROFS"
	case syscall.ENOSPC:
		return "ENOSPC"
	case syscall.EDQUOT:
		return "EDQUOT"
	case syscall.EIO:
		return "EIO"
	case syscall.ESTALE:
		return "ESTALE"
	case syscall.ENOTCONN:
		return "ENOTCONN"
	case syscall.EHOSTDOWN:
		return "EHOSTDOWN"
	case syscall.EHOSTUNREACH:
		return "EHOSTUNREACH"
	case syscall.ETIMEDOUT:
		return "ETIMEDOUT"
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.ENODEV:
		return "ENODEV"
	case syscall.ECONNREFUSED:
		return "ECONNREFUSED"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.ELOOP:
		return "ELOOP"
	}
	return "errno " + strconv.Itoa(int(e))
}

// FSTypeName names the statfs magic numbers a backup target can be on.
func FSTypeName(magic int64) string {
	switch magic {
	case 0x6969:
		return "nfs"
	case 0xFF534D42:
		return "cifs"
	case 0xFE534D42:
		return "smb2"
	case 0x01021994:
		return "tmpfs"
	case 0xEF53:
		return "ext4"
	case 0x4d44:
		return "vfat"
	case 0x2011BAB0:
		return "exfat"
	case 0x5346544e:
		return "ntfs"
	case 0x7366746e:
		return "ntfs3"
	case 0x65735546:
		return "fuse"
	case 0x9123683E:
		return "btrfs"
	case 0x58465342:
		return "xfs"
	case 0x0187:
		return "autofs"
	case 0x73717368:
		return "squashfs"
	case 0x794c7630:
		return "overlay"
	}
	return fmt.Sprintf("0x%x", magic)
}
