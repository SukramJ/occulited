package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
	"github.com/hobbyquaker/occulited/internal/sshkeys"
)

const (
	testEdKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj laptop ed"
	testEdFP   = "SHA256:mZ+3k7WSTBEIONtZ4sWyKZdRbsGKX4yQvqbX/7pwwqs"
	testEcKey  = "ecdsa-sha2-nistp384 AAAAE2VjZHNhLXNoYTItbmlzdHAzODQAAAAIbmlzdHAzODQAAABhBPUSfBJXk7J2BPZi3b7iRP74eM1MdcEXFedN6chSVTyF/9aDxY1p7bBOHauaCNJAr8v/NTm3IalSCRmjTG/Npl7TxfRK0yehzV9v+hQe5qbKdfpLnVSd9f3J/45iXcD/xg== ec384"
	testEcFP   = "SHA256:JPKsqnUXPohkbwQb+p8EaDAvyHUVqk93XQ0m5tVy6JA"
	testLabKey = `command="uptime" ` + testEcKey
)

// sshFake is the helper's SSH side in memory; the rest of the helper is Local's.
type sshFake struct {
	priv.Local
	file  []byte
	ended []int
}

func (f *sshFake) ReadAuthorizedKeys() ([]byte, error) { return f.file, nil }
func (f *sshFake) WriteAuthorizedKeys(lines []string) error {
	keys := []sshkeys.Key{}
	for _, l := range lines {
		k, err := sshkeys.Parse(l)
		if err != nil {
			return err
		}
		keys = append(keys, k)
	}
	f.file = sshkeys.WithSection(f.file, keys)
	return nil
}
func (f *sshFake) SSHKeyOnly() (bool, error)   { return false, nil }
func (f *sshFake) SetSSHKeyOnly(on bool) error { return nil }
func (f *sshFake) EndSSHSession(pid int) error {
	f.ended = append(f.ended, pid)
	return nil
}

func withSSHFake(t *testing.T, file string) *sshFake {
	t.Helper()
	f := &sshFake{file: []byte(file)}
	old := Priv
	Priv = f
	t.Cleanup(func() { Priv = old })
	return f
}

// task 185: a pasted key goes into the section, a second paste of it is refused, a key outside the
// section is listed and cannot be removed, and a key of the section can
func TestSSHKeysAddAndRemove(t *testing.T) {
	f := withSSHFake(t, testLabKey+"\n")
	k, err := AddSSHKey(testEdKey)
	if err != nil || k.Fingerprint != testEdFP {
		t.Fatalf("%v %+v", err, k)
	}
	if !strings.HasPrefix(string(f.file), testLabKey+"\n") || !strings.Contains(string(f.file), sshkeys.Begin+"\n"+testEdKey+"\n"+sshkeys.End) {
		t.Errorf("file:\n%s", f.file)
	}
	if _, err := AddSSHKey(testEdKey); !errors.Is(err, ErrKeyPresent) {
		t.Errorf("twice: %v", err)
	}
	// the lab's key is outside the section: the same key pasted is there already
	if _, err := AddSSHKey(testEcKey); !errors.Is(err, ErrKeyPresent) {
		t.Errorf("a key outside the section: %v", err)
	}
	keys, _ := SSHKeys()
	if len(keys.Managed) != 1 || len(keys.Other) != 1 || keys.Other[0].Options != `command="uptime"` {
		t.Errorf("%+v", keys)
	}
	if _, err := RemoveSSHKey(testEcFP); !errors.Is(err, ErrKeyNotManaged) {
		t.Errorf("removed a key outside the section: %v", err)
	}
	if k, err := RemoveSSHKey(testEdFP); err != nil || k.Comment != "laptop ed" {
		t.Fatalf("%v %+v", err, k)
	}
	if string(f.file) != testLabKey+"\n" {
		t.Errorf("after the removal:\n%q", f.file)
	}
	if _, err := AddSSHKey("ssh-rsa AAAA short"); err == nil {
		t.Error("a broken key was taken")
	}
}

