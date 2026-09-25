package acme

import "strings"

// domainState is domain.json: the DNS domain FollowDomain saw last.
type domainState struct {
	Domain string `json:"domain"`
}

// FollowDomain is the rename rule for a domain change: when the DHCP lease brings another domain
// while the host name stays, stored names that are the default of the old domain
// (<host>.<old domain>, <host>, in that order, case-insensitive) become the default of the new
// one. Names set by hand stay, and so does an empty list - the suggestion follows the domain by
// itself. As with a rename, nothing is ordered here and the mode does not matter: the renewal
// timer orders for the new names under ACME (NamesChanged).
//
// The old domain is the one this method saw last, kept in domain.json so that a change while
// occulited was down is seen at the next start. The first domain ever seen is only remembered. An
// empty domain is neither remembered nor acted on: at boot resolv.conf may not carry the lease
// yet, and the names must not flip to the bare host name and back. It reports whether the names
// were rewritten; an error is a file that could not be written.
func (s *Service) FollowDomain(hostname, domain string) (bool, error) {
	dom := strings.ToLower(strings.Trim(strings.TrimSpace(domain), "."))
	if dom == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.domain
	if old == dom {
		return false, nil
	}
	adapted := false
	if prev := s.settings.Names; old != "" && len(prev) > 0 && sameNames(prev, DefaultNames(hostname, old)) {
		n := s.settings
		n.Names = DefaultNames(hostname, dom)
		if err := s.st.writeJSON(fileSettings, n); err != nil {
			// the domain is not remembered either: the next look tries again
			return false, err
		}
		s.settings = n
		adapted = true
		s.log().Info("certificate: the ACME names follow the new domain", "domain", dom, "previous_domain", old, "from", strings.Join(prev, ","), "to", strings.Join(n.Names, ","))
	}
	s.domain = dom
	if err := s.st.writeJSON(fileDomain, domainState{Domain: dom}); err != nil {
		return adapted, err
	}
	return adapted, nil
}

// followDomainNow is FollowDomain with what HostDomain says, for the renewal check.
func (s *Service) followDomainNow() {
	if s.HostDomain == nil {
		return
	}
	host, dom := s.HostDomain()
	if _, err := s.FollowDomain(host, dom); err != nil {
		s.log().Warn("certificate: the ACME names could not follow the domain", "err", err)
	}
	if s.Follow != nil {
		s.Follow(host, dom)
	}
}
