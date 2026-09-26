package main

import (
	"sync"
	"time"

	"github.com/hobbyquaker/occulited/internal/certpem"
	"github.com/hobbyquaker/occulited/internal/ssdp"
	"github.com/hobbyquaker/occulited/internal/system"
)

// ssdpPresentation is the UPnP description's presentationURL (task 165): the system's name its
// certificate covers - <host>.<domain>, then the host name - so that the double click in Windows'
// network view ends on the web UI without a certificate warning; the address otherwise. Always
// http:// (B-245), the CCU's shape; lighttpd's redirect takes the browser to HTTPS. certNames reads the live certificate's names; they are kept for a minute, as a
// scanner may fetch the description often and the certificate is read through the helper.
func ssdpPresentation(root system.Root, certNames func() []string) func(string) string {
	var (
		mu     sync.Mutex
		at     time.Time
		cached []string
	)
	return func(rootURL string) string {
		mu.Lock()
		if at.IsZero() || time.Since(at) > time.Minute {
			cached, at = certNames(), time.Now()
		}
		names := cached
		mu.Unlock()
		host := root.Hostname()
		return ssdp.PresentationFor(rootURL, []string{system.FQDN(host, root.Domain()), host},
			func(h string) bool { return certpem.Covers(names, h) })
	}
}
