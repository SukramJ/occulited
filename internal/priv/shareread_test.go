package priv

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// openccu-lite B-217: a share's folder is listed, and a backup file on it opened, as root - only
// under /media/net/<id>, never through a link, and shareopen only for a backup file.
func TestShareReadBoundary(t *testing.T) {
	root := t.TempDir()
	c, _ := mountHelper(t, root)
	ctx := context.Background()
	for _, d := range []string{"media/net/nas/lab/ccu", "media/usb1", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lab := filepath.Join(root, "media/net/nas/lab")
	_ = os.WriteFile(filepath.Join(lab, "box-1-2026-09-25-0007.sbk"), []byte("backup"), 0o600)
	_ = os.WriteFile(filepath.Join(lab, "notes.txt"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(root, "etc/shadow.sbk"), []byte("secret"), 0o600)
	_ = os.Symlink(filepath.Join(root, "etc"), filepath.Join(root, "media/net/nas/evil"))
	_ = os.Symlink(filepath.Join(root, "etc/shadow.sbk"), filepath.Join(lab, "link-1-2026-09-25-0007.sbk"))

	entries, err := ListShare(ctx, c, lab)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ShareEntry{}
	for _, e := range entries {
		got[e.Name] = e
	}
	if !got["ccu"].Dir || got["box-1-2026-09-25-0007.sbk"].Size != 6 || got["box-1-2026-09-25-0007.sbk"].MTime.IsZero() || !got["link-1-2026-09-25-0007.sbk"].Link || len(got) != 4 {
		t.Fatalf("%+v", entries)
	}
	// the share's root, and a folder that is not there: ENOENT, as an error the caller can tell
	if _, err := ListShare(ctx, c, filepath.Join(root, "media/net/nas")); err != nil {
		t.Fatal(err)
	}
	if _, err := ListShare(ctx, c, filepath.Join(root, "media/net/nas/new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	// through a link: refused by the descriptor check, not listed
	if _, err := ListShare(ctx, c, filepath.Join(root, "media/net/nas/evil")); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a link: %v", err)
	}
	if _, err := ListShare(ctx, c, filepath.Join(root, "media/net/nas/evil/x")); err == nil {
		t.Fatal("below a link listed")
	}
	for _, bad := range []string{"/etc", "/media/usb1", "/media/net", "/media/net/../etc", "/media/net/NAS/x", "/media/net/nas/../../etc"} {
		if _, err := c.ShareList(ctx, root+bad); !errors.Is(err, ErrRefused) {
			t.Errorf("%s: %v", bad, err)
		}
	}
	// shareopen: a backup file, read-only
	f, err := c.ShareOpen(filepath.Join(lab, "box-1-2026-09-25-0007.sbk"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "backup" {
		t.Fatalf("%q", b)
	}
	if _, err := c.ShareOpen(filepath.Join(lab, "gone-1-2026-09-25-0007.sbk")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	for _, bad := range []string{"/media/net/nas/lab/notes.txt", "/etc/shadow.sbk", "/media/net/nas/evil/shadow.sbk", "/media/usb1/x.sbk", "/media/net/x.sbk"} {
		if f, err := c.ShareOpen(root + bad); err == nil {
			f.Close()
			t.Errorf("%s opened", bad)
		}
	}
	// a link named like a backup: not followed
	if f, err := c.ShareOpen(filepath.Join(lab, "link-1-2026-09-25-0007.sbk")); err == nil {
		f.Close()
		t.Error("a link opened")
	}
	if err := (&ShareError{Errno: "EACCES", Msg: "x"}); !errors.Is(err, syscall.EACCES) || !strings.Contains(err.Error(), "x") {
		t.Fatal("ShareError")
	}
}
