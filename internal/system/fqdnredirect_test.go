package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFQDN: <host>.<domain> in lower case, and nothing S50lighttpd would not accept.
func TestFQDN(t *testing.T) {
	for _, c := range []struct{ host, domain, want string }{
		{"ccu", "home.arpa", "ccu.home.arpa"},
		{"CCU", "Home.Arpa.", "ccu.home.arpa"},
		{" ccu ", " lan ", "ccu.lan"},
		{"ccu", "", ""},
		{"", "lan", ""},
		{"ccu.lan", "home.arpa", ""},
		{"ccu_1", "lan", ""},
		{"-ccu", "lan", ""},
		{strings.Repeat("a", 64), "lan", ""},
	} {
		if got := FQDN(c.host, c.domain); got != c.want {
			t.Errorf("FQDN(%q, %q) = %q, want %q", c.host, c.domain, got, c.want)
		}
	}
}

// TestValidFQDN is S50lighttpd's rule: lower case, two labels or more, no empty label.
func TestValidFQDN(t *testing.T) {
	for _, c := range []struct {
		name string
		want bool
	}{
		{"ccu.home.arpa", true},
		{"ccu-2.lan", true},
		{"ccu", false},
		{"CCU.lan", false},
		{"ccu..lan", false},
		{"ccu.lan.", false},
		{".ccu.lan", false},
		{"ccu-.lan", false},
		{`ccu.lan" + "x`, false},
		{"ccu.lan\n", false},
		{strings.Repeat("a.", 126) + "ab", false}, // 254 characters
	} {
		if got := ValidFQDN(c.name); got != c.want {
			t.Errorf("ValidFQDN(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestHTTPSSettingsFQDNMarker: the marker is the switch; its name is read as the script reads it.
func TestHTTPSSettingsFQDNMarker(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	for _, c := range []struct {
		content string // "-" = no marker
		on      bool
		target  string
	}{
		{"-", false, ""},
		{"ccu.home.arpa\n", true, "ccu.home.arpa"},
		{"CCU.Home.Arpa\r\n", true, "ccu.home.arpa"},
		{"ccu.home.arpa\nsomething else\n", true, "ccu.home.arpa"},
		{"not a name\n", true, ""},
		{"ccu", true, ""},
		{"", true, ""},
	} {
		_ = os.Remove(filepath.Join(dir, FQDNRedirectMarker))
		if c.content != "-" {
			_ = os.WriteFile(filepath.Join(dir, FQDNRedirectMarker), []byte(c.content), 0o644)
		}
		if got := root.HTTPSSettings(); got.RedirectFQDN != c.on || got.FQDNTarget != c.target {
			t.Errorf("%q: %+v", c.content, got)
		}
	}
}

// TestHTTPSConfigSetFQDN: the marker holds the name, one reload; nothing when nothing changed; a
// name the script would ignore is refused; a hand-broken marker stays while the other switches are
// saved; off removes it.
func TestHTTPSConfigSetFQDN(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	marker := filepath.Join(dir, FQDNRedirectMarker)
	_ = os.MkdirAll(filepath.Join(dir, "etc/config"), 0o755)
	var cmds []string
	rec := func(_ context.Context, name string, args ...string) ([]byte, error) {
		cmds = append(cmds, name+" "+strings.Join(args, " "))
		return nil, nil
	}
	var lines []string
	log := func(l string) { lines = append(lines, l) }
	h := HTTPSConfig{Root: root, Run: rec, Systemd: true}
	ctx := context.Background()

	got, err := h.Set(ctx, HTTPSSettings{RedirectFQDN: true, FQDNTarget: "ccu.home.arpa"}, log)
	if err != nil || got != (HTTPSSettings{HSTSMaxAgeDays: DefaultHSTSMaxAgeDays, RedirectFQDN: true, FQDNTarget: "ccu.home.arpa"}) {
		t.Fatalf("on: %v %+v", err, got)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "ccu.home.arpa\n" {
		t.Fatalf("marker: %v %q", err, b)
	}
	if len(cmds) != 1 || !strings.Contains(strings.Join(lines, "\n"), "bare host name redirect to ccu.home.arpa") {
		t.Fatalf("one reload, logged: %v %v", cmds, lines)
	}
	cmds = nil
	if _, err := h.Set(ctx, HTTPSSettings{RedirectFQDN: true, FQDNTarget: "ccu.home.arpa"}, log); err != nil || len(cmds) != 0 {
		t.Fatalf("unchanged: %v %v", err, cmds)
	}
	for _, bad := range []string{"", "ccu", "CCU.home.arpa", "ccu.home.arpa\"\ninclude"} {
		if _, err := h.Set(ctx, HTTPSSettings{RedirectFQDN: true, FQDNTarget: bad}, log); !errors.Is(err, ErrHTTPSInvalid) || len(cmds) != 0 {
			t.Fatalf("%q: %v %v", bad, err, cmds)
		}
	}
	if b, _ := os.ReadFile(marker); string(b) != "ccu.home.arpa\n" {
		t.Fatalf("a refused name was written: %q", b)
	}
	// a marker broken by hand: saving the redirect to HTTPS leaves it alone
	_ = os.WriteFile(marker, []byte("not a name\n"), 0o644)
	if _, err := h.Set(ctx, HTTPSSettings{RedirectHTTPS: true, RedirectFQDN: true}, log); err != nil || len(cmds) != 1 {
		t.Fatalf("broken marker kept: %v %v", err, cmds)
	}
	if b, _ := os.ReadFile(marker); string(b) != "not a name\n" {
		t.Fatalf("the broken marker was rewritten: %q", b)
	}
	cmds = nil
	got, err = h.Set(ctx, HTTPSSettings{RedirectHTTPS: true}, log)
	if err != nil || got.RedirectFQDN || len(cmds) != 1 {
		t.Fatalf("off: %v %+v %v", err, got, cmds)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the marker is still there")
	}
}

// TestFollowFQDN: a rename or a new domain rewrites the target, with one reload; the switch off,
// the same name, no domain or no usable name change nothing.
func TestFollowFQDN(t *testing.T) {
	cases := []struct {
		name       string
		marker     string // "-" = no marker
		host       string
		domain     string
		failReload bool
		prev       string
		rewritten  bool
		want       string // the marker afterwards, "-" = none
		reloads    int
	}{
		{"off: nothing", "-", "lite", "home.arpa", false, "", false, "-", 0},
		{"the same name: nothing", "ccu.home.arpa\n", "ccu", "home.arpa", false, "ccu.home.arpa", false, "ccu.home.arpa\n", 0},
		{"capitals in the marker are the same name", "CCU.Home.Arpa\n", "CCU", "home.arpa", false, "ccu.home.arpa", false, "CCU.Home.Arpa\n", 0},
		{"a rename: rewritten", "ccu.home.arpa\n", "lite", "home.arpa", false, "ccu.home.arpa", true, "lite.home.arpa\n", 1},
		{"a new domain: rewritten", "ccu.home.arpa\n", "ccu", "lan", false, "ccu.home.arpa", true, "ccu.lan\n", 1},
		{"a broken marker gets the name", "garbage\n", "ccu", "home.arpa", false, "", true, "ccu.home.arpa\n", 1},
		{"no domain yet: nothing", "ccu.home.arpa\n", "lite", "", false, "", false, "ccu.home.arpa\n", 0},
		{"a host name with a dot: nothing", "ccu.home.arpa\n", "lite.lan", "home.arpa", false, "", false, "ccu.home.arpa\n", 0},
		{"a failed reload: the error, the marker written", "ccu.home.arpa\n", "lite", "home.arpa", true, "ccu.home.arpa", true, "lite.home.arpa\n", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, FQDNRedirectMarker)
			_ = os.MkdirAll(filepath.Dir(marker), 0o755)
			if c.marker != "-" {
				_ = os.WriteFile(marker, []byte(c.marker), 0o644)
			}
			reloads := 0
			run := func(context.Context, string, ...string) ([]byte, error) {
				reloads++
				if c.failReload {
					return []byte("unit not active"), errors.New("exit 1")
				}
				return nil, nil
			}
			var lines []string
			h := &HTTPSConfig{Root: Root(dir), Run: run}
			prev, rewritten, err := h.FollowFQDN(context.Background(), c.host, c.domain, func(l string) { lines = append(lines, l) })
			if (err != nil) != c.failReload || prev != c.prev || rewritten != c.rewritten || reloads != c.reloads {
				t.Fatalf("got %q %v %v, %d reloads", prev, rewritten, err, reloads)
			}
			b, rerr := os.ReadFile(marker)
			if got := string(b); (c.want == "-") != (rerr != nil) || (c.want != "-" && got != c.want) {
				t.Fatalf("marker %q (%v), want %q", got, rerr, c.want)
			}
			if c.rewritten && !strings.Contains(strings.Join(lines, "\n"), "follows the box's name: "+strings.TrimSpace(c.want)) {
				t.Fatalf("not logged: %v", lines)
			}
		})
	}
}
