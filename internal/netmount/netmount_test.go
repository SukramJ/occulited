package netmount

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	ok := Spec{ID: "nas1", Kind: KindNFS, Server: "nas.lan", Path: "/volume1/ccu-backup"}
	for _, tc := range []struct {
		name string
		mod  func(*Spec)
		want string // "" = valid
	}{
		{"nfs", func(*Spec) {}, ""},
		{"nfs v3", func(s *Spec) { s.Version = "3" }, ""},
		{"nfs v4.2 on an IPv4", func(s *Spec) { s.Version, s.Server = "4.2", "192.0.2.10" }, ""},
		{"nfs on an IPv6", func(s *Spec) { s.Server = "2001:db8::1" }, ""},
		{"cifs", func(s *Spec) { s.Kind, s.Path = KindCIFS, "backup" }, ""},
		{"cifs with a directory", func(s *Spec) { s.Kind, s.Path, s.Version, s.Seal = KindCIFS, "backup/ccu", "3.1.1", true }, ""},
		{"id with a dash", func(s *Spec) { s.ID = "my-nas" }, "id"},
		{"id upper case", func(s *Spec) { s.ID = "Nas" }, "id"},
		{"id empty", func(s *Spec) { s.ID = "" }, "id"},
		{"id path", func(s *Spec) { s.ID = "../etc" }, "id"},
		{"id too long", func(s *Spec) { s.ID = "a1234567890123456" }, "id"},
		{"server with a space", func(s *Spec) { s.Server = "nas lan" }, "server"},
		{"server with an option", func(s *Spec) { s.Server = "nas,uid=0" }, "server"},
		{"server with a zone", func(s *Spec) { s.Server = "fe80::1%eth0" }, "server"},
		{"server empty", func(s *Spec) { s.Server = "" }, "server"},
		{"export relative", func(s *Spec) { s.Path = "volume1" }, "path"},
		{"export with a comma", func(s *Spec) { s.Path = "/v,uid=0" }, "path"},
		{"export with a space", func(s *Spec) { s.Path = "/v 1" }, "path"},
		{"export with a newline", func(s *Spec) { s.Path = "/v\nWhere=/etc" }, "path"},
		{"export with a specifier", func(s *Spec) { s.Path = "/v%h" }, "path"},
		{"export with ..", func(s *Spec) { s.Path = "/v/../etc" }, "path"},
		{"export with a quote", func(s *Spec) { s.Path = `/v"` }, "path"},
		{"nfs version 2", func(s *Spec) { s.Version = "2" }, "version"},
		{"nfs seal", func(s *Spec) { s.Seal = true }, "seal"},
		{"cifs absolute", func(s *Spec) { s.Kind, s.Path = KindCIFS, "/backup" }, "path"},
		{"cifs smb1", func(s *Spec) { s.Kind, s.Path, s.Version = KindCIFS, "backup", "1.0" }, "version"},
		{"cifs backslash", func(s *Spec) { s.Kind, s.Path = KindCIFS, `back\up` }, "path"},
		{"kind", func(s *Spec) { s.Kind = "sshfs" }, "kind"},
	} {
		s := ok
		tc.mod(&s)
		err := s.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.HasPrefix(err.Error(), tc.want)):
			t.Errorf("%s: %v, want a %s error", tc.name, err, tc.want)
		}
		if tc.want != "" {
			if _, _, err := s.Render("/c", 1); err == nil {
				t.Errorf("%s: rendered", tc.name)
			}
		}
	}
}

func TestRender(t *testing.T) {
	for _, tc := range []struct {
		name          string
		s             Spec
		what, typ, op string
	}{
		{"nfs auto", Spec{ID: "nas1", Kind: KindNFS, Server: "nas.lan", Path: "/volume1/b"}, "nas.lan:/volume1/b", "nfs",
			"proto=tcp,soft,timeo=100,retrans=2,nosuid,nodev,noexec"},
		{"nfs 4.2 by address", Spec{ID: "nas1", Kind: KindNFS, Server: "192.0.2.10", Path: "/b", Version: "4.2"}, "192.0.2.10:/b", "nfs",
			"vers=4.2,proto=tcp,soft,timeo=100,retrans=2,addr=192.0.2.10,nosuid,nodev,noexec"},
		{"nfs 3", Spec{ID: "nas1", Kind: KindNFS, Server: "nas", Path: "/b", Version: "3"}, "nas:/b", "nfs",
			"vers=3,proto=tcp,soft,timeo=100,retrans=2,nolock,nosuid,nodev,noexec"},
		{"nfs IPv6", Spec{ID: "nas1", Kind: KindNFS, Server: "2001:db8::1", Path: "/b"}, "[2001:db8::1]:/b", "nfs",
			"proto=tcp,soft,timeo=100,retrans=2,addr=2001:db8::1,nosuid,nodev,noexec"},
		{"cifs", Spec{ID: "smb", Kind: KindCIFS, Server: "nas", Path: "backup/ccu", Seal: true}, "//nas/backup/ccu", "cifs",
			"vers=3,credentials=/usr/local/etc/occulite/backup-targets/smb/cifs.cred,uid=0,gid=998,file_mode=0640,dir_mode=0750,soft,echo_interval=15,seal,nosuid,nodev,noexec"},
		// task 228: a share mounted read-only
		{"nfs read-only", Spec{ID: "nas1", Kind: KindNFS, Server: "nas", Path: "/b", Version: "4.1", ReadOnly: true}, "nas:/b", "nfs",
			"ro,vers=4.1,proto=tcp,soft,timeo=100,retrans=2,nosuid,nodev,noexec"},
		{"cifs read-only", Spec{ID: "smb", Kind: KindCIFS, Server: "nas", Path: "media", ReadOnly: true, Share: true}, "//nas/media", "cifs",
			"ro,vers=3,credentials=/usr/local/etc/occulite/backup-targets/smb/cifs.cred,uid=0,gid=998,file_mode=0640,dir_mode=0750,soft,echo_interval=15,nosuid,nodev,noexec"},
	} {
		m, a, err := tc.s.Render("/usr/local/etc/occulite/backup-targets/"+tc.s.ID+"/cifs.cred", 998)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		for _, want := range []string{"What=" + tc.what + "\n", "Where=/media/net/" + tc.s.ID + "\n", "Type=" + tc.typ + "\n", "Options=" + tc.op + "\n", "TimeoutSec=30s\n"} {
			if !strings.Contains(string(m), want) {
				t.Errorf("%s: mount unit lacks %q:\n%s", tc.name, want, m)
			}
		}
		for _, want := range []string{"[Automount]\n", "Where=/media/net/" + tc.s.ID + "\n", "TimeoutIdleSec=5min\n"} {
			if !strings.Contains(string(a), want) {
				t.Errorf("%s: automount unit lacks %q:\n%s", tc.name, want, a)
			}
		}
		if n := strings.Count(string(m), "\nWhere="); n != 1 {
			t.Errorf("%s: %d Where= lines", tc.name, n)
		}
	}
	if UnitName("nas1") != "media-net-nas1" {
		t.Error(UnitName("nas1"))
	}
}
