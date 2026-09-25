package acme

import (
	"strings"

	"github.com/hobbyquaker/occulited/internal/certpem"
)

// DefaultNames is what a box's certificate is named when nobody chose: the FQDN first - the first
// name is the subject, and a public CA does not issue for a bare host name - then the bare host
// name. Without a domain the host name alone; without a host name none. Lower case, as the
// settings store names.
func DefaultNames(hostname, domain string) []string {
	host := strings.ToLower(strings.TrimSpace(hostname))
	dom := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	if host == "" {
		return nil
	}
	if dom == "" {
		return []string{host}
	}
	return []string{host + "." + dom, host}
}

// FollowsDefault reports whether names are the default for that host name and domain: the same
// list in the same order, compared case-insensitively. An empty list counts as following. The
// comparison is the whole rule - there is no "set by hand" flag to keep in sync, so names edited
// back to exactly the convention follow it again.
func FollowsDefault(names []string, hostname, domain string) bool {
	if len(names) == 0 {
		return true
	}
	return sameNames(names, DefaultNames(hostname, domain))
}

func sameNames(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(strings.TrimSpace(a[i]), strings.TrimSpace(b[i])) {
			return false
		}
	}
	return true
}

// sameNameSet: the same names in any order, case-insensitively - what a certificate covers.
func sameNameSet(a, b []string) bool {
	set := map[string]bool{}
	for _, n := range a {
		set[strings.ToLower(strings.TrimSpace(n))] = true
	}
	other := map[string]bool{}
	for _, n := range b {
		n = strings.ToLower(strings.TrimSpace(n))
		if !set[n] {
			return false
		}
		other[n] = true
	}
	return len(set) == len(other)
}

// What a rename did to the ACME names (NamesChange.State).
const (
	// NamesAdapted: the names followed the old host name's default and now are the new one's.
	NamesAdapted = "adapted"
	// NamesFitting: the names already are the new host name's default; nothing changed.
	NamesFitting = "fitting"
	// NamesSetByHand: the names differ from the default in some way and were left alone.
	NamesSetByHand = "set-by-hand"
	// NamesNotInUse: the box is not in ACME mode. Names that followed the default are adapted all
	// the same, so they still follow when ACME is switched on; Names says what they are now.
	NamesNotInUse = "not-in-use"
	// NamesFailed: they should have been adapted, and the settings could not be written.
	NamesFailed = "failed"
)

// NamesChange is the answer of FollowHostname, and what the network routes hand on after a
// rename so the page can say whether the certificate's names were adapted.
type NamesChange struct {
	State    string   `json:"state"`
	Names    []string `json:"names"`
	Previous []string `json:"previous"`
	Error    string   `json:"error,omitempty"`
}

// FollowHostname is the rename rule: the stored names are compared with the default of the old
// host name and the current domain, and only when they are that default - or empty - they become
// the default of the new host name. Anything else was chosen by someone and stays. Nothing is
// ordered here: the renewal timer orders for the new names on its next run (NamesChanged), or
// the user does with Issue now.
func (s *Service) FollowHostname(oldHost, newHost, domain string) (NamesChange, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := append([]string{}, s.settings.Names...)
	ch := NamesChange{Names: append([]string{}, prev...), Previous: prev}
	target := DefaultNames(newHost, domain)
	inUse := s.settings.Mode == ModeACME
	switch {
	case len(target) == 0:
		ch.State = NamesSetByHand
	case len(prev) > 0 && sameNames(prev, target):
		ch.State = NamesFitting
	case len(prev) == 0 && !inUse:
		// nothing stored: the page's suggestion follows the host name by itself
		ch.State = NamesFitting
	case FollowsDefault(prev, oldHost, domain):
		n := s.settings
		n.Names = target
		if err := s.st.writeJSON(fileSettings, n); err != nil {
			ch.State, ch.Error = NamesFailed, err.Error()
			return ch, err
		}
		s.settings = n
		ch.Names = append([]string{}, target...)
		ch.State = NamesAdapted
		s.log().Info("certificate: the ACME names follow the new host name", "from", strings.Join(prev, ","), "to", strings.Join(target, ","))
	default:
		ch.State = NamesSetByHand
	}
	if !inUse {
		ch.State = NamesNotInUse
	}
	return ch, nil
}

// NamesChanged is the renewal timer's second reason, beside ShouldRenew: under ACME the issued
// certificate names other DNS names than the settings - a rename adapted them - so it is ordered
// again for the new names. Not when the last order was already one for exactly these names and
// succeeded: a CA that leaves a name out would otherwise be asked again at every check.
func NamesChanged(mode string, names []string, issued *certpem.Info, last *Attempt) bool {
	if mode != ModeACME || issued == nil || len(names) == 0 {
		return false
	}
	if sameNameSet(names, issued.Names) {
		return false
	}
	if last != nil && last.OK && last.Kind != KindTest && sameNameSet(last.Names, names) {
		return false
	}
	return true
}

// blankNames: no name in the list is more than whitespace.
func blankNames(names []string) bool {
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			return false
		}
	}
	return true
}
