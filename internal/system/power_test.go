package system

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestHaltCommand(t *testing.T) {
	cases := []struct {
		name    string
		systemd bool
		files   []string
		want    string // "systemctl", or the path under the root
		args    []string
		err     bool
	}{
		{"systemd", true, []string{"/sbin/poweroff"}, "systemctl", []string{"poweroff"}, false},
		{"busybox in /sbin", false, []string{"/sbin/poweroff", "/bin/poweroff"}, "/sbin/poweroff", nil, false},
		{"busybox in /bin", false, []string{"/bin/poweroff"}, "/bin/poweroff", nil, false},
		{"no poweroff", false, nil, "", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Root(t.TempDir())
			for _, f := range c.files {
				_ = os.MkdirAll(r.join(f+"/.."), 0o755)
				if err := os.WriteFile(r.join(f), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			name, args, err := Power{Root: r, Systemd: c.systemd}.HaltCommand()
			if c.err {
				if err == nil {
					t.Fatalf("want an error, got %q", name)
				}
				return
			}
			want := c.want
			if want != "systemctl" {
				want = r.join(c.want)
			}
			if err != nil || name != want || !reflect.DeepEqual(args, c.args) {
				t.Fatalf("got %q %v %v, want %q %v", name, args, err, want, c.args)
			}
		})
	}
}

// The halt runs the command only once the route has answered - the recorder stands in for the
// helper, which on a box would power the machine off.
func TestHaltRunsAfterTheAnswer(t *testing.T) {
	for _, systemd := range []bool{true, false} {
		r := Root(t.TempDir())
		_ = os.MkdirAll(r.join("/sbin"), 0o755)
		_ = os.WriteFile(r.join("/sbin/poweroff"), []byte("#!/bin/sh\n"), 0o755)
		var ran []string
		var pending func()
		p := Power{Root: r, Systemd: systemd,
			Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
				ran = append([]string{name}, args...)
				return nil, nil
			},
			Later: func(f func()) { pending = f },
		}
		if err := p.Halt(); err != nil {
			t.Fatal(err)
		}
		if ran != nil || pending == nil {
			t.Fatalf("systemd=%v: ran %v before the answer", systemd, ran)
		}
		pending()
		want := []string{r.join("/sbin/poweroff")}
		if systemd {
			want = []string{"systemctl", "poweroff"}
		}
		if !reflect.DeepEqual(ran, want) {
			t.Errorf("systemd=%v: ran %v, want %v", systemd, ran, want)
		}
	}
	if err := (Power{Root: Root(t.TempDir())}).Halt(); err == nil {
		t.Error("a box without poweroff must refuse before it answers")
	}
}

func TestArmRecovery(t *testing.T) {
	cases := []struct {
		name  string
		setup func(r Root)
		err   error
	}{
		{"nothing staged", func(Root) {}, nil},
		{"an update is staged", func(r Root) {
			_ = os.MkdirAll(r.join("/usr/local/tmp"), 0o755)
			_ = os.WriteFile(r.join("/usr/local/tmp/openccu-lite-x86_64-ova-1.0.0.zip"), []byte("zip"), 0o644)
			_ = os.Symlink("/usr/local/tmp/openccu-lite-x86_64-ova-1.0.0.zip", r.join("/usr/local/.firmwareUpdate"))
		}, ErrUpdateStaged},
		{"a dangling staged link still counts", func(r Root) {
			_ = os.MkdirAll(r.join("/usr/local"), 0o755)
			_ = os.Symlink("/usr/local/tmp/gone.zip", r.join("/usr/local/.firmwareUpdate"))
		}, ErrUpdateStaged},
		{"a container", func(r Root) {
			_ = os.MkdirAll(r.join("/run/systemd"), 0o755)
			_ = os.WriteFile(r.join("/run/systemd/container"), []byte("lxc\n"), 0o644)
		}, ErrNoRecovery},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Root(t.TempDir())
			c.setup(r)
			err := r.ArmRecovery()
			_, statErr := os.Stat(r.join("/usr/local/.recoveryMode"))
			if c.err != nil {
				if !errors.Is(err, c.err) || statErr == nil {
					t.Fatalf("want %v and no marker, got %v (marker: %v)", c.err, err, statErr == nil)
				}
				return
			}
			if err != nil || statErr != nil {
				t.Fatalf("armed: %v, marker: %v", err, statErr)
			}
			if err := r.DisarmRecovery(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(r.join("/usr/local/.recoveryMode")); !os.IsNotExist(err) {
				t.Error("the marker survived the disarm")
			}
		})
	}
}
