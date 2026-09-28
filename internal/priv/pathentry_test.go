package priv

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// B-6: a Paths entry without a trailing slash is one file (or, with a "*", a pattern for one
// name), never a prefix - /usr/local/.recoveryMode must not admit /usr/local/.recoveryModeXYZ.
func TestPathAllowedEntryShapes(t *testing.T) {
	p := DefaultPolicy("/", "/usr/local/etc/occulite")
	for path, want := range map[string]bool{
		// the exact entries, and their near misses
		"/usr/local/.recoveryMode":             true,
		"/usr/local/.recoveryModeXYZ":          false,
		"/usr/local/.recoveryMode/x":           false,
		"/usr/local/.firmwareUpdate":           true,
		"/usr/local/.firmwareUpdateFoo":        false,
		"/usr/local/.doFactoryReset":           true,
		"/usr/local/.doFactoryReset2":          false,
		"/etc/hostname":                        true,
		"/etc/hostname.bak":                    false,
		"/etc/hosts":                           true,
		"/etc/hosts.allow":                     false,
		"/etc/hosts.deny":                      false,
		"/var/etc/hosts":                       true,
		"/var/etc/hostsX":                      false,
		"/usr/local/crontabs/root":             true,
		"/usr/local/crontabs/rootkit":          false,
		"/usr/local/crontabs/root/x":           false,
		"/usr/local/etc/ca-certificates.conf":  true,
		"/usr/local/etc/ca-certificates.confX": false,
		// the pattern entry: one name, no "/" through the "*"
		"/usr/local/etc/monit-mosquitto.cfg": true,
		"/usr/local/etc/monit-.cfg":          true,
		"/usr/local/etc/monit-x/y.cfg":       false,
		"/usr/local/etc/monit-x.cfg.bak":     false,
		"/usr/local/etc/monitrc":             false,
		"/usr/local/etc/monit-x":             false,
		// directory entries: the directory itself and below, not a sibling with the same start
		"/usr/local/tmp":       true,
		"/usr/local/tmp/x":     true,
		"/usr/local/tmpfoo":    false,
		"/etc/config":          true,
		"/etc/config/rfd.conf": true,
		"/etc/configX/x":       false,
		"/media/usb0/x":        true,
		"/mediaX":              false,
		// outside, traversal
		"/etc/passwd":                   false,
		"/usr/local/tmp/../etc/passwd":  false,
		"/usr/local/etc/monit-../x.cfg": false,
		"/usr/local/etc/config/../x":    false,
		"usr/local/tmp/x":               false,
	} {
		if got := p.pathAllowed(path); got != want {
			t.Errorf("pathAllowed(%q) = %v, want %v", path, got, want)
		}
	}
}

// Every entry of the default policy has one of the three shapes, so none is a prefix by accident:
// absolute and clean, a directory with its "/", a valid pattern, or a file.
func TestDefaultPathsShapes(t *testing.T) {
	for _, a := range DefaultPolicy("/", "/usr/local/etc/occulite").Paths {
		trimmed := strings.TrimSuffix(a, "/")
		if !filepath.IsAbs(a) || filepath.Clean(trimmed) != trimmed || strings.Contains(a, "..") {
			t.Errorf("Paths entry %q is not an absolute clean path", a)
		}
		if strings.Contains(a, "*") {
			if _, err := filepath.Match(a, "/"); err != nil || strings.HasSuffix(a, "*") || strings.HasSuffix(a, "/") {
				t.Errorf("Paths pattern %q: a pattern names one file with a fixed ending", a)
			}
		}
		if strings.ContainsAny(a, "?[\\") {
			t.Errorf("Paths entry %q: only \"*\" is a pattern character here", a)
		}
	}
}