func sshProc(t *testing.T, root string, pid, ppid int, comm, title string) {
	t.Helper()
	d := filepath.Join(root, "proc", strconv.Itoa(pid))
	_ = os.MkdirAll(d, 0o755)
	_ = os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(title+"\x00\x00\x00"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "status"), []byte("Name:\t"+comm+"\nPPid:\t"+strconv.Itoa(ppid)+"\n"), 0o644)
}

// the sessions: /proc says which are open, the journal's Accepted lines say from where and how
func TestSSHSessions(t *testing.T) {
	root := t.TempDir()
	sshProc(t, root, 1354, 1, "sshd", "sshd: /usr/sbin/sshd [listener] 0 of 10-100 startups")
	sshProc(t, root, 37324, 1354, "sshd-session", "sshd-session: root [postauth]")
	sshProc(t, root, 37326, 37324, "sshd-session", "sshd-session: root@notty")
	sshProc(t, root, 40100, 1354, "sshd-session", "sshd-session: root [priv]")
	sshProc(t, root, 40102, 40100, "sshd-session", "sshd-session: root@pts/0")
	// one still logging in: no child titled user@tty yet
	sshProc(t, root, 41000, 1354, "sshd-session", "sshd-session: root [priv]")
	sshProc(t, root, 500, 1, "bash", "sshd-session: root@pts/9")
	journal := strings.Join([]string{
		`{"_PID":"37324","MESSAGE":"Accepted publickey for root from 192.0.2.114 port 46139 ssh2: ED25519 SHA256:MBm1zNU3tdJvms19ANAdCN9mhUIvo62COPiBA1ZzK3g","__REALTIME_TIMESTAMP":"1789840000000000"}`,
		`{"_PID":"40100","MESSAGE":"Accepted password for root from fe80::1%eth0 port 50000 ssh2","__REALTIME_TIMESTAMP":"1789841000000000"}`,
		`{"_PID":"30000","MESSAGE":"Accepted password for root from 10.0.0.9 port 1 ssh2","__REALTIME_TIMESTAMP":"1789830000000000"}`,
	}, "\n") + "\n"
	var args []string
	run := func(_ context.Context, name string, a ...string) ([]byte, error) {
		args = append([]string{name}, a...)
		return []byte(journal), nil
	}
	list, err := Root(root).SSHSessions(context.Background(), run)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %+v", err, list)
	}
	a, b := list[0], list[1]
	if a.ID != 37324 || a.User != "root" || a.TTY != "" || a.From != "192.0.2.114" || a.Port != 46139 || a.Method != "publickey" || a.KeyType != "ED25519" || !strings.HasPrefix(a.KeyFingerprint, "SHA256:") || a.Since == "" {
		t.Errorf("first %+v", a)
	}
	if b.ID != 40100 || b.TTY != "pts/0" || b.From != "fe80::1%eth0" || b.Method != "password" || b.KeyType != "" {
		t.Errorf("second %+v", b)
	}
	if strings.Join(args, " ") != "journalctl -b -t sshd-session -t sshd -o json --no-pager -q -g ^Accepted " {
		t.Errorf("journalctl %q", args)
	}

	// only an open session can be ended, and through the helper
	f := withSSHFake(t, "")
	if _, err := Root(root).EndSSHSession(context.Background(), run, 41000); !errors.Is(err, ErrNoSession) {
		t.Errorf("a login in progress: %v", err)
	}
	if _, err := Root(root).EndSSHSession(context.Background(), run, 1354); !errors.Is(err, ErrNoSession) {
		t.Errorf("the listener: %v", err)
	}
	if s, err := Root(root).EndSSHSession(context.Background(), run, 40100); err != nil || s.From != "fe80::1%eth0" || len(f.ended) != 1 || f.ended[0] != 40100 {
		t.Errorf("%v %+v %v", err, s, f.ended)
	}
}
