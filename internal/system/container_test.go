package system

import (
	"os"
	"path/filepath"
	"testing"
)

func TestContainer(t *testing.T) {
	w := func(dir, p, c string) {
		t.Helper()
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"a machine", map[string]string{"VERSION": "VERSION=1\nPRODUCT=rpi4\nPLATFORM=rpi4\nVARIANT=lite\n"}, ""},
		{"a VM", map[string]string{"VERSION": "VERSION=1\nPRODUCT=ova\nPLATFORM=ova\nVARIANT=lite\n", "proc/1/environ": "PATH=/usr/bin\x00TERM=linux\x00"}, ""},
		{"PID 1 says lxc", map[string]string{"VERSION": "VERSION=1\nPRODUCT=ova\nPLATFORM=ova\n", "run/systemd/container": "lxc\n"}, "lxc"},
		{"PID 1's environment says lxc", map[string]string{"VERSION": "VERSION=1\nPRODUCT=ova\nPLATFORM=ova\n", "proc/1/environ": "container=lxc\x00PATH=/usr/bin\x00"}, "lxc"},
		{"PID 1's environment says docker", map[string]string{"proc/1/environ": "container=docker\x00"}, "docker"},
		{"the product says lxc (D-31)", map[string]string{"VERSION": "VERSION=3.89.8.20260719\nPRODUCT=lxc_amd64\nPLATFORM=lxc\nVARIANT=lite\nLITE=1.0.0-alpha.0\n"}, "lxc"},
		{"the product says oci", map[string]string{"VERSION": "VERSION=1\nPRODUCT=oci_amd64\nPLATFORM=oci\n"}, "oci"},
		{"an empty marker does not count", map[string]string{"run/systemd/container": "\n", "VERSION": "VERSION=1\nPRODUCT=ova\nPLATFORM=ova\n"}, ""},
		{"nothing at all", map[string]string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for p, content := range c.files {
				w(dir, p, content)
			}
			r := Root(dir)
			if got := r.Container(); got != c.want {
				t.Fatalf("Container() = %q, want %q", got, c.want)
			}
			if got := r.HostManaged(); got != (c.want != "") {
				t.Fatalf("HostManaged() = %v", got)
			}
		})
	}
}

func TestStatusReportsContainer(t *testing.T) {
	r := fakeRoot(t)
	if s := r.ReadStatus(); s.Container != "" {
		t.Fatalf("a VM reports container %q", s.Container)
	}
	_ = os.MkdirAll(filepath.Join(string(r), "run/systemd"), 0o755)
	_ = os.WriteFile(filepath.Join(string(r), "run/systemd/container"), []byte("lxc\n"), 0o644)
	if s := r.ReadStatus(); s.Container != "lxc" {
		t.Fatalf("container = %q", s.Container)
	}
}