// The refusal path through the helper's socket: write, touch, remove, symlink and rename of a
// near-miss name are refused, the exact marker file is not.
func TestServerRefusesNearMissPaths(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"usr/local/tmp", "usr/local/etc", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite")}
	go func() { _ = srv.Serve(ctx, l) }()
	c := Client{Socket: sock}
	j := func(p string) string { return filepath.Join(root, p) }
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: want a refusal, got %v", what, err)
		}
	}

	if err := c.Touch(j("usr/local/.recoveryMode"), 0o644); err != nil {
		t.Errorf("touch the marker: %v", err)
	}
	refused("touch .recoveryModeXYZ", c.Touch(j("usr/local/.recoveryModeXYZ"), 0o644))
	refused("write .firmwareUpdateFoo", c.WriteFile(j("usr/local/.firmwareUpdateFoo"), []byte("x"), 0o644))
	refused("write /etc/hosts.allow", c.WriteFile(j("etc/hosts.allow"), []byte("ALL: ALL\n"), 0o644))
	refused("write monit-x.cfg.sh", c.WriteFile(j("usr/local/etc/monit-x.cfg.sh"), []byte("x"), 0o755))
	refused("symlink .firmwareUpdateFoo", c.Symlink(j("usr/local/tmp/f"), j("usr/local/.firmwareUpdateFoo")))
	if err := c.WriteFile(j("usr/local/tmp/f"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write into tmp: %v", err)
	}
	refused("rename to .doFactoryResetX", c.Rename(j("usr/local/tmp/f"), j("usr/local/.doFactoryResetX")))
	if err := os.WriteFile(j("usr/local/.recoveryModeXYZ"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	refused("remove .recoveryModeXYZ", c.Remove(j("usr/local/.recoveryModeXYZ")))
	for _, p := range []string{"usr/local/.firmwareUpdateFoo", "etc/hosts.allow", "usr/local/etc/monit-x.cfg.sh", "usr/local/.doFactoryResetX"} {
		if _, err := os.Lstat(j(p)); !os.IsNotExist(err) {
			t.Errorf("%s exists after a refusal: %v", p, err)
		}
	}
	if _, err := os.Stat(j("usr/local/.recoveryModeXYZ")); err != nil {
		t.Errorf("the refused remove removed it: %v", err)
	}
	// the pattern entry still admits what the uninstall removes
	if err := os.WriteFile(j("usr/local/etc/monit-mosquitto.cfg"), []byte("check process"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(j("usr/local/etc/monit-mosquitto.cfg")); err != nil {
		t.Errorf("remove monit-mosquitto.cfg: %v", err)
	}
}

// B-271: a file the helper reads for the daemon and that is not there is fs.ErrNotExist to the
// caller, with the helper's text; a file that is there is read, and any other failure is not
// "missing" - the caller that rewrites a file tells the two apart.
func TestClientReadMissingIsNotExist(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "etc/config/crRFD")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "h.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite")}
	go func() { _ = srv.Serve(ctx, l) }()
	c := Client{Socket: sock}
	p := filepath.Join(dir, "hmip_user.conf")
	_, err = c.ReadFile(p)
	if !errors.Is(err, fs.ErrNotExist) || !strings.Contains(err.Error(), "hmip_user.conf") {
		t.Fatalf("missing: %v", err)
	}
	if err := os.WriteFile(p, []byte("KeyServer.Mode=LOCAL\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if b, err := c.ReadFile(p); err != nil || string(b) != "KeyServer.Mode=LOCAL\n" {
		t.Fatalf("read: %q %v", b, err)
	}
	if _, err := c.ReadFile(filepath.Join(dir, "other.conf")); err == nil || errors.Is(err, fs.ErrNotExist) || !errors.Is(err, ErrRefused) {
		t.Fatalf("a refusal is not a missing file: %v", err)
	}
	for _, e := range []response{{Error: "read x: is a directory"}, {Error: "privilege helper: no such file or directory in the policy"}} {
		if errors.Is(e.err(), fs.ErrNotExist) {
			t.Errorf("%q is not a missing file", e.Error)
		}
	}
}
