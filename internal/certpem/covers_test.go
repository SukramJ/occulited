package certpem

import "testing"

// TestCovers: a name in any case, a trailing dot, one wildcard label - and never an address.
func TestCovers(t *testing.T) {
	names := []string{"ccu.home.arpa", "CCU", "*.example.org"}
	for _, c := range []struct {
		host string
		want bool
	}{
		{"ccu.home.arpa", true},
		{"CCU.Home.Arpa", true},
		{"ccu.home.arpa.", true},
		{"ccu", true},
		{"box.example.org", true},
		{"a.box.example.org", false},
		{"example.org", false},
		{"lite.home.arpa", false},
		{"home.arpa", false},
		{"", false},
		{"192.0.2.1", false},
		{"[::1]", false},
		{"fe80::1", false},
	} {
		if got := Covers(names, c.host); got != c.want {
			t.Errorf("Covers(%q) = %v, want %v", c.host, got, c.want)
		}
	}
	if Covers(nil, "ccu") || Covers([]string{"*."}, "x.") || Covers([]string{""}, "ccu") {
		t.Error("empty names cover nothing")
	}
}
