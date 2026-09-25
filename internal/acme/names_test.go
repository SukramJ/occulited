package acme

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hobbyquaker/occulited/internal/certpem"
)

func TestDefaultNames(t *testing.T) {
	cases := []struct {
		host, domain string
		want         []string
	}{
		{"openccu", "home.arpa", []string{"openccu.home.arpa", "openccu"}},
		{"OpenCCU", " Home.Arpa. ", []string{"openccu.home.arpa", "openccu"}},
		{"openccu", "", []string{"openccu"}},
		{"", "home.arpa", nil},
	}
	for _, c := range cases {
		if got := DefaultNames(c.host, c.domain); !reflect.DeepEqual(got, c.want) {
			t.Errorf("DefaultNames(%q, %q) = %v, want %v", c.host, c.domain, got, c.want)
		}
	}
}

func TestFollowsDefault(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		want  bool
	}{
		{"the default", []string{"ccu.home.arpa", "ccu"}, true},
		{"the default in capitals", []string{"CCU.Home.Arpa", "Ccu"}, true},
		{"empty", nil, true},
		{"another order", []string{"ccu", "ccu.home.arpa"}, false},
		{"an extra name", []string{"ccu.home.arpa", "ccu", "ccu.example.org"}, false},
		{"another domain", []string{"ccu.lan", "ccu"}, false},
		{"typed by hand", []string{"homematic.example.org"}, false},
		{"the FQDN alone", []string{"ccu.home.arpa"}, false},
	}
	for _, c := range cases {
		if got := FollowsDefault(c.names, "ccu", "home.arpa"); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// The rename rule, with the settings as a box stores them: names equal to the old default follow
// the new host name, anything else stays.
func TestFollowHostname(t *testing.T) {
	adapted := []string{"lite.home.arpa", "lite"}
	cases := []struct {
		name  string
		mode  string
		names []string
		state string
		want  []string
	}{
		{"ACME, the old default: adapted", ModeACME, []string{"ccu.home.arpa", "ccu"}, NamesAdapted, adapted},
		{"ACME, the old default in capitals: adapted", ModeACME, []string{"CCU.home.arpa", "CCU"}, NamesAdapted, adapted},
		{"ACME, empty (an older state): adapted", ModeACME, []string{}, NamesAdapted, adapted},
		{"ACME, already the new default: fitting", ModeACME, []string{"lite.home.arpa", "lite"}, NamesFitting, []string{"lite.home.arpa", "lite"}},
		{"ACME, another order: set by hand", ModeACME, []string{"ccu", "ccu.home.arpa"}, NamesSetByHand, []string{"ccu", "ccu.home.arpa"}},
		{"ACME, an extra name: set by hand", ModeACME, []string{"ccu.home.arpa", "ccu", "ccu.example.org"}, NamesSetByHand, []string{"ccu.home.arpa", "ccu", "ccu.example.org"}},
		{"ACME, another domain: set by hand", ModeACME, []string{"ccu.lan", "ccu"}, NamesSetByHand, []string{"ccu.lan", "ccu"}},
		{"ACME, typed by hand: set by hand", ModeACME, []string{"homematic.example.org"}, NamesSetByHand, []string{"homematic.example.org"}},
		{"self-signed, the old default: adapted, not in use", ModeSelfSigned, []string{"ccu.home.arpa", "ccu"}, NamesNotInUse, adapted},
		{"self-signed, empty: stays empty", ModeSelfSigned, []string{}, NamesNotInUse, []string{}},
		{"manual, typed by hand: left alone", ModeManual, []string{"homematic.example.org"}, NamesNotInUse, []string{"homematic.example.org"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newService(t, &fakeIssuer{}, &fakeInstaller{})
			s.settings = Settings{Mode: c.mode, Directory: DirLetsEncrypt, Challenge: ChallengeHTTP01, Names: c.names}
			ch, err := s.FollowHostname("ccu", "lite", "home.arpa")
			if err != nil {
				t.Fatal(err)
			}
			if ch.State != c.state || !reflect.DeepEqual(ch.Names, c.want) || !reflect.DeepEqual(ch.Previous, c.names) {
				t.Fatalf("got %+v, want state %s names %v previous %v", ch, c.state, c.want, c.names)
			}
			if got := s.Settings().Names; !reflect.DeepEqual(got, c.want) {
				t.Fatalf("stored %v, want %v", got, c.want)
			}
			// what was adapted is on disk
			again, err := New(s.st.dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.state == NamesAdapted || (c.state == NamesNotInUse && !reflect.DeepEqual(c.want, c.names)), !reflect.DeepEqual(c.names, c.want)) {
				t.Fatal("the table is inconsistent")
			}
			if !reflect.DeepEqual(c.want, c.names) && !reflect.DeepEqual(again.Settings().Names, c.want) {
				t.Fatalf("not saved: %v", again.Settings().Names)
			}
		})
	}
}

// An ACME save with no names takes the default, when there is one; the status proposes it.
func TestSettingsTakeTheSuggestedNames(t *testing.T) {
	s := newService(t, &fakeIssuer{}, &fakeInstaller{})
	suggest := func() []string { return DefaultNames("ccu", "home.arpa") }
	if st := s.Status(); st.SuggestedNames == nil || len(st.SuggestedNames) != 0 {
		t.Fatalf("without a suggestion: %v", st.SuggestedNames)
	}
	s.Suggest = suggest
	if st := s.Status(); !reflect.DeepEqual(st.SuggestedNames, []string{"ccu.home.arpa", "ccu"}) {
		t.Fatalf("suggested %v", st.SuggestedNames)
	}
	u := acmeSettings()
	u.Names = []string{" ", ""}
	n, err := s.SetSettings(u)
	if err != nil || !reflect.DeepEqual(n.Names, []string{"ccu.home.arpa", "ccu"}) {
		t.Fatalf("empty ACME names: %v %v", n.Names, err)
	}
	// names typed by hand are what is saved
	u.Names = []string{"homematic.example.org"}
	if n, err := s.SetSettings(u); err != nil || !reflect.DeepEqual(n.Names, []string{"homematic.example.org"}) {
		t.Fatalf("typed: %v %v", n.Names, err)
	}
	// another mode keeps an empty list empty
	if n, err := s.SetSettings(Update{Mode: ModeSelfSigned, Directory: DirLetsEncrypt, Challenge: ChallengeHTTP01}); err != nil || len(n.Names) != 0 {
		t.Fatalf("self-signed: %v %v", n.Names, err)
	}
	// without a default the refusal stands
	s.Suggest = func() []string { return nil }
	u.Names = nil
	if _, err := s.SetSettings(u); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no default: %v", err)
	}
}

