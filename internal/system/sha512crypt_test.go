package system

import (
	"strings"
	"testing"
)

// task 143: the pair's hash, known answers from the specification's test vector and from
// openssl passwd -6
func TestSHA512Crypt(t *testing.T) {
	for _, c := range []struct{ pw, salt, want string }{
		{"Hello world!", "saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"lab-rpc-pass-1234", "t143labsalt", "$6$t143labsalt$ar7iAgsbvfuPLMfjzucNosaKu9oQ4T.ArYoTYgrTGSkET6s7OGb9M2/3191F4J4McMlRYIjm55Glyd.yx0egV0"},
		{"a longer password with spaces and ümlauts 0123456789", "abcdefghijklmnop", "$6$abcdefghijklmnop$jGRB9th6NWu69GDNvo55jNuj3/gae7fa8IqrFKUWVaoXXnopoG/EcDM5XZsSuiWNY.ABzwwtZV97lsufvKIgt0"},
	} {
		if got := sha512Crypt(c.pw, c.salt); got != c.want {
			t.Errorf("%q/%q:\n got %s\nwant %s", c.pw, c.salt, got, c.want)
		}
	}
	s := newCryptSalt()
	if len(s) != 16 || strings.Trim(s, cryptAlphabet) != "" {
		t.Fatalf("salt %q", s)
	}
	// a password over 64 bytes takes the other branches of the sequences
	if h := sha512Crypt(strings.Repeat("x", 100), "s"); h != "$6$s$Ry4ZPOXCn8T2Vsfwu/WXZRluJIbjyELSq6ml2qfjBoLUw0RjK7drmwYPczkn3j4NYpDktoCI4V4Al5zm713vM." {
		t.Fatalf("long: %s", h)
	}
}
