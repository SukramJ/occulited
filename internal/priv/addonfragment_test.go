package priv

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// occulited B-35: the helper reads an addon's lighttpd fragment - exactly
// /usr/local/addons/<id>/etc/lighttpd.conf, through os.Root on the tree - and nothing else: no other
// file of the tree, no other directory, no traversal, no link out of the tree, nothing too large.
func TestAddonFragmentRead(t *testing.T) {
	root := t.TempDir()
	w := func(rel, content string) string {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
		return p
	}
	frag := w("usr/local/addons/hmm/etc/lighttpd.conf", "url.redirect = ()\n")
	w("usr/local/addons/hmm/etc/hmm.env", "HMM_TOKEN=secret\n")
	w("etc/shadow", "root:SECRETHASH:::\n")
	w("usr/local/addons/big/etc/lighttpd.conf", strings.Repeat("#", AddonFragmentMax+1))
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/outlink/etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc/shadow"), filepath.Join(root, "usr/local/addons/outlink/etc/lighttpd.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/outdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "usr/local/addons/outdir/etc")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "usr/local/addons/dir/etc/lighttpd.conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	c, sock := logListHelper(t, root, nil)

	b, err := c.ReadAddonFragment(frag)
	if err != nil || string(b) != "url.redirect = ()\n" {
		t.Fatalf("the fragment: %q %v", b, err)
	}
	for rel, want := range map[string]error{
		"usr/local/addons/none/etc/lighttpd.conf":    fs.ErrNotExist,
		"usr/local/addons/outlink/etc/lighttpd.conf": ErrFragmentLink,
		"usr/local/addons/outdir/etc/lighttpd.conf":  ErrFragmentUnreachable,
		"usr/local/addons/big/etc/lighttpd.conf":     ErrFragmentTooLarge,
		"usr/local/addons/dir/etc/lighttpd.conf":     ErrFragmentNotRegular,
	} {
		b, err := c.ReadAddonFragment(filepath.Join(root, rel))
		if !errors.Is(err, want) || b != nil {
			t.Errorf("%s: %q %v, want %v", rel, b, err, want)
		}
		if err != nil && strings.Contains(err.Error(), "SECRETHASH") {
			t.Errorf("%s: the answer quotes the file behind the link", rel)
		}
	}
	for _, bad := range []string{
		"usr/local/addons/hmm/etc/hmm.env",
		"usr/local/addons/hmm/lighttpd.conf",
		"usr/local/addons/hmm/etc/lighttpd.conf.in",
		"usr/local/addons/../../etc/shadow",
		"usr/local/addons/hmm/../../../etc/lighttpd.conf",
		"usr/local/addons/hmm/x/etc/lighttpd.conf",
		"usr/local/addons/-x/etc/lighttpd.conf",
		"usr/local/etc/config/addons/hmm/etc/lighttpd.conf",
		"etc/lighttpd.conf",
	} {
		if b, err := c.ReadAddonFragment(filepath.Join(root, bad)); !errors.Is(err, ErrRefused) || b != nil {
			t.Errorf("%s: %q %v", bad, b, err)
		}
	}
	if b, err := c.ReadAddonFragment("/usr/local/addons/hmm/etc/lighttpd.conf"); !errors.Is(err, ErrRefused) || b != nil {
		t.Errorf("a path outside the policy's root: %q %v", b, err)
	}
	// a request with more than a path is refused
	if res, _ := rawLogList(t, sock, request{Op: opAddonFragment, Path: frag, Recursive: true}); res.OK || !strings.Contains(res.Error, "nothing else") {
		t.Errorf("with a flag: %+v", res)
	}
	// Local alone: a path of another shape is no fragment
	if _, err := (Local{}).ReadAddonFragment(filepath.Join(root, "etc/shadow")); err == nil {
		t.Error("Local read a file that is no fragment")
	}
}
