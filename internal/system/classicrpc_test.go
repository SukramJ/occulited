package system

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hobbyquaker/occulited/internal/firewall"
)

func classicRig(t *testing.T) (*ClassicRPCConfig, func() []string, *[]string) {
	t.Helper()
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	old := Priv
	Priv = &fwPriv{}
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
	if st, _ := os.Stat(c.Root.join(ClassicRPCHtpasswd)); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
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
