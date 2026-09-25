package system

import (
	"reflect"
	"testing"
)

// B-152: the policy line of `iptables -S INPUT`, and which families count as not loaded
func TestParseInputPolicy(t *testing.T) {
	cases := map[string]string{
		"-P INPUT ACCEPT\n": "ACCEPT",
		// .170's, measured 2026-09-18
		"-P INPUT DROP\n-A INPUT -i lo -j ACCEPT\n-A INPUT -m state --state RELATED,ESTABLISHED -j ACCEPT\n": "DROP",
		"-A INPUT -i lo -j ACCEPT\n": "",
		"":                           "",
		"-P FORWARD DROP\n":          "",
		"-P INPUT\n":                 "",
	}
	for in, want := range cases {
		if got := ParseInputPolicy([]byte(in)); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// task 157: loaded means INPUT jumps to lite-input
func TestFirewallNotLoaded(t *testing.T) {
	if !HasFirewallJump([]byte("-P INPUT DROP\n-A INPUT -i lo -j ACCEPT\n-A INPUT -j lite-input\n")) || HasFirewallJump([]byte("-P INPUT DROP\n-A INPUT -j local-only\n")) {
		t.Fatal("jump")
	}
	cases := []struct {
		loaded map[string]bool
		want   []string
	}{
		{map[string]bool{"ipv4": true, "ipv6": true}, nil},
		{map[string]bool{"ipv4": false, "ipv6": false}, []string{"ipv4", "ipv6"}},
		{map[string]bool{"ipv4": true, "ipv6": false}, []string{"ipv6"}},
		{map[string]bool{}, nil},
	}
	for _, c := range cases {
		if got := FirewallNotLoaded(c.loaded); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: %q, want %q", c.loaded, got, c.want)
		}
	}
}
