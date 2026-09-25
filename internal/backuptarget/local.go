package backuptarget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/hobbyquaker/occulited/internal/netmount"
	"github.com/hobbyquaker/occulited/internal/shares"
)

// Directory and mount targets as the daemon reads them: every stat, listing and open of a target's
// path goes through a Prober (shares.Prober, task 228 lifted it there with the shares), so a share
// that hangs becomes the state stale after a deadline instead of a handler that never returns.

// Prober runs the reads (shares.Prober).
type Prober = shares.Prober

// Space is a directory's filesystem.
type Space = shares.Space

// StatSpace reads the filesystem of path.
func StatSpace(path string) (Space, error) { return shares.StatSpace(path) }

// The filesystem magics the pipeline cares about besides the network ones.
const (
	magicTmpfs  = 0x01021994
	magicAutofs = 0x0187
)

// IsNetworkFS says whether a statfs type is a mounted network share.
func IsNetworkFS(t int64) bool { return shares.IsNetworkFS(t) }

// ListDir lists the backups in dir, newest first; a missing directory is an empty list.
func ListDir(dir string) ([]BackupFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []BackupFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupFile{}
	for _, e := range entries {
		if e.IsDir() || !IsBackupName(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: info.Size(), Time: info.ModTime(), Encrypted: filepath.Ext(e.Name()) == ".age"})
	}
	SortBackups(out)
	return out, nil
}

// copyVerified writes src into dir as name: name.partial created exclusively, synced, read back
// against the SHA-256 of what was written, then renamed. It answers the hash and the size, and
// the step where it failed.
func copyVerified(src, dir, name string, uid, gid int) (sum string, size int64, step string, err error) {
	in, err := os.Open(src)
	if err != nil {
		return "", 0, "open", err
	}
	defer in.Close()
	final := filepath.Join(dir, name)
	partial := final + ".partial"
	_ = os.Remove(partial)
	out, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", 0, "create", err
	}
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(out, h), in)
	if err != nil {
		out.Close()
		os.Remove(partial)
		return "", 0, "write", err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(partial)
		return "", 0, "fsync", err
	}
	if err := out.Close(); err != nil {
		os.Remove(partial)
		return "", 0, "write", err
	}
	sum = hex.EncodeToString(h.Sum(nil))
	back, err := os.Open(partial)
	if err != nil {
		os.Remove(partial)
		return "", 0, "read", err
	}
	h2 := sha256.New()
	_, err = io.Copy(h2, back)
	back.Close()
	if err != nil {
		os.Remove(partial)
		return "", 0, "read", err
	}
	if hex.EncodeToString(h2.Sum(nil)) != sum {
		os.Remove(partial)
		return "", 0, "compare", errors.New("the copy read back differs from the backup")
	}
	// readable by occulited for the list and a restore: its group where the filesystem lets root
	// give it, world-readable where it does not (a root-squashed export)
	if err := os.Chown(partial, 0, gid); err == nil {
		_ = os.Chmod(partial, 0o640)
	} else {
		_ = os.Chmod(partial, 0o644)
	}
	if err := os.Rename(partial, final); err != nil {
		os.Remove(partial)
		return "", 0, "rename", err
	}
	return sum, size, "", nil
}

// checkDir is checkLocalDir; a test's temporary directory is on the root filesystem.
var checkDir = checkLocalDir

// OnUSB says whether the target is a directory on a USB stick: under /media/, and not a share's
// mount point under /media/net (task 161: the sticks are mounted at /media/usb1…8, the first one
// linked as /media/usb0).
func (t Target) OnUSB() bool {
	if t.Kind != KindDirectory || t.IsMount() {
		return false
	}
	dir := t.Dir() + "/"
	return strings.HasPrefix(dir, "/media/") && !strings.HasPrefix(dir, netmount.Base+"/")
}

// noMediumAt is noMedium for the manager (a test swaps it; the dry-run root has no /media).
var noMediumAt = noMedium

