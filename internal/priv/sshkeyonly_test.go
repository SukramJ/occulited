package priv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runRec is an Ops whose Run records the commands and answers sshd -t with exit.
type runRec struct {
	Local
	runs []string
	exit int
}

func (r *runRec) Run(_ context.Context, name string, args []string, _ []byte) (Result, error) {
	r.runs = append(r.runs, name+" "+strings.Join(args, " "))
	if name == "/usr/sbin/sshd" {
		return Result{Exit: r.exit, Stderr: []byte("line 2: Bad configuration option")}, nil
	}
	return Result{}, nil
}

// openccu-lite task 245: occulited's block in the included sshd_config - first, so sshd takes its
// values; the user's lines kept; off takes it out; sshd -t checks it and the old file comes back
// when it fails; a running sshd is reloaded.
func TestSSHKeyOnly(t *testing.T) {
	s, keys := sshServer(t)
	root := s.Policy.Root
	_ = os.MkdirAll(filepath.Join(root, "usr/sbin"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "usr/sbin/sshd"), nil, 0o755)
	_ = os.MkdirAll(filepath.Dir(keys), 0o700)
	rec := &runRec{}
	s.Ops = rec
	conf := filepath.Join(filepath.Dir(keys), SSHDConfigName)
	ctx := context.Background()
	read := func() string {
		t.Helper()
		res := s.do(ctx, request{Op: opSSHKeyOnly})
		if !res.OK {
			t.Fatalf("%+v", res)
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	if read() != "off" {
		t.Fatal("no file: off")
	}
	// the user's own line stays, below the block
	_ = os.WriteFile(conf, []byte("ClientAliveInterval 60\n"), 0o600)
	if res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"on"}}); !res.OK {
		t.Fatalf("on: %+v", res)
	}
	b, _ := os.ReadFile(conf)
	if !strings.HasPrefix(string(b), keyOnlyBegin+"\nPasswordAuthentication no\nKbdInteractiveAuthentication no\n"+keyOnlyEnd+"\n") || !strings.Contains(string(b), "ClientAliveInterval 60") {
		t.Fatalf("%q", b)
	}
	if st, _ := os.Stat(conf); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if strings.Join(rec.runs, "|") != "/usr/sbin/sshd -t|systemctl try-reload-or-restart sshd" {
		t.Fatalf("%q", rec.runs)
	}
	if read() != "on" {
		t.Fatal("on")
	}
	// twice on: one block
	_ = s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"on"}})
	b, _ = os.ReadFile(conf)
	if strings.Count(string(b), keyOnlyBegin) != 1 {
		t.Fatalf("%q", b)
	}
	// off: the block goes, the user's line stays
	if res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"off"}}); !res.OK {
		t.Fatalf("off: %+v", res)
	}
	b, _ = os.ReadFile(conf)
	if string(b) != "ClientAliveInterval 60\n" || read() != "off" {
		t.Fatalf("%q", b)
	}
	// sshd refuses the file: the old one comes back, the error says so, nothing reloaded
	rec.exit, rec.runs = 255, nil
	res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"on"}})
	if res.OK || !strings.Contains(res.Error, "sshd refused the configuration") {
		t.Fatalf("%+v", res)
	}
	if b, _ = os.ReadFile(conf); string(b) != "ClientAliveInterval 60\n" || len(rec.runs) != 1 {
		t.Fatalf("%q %q", b, rec.runs)
	}
	// and without a file before: none after
	_ = os.Remove(conf)
	if res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"on"}}); res.OK {
		t.Fatal("refused config taken")
	}
	if _, err := os.Stat(conf); !os.IsNotExist(err) {
		t.Fatal("a refused file was left")
	}
	// on and off on a system without the file: none afterwards
	rec.exit = 0
	if res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"on"}}); !res.OK {
		t.Fatalf("%+v", res)
	}
	if res := s.do(ctx, request{Op: opSSHKeyOnly, Args: []string{"off"}}); !res.OK {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(conf); !os.IsNotExist(err) {
		t.Fatal("an empty file was left")
	}
	// only its own arguments
	for _, bad := range []request{{Op: opSSHKeyOnly, Args: []string{"yes"}}, {Op: opSSHKeyOnly, Args: []string{"on", "off"}}, {Op: opSSHKeyOnly, Path: "/etc/ssh/sshd_config"}} {
		if res := s.do(ctx, bad); res.OK {
			t.Fatalf("taken: %+v", bad)
		}
	}
}
