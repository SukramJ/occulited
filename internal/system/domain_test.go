package system

import "testing"

// The domain comes from /etc/resolv.conf: its domain line, else the first search entry, else none.
func TestDomainFromResolvConf(t *testing.T) {
	cases := []struct {
		name   string
		resolv string
		want   string
	}{
		{"domain only", "domain home.arpa\nnameserver 192.0.2.1\n", "home.arpa"},
		{"search only, the first entry", "search home.arpa lan.example\nnameserver 192.0.2.1\n", "home.arpa"},
		{"both: the domain line wins", "search lan.example\ndomain home.arpa\n", "home.arpa"},
		{"none", "nameserver 192.0.2.1\n", ""},
		{"no file", "", ""},
		{"a trailing dot and capitals", "domain Home.Arpa.\n", "home.arpa"},
		{"comments are not lines", "# domain nope.example\n; search nope.example\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"etc/config/netconfig": "HOSTNAME=openccu\nMODE=DHCP\n"}
			if c.resolv != "" {
				files["etc/resolv.conf"] = c.resolv
			}
			r := rootWith(t, files)
			if got := r.Domain(); got != c.want {
				t.Errorf("Domain() = %q, want %q", got, c.want)
			}
			if got := r.ReadNetwork().Domain; got != c.want {
				t.Errorf("ReadNetwork().Domain = %q, want %q", got, c.want)
			}
		})
	}
}

func TestHostname(t *testing.T) {
	r := rootWith(t, map[string]string{"etc/config/netconfig": "HOSTNAME=lite\n", "proc/sys/kernel/hostname": "kernel\n"})
	if got := r.Hostname(); got != "lite" {
		t.Errorf("netconfig first: %q", got)
	}
	r = rootWith(t, map[string]string{"etc/config/netconfig": "MODE=DHCP\n", "proc/sys/kernel/hostname": "kernel\n"})
	if got := r.Hostname(); got != "kernel" || r.ReadNetwork().Hostname != "kernel" {
		t.Errorf("the kernel's without a HOSTNAME: %q", got)
	}
}
