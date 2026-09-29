package system

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// sbkWithSignature writes an outer .sbk whose inner archive is payload, signed as signature.
func sbkWithSignature(t *testing.T, payload, signature, keyIndex string) string {
	t.Helper()
	var outer bytes.Buffer
	ow := tar.NewWriter(&outer)
	for _, m := range []struct{ name, body string }{{"usr_local.tar.gz", payload}, {"signature", signature}, {"key_index", keyIndex}, {"firmware_version", "VERSION=3.89.11\n"}} {
		if m.body == "-" {
			continue
		}
		_ = ow.WriteHeader(&tar.Header{Name: m.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(m.body))})
		_, _ = ow.Write([]byte(m.body))
	}
	_ = ow.Close()
	path := filepath.Join(t.TempDir(), "restore-x.sbk")
	if err := os.WriteFile(path, outer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBackupSignature: the passphrase check against a backup's signature (openccu-lite task 296).
// The signatures are crypttool's own (`crypttool -s -t 3 -k <passphrase>` of the payload on a
// lab system, made-up passphrases): the check is the firmware's construction, not a guess.
func TestBackupSignature(t *testing.T) {
	payload := "hello world\n"
	for _, c := range []struct {
		name, sig, index, pass, want string
	}{
		{"match", "4296d8824f59b93258ae0aff52772dbf\n", "1\n", "abc", KeyCheckMatch},
		{"match, upper-case signature and blanks around the passphrase", "4296D8824F59B93258AE0AFF52772DBF", "2", "  abc\t", KeyCheckMatch},
		{"another passphrase", "94dfc6f40682296de67e1624d2990427\n", "3\n", "lab296x", KeyCheckMatch},
		{"mismatch", "94dfc6f40682296de67e1624d2990427\n", "3\n", "abc", KeyCheckMismatch},
		{"skipped", "94dfc6f40682296de67e1624d2990427\n", "3\n", "", KeyCheckSkipped},
		{"skipped, blanks only", "94dfc6f40682296de67e1624d2990427\n", "3\n", "   ", KeyCheckSkipped},
		// the factory key: nothing to know, whatever is typed
		{"default key", "2d132dadf2923e1aa10a4ed7a08313ea\n", "0\n", "abc", KeyCheckNone},
		{"default key, nothing typed", "2d132dadf2923e1aa10a4ed7a08313ea\n", "0\n", "", KeyCheckNone},
		{"no key_index file", "2d132dadf2923e1aa10a4ed7a08313ea\n", "-", "abc", KeyCheckNone},
		{"a damaged signature", "not hex at all, not hex at all!!\n", "1\n", "abc", KeyCheckMismatch},
		{"no signature", "-", "1\n", "abc", KeyCheckMismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := ReadBackupSignature(sbkWithSignature(t, payload, c.sig, c.index))
			if err != nil {
				t.Fatal(err)
			}
			if got := s.KeyCheck(c.pass); got != c.want {
				t.Errorf("KeyCheck(%q) = %s, want %s (%+v)", c.pass, got, c.want, s)
			}
		})
	}
	// another payload does not match the same signature
	s, _ := ReadBackupSignature(sbkWithSignature(t, "hello world", "4296d8824f59b93258ae0aff52772dbf", "1"))
	if s.Matches("abc") {
		t.Error("the signature matched another payload")
	}
	if _, err := ReadBackupSignature(sbkWithSignature(t, "-", "4296d8824f59b93258ae0aff52772dbf", "1")); err != ErrNotSBK {
		t.Errorf("no inner archive: %v", err)
	}
	if _, err := ReadBackupSignature(filepath.Join(t.TempDir(), "missing.sbk")); err == nil {
		t.Error("a missing file read")
	}
	bad := filepath.Join(t.TempDir(), "restore-bad.sbk")
	_ = os.WriteFile(bad, []byte("this is not a tar archive at all, but long enough to be read as a header block"), 0o600)
	if _, err := ReadBackupSignature(bad); err == nil {
		t.Error("a damaged archive read")
	}
}

// Without crypttool (a development root) the system's side is not known; the helper is not asked.
func TestSystemKeyMatchesWithoutCrypttool(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/.keep": ""})
	if set, match, known := r.SystemKeyMatches(context.Background(), "abcde"); set || match || known {
		t.Errorf("%v %v %v", set, match, known)
	}
}
