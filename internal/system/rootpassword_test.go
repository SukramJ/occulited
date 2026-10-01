package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// noRunPriv is the privilege boundary as the daemon meets it on a system: it writes the shadow
// line (B-15's operation) and refuses every program - the helper's list has no mkpasswd, which
// is how GitHub #2 showed itself ("refused run mkpasswd"; openccu-lite task 302).
type noRunPriv struct {
	priv.Local
	mu   sync.Mutex
	runs []string
}

func (p *noRunPriv) Run(_ context.Context, name string, args []string, _ []byte) (priv.Result, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.runs = append(p.runs, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return priv.Result{Exit: 1, Stderr: []byte("refused run " + name)}, nil
}

func (p *noRunPriv) Runs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.runs...)
}

// fakeMkpasswd puts a shell script named mkpasswd first on PATH. It records its standard input
// and its arguments beside itself and then runs body, so a test sees exactly what the default
// hasher handed the applet.
func fakeMkpasswd(t *testing.T, body string) (stdinFile, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	stdinFile, argsFile = filepath.Join(dir, "stdin"), filepath.Join(dir, "args")
	script := "#!/bin/sh\ncat > " + stdinFile + "\necho \"$@\" > " + argsFile + "\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mkpasswd"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stdinFile, argsFile
}

const shadowBefore = "root:*:19000:0:99999:7:::\nhm:*:19000:0:99999:7:::\n"

func shadowRoot(t *testing.T) Root {
	t.Helper()
	return rootWith(t, map[string]string{"etc/config/shadow": shadowBefore})
}

func shadowOf(t *testing.T, r Root) string {
	t.Helper()
	b, err := os.ReadFile(r.join("/etc/config/shadow"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The default path, as production takes it: no hasher given, the helper refusing every program.
// mkpasswd runs in the daemon's own process with the password on its stdin, and the hash it
// prints lands in root's line.
func TestSetRootPasswordHashesInProcess(t *testing.T) {
	r := shadowRoot(t)
	p := &noRunPriv{}
	old := Priv
	Priv = p
	t.Cleanup(func() { Priv = old })
	stdinFile, argsFile := fakeMkpasswd(t, `echo '$6$saltsalt$fakehashfromstdin'`)

	if err := r.SetRootPassword(context.Background(), nil, "longenough1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(stdinFile); string(got) != "longenough1\n" {
		t.Errorf("mkpasswd read %q from stdin, not the password", got)
	}
	if got, _ := os.ReadFile(argsFile); strings.TrimSpace(string(got)) != "-m sha512 -s" {
		t.Errorf("mkpasswd's arguments: %q", got)
	}
	if got := shadowOf(t, r); !strings.HasPrefix(got, "root:$6$saltsalt$fakehashfromstdin:19000:") || !strings.Contains(got, "\nhm:*:19000:") {
		t.Errorf("shadow: %q", got)
	}
	if runs := p.Runs(); len(runs) != 0 {
		t.Errorf("the hash went through the privilege helper: %v", runs)
	}
}

// A hasher that fails, or answers nothing a shadow line can hold, sets nothing: the old
// password stays rather than one nobody knows (the empty-stdin hash the Runner path would have
// produced had the helper allowed mkpasswd).
func TestSetRootPasswordRefusesABrokenHash(t *testing.T) {
	p := &noRunPriv{}
	old := Priv
	Priv = p
	t.Cleanup(func() { Priv = old })
	hashers := map[string]PasswordHasher{
		"error":     func(context.Context, string) (string, error) { return "", errors.New("mkpasswd: exit 1") },
		"empty":     func(context.Context, string) (string, error) { return "", nil },
		"blank":     func(context.Context, string) (string, error) { return " \n", nil },
		"plaintext": func(context.Context, string) (string, error) { return "longenough1", nil },
		"a field":   func(context.Context, string) (string, error) { return "$6$s$h:0:0", nil },
		"two lines": func(context.Context, string) (string, error) { return "$6$s$h\n$6$s$h2", nil },
		"an mkpasswd that exits 1": func(ctx context.Context, pw string) (string, error) {
			fakeMkpasswd(t, `echo 'mkpasswd: boom' >&2; exit 1`)
			return MkpasswdHasher(ctx, pw)
		},
		"an mkpasswd that prints nothing": func(ctx context.Context, pw string) (string, error) {
			fakeMkpasswd(t, `exit 0`)
			return MkpasswdHasher(ctx, pw)
		},
	}
	for name, h := range hashers {
		t.Run(name, func(t *testing.T) {
			r := shadowRoot(t)
			err := r.SetRootPassword(context.Background(), h, "longenough1")
			if err == nil {
				t.Fatal("accepted")
			}
			if got := shadowOf(t, r); got != shadowBefore {
				t.Errorf("the refused hash changed shadow: %q", got)
			}
			if strings.Contains(err.Error(), "longenough1") {
				t.Errorf("the error carries the password: %v", err)
			}
		})
	}
	if runs := p.Runs(); len(runs) != 0 {
		t.Errorf("the privilege helper was asked: %v", runs)
	}
}

// MkpasswdHasher's error for an exit code names it and carries stderr, and a missing program
// is an error too - never an empty hash that looks like success.
func TestMkpasswdHasherErrors(t *testing.T) {
	fakeMkpasswd(t, `echo 'mkpasswd: unsupported' >&2; exit 2`)
	if _, err := MkpasswdHasher(context.Background(), "longenough1"); err == nil || !strings.Contains(err.Error(), "exit 2") || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("%v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if h, err := MkpasswdHasher(context.Background(), "longenough1"); err == nil || h != "" {
		t.Errorf("without mkpasswd: %q %v", h, err)
	}
}
