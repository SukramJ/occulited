package priv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openccu-lite B-238: the files of the named operations - shadow, the certificate and its markers -
// lie under the /etc/config/ write prefix, and the generic operations reached them through it. Every
// generic operation is refused for them now, in both spellings of the config directory, and so is
// every operation that would take the directory that holds them as a whole; the named operations
// and the prefix's other files are as before. Since B-293 the certificate's remove is a named
// operation as well (RemoveCertificate): no generic exception is left.
func TestNamedOperationFilesAreOutsideTheGenericOnes(t *testing.T) {
	root := t.TempDir()
	at := func(rel string) string { return filepath.Join(root, rel) }
	for _, d := range []string{"usr/local/etc/config/crRFD", "usr/local/tmp", "etc"} {
		if err := os.MkdirAll(at(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const shadow = "root:$6$old$oldhash:19000:0:99999:7:::\n"
	files := map[string]string{
		"usr/local/etc/config/shadow":             shadow,
		"usr/local/etc/config/server.pem":         "pem\n",
		"usr/local/etc/config/server.pem.managed": "acme\n",
		"usr/local/etc/config/server.pem.acme":    "acme\n",
		"usr/local/etc/config/rfd.conf":           "[rfd]\n",
		"usr/local/tmp/new":                       "new\n",
	}
	for rel, c := range files {
		if err := os.WriteFile(at(rel), []byte(c), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	// the image's link: /etc/config -> /usr/local/etc/config, root's in a root-owned directory
	if err := os.Symlink(at("usr/local/etc/config"), at("etc/config")); err != nil {
		t.Fatal(err)
	}
	pol := DefaultPolicy(root, "/usr/local/etc/occulite")
	srv := &Server{Policy: pol}
	ctx := context.Background()
	refused := func(req request) {
		t.Helper()
		if r := srv.do(ctx, req); !strings.HasPrefix(r.Error, "refused") {
			t.Errorf("%s %s%s%s -> %s: not refused: %+v", req.Op, req.Path, req.Src, req.Dst, req.Target, r)
		}
	}
	ok := func(req request) {
		t.Helper()
		if r := srv.do(ctx, req); r.Error != "" {
			t.Errorf("%s %s%s%s -> %s: %s", req.Op, req.Path, req.Src, req.Dst, req.Target, r.Error)
		}
	}

	named := []string{"shadow", "server.pem", "server.pem.managed", "server.pem.acme"}
	for _, dir := range []string{"etc/config/", "usr/local/etc/config/"} {
		for _, n := range named {
			p := at(dir + n)
			refused(request{Op: "write", Path: p, Data: []byte("root::0:0:::\n"), Mode: 0o644})
			refused(request{Op: "touch", Path: p, Mode: 0o644})
			refused(request{Op: "chmod", Path: p, Mode: 0o666})
			refused(request{Op: "chown", Path: p, UID: 30001, GID: 30001})
			refused(request{Op: "remove", Path: p}) // B-293: RemoveCertificate, no exception
			refused(request{Op: "removeall", Path: p})
			refused(request{Op: "mkdir", Path: p, Mode: 0o755})
			refused(request{Op: "rename", Src: p, Dst: at("usr/local/tmp/moved")})
			refused(request{Op: "rename", Src: at("usr/local/tmp/new"), Dst: p})
			refused(request{Op: "symlink", Path: at("usr/local/tmp/link"), Target: p})
		}
	}
	// the files are as they were
	for _, n := range named {
		if got, err := os.ReadFile(at("usr/local/etc/config/" + n)); err != nil || string(got) != files["usr/local/etc/config/"+n] {
			t.Errorf("%s: %q %v", n, got, err)
		}
	}

	// the directory that holds them, as a whole: moved away (the file written there, moved back),
	// removed, given away, opened up - in both spellings
	for _, dir := range []string{"usr/local/etc/config", "etc/config"} {
		refused(request{Op: "rename", Src: at(dir), Dst: at("usr/local/tmp/cfg")})
		refused(request{Op: "removeall", Path: at(dir)})
		refused(request{Op: "chmod", Path: at(dir), Mode: 0o777})
		refused(request{Op: "chmod", Path: at(dir), Mode: 0o775})
	}
	refused(request{Op: "rename", Src: at("usr/local/tmp/new"), Dst: at("usr/local/etc/config")})
	refused(request{Op: "chown", Path: at("usr/local/etc/config"), UID: 30001, GID: 30001})
	refused(request{Op: "chown", Path: at("usr/local/etc/config"), UID: 30001, GID: 30001, Recursive: true})
	// taking a bit away is fine (cleanup.go's hardening takes o+w off a world-writable one)
	ok(request{Op: "chmod", Path: at("usr/local/etc/config"), Mode: 0o750})
	if st, _ := os.Stat(at("usr/local/etc/config")); st.Mode().Perm() != 0o750 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	refused(request{Op: "chmod", Path: at("usr/local/etc/config"), Mode: 0o755}) // and not back up
	if err := os.Chmod(at("usr/local/etc/config"), 0o755); err != nil {
		t.Fatal(err)
	}
	// through a link the daemon made to the directory: the resolved path is the one checked
	ok(request{Op: "symlink", Path: at("usr/local/tmp/cfglink"), Target: at("usr/local/etc/config")})
	refused(request{Op: "write", Path: at("usr/local/tmp/cfglink/shadow"), Data: []byte("x"), Mode: 0o644})
	refused(request{Op: "chmod", Path: at("usr/local/tmp/cfglink"), Mode: 0o777})
	if got, _ := os.ReadFile(at("usr/local/etc/config/shadow")); string(got) != shadow {
		t.Errorf("shadow changed: %q", got)
	}

	// what stays: the prefix's other files and a directory below it that holds no named file; the
	// certificate and its markers go through RemoveCertificate (CertInstaller.Remove, B-293), in
	// either spelling, and nothing else does: not a marker alone, not another file
	ok(request{Op: "write", Path: at("etc/config/rfd.conf"), Data: []byte("[rfd]\nx\n"), Mode: 0o644})
	ok(request{Op: "write", Path: at("usr/local/etc/config/rfd.conf"), Data: []byte("[rfd]\n"), Mode: 0o644})
	ok(request{Op: "write", Path: at("usr/local/etc/config/shadow.bak"), Data: []byte("x"), Mode: 0o600})
	ok(request{Op: "rename", Src: at("usr/local/etc/config/crRFD"), Dst: at("usr/local/etc/config/crRFD.aside")})
	ok(request{Op: "removeall", Path: at("usr/local/etc/config/crRFD.aside")})
	ok(request{Op: "chmod", Path: at("usr/local/etc/config/rfd.conf"), Mode: 0o600})
	refused(request{Op: opRemoveCert, Path: at("etc/config/server.pem.acme")})
	refused(request{Op: opRemoveCert, Path: at("usr/local/etc/config/server.pem.managed")})
	refused(request{Op: opRemoveCert, Path: at("usr/local/etc/config/rfd.conf")})
	refused(request{Op: opRemoveCert, Path: at("usr/local/tmp/new")})
	ok(request{Op: opRemoveCert, Path: at("etc/config/server.pem")})
	ok(request{Op: opRemoveCert, Path: at("usr/local/etc/config/server.pem")}) // gone already: not an error
	for _, n := range []string{"server.pem", "server.pem.managed", "server.pem.acme"} {
		if _, err := os.Lstat(at("usr/local/etc/config/" + n)); err == nil {
			t.Errorf("%s is still there", n)
		}
	}
}

// Every file a named operation owns is in the list, in both spellings, so a new named operation's
// file on a write prefix is refused without anyone remembering this list.
func TestNamedPaths(t *testing.T) {
	p := DefaultPolicy("/", "/usr/local/etc/occulite")
	got := strings.Join(p.namedPaths(), " ")
	for _, want := range []string{
		"/etc/config/shadow", "/usr/local/etc/config/shadow",
		"/etc/config/server.pem", "/usr/local/etc/config/server.pem",
		"/etc/config/server.pem" + ManagedSuffix, "/usr/local/etc/config/server.pem" + MarkerSuffix,
		AuthorizedKeysPath, "/etc/passwd", "/etc/group",
	} {
		if !strings.Contains(" "+got+" ", " "+want+" ") {
			t.Errorf("%s is not in %s", want, got)
		}
	}
	for rel, want := range map[string]bool{
		"/":                         true,
		"/etc":                      true,
		"/etc/config":               true,
		"/usr/local/etc/config":     true,
		"/usr/local/etc/config/":    true,
		"/usr/local/etc/config/rfd": false,
		"/usr/local/tmp":            false,
		"/etc/config/shadow":        false, // the file itself is namedOnly, not a holder
	} {
		if p.holdsNamed(rel) != want {
			t.Errorf("holdsNamed(%s) = %v", rel, !want)
		}
	}
	// every Paths prefix that holds a named file is refused for the file itself
	for _, n := range p.namedPaths() {
		if p.pathAllowed(n) {
			t.Errorf("pathAllowed(%s)", n)
		}
	}
}
