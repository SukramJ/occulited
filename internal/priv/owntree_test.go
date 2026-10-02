package priv

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/hobbyquaker/occulited/internal/addonunit"
)

// Task 107: the helper's owntree operation - one addon's directories and that addon's uid, nothing
// else - and the recursive chown that is ownwalk's walk now.

func ownTreeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for p, content := range map[string]string{
		"etc/passwd":                 "root:x:0:0::/:/bin/sh\naddon-hmm:x:30007:30007::/usr/local/addons/hmm:/bin/false\naddon-www:x:30009:30009::/usr/local/addons/www:/bin/false\n",
		"usr/local/addons/hmm/var/x": "",
		"outside/secret":             "",
		// B-295: the helper gives a tree only to an addon its own drop-in confines
		"usr/local/etc/config/addon-policy/hmm.conf": addonunit.DropIn{ID: "hmm", Mode: "confined", UID: 30007}.Render(),
		"usr/local/etc/config/addon-policy/www.conf": addonunit.DropIn{ID: "www", Mode: "confined", UID: 30009}.Render(),
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestOwnTreeAllowed(t *testing.T) {
	root := ownTreeRoot(t)
	// accounts that exist (AddAddonUser makes one for any id), with drop-ins that do not confine
	// the addon to them: none, root's, another uid's, a text the helper did not render
	if err := os.WriteFile(filepath.Join(root, "etc/passwd"), []byte("root:x:0:0::/:/bin/sh\naddon-hmm:x:30007:30007::/usr/local/addons/hmm:/bin/false\naddon-www:x:30009:30009::/usr/local/addons/www:/bin/false\n"+
		"addon-noconf:x:30014:30014::/:/bin/false\naddon-jp:x:30011:30011::/:/bin/false\naddon-red:x:30012:30012::/:/bin/false\naddon-fake:x:30013:30013::/:/bin/false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for id, text := range map[string]string{
		"jp":   addonunit.DropIn{ID: "jp", Mode: "root"}.Render(),
		"red":  addonunit.DropIn{ID: "red", Mode: "confined", UID: 30099}.Render(),
		"fake": "# mode=confined uid=30013\n[Service]\nUser=addon-fake\nGroup=addon-fake\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "usr/local/etc/config/addon-policy", id+".conf"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := DefaultPolicy(root, "/usr/local/etc/occulite")
	at := func(s string) string { return root + s }
	std := []string{at("/usr/local/addons/hmm"), at("/usr/local/etc/config/addons/hmm"), at("/usr/local/etc/config/addons/www/hmm")}
	many := make([]string, 33)
	for i := range many {
		many[i] = at("/usr/local/addons/hmm")
	}
	for _, tc := range []struct {
		name string
		id   string
		uid  int
		dirs []string
		want string // "" = allowed, else a part of the refusal
	}{
		{name: "the standard directories", id: "hmm", uid: 30007, dirs: std},
		{name: "data directories", id: "hmm", uid: 30007, dirs: append(append([]string{}, std...), at("/usr/local/hmm"), at("/usr/local/hmm-data/sub"))},
		{name: "another addon's uid", id: "hmm", uid: 30009, dirs: std, want: "addon-hmm has uid 30007, not 30009"},
		{name: "root", id: "hmm", uid: 0, dirs: std, want: "not an addon account"},
		{name: "below the addon range", id: "hmm", uid: 29999, dirs: std, want: "not an addon account"},
		{name: "no such user", id: "cuxd", uid: 30010, dirs: []string{at("/usr/local/addons/cuxd")}, want: "no user addon-cuxd"},
		{name: "not an id", id: "../x", uid: 30007, dirs: std, want: "not an addon account"},
		{name: "another addon's directory", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/addons/redmatic")}, want: "not a directory of hmm"},
		{name: "the addons tree", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/addons")}, want: "not a directory of hmm"},
		{name: "the config tree", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/etc")}, want: "not a directory of hmm"},
		{name: "every addon's www directory", id: "www", uid: 30009, dirs: []string{at("/usr/local/etc/config/addons/www")}, want: "not a directory of www"},
		{name: "www's own www directory", id: "www", uid: 30009, dirs: []string{at("/usr/local/etc/config/addons/www/www")}},
		{name: "below a shared tree", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/tmp/hmm")}, want: "not a directory of hmm"},
		{name: "the userfs", id: "hmm", uid: 30007, dirs: []string{at("/usr/local")}, want: "not a directory of hmm"},
		{name: "a dotfile", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/.firmwareUpdate")}, want: "not a directory of hmm"},
		{name: "outside the userfs", id: "hmm", uid: 30007, dirs: []string{at("/etc/config")}, want: "not a directory of hmm"},
		{name: "outside the root", id: "hmm", uid: 30007, dirs: []string{"/usr/local/addons/hmm"}, want: "not a clean path"},
		{name: "unclean", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/addons/other/../hmm")}, want: "not a clean path"},
		{name: "a trailing slash", id: "hmm", uid: 30007, dirs: []string{at("/usr/local/addons/hmm/")}, want: "not a clean path"},
		{name: "none", id: "hmm", uid: 30007, want: "0 directories"},
		{name: "too many", id: "hmm", uid: 30007, dirs: many, want: "33 directories"},
		// B-295: an addon whose drop-in does not confine it to that uid
		{name: "an addon without a drop-in", id: "noconf", uid: 30014, dirs: []string{at("/usr/local/addons/noconf")}, want: "noconf is not confined to uid 30014"},
		{name: "a root drop-in", id: "jp", uid: 30011, dirs: []string{at("/usr/local/addons/jp")}, want: "jp is not confined to uid 30011"},
		{name: "a drop-in for another uid", id: "red", uid: 30012, dirs: []string{at("/usr/local/addons/red")}, want: "red is not confined to uid 30012"},
		{name: "a drop-in that is not the helper's text", id: "fake", uid: 30013, dirs: []string{at("/usr/local/addons/fake")}, want: "fake is not confined to uid 30013"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := p.OwnTreeAllowed(tc.id, tc.dirs, tc.uid)
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("got %v, want a refusal with %q", err, tc.want)
			}
		})
	}
	// no chown on the program list any more: it checked the program and never its arguments
	if p.programAllowed("chown", []string{"-R", "0:0", "/"}) || p.programAllowed("/bin/chown", []string{"-R", "0:0", "/"}) {
		t.Error("chown may still run through the helper")
	}
}

// Through the socket: the dry run's result comes back whole, a refusal is ErrRefused and walks nothing.
func TestOwnTreeThroughTheHelper(t *testing.T) {
	root := ownTreeRoot(t)
	if err := os.Symlink(filepath.Join(root, "outside/secret"), filepath.Join(root, "usr/local/addons/hmm/var/link")); err != nil {
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
	dirs := []string{root + "/usr/local/addons/hmm", root + "/usr/local/etc/config/addons/hmm", root + "/usr/local/etc/config/addons/www/hmm"}
	res, err := c.OwnTree("hmm", dirs, 30007, OwnTreeOptions{DryRun: true})
	// the tester owns the tree: hmm, var and x are "wrong" for uid 30007; the link is not followed
	if err != nil || res.Checked != 4 || res.Wrong != 3 || res.Fixed != 0 || res.Symlinks != 1 || len(res.Skipped) != 2 || res.Problem() != "" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	// task 110's quick check crosses the socket: hmm and var, nothing below var (neither x nor the link)
	if res, err := c.OwnTree("hmm", dirs, 30007, OwnTreeOptions{DryRun: true, Quick: true}); err != nil || res.Checked != 2 || res.Wrong != 2 || res.Symlinks != 0 || res.TooDeep != 1 || res.Fixed != 0 {
		t.Fatalf("quick dry run: %+v %v", res, err)
	}
	if _, err := c.OwnTree("hmm", []string{root + "/outside"}, 30007, OwnTreeOptions{}); !errors.Is(err, ErrRefused) {
		t.Errorf("outside the addon's directories: %v", err)
	}
	if _, err := c.OwnTree("hmm", dirs, 30009, OwnTreeOptions{}); !errors.Is(err, ErrRefused) {
		t.Errorf("another addon's uid: %v", err)
	}
}

// The recursive chown is ownwalk's walk. As a normal user it can still be seen at work: a user may give
// a file to another group of its own. A link planted in the tree to a file outside is not followed.
func TestLocalChownRecursiveFollowsNoLink(t *testing.T) {
	groups, _ := os.Getgroups()
	other := -1
	for _, g := range groups {
		if g != os.Getegid() {
			other = g
			break
		}
	}
	if other < 0 {
		t.Skip("the test user has no second group")
	}
	root := ownTreeRoot(t)
	tree := filepath.Join(root, "usr/local/addons/hmm")
	secret := filepath.Join(root, "outside/secret")
	if err := os.Symlink(secret, filepath.Join(tree, "var/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(tree, "var/dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := (Local{}).Chown(tree, os.Getuid(), other, true); err != nil {
		t.Fatal(err)
	}
	gid := func(p string) int {
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			t.Fatal(err)
		}
		return int(st.Gid)
	}
	for _, p := range []string{tree, filepath.Join(tree, "var"), filepath.Join(tree, "var/x")} {
		if gid(p) != other {
			t.Errorf("%s was not given to group %d", p, other)
		}
	}
	for _, p := range []string{secret, filepath.Join(root, "outside"), filepath.Join(tree, "var/link"), filepath.Join(tree, "var/dirlink")} {
		if gid(p) == other {
			t.Errorf("%s was changed: a link was followed or changed", p)
		}
	}
	if err := (Local{}).Chown(filepath.Join(root, "nothing"), os.Getuid(), other, true); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a missing tree: %v", err)
	}
}