func TestNamesChanged(t *testing.T) {
	old := []string{"ccu.home.arpa", "ccu"}
	renamed := []string{"lite.home.arpa", "lite"}
	issued := &certpem.Info{Names: old}
	cases := []struct {
		name   string
		mode   string
		names  []string
		issued *certpem.Info
		last   *Attempt
		want   bool
	}{
		{"the issued names", ModeACME, old, issued, nil, false},
		{"the issued names in another order and case", ModeACME, []string{"CCU", "ccu.home.arpa"}, issued, nil, false},
		{"renamed", ModeACME, renamed, issued, nil, true},
		{"a name more", ModeACME, append(append([]string{}, old...), "ccu.example.org"), issued, nil, true},
		{"not ACME", ModeSelfSigned, renamed, issued, nil, false},
		{"nothing issued", ModeACME, renamed, nil, nil, false},
		{"the last order for these names succeeded (the CA left one out)", ModeACME, renamed, &certpem.Info{Names: []string{"lite.home.arpa"}}, &Attempt{Kind: KindRenew, OK: true, Names: renamed}, false},
		{"the last order for these names failed", ModeACME, renamed, issued, &Attempt{Kind: KindRenew, OK: false, Names: renamed}, true},
		{"a test is not an order", ModeACME, renamed, issued, &Attempt{Kind: KindTest, OK: true, Names: renamed}, true},
	}
	for _, c := range cases {
		if got := NamesChanged(c.mode, c.names, c.issued, c.last); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// After a rename adapted the names, the timer's next check orders for them - once.
func TestCheckOrdersTheChangedNames(t *testing.T) {
	iss := &fakeIssuer{}
	s := newService(t, iss, &fakeInstaller{})
	if _, err := s.SetSettings(acmeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(KindIssue); err != nil {
		t.Fatal(err)
	}
	if a := wait(t, s); !a.OK {
		t.Fatalf("issue: %+v", a)
	}
	s.check()
	if st := s.Status(); st.Running != nil || len(iss.reqs) != 1 {
		t.Fatalf("the same names, far from expiry: %d orders", len(iss.reqs))
	}
	s.settings.Names = []string{"lite.example.org", "alias.example.org"}
	s.check()
	a := wait(t, s)
	if !a.OK || a.Kind != KindRenew || len(iss.reqs) != 2 || !reflect.DeepEqual(iss.reqs[1].Names, []string{"lite.example.org", "alias.example.org"}) {
		t.Fatalf("renewal for the new names: %+v, %d orders", a, len(iss.reqs))
	}
	s.check()
	if st := s.Status(); st.Running != nil || len(iss.reqs) != 2 {
		t.Fatalf("ordered again: %d orders", len(iss.reqs))
	}
}
