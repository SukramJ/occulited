package radio

import "testing"

// Task 201: the decline hmipserver writes when a device's key in the map is not that device's.
func TestInclusionDeclinedSGTIN(t *testing.T) {
	const sgtin = "3014F711A0001F5F298D97AF"
	cases := []struct {
		name, line, want string
	}{
		{"the decline", "AP 3014F711A000041709ADFA5B: The inclusion for device " + sgtin + " is declined, the local key is wrong", sgtin},
		{"with a journal prefix", "Sep 22 20:01:02 lab hmipserver[812]: AP 30: The inclusion for device " + sgtin + " is declined, the local key is wrong", sgtin},
		{"lower case in the log", "AP 30: The inclusion for device " + "3014f711a0001f5f298d97af" + " is declined, the local key is wrong", sgtin},
		// the neighbouring lines about the same map are not a failed pairing
		{"the key was taken from the map", "AP 30: Add local key of device " + sgtin + " for inclusion from map to whitelist", ""},
		{"a map entry of the wrong length", "AP 30: Invalid local key (size) of device " + sgtin + " in local key map", ""},
		{"an inclusion already running", "AP 30: The inclusion for device " + sgtin + " is already in progress 3, skip this inclusion request", ""},
		{"a declined transaction", "AP 30: Transaction 7: Transaction declined because of active live update", ""},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := InclusionDeclinedSGTIN(c.line); got != c.want {
				t.Errorf("InclusionDeclinedSGTIN(%q) = %q, want %q", c.line, got, c.want)
			}
		})
	}
}

// The substring journalctl pre-filters on must be in a line the matcher then accepts, or the
// two would disagree and the warning would never be raised.
func TestInclusionDeclinedMatchIsInTheLine(t *testing.T) {
	line := "AP 30: The inclusion for device 3014F711A0001F5F298D97AF is declined, the local key is wrong"
	if got := InclusionDeclinedSGTIN(line); got == "" {
		t.Fatal("the matcher does not accept the line the constant filters for")
	}
	if InclusionDeclinedSGTIN("AP 30: something else "+InclusionDeclinedMatch) != "" {
		t.Error("a line that only carries the substring must not count as a decline")
	}
}