// noMedium answers whether a USB directory target's stick is missing: checkDir refuses its
// directory for landing in RAM or on the root filesystem - what /media is without a stick. The
// error says so in the words of a missing stick.
func noMedium(t Target) (bool, error) {
	if !t.OnUSB() {
		return false, nil
	}
	if step, err := checkDir(t); err != nil && step == "statfs" {
		return true, fmt.Errorf("no USB stick is mounted for %s", t.Dir())
	}
	return false, nil
}

// checkLocalDir refuses a directory target that would not survive the system: on the root
// filesystem, or in RAM. mountTarget: the path must be on the mounted share, not the tmpfs under
// it (a mount that did not come up).
func checkLocalDir(t Target) (string, error) {
	if t.IsMount() {
		where := netmount.Base + "/" + t.MountID()
		// the first access mounts the share (the automount)
		if _, err := os.ReadDir(where); err != nil {
			return "mount", err
		}
		sp, err := StatSpace(where)
		if err != nil {
			return "mount", err
		}
		if !IsNetworkFS(sp.FSType) {
			return "mount", fmt.Errorf("%s is not mounted (%s)", where, fsName(sp.FSType))
		}
		return "", nil
	}
	dir := t.Dir()
	// the deepest existing part decides where the files would land
	existing := dir
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	sp, err := StatSpace(existing)
	if err != nil {
		return "statfs", err
	}
	if sp.FSType == magicTmpfs || sp.FSType == magicAutofs {
		return "statfs", fmt.Errorf("%s is in RAM, not on a USB stick or a share: nothing there survives a reboot", dir)
	}
	var a, b syscall.Stat_t
	if syscall.Stat(existing, &a) == nil && syscall.Stat("/", &b) == nil && a.Dev == b.Dev {
		return "statfs", fmt.Errorf("%s is on the system's own root filesystem", dir)
	}
	return "", nil
}

func fsName(t int64) string {
	switch t {
	case magicTmpfs:
		return "tmpfs"
	case magicAutofs:
		return "autofs: the mount failed"
	}
	return fmt.Sprintf("0x%x", t)
}

// deliverLocal copies a staged backup onto a directory or mount target as root: checks, free
// space, the verified copy, then retention of this system's files (unless the target is
// append-only). The result is written by the caller.
func deliverLocal(ctx context.Context, t Target, f StagedFile, hostname string, gid int) Result {
	start := time.Now()
	r := Result{At: start, Name: f.Name, Encrypted: f.Encrypted}
	fail := func(step string, err error) Result {
		r.OK, r.Step, r.Error, r.State = false, step, err.Error(), Classify(err)
		r.DurationMS = time.Since(start).Milliseconds()
		return r
	}
	if step, err := checkDir(t); err != nil {
		return fail(step, err)
	}
	dir := t.Dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail("mkdir", err)
	}
	if sp, err := StatSpace(dir); err == nil && sp.Free < f.Size+f.Size/10 {
		r.State = StateFull
		r.Step, r.Error = "space", fmt.Sprintf("%d bytes free, %d needed", sp.Free, f.Size+f.Size/10)
		r.DurationMS = time.Since(start).Milliseconds()
		return r
	}
	if ctx.Err() != nil {
		return fail("cancelled", ctx.Err())
	}
	sum, size, step, err := copyVerified(f.Path, dir, f.Name, 0, gid)
	if err != nil {
		return fail(step, err)
	}
	if sum != f.SHA256 {
		_ = os.Remove(filepath.Join(dir, f.Name))
		return fail("compare", errors.New("the copy's hash is not the backup's"))
	}
	r.OK, r.State, r.SHA256, r.Size = true, StateWritable, sum, size
	if !t.AppendOnly {
		if files, err := ListDir(dir); err == nil {
			for _, n := range Expired(files, hostname, t.MaxBackups) {
				if n == f.Name {
					continue
				}
				if err := os.Remove(filepath.Join(dir, n)); err == nil {
					r.Removed = append(r.Removed, n)
				}
			}
		}
	}
	r.DurationMS = time.Since(start).Milliseconds()
	return r
}
