package tclrega

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// B-102: occulited's session mirror names a session's file by the SHA-256 of its id, and the shim
// hashes the id an addon asks about with its own sha256.h.

func compiler(t *testing.T) string {
	t.Helper()
	for _, c := range []string{os.Getenv("CC"), "cc", "gcc"} {
		if c == "" {
			continue
		}
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	t.Skip("no C compiler")
	return ""
}

// shimDir is deploy/tclrega.
func shimDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.Abs(filepath.Join("..", "..", "deploy", "tclrega"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSHA256MatchesGo(t *testing.T) {
	cc := compiler(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "sha.c")
	prog := `#include <stdio.h>
#include "sha256.h"
static unsigned char buf[1 << 20];
int main(void) {
    size_t n = fread(buf, 1, sizeof buf, stdin);
    char out[65];
    occulite_sha256_hex(buf, n, out);
    puts(out);
    return 0;
}
`
	if err := os.WriteFile(src, []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "sha")
	if out, err := exec.Command(cc, "-std=c99", "-Wall", "-Wextra", "-Werror", "-O2", "-I", shimDir(t), "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	var inputs [][]byte
	for n := 0; n <= 130; n++ { // every padding case around one and two blocks
		inputs = append(inputs, bytes.Repeat([]byte{byte('a' + n%26)}, n))
	}
	big := make([]byte, 5000)
	for i := range big {
		big[i] = byte(i * 7)
	}
	inputs = append(inputs, big, []byte("ABCDEFGHIJ"), []byte("anonymous0"), []byte("LIVESESSIONAAAAAAAAAAAAAAA"), []byte{0xff, 0x00, 0x80})
	for _, in := range inputs {
		cmd := exec.Command(bin)
		cmd.Stdin = bytes.NewReader(in)
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(in)
		if got, want := strings.TrimSpace(string(out)), hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%d bytes: %s, want %s", len(in), got, want)
		}
	}
}

// The shim itself, loaded into tclsh as an addon's CGI loads it: the session check answers the user
// for a session whose file is named by the hash, for an alias whose file is named by its hash in
// the alias directory (task 125), and nobody for a file named by the id itself, for an alias
// among the sessions or a session id among the aliases. Needs a Tcl installation:
// OCCULITE_TCL_PREFIX=<prefix> with include/tcl.h and bin/tclsh*.
func TestShimLooksUpTheHash(t *testing.T) {
	prefix := os.Getenv("OCCULITE_TCL_PREFIX")
	if prefix == "" {
		t.Skip("set OCCULITE_TCL_PREFIX to a Tcl 8.6 installation (include/tcl.h, bin/tclsh*) to load the shim")
	}
	cc := compiler(t)
	tclsh, _ := filepath.Glob(filepath.Join(prefix, "bin", "tclsh*"))
	if len(tclsh) == 0 {
		t.Fatalf("no tclsh under %s/bin", prefix)
	}
	dir := t.TempDir()
	mirror := filepath.Join(dir, "sessions")
	legacy := filepath.Join(dir, "legacy-sessions")
	for _, d := range []string{mirror, legacy} {
		if err := os.Mkdir(d, 0o711); err != nil {
			t.Fatal(err)
		}
	}
	so := filepath.Join(dir, "tclrega.so")
	// as the fork's package builds it: -fPIC -shared against tcl.h, plus the test's directories
	build := exec.Command(cc, "-fPIC", "-shared", "-O2", "-Wall", "-Werror",
		`-DOCCULITE_SESSION_DIR="`+mirror+`"`, `-DOCCULITE_LEGACY_DIR="`+legacy+`"`,
		"-I", filepath.Join(prefix, "include"), "-o", so, filepath.Join(shimDir(t), "tclrega.c"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile the shim: %v\n%s", err, out)
	}
	file := func(dir, name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	key := func(id string) string { s := sha256.Sum256([]byte(id)); return hex.EncodeToString(s[:]) }
	const live = "LIVESESSIONAAAAAAAAAAAAAAA"
	file(mirror, key(live), "admin\n")
	file(mirror, "RAWSESS001", "admin\n") // the mirror's old shape
	file(mirror, strings.ToUpper(key("UPPERCASEAAAAAAAAAAAAAAAAA")), "admin\n")
	file(mirror, key("aliasInSes"), "admin\n") // an alias's hash among the sessions: no alias
	file(legacy, key("aliasLive1"), "bob\n"+key(live)+"\n")
	file(legacy, key("SIDINLEGACYAAAAAAAAAAAAAAA"), "admin\n") // a session id's hash among the aliases: no session
	file(legacy, "aliasRaw01", "admin\n")
	script := `load ` + so + `
foreach s {` + live + ` @` + live + `@ RAWSESS001 UPPERCASEAAAAAAAAAAAAAAAAA STALESESSIONAAAAAAAAAAAAAA aliasLive1 @aliasLive1@ aliasInSes SIDINLEGACYAAAAAAAAAAAAAAA aliasRaw01 aliasGone1} {
    puts [rega_script "Write(system.GetSessionVarStr('$s'));"]
}
`
	cmd := exec.Command(tclsh[0])
	cmd.Stdin = strings.NewReader(script)
	cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+filepath.Join(prefix, "lib"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("tclsh: %v\n%s", err, out)
	}
	want := "STDOUT admin\nSTDOUT admin\nSTDOUT {}\nSTDOUT {}\nSTDOUT {}\nSTDOUT bob\nSTDOUT bob\nSTDOUT {}\nSTDOUT {}\nSTDOUT {}\nSTDOUT {}\n"
	if string(out) != want {
		t.Errorf("answers:\n%s\nwant:\n%s", out, want)
	}
}
