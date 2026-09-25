package priv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /run/systemd/system is where a runtime unit shadows a shipped one, so the helper admits exactly
// two shapes there (task 27.4, task 50): an override drop-in, <unit>.d/50-occulite.conf, and an own
// timer's two units, local-<name>.timer and local-<name>.service. It used to be a prefix on Paths,
// which let the daemon write any runtime unit - a shipped name, a mask, a wants link. Everything
// here goes through Server.do, the boundary itself, against a temporary root.
func TestRunUnitPolicy(t *testing.T) {
	root := t.TempDir()
	srv := &Server{Policy: DefaultPolicy(root, "/usr/local/etc/occulite")}
	ctx := context.Background()
	at := func(rel string) string { return filepath.Join(root, rel) }
	refused := func(req request) bool { return strings.HasPrefix(srv.do(ctx, req).Error, "refused") }

	// the drop-ins: the directory, the file, removing it
	for _, unit := range []string{"rfd.service", "occu-fstrim.timer", "getty@tty1.service", "local-backup.timer"} {
		dir := at("/run/systemd/system/" + unit + ".d")
		if r := srv.do(ctx, request{Op: "mkdir", Path: dir, Mode: 0o755}); r.Error != "" {
			t.Errorf("mkdir %s: %s", dir, r.Error)
		}
		file := filepath.Join(dir, "50-occulite.conf")
		if r := srv.do(ctx, request{Op: "write", Path: file, Data: []byte("[Service]\n"), Mode: 0o644}); r.Error != "" {
			t.Errorf("write %s: %s", file, r.Error)
		}
		if r := srv.do(ctx, request{Op: "remove", Path: file}); r.Error != "" {
			t.Errorf("remove %s: %s", file, r.Error)
		}
	}
	// an own timer's two units
	for _, name := range []string{"local-backup.timer", "local-backup.service", "local-Nightly_2.timer", "local-" + strings.Repeat("a", 32) + ".service"} {
		file := at("/run/systemd/system/" + name)
		if r := srv.do(ctx, request{Op: "write", Path: file, Data: []byte("[Timer]\n"), Mode: 0o644}); r.Error != "" {
			t.Errorf("write %s: %s", name, r.Error)
		}
		if b, err := os.ReadFile(file); err != nil || string(b) != "[Timer]\n" {
			t.Errorf("%s not written: %v %q", name, err, b)
		}
		if r := srv.do(ctx, request{Op: "remove", Path: file}); r.Error != "" {
			t.Errorf("remove %s: %s", name, r.Error)
		}
	}

	// everything else under the directory, and what only looks like it
	for _, rel := range []string{
		"/run/systemd/system/rfd.service",                                 // a runtime unit shadows the shipped one
		"/run/systemd/system/local-backup.socket",                         // an own unit is a timer or its service
		"/run/systemd/system/local-.timer",                                // no name
		"/run/systemd/system/local--x.timer",                              // a name starts with a letter or digit
		"/run/systemd/system/local-a b.timer",                             // the name rule
		"/run/systemd/system/local-" + strings.Repeat("a", 33) + ".timer", // 32 at most
		"/run/systemd/system/local-x.timer/y",                             // not a file of that name
		"/run/systemd/system/timers.target.wants/local-x.timer",           // wants links are systemctl's
		"/run/systemd/system/multi-user.target.wants/rfd.service",
		"/run/systemd/system/rfd.service.d/other.conf",             // one drop-in name
		"/run/systemd/system/multi-user.target.d/50-occulite.conf", // drop-ins for services and timers only
		"/run/systemd/system/rfd.service.d/../../../../etc/passwd", // cleaned: /etc/passwd
		"/run/systemd/system/local-x.timer.d/../../rfd.service",    // cleaned: /run/systemd/rfd.service
		"/run/systemd/systemx/local-x.timer",                       // a sibling with the same prefix
		"/run/systemd/system",
		"/run/systemd/system/",
	} {
		if !refused(request{Op: "write", Path: at(rel), Data: []byte("x"), Mode: 0o644}) {
			t.Errorf("write %s allowed", rel)
		}
		if !refused(request{Op: "remove", Path: at(rel)}) {
			t.Errorf("remove %s allowed", rel)
		}
	}

	// the other operations get nothing there, not even on an admitted name
	own := at("/run/systemd/system/local-backup.timer")
	_ = os.MkdirAll(at("/usr/local/tmp"), 0o755)
	_ = os.WriteFile(at("/usr/local/tmp/x"), []byte("[Timer]\n"), 0o644)
	for _, req := range []request{
		{Op: "touch", Path: own, Mode: 0o644},
		{Op: "symlink", Path: own, Target: "/dev/null"},
		{Op: "chmod", Path: own, Mode: 0o777},
		{Op: "chown", Path: own},
		{Op: "removeall", Path: at("/run/systemd/system/rfd.service.d")},
		{Op: "rename", Src: at("/usr/local/tmp/x"), Dst: own},
		{Op: "mkdir", Path: at("/run/systemd/system/timers.target.wants"), Mode: 0o755},
		{Op: "mkdir", Path: own, Mode: 0o755},
		{Op: "mkdir", Path: at("/run/systemd/system"), Mode: 0o755},
	} {
		if !refused(req) {
			t.Errorf("%s %s%s allowed", req.Op, req.Path, req.Dst)
		}
	}

	// the name rule both sides share
	for _, ok := range []string{"backup", "a", "Nightly_Job-2", "0day", strings.Repeat("x", 32)} {
		if !ValidLocalUnitName(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"", "-x", "_x", "a.b", "a/b", "a b", "ä", strings.Repeat("x", 33), "x\n", "local-x.timer"} {
		if ValidLocalUnitName(bad) {
			t.Errorf("%q accepted", bad)
		}
	}

	// a policy without the directory admits nothing in it
	p := DefaultPolicy(root, "/usr/local/etc/occulite")
	p.RunUnitDir = ""
	if p.runUnitFileAllowed(own) || p.runUnitDirAllowed(at("/run/systemd/system/rfd.service.d")) {
		t.Error("an empty RunUnitDir admitted a path")
	}
}
