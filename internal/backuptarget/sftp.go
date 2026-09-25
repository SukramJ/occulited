package backuptarget

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// The SFTP side: a fresh connection per run, nothing left connected that could go stale. Uploads go
// to <name>.partial, are synced (fsync@openssh.com where the server has it), checked for size and
// renamed; an interrupted upload never looks like a backup.

// SFTPConn is one connection to a target's server.
type SFTPConn struct {
	ssh *ssh.Client
	c   *sftp.Client
}

// DialSFTP connects with the target's key, checks the pinned host key and opens the SFTP
// subsystem. dir is the target's secret directory.
func DialSFTP(ctx context.Context, s *SFTP, dir string) (*SFTPConn, error) {
	client, _, err := dialSSH(ctx, s, dir, false)
	if err != nil {
		return nil, err
	}
	c, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, err
	}
	return &SFTPConn{ssh: client, c: c}, nil
}

// Close ends the connection.
func (c *SFTPConn) Close() error {
	_ = c.c.Close()
	return c.ssh.Close()
}

// Space answers the free and total bytes of the filesystem dir is on, ok false when the server
// has no statvfs@openssh.com.
func (c *SFTPConn) Space(dir string) (free, total int64, ok bool) {
	st, err := c.c.StatVFS(dir)
	if err != nil {
		return 0, 0, false
	}
	return int64(st.Bavail * st.Frsize), int64(st.Blocks * st.Frsize), true
}

// Upload writes src as dir/name: to name.partial, synced, size-checked, renamed. The .partial is
// removed when anything fails.
func (c *SFTPConn) Upload(dir, name string, src io.Reader, size int64) error {
	if err := c.c.MkdirAll(dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	final := path.Join(dir, name)
	partial := final + ".partial"
	f, err := c.c.OpenFile(partial, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("create: %w", err)
	}
	// the server's umask made it 0644 there: a backup is the account's alone (best effort - a
	// server may refuse the mode and still take the file)
	_ = f.Chmod(0o600)
	n, err := io.Copy(f, src)
	if err == nil {
		if serr := f.Sync(); serr != nil && !unsupported(serr) {
			err = fmt.Errorf("fsync: %w", serr)
		}
	}
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("close: %w", cerr)
	}
	if err == nil && size >= 0 && n != size {
		err = fmt.Errorf("wrote %d of %d bytes", n, size)
	}
	if err == nil {
		var st os.FileInfo
		if st, err = c.c.Stat(partial); err == nil && size >= 0 && st.Size() != size {
			err = fmt.Errorf("the server has %d of %d bytes", st.Size(), size)
		}
	}
	if err == nil {
		err = c.rename(partial, final)
	}
	if err != nil {
		_ = c.c.Remove(partial)
		return err
	}
	return nil
}

func (c *SFTPConn) rename(from, to string) error {
	err := c.c.PosixRename(from, to)
	if err == nil || !unsupported(err) {
		return err
	}
	// a server without posix-rename: SFTPv3's rename refuses an existing target
	_ = c.c.Remove(to)
	return c.c.Rename(from, to)
}

func unsupported(err error) bool {
	var st *sftp.StatusError
	return errors.As(err, &st) && st.FxCode() == sftp.ErrSSHFxOpUnsupported
}

// List answers the backups in dir, newest first.
func (c *SFTPConn) List(dir string) ([]BackupFile, error) {
	entries, err := c.c.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []BackupFile{}, nil
		}
		return nil, err
	}
	out := []BackupFile{}
	for _, e := range entries {
		if e.IsDir() || !IsBackupName(e.Name()) {
			continue
		}
		out = append(out, BackupFile{Name: e.Name(), Size: e.Size(), Time: e.ModTime(), Encrypted: strings.HasSuffix(e.Name(), ".age")})
	}
	SortBackups(out)
	return out, nil
}

// Open opens dir/name for reading.
func (c *SFTPConn) Open(dir, name string) (io.ReadCloser, error) {
	return c.c.Open(path.Join(dir, name))
}

// Remove deletes dir/name.
func (c *SFTPConn) Remove(dir, name string) error { return c.c.Remove(path.Join(dir, name)) }

