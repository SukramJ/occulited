package acme

import (
	"reflect"
	"testing"
)

// The renewal check hands the host name and domain on to whatever else follows the box's name.
func TestRenewalCheckHandsTheNameOn(t *testing.T) {
	s := newService(t, &fakeIssuer{}, &fakeInstaller{})
	s.settings = Settings{Mode: ModeSelfSigned, Directory: DirLetsEncrypt, Challenge: ChallengeHTTP01, Names: []string{}}
	var got []string
	s.Follow = func(host, domain string) { got = append(got, host+"|"+domain) }
	s.check()
	if len(got) != 0 {
		t.Fatalf("without HostDomain: %v", got)
	}
	s.HostDomain = func() (string, string) { return "ccu", "lan" }
	s.check()
	if !reflect.DeepEqual(got, []string{"ccu|lan"}) {
		t.Fatalf("got %v", got)
	}
}
