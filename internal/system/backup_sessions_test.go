package system

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/auth"
)

// B-102, D-67: the session store is not in a backup. Through CreateBackup, with a createBackup.sh
// that archives /usr/local with the tar options of the firmware's own
// (buildroot-external/overlay/base-openccu/bin/createBackup.sh in the fork: --exclude-tag=.nobackup
// among them) and packs the .sbk the same way; the signature steps are left out.
func TestBackupLeavesOutTheSessionStore(t *testing.T) {
	if out, err := exec.Command("tar", "--version").Output(); err != nil || !bytes.Contains(out, []byte("GNU tar")) {
		t.Skip("needs GNU tar (--exclude-tag), as the firmware's /bin/tar is")
	}
	root := t.TempDir()
	r := Root(root)
	state := r.join("/usr/local/etc/occulite")
	for _, d := range []string{state, r.join(BackupDir), r.join("/bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\nset -e\n" +
		"tmp=$(mktemp -d -p \"$(dirname \"$1\")\")\ntrap 'rm -rf \"$tmp\"' EXIT\n" +
		"tar -C '" + root + "' --owner=root --group=root --exclude=usr/local/tmp --exclude=\"usr/local/.*\" --exclude=usr/local/lost+found --exclude=usr/local/eQ-3-Backup --exclude-tag=.nobackup --one-file-system --ignore-failed-read --warning=no-file-changed -czf \"$tmp/usr_local.tar.gz\" usr/local\n" +
		"tar -C \"$tmp\" --owner=root --group=root -cf \"$1\" usr_local.tar.gz\n"
	if err := os.WriteFile(r.join("/bin/createBackup.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	// occulited's state as a running box has it: an account, a login, the session store
	store, err := auth.Open(state, auth.Options{SessionDir: r.join("/run/occulite/sessions"), SessionFile: filepath.Join(state, "sessions", "sessions.json"), RestoreMethods: []string{auth.MethodPassword}})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Setup("admin", "secret123"); err != nil {
		t.Fatal(err)
	}
	sess, err := store.Login("admin", "secret123", "192.168.0.24", "Firefox")
	if err != nil {
		t.Fatal(err)
	}

	backup := func() map[string][]byte {
		t.Helper()
		path, err := r.CreateBackup(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		return usrLocalOf(t, path)
	}
	files := backup()
	if _, ok := files["usr/local/etc/occulite/users.json"]; !ok {
		t.Fatalf("users.json is not in the backup: %v", names(files))
	}
	for name, body := range files {
		if strings.Contains(name, "sessions.json") {
			t.Errorf("the session store is in the backup: %s", name)
		}
		if bytes.Contains(body, []byte(sess.ID)) || bytes.Contains(body, []byte(auth.SessionHandle(sess.ID)[:6])) {
			t.Errorf("%s in the backup names the session", name)
		}
	}

	// the test is not vacuous: without the tag the same backup takes the store
	if err := os.Remove(filepath.Join(state, "sessions", ".nobackup")); err != nil {
		t.Fatal(err)
	}
	if _, ok := backup()["usr/local/etc/occulite/sessions/sessions.json"]; !ok {
		t.Fatal("without .nobackup the store should have been archived; the check proves nothing")
	}
}

// usrLocalOf reads an .sbk's usr_local.tar.gz: every regular file by its name.
func usrLocalOf(t *testing.T, sbk string) map[string][]byte {
	t.Helper()
	f, err := os.Open(sbk)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	outer := tar.NewReader(f)
	for {
		h, err := outer.Next()
		if err != nil {
			t.Fatalf("no usr_local.tar.gz in %s: %v", sbk, err)
		}
		if h.Name != "usr_local.tar.gz" {
			continue
		}
		gz, err := gzip.NewReader(outer)
		if err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{}
		inner := tar.NewReader(gz)
		for {
			h, err := inner.Next()
			if err == io.EOF {
				return files
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.Typeflag == tar.TypeReg {
				b, _ := io.ReadAll(inner)
				files[strings.TrimPrefix(h.Name, "./")] = b
			}
		}
	}
}

func names(files map[string][]byte) []string {
	var out []string
	for n := range files {
		out = append(out, n)
	}
	return out
}
