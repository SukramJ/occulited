package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
)

// classicPriv records the chowns instead of making them: a chown to root works only as root, and
// the test must say the same as root and as a user (B-7: the runner runs the tests as uid 0).
type classicPriv struct {
	*fwPriv
	mu     sync.Mutex
	chowns []string
}

func (p *classicPriv) Chown(path string, uid, gid int, recursive bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.chowns = append(p.chowns, fmt.Sprintf("%s %d:%d", filepath.Base(path), uid, gid))
	return nil
}

func classicRig(t *testing.T) (*ClassicRPCConfig, func() []string, *[]string) {
	t.Helper()
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	old := Priv
	Priv = &classicPriv{fwPriv: &fwPriv{}}
	t.Cleanup(func() { Priv = old })
	var cmds, logs []string
	var mu sync.Mutex
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	calls := 0
	oldFC := FirewallChanged
	FirewallChanged = func(context.Context) { calls++ }
	t.Cleanup(func() { FirewallChanged = oldFC })
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), cmds...)
	}
	return &ClassicRPCConfig{Root: r, Run: run, Systemd: true, RestartDelay: time.Millisecond}, snapshot, &logs
}

// task 143: the switches are the markers, the pair the htpasswd file; every change reloads
// lighttpd; no password in any log line
func TestClassicRPC(t *testing.T) {
	c, cmds, logs := classicRig(t)
	var logMu sync.Mutex
	logf := func(l string) {
		logMu.Lock()
		defer logMu.Unlock()
		*logs = append(*logs, l)
	}
	ctx := t.Context()
	if cur := c.Root.ReadClassicRPC(); cur.Plain || cur.TLS || cur.Auth != "none" || cur.PasswordSet {
		t.Fatalf("fresh: %+v", cur)
	}
	// password auth without a pair is refused, a bad auth value too
	if _, err := c.Set(ctx, ClassicRPC{Plain: true, Auth: "password"}, logf); !errors.Is(err, ErrClassicRPCInvalid) {
		t.Fatalf("password without a pair: %v", err)
	}
	if _, err := c.Set(ctx, ClassicRPC{Auth: "rega"}, logf); !errors.Is(err, ErrClassicRPCInvalid) {
		t.Fatal("auth rega")
	}
	got, err := c.Set(ctx, ClassicRPC{Plain: true, Auth: "none"}, logf)
	if err != nil || !got.Plain || got.TLS {
		t.Fatalf("plain on: %+v %v", got, err)
	}
	if _, err := os.Stat(c.Root.join(ClassicRPCPlainMarker)); err != nil {
		t.Fatal("no plain marker")
	}
	// a switch: lighttpd restarts, off the request - a reload would keep a socket that goes
	waitFor(t, func() bool { return len(cmds()) == 1 })
	if !strings.Contains(cmds()[0], "restart") {
		t.Fatalf("cmds %v", cmds())
	}
	// the same settings again: nothing written, no reload
	if _, err := c.Set(ctx, ClassicRPC{Plain: true, Auth: "none"}, logf); err != nil || len(cmds()) != 1 {
		t.Fatalf("no-op reloaded: %v", cmds())
	}
	// the pair: the rules, then a typed one, then a generated one
	for _, bad := range []struct{ user, pw string }{{"", "long-enough-password"}, {"a:b", "long-enough-password"}, {"ccu", "short"}, {"ccu", "twelve chars\nx"}} {
		if _, err := c.SetPassword(ctx, bad.user, bad.pw, false, logf); !errors.Is(err, ErrClassicRPCInvalid) {
			t.Errorf("%q/%q accepted", bad.user, bad.pw)
		}
	}
	if out, err := c.SetPassword(ctx, "ccu-client", "a typed password", false, logf); err != nil || out != "" {
		t.Fatalf("typed: %q %v", out, err)
	}
	b, _ := os.ReadFile(c.Root.join(ClassicRPCHtpasswd))
	user, hash, _ := strings.Cut(strings.TrimSpace(string(b)), ":")
	salt := strings.Split(hash, "$")[2]
	if user != "ccu-client" || hash != sha512Crypt("a typed password", salt) {
		t.Fatalf("htpasswd %q", b)
	}
	// no www-data in this system's /etc/group (the busybox products): root's 0600, no chown -
	// whatever the host's groups and the test's uid (B-7)
	if st, _ := os.Stat(c.Root.join(ClassicRPCHtpasswd)); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	if ch := Priv.(*classicPriv).chowns; len(ch) != 0 {
		t.Errorf("chowned without the group: %q", ch)
	}
	if cur := c.Root.ReadClassicRPC(); cur.Auth != "password" || cur.User != "ccu-client" || !cur.PasswordSet {
		t.Fatalf("after the pair: %+v", cur)
	}
	// the pair: the same sockets, a reload
	if last := cmds()[len(cmds())-1]; !strings.Contains(last, "reload") {
		t.Fatalf("the pair: %v", cmds())
	}
	gen, err := c.SetPassword(ctx, "ccu", "", true, logf)
	if err != nil || len(gen) != ClassicRPCGenerated {
		t.Fatalf("generated %q %v", gen, err)
	}
	logMu.Lock()
	for _, l := range *logs {
		if strings.Contains(l, "a typed password") || strings.Contains(l, gen) {
			t.Errorf("a password in the log: %q", l)
		}
	}
	logMu.Unlock()
	// TLS on with the pair, then auth none removes the file
	if got, err := c.Set(ctx, ClassicRPC{Plain: true, TLS: true, Auth: "password"}, logf); err != nil || !got.TLS || got.Auth != "password" {
		t.Fatalf("tls: %+v %v", got, err)
	}
	if got, err := c.Set(ctx, ClassicRPC{Plain: false, TLS: true, Auth: "none"}, logf); err != nil || got.Plain || got.Auth != "none" {
		t.Fatalf("none: %+v %v", got, err)
	}
	if _, err := os.Stat(c.Root.join(ClassicRPCHtpasswd)); err == nil {
		t.Fatal("the pair is still there")
	}
	// the firewall owner: the TLS ports, 42000 only with hs485d
	ports := func() []int {
		var out []int
		for _, p := range c.Root.ClassicRPCOwner() {
			out = append(out, p.Port)
		}
		return out
	}
	if got := ports(); len(got) != 3 || got[0] != 42001 {
		t.Fatalf("owner: %v", got)
	}
	_ = os.MkdirAll(c.Root.join("/run/occulite/radio"), 0o755)
	_ = os.WriteFile(c.Root.join(HS485DEnabledMarker), nil, 0o644)
	if got := ports(); len(got) != 4 || got[3] != 42000 {
		t.Fatalf("owner with hs485d: %v", got)
	}
	if p := c.Root.ClassicRPCOwner()[0]; p.Comment != firewall.XMLRPCComments[42001] {
		t.Errorf("comment %q", p.Comment)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// B-7, B-120: where the system has lighttpd's group, the pair is root:www-data 0640 - the gid from
// the system's own /etc/group, not the host's (the host of this test may have a www-data of its own
// with another id, or none).
func TestClassicRPCPairGroupReadable(t *testing.T) {
	c, _, _ := classicRig(t)
	if err := os.MkdirAll(c.Root.join("/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.Root.join("/etc/group"), []byte("root:x:0:\nwww-data:x:4242:\nocculite:x:999:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetPassword(t.Context(), "ccu-client", "a typed password", false, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(c.Root.join(ClassicRPCHtpasswd)); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v, want 0640", st.Mode())
	}
	if ch := Priv.(*classicPriv).chowns; len(ch) != 1 || ch[0] != "classic-rpc.htpasswd 0:4242" {
		t.Errorf("chowns %q, want the pair to root:4242", ch)
	}
}

func TestRootGroupID(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/group": "root:x:0:\nwww-data:x:33:\nbad:x:nan:\nnocolon\n\n"})
	for name, want := range map[string]int{"root": 0, "www-data": 33, "bad": -1, "nocolon": -1, "": -1, "missing": -1} {
		gid, ok := r.GroupID(name)
		if (want >= 0) != ok || ok && gid != want {
			t.Errorf("GroupID(%q) = %d %v, want %d", name, gid, ok, want)
		}
	}
	if !r.HasGroup("bad") || r.HasGroup("nocolon") || r.HasGroup("") {
		t.Error("HasGroup: a line with a colon names a group, whatever its id; one without does not")
	}
}
