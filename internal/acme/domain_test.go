package acme

import (
	"reflect"
	"testing"
)

// The domain rule: names that are the old domain's default follow a new domain while the host
// name stays; anything else stays, and an empty or first-seen domain changes nothing.
func TestFollowDomain(t *testing.T) {
	old := []string{"ccu.home.arpa", "ccu"}
	cases := []struct {
		name    string
		mode    string
		seen    string // the domain seen before; "" = never
		names   []string
		host    string
		domain  string
		adapted bool
		want    []string
		seenNow string
	}{
		{"the first domain ever seen is only remembered", ModeACME, "", old, "ccu", "lan", false, old, "lan"},
		{"the old domain's default: adapted", ModeACME, "home.arpa", old, "ccu", "lan", true, []string{"ccu.lan", "ccu"}, "lan"},
		{"in capitals, a trailing dot on the domain: adapted", ModeACME, "home.arpa", []string{"CCU.Home.Arpa", "CCU"}, "ccu", "LAN.", true, []string{"ccu.lan", "ccu"}, "lan"},
		{"not in ACME mode: adapted all the same", ModeSelfSigned, "home.arpa", old, "ccu", "lan", true, []string{"ccu.lan", "ccu"}, "lan"},
		{"set by hand: left alone", ModeACME, "home.arpa", []string{"homematic.example.org"}, "ccu", "lan", false, []string{"homematic.example.org"}, "lan"},
		{"another order: left alone", ModeACME, "home.arpa", []string{"ccu", "ccu.home.arpa"}, "ccu", "lan", false, []string{"ccu", "ccu.home.arpa"}, "lan"},
		{"an extra name: left alone", ModeACME, "home.arpa", []string{"ccu.home.arpa", "ccu", "ccu.example.org"}, "ccu", "lan", false, []string{"ccu.home.arpa", "ccu", "ccu.example.org"}, "lan"},
		{"another host name: left alone", ModeACME, "home.arpa", old, "lite", "lan", false, old, "lan"},
		{"empty names stay empty", ModeACME, "home.arpa", []string{}, "ccu", "lan", false, []string{}, "lan"},
		{"the domain went away: nothing, and it is not remembered", ModeACME, "home.arpa", old, "ccu", "", false, old, "home.arpa"},
		{"the same domain: nothing", ModeACME, "home.arpa", old, "ccu", "home.arpa", false, old, "home.arpa"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newService(t, &fakeIssuer{}, &fakeInstaller{})
			s.settings = Settings{Mode: c.mode, Directory: DirLetsEncrypt, Challenge: ChallengeHTTP01, Names: c.names}
			if c.seen != "" {
				if adapted, err := s.FollowDomain(c.host, c.seen); adapted || err != nil {
					t.Fatalf("first sight: %v %v", adapted, err)
				}
			}
			adapted, err := s.FollowDomain(c.host, c.domain)
			if err != nil || adapted != c.adapted {
				t.Fatalf("adapted %v (%v), want %v", adapted, err, c.adapted)
			}
			if got := s.Settings().Names; !reflect.DeepEqual(got, c.want) {
				t.Fatalf("names %v, want %v", got, c.want)
			}
			// the domain is remembered across a restart, and what was adapted is on disk
			again, err := New(s.st.dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if again.domain != c.seenNow {
				t.Fatalf("remembered %q, want %q", again.domain, c.seenNow)
			}
			if c.adapted && !reflect.DeepEqual(again.Settings().Names, c.want) {
				t.Fatalf("not saved: %v", again.Settings().Names)
			}
		})
	}
}

// The renewal check looks at the domain first, so a box nobody opens still follows a new lease.
func TestRenewalCheckFollowsTheDomain(t *testing.T) {
	s := newService(t, &fakeIssuer{}, &fakeInstaller{})
	s.settings = Settings{Mode: ModeSelfSigned, Directory: DirLetsEncrypt, Challenge: ChallengeHTTP01, Names: []string{"ccu.home.arpa", "ccu"}}
	domain := "home.arpa"
	s.HostDomain = func() (string, string) { return "ccu", domain }
	s.check()
	if s.domain != "home.arpa" {
		t.Fatalf("first check remembered %q", s.domain)
	}
	domain = "lan"
	s.check()
	if got := s.Settings().Names; !reflect.DeepEqual(got, []string{"ccu.lan", "ccu"}) {
		t.Fatalf("names %v", got)
	}
}
