package firewall

import "testing"

// openccu-lite task 217: what the confirmed rules do with the access point ports - the first
// enabled rule for the port, passing over rules narrowed by a source port or a destination, or the
// policy.
func TestVerdict(t *testing.T) {
	c := Config{Policy: Policy{V4: "DROP", V6: "DROP"}, Rules: []Rule{
		{ID: "d1", Proto: "udp", SPort: 43439, Source: AnySource, Family: FamilyBoth, Target: "ACCEPT"},
		{ID: "m1", Proto: "udp", Dest: "224.0.0.0/24", Source: AnySource, Family: FamilyV4, Target: "ACCEPT"},
		{ID: "v6", Port: 9293, Proto: "tcp", Source: AnySource, Family: FamilyV6, Target: "ACCEPT"},
		{ID: "off", Port: 9293, Proto: "tcp", Source: AnySource, Family: FamilyBoth, Target: "ACCEPT", Disabled: true},
		{ID: "r1", Port: 9293, Proto: "tcp", Source: LocalNetworks, Family: FamilyBoth, Target: "REJECT", Owner: OwnerHmIPAP},
		{ID: "r2", Port: 9293, Proto: "tcp", Source: AnySource, Family: FamilyBoth, Target: "ACCEPT"},
		{ID: "rg", Port: 9000, PortTo: 9300, Proto: "tcp", Source: AnySource, Family: FamilyV4, Target: "ACCEPT"},
		{ID: "all", Proto: "udp", Source: "192.0.2.0/24", Family: FamilyV4, Target: "ACCEPT"},
	}}
	for _, tc := range []struct {
		proto string
		port  int
		want  PortVerdict
	}{
		{"tcp", 9293, PortVerdict{Port: 9293, Proto: "tcp", Target: "REJECT", Rule: "r1", Source: LocalNetworks, Owner: OwnerHmIPAP}},
		{"tcp", 9294, PortVerdict{Port: 9294, Proto: "tcp", Target: "ACCEPT", Rule: "rg", Source: AnySource}},
		{"udp", 43438, PortVerdict{Port: 43438, Proto: "udp", Target: "ACCEPT", Rule: "all", Source: "192.0.2.0/24"}},
		{"tcp", 9400, PortVerdict{Port: 9400, Proto: "tcp", Target: "DROP"}},
	} {
		if got := Verdict(c, tc.proto, tc.port); got != tc.want {
			t.Errorf("%s/%d: %+v, want %+v", tc.proto, tc.port, got, tc.want)
		}
	}
	v := HmIPAPVerdicts(c)
	if len(v) != 3 || v[0].Port != 9293 || v[1].Port != 9294 || v[2].Port != 43438 {
		t.Errorf("the access point ports without the discovery: %+v", v)
	}
}