// Test is the write test over SFTP: the directory made, 1 MiB written, synced, read back,
// compared and deleted.
func (c *SFTPConn) Test(dir string) TestResult {
	r := TestResult{}
	fail := func(step string, err error) TestResult {
		r.Step, r.Error, r.State = step, err.Error(), Classify(err)
		return r
	}
	if err := c.c.MkdirAll(dir); err != nil {
		return fail("mkdir", err)
	}
	if free, total, ok := c.Space(dir); ok {
		r.FreeBytes, r.TotalBytes = free, total
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	p := path.Join(dir, ".occulite-write-test-"+hex.EncodeToString(rnd[:]))
	data := make([]byte, TestSize)
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)
	f, err := c.c.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return fail("create", err)
	}
	start := time.Now()
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = c.c.Remove(p)
		return fail("write", err)
	}
	if err := f.Sync(); err != nil && !unsupported(err) {
		f.Close()
		_ = c.c.Remove(p)
		return fail("fsync", err)
	}
	if err := f.Close(); err != nil {
		_ = c.c.Remove(p)
		return fail("write", err)
	}
	if s := time.Since(start).Seconds(); s > 0 {
		r.WriteMBps = float64(TestSize) / 1e6 / s
	}
	rf, err := c.c.Open(p)
	if err != nil {
		_ = c.c.Remove(p)
		return fail("read", err)
	}
	h := sha256.New()
	_, err = io.Copy(h, rf)
	rf.Close()
	if err != nil {
		_ = c.c.Remove(p)
		return fail("read", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != hex.EncodeToString(sum[:]) {
		_ = c.c.Remove(p)
		return fail("compare", errors.New("the file read back differs from what was written"))
	}
	if err := c.c.Remove(p); err != nil {
		return fail("delete", err)
	}
	if _, err := c.c.Stat(p); err == nil {
		return fail("delete", errors.New("the test file is still there after the delete"))
	}
	r.OK, r.Step, r.State = true, "done", StateWritable
	return r
}

// TestSize is what a write test writes.
const TestSize = 1 << 20

// TestResult is POST /backup/targets/{id}/test's answer.
type TestResult struct {
	OK          bool      `json:"ok"`
	State       string    `json:"state"`
	Step        string    `json:"step"`
	Error       string    `json:"error,omitempty"`
	FreeBytes   int64     `json:"free_bytes"`
	TotalBytes  int64     `json:"total_bytes"`
	NeededBytes int64     `json:"needed_bytes"`
	WriteMBps   float64   `json:"write_mbps,omitempty"`
	FSType      string    `json:"fs_type,omitempty"`
	At          time.Time `json:"at"`
}

// BackupFile is one backup on a target.
type BackupFile struct {
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	Time      time.Time `json:"time"`
	Encrypted bool      `json:"encrypted"`
	// the header's recovery key, when it was read (encrypted files)
	RecoveryFingerprint string `json:"recovery_fingerprint,omitempty"`
	Known               string `json:"known,omitempty"`
}

// IsBackupName: a .sbk or .sbk.age.
func IsBackupName(n string) bool {
	return !strings.HasPrefix(n, ".") && (strings.HasSuffix(n, ".sbk") || strings.HasSuffix(n, ".sbk.age"))
}

// SortBackups orders newest first, then by name.
func SortBackups(b []BackupFile) {
	sort.SliceStable(b, func(i, j int) bool {
		if !b[i].Time.Equal(b[j].Time) {
			return b[i].Time.After(b[j].Time)
		}
		return b[i].Name > b[j].Name
	})
}

// OwnBackup says whether name is one of this system's backups: <hostname>-...sbk[.age]. Retention
// touches nothing else - another system's files in the same directory stay.
func OwnBackup(name, hostname string) bool {
	return hostname != "" && strings.HasPrefix(name, hostname+"-") && IsBackupName(name)
}

// Expired is what retention removes: this system's backups beyond the newest max (0 = keep all).
func Expired(files []BackupFile, hostname string, max int) []string {
	if max <= 0 {
		return nil
	}
	var own []BackupFile
	for _, f := range files {
		if OwnBackup(f.Name, hostname) {
			own = append(own, f)
		}
	}
	SortBackups(own)
	var out []string
	for i := max; i < len(own); i++ {
		out = append(out, own[i].Name)
	}
	return out
}
