package priv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/sshkeys"
)

const (
	edKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj laptop ed"
	labKey = `command="uptime" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEFSx3g/oLwaC84xZMOZIv9tk8m/eImdz0UxBwEjrnMj lab`
)

func sshServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	return &Server{Policy: Policy{Root: root, AuthorizedKeys: AuthorizedKeysPath}}, filepath.Join(root, AuthorizedKeysPath)
}

// task 185: the section is written as root 0600, the rest of the file stays, and a line that is
// not a key the page takes is refused at the boundary
func TestAuthorizedKeysSection(t *testing.T) {
	s, file := sshServer(t)
	ctx := context.Background()
	if res := s.do(ctx, request{Op: opAuthKeysRead}); !res.OK || len(res.Stdout) != 0 {
		t.Fatalf("no file yet: %+v", res)
	}
	_ = os.MkdirAll(filepath.Dir(file), 0o700)
	_ = os.WriteFile(file, []byte(labKey+"\n"), 0o600)

	if res := s.do(ctx, request{Op: opAuthKeysWrite, Args: []string{edKey}}); !res.OK {
		t.Fatalf("write: %+v", res)
	}
	b, _ := os.ReadFile(file)
	want := labKey + "\n\n" + sshkeys.Begin + "\n" + edKey + "\n" + sshkeys.End + "\n"
	if string(b) != want {
		t.Errorf("file:\n%s", b)
	}
	if st, _ := os.Stat(file); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if res := s.do(ctx, request{Op: opAuthKeysRead}); string(res.Stdout) != want {
		t.Errorf("read back %q", res.Stdout)
	}
	// no stray temporary file beside it
	entries, _ := os.ReadDir(filepath.Dir(file))
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries", len(entries))
	}

	for name, req := range map[string]request{
		"an options prefix":        {Op: opAuthKeysWrite, Args: []string{labKey}},
		"two keys in one argument": {Op: opAuthKeysWrite, Args: []string{edKey + "\n" + edKey}},
		"a path":                   {Op: opAuthKeysRead, Path: "/etc/config/shadow"},
		"data beside the keys":     {Op: opAuthKeysWrite, Args: []string{edKey}, Data: []byte("x")},
	} {
		if res := s.do(ctx, req); res.OK || !strings.HasPrefix(res.Error, "refused") {
			t.Errorf("%s: %+v", name, res)
		}
	}
	if b2, _ := os.ReadFile(file); string(b2) != want {
		t.Errorf("a refused write changed the file:\n%s", b2)
	}
	// an empty section removes it
	if res := s.do(ctx, request{Op: opAuthKeysWrite}); !res.OK {
		t.Fatalf("empty: %+v", res)
	}
	if b, _ := os.ReadFile(file); string(b) != labKey+"\n" {
		t.Errorf("emptied: %q", b)
	}
}

// ssh-end signals an sshd-session process and nothing else
func TestEndSSHSession(t *testing.T) {
	s, _ := sshServer(t)
	proc := filepath.Join(s.Policy.Root, "proc")
	s.Policy.ProcDir = proc
	fake := func(pid int, comm, cmdline string) {
		d := filepath.Join(proc, strconv.Itoa(pid))
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		_ = os.WriteFile(filepath.Join(d, "cmdline"), []byte(cmdline+"\x00"), 0o644)
	}
	// a real process stands in for the session, so the signal has somewhere to go
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	fake(cmd.Process.Pid, "sshd-session", "sshd-session: root [postauth]")
	fake(4242, "sshd", "sshd: /usr/sbin/sshd [listener] 0 of 10-100 startups")
	fake(4343, "bash", "sshd-session: root@pts/0")

	ctx := context.Background()
	for name, args := range map[string][]string{"the listener": {"4242"}, "another program": {"4343"}, "no process": {"99999"}, "not a pid": {"x"}, "pid 1": {"1"}, "two pids": {"4242", "4343"}} {
		if res := s.do(ctx, request{Op: opSSHEnd, Args: args}); res.OK {
			t.Errorf("%s: ended", name)
		}
	}
	if res := s.do(ctx, request{Op: opSSHEnd, Args: []string{strconv.Itoa(cmd.Process.Pid)}}); !res.OK {
		t.Fatalf("the session: %+v", res)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("the session's process was not signalled")
	}
}
