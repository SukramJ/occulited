package httpapi

import (
	"strings"

	"github.com/hobbyquaker/occulited/internal/catalog"
	"github.com/hobbyquaker/occulited/internal/system"
)

// Task 88's postponed part (D-61, D-67): ?sid= leaves the URLs of the addons that read the session
// from the gate.
//
// The shell appends the CCU's ?sid=@…@ to an addon's frame, its new-tab link and its settings page,
// and a URL with a session id ends up in the history, in bookmarks, in referrers and in the access
// log. Since B-94 lighttpd's gate hands every request under /addons/ to the addon with
// X-Occulite-Session, so an addon that reads it needs no ?sid=. Its manifest says so for the
// installed version (ui.session_header, D-119); the box answers per addon and per request whether
// the shell leaves ?sid= off - `session_header` on GET /nav and GET /addons - and everything it
// cannot vouch for keeps ?sid= as before.

// SessionHeader says whether addon id, installed at version, reads X-Occulite-Session on path, the URL
// the shell would open - so that the shell leaves ?sid= off it. True when the addon's stored manifest
// (the one its install brought, D-119) says ui.session_header; for an addon without a stored manifest
// the runtime block the old catalogue stored in its policy still counts, with its header_since version
// (catalog.VersionAtLeast). False, and ?sid= stays, for a path outside /addons/, where the gate passes
// no header; for an addon without either declaration; and for an unknown or too old installed version.
func SessionHeader(root system.Root, id, version, path string) bool {
	if !strings.HasPrefix(path, "/addons/") || version == "" {
		return false
	}
	if m := root.ReadAddonManifest(id); m != nil {
		return m.UI.SessionHeader
	}
	if p := root.ReadAddonPolicy(id); p != nil && p.Runtime != nil && p.Runtime.Session != nil {
		return p.Runtime.Session.HeaderSince != "" && catalog.VersionAtLeast(version, p.Runtime.Session.HeaderSince)
	}
	return false
}

// SettingsURL is the settings page the addon's manifest names (ui.settings_url, B-134), where the
// addon's Config-Url is not it - Homematic Manager's Config-Url is the CCU's button into the app - or
// "" when it names none. For an addon without a stored manifest the old catalogue's runtime.settings_url
// in its stored policy still counts.
func SettingsURL(root system.Root, id string) string {
	if m := root.ReadAddonManifest(id); m != nil {
		return m.UI.SettingsURL
	}
	if p := root.ReadAddonPolicy(id); p != nil && p.Runtime != nil {
		return p.Runtime.SettingsURL
	}
	return ""
}

// Fullscreen says whether the addon's stored manifest declares ui.fullscreen (occulited task 24,
// openccu-lite #11): its frontend offers its own way back to the system, so the shell may show it
// without the top bar once the user ticks that for the addon. Only the manifest the install
// brought (D-119) counts - the old catalogue's policy block has no such field - and an addon
// without one is never offered the choice.
func Fullscreen(root system.Root, id string) bool {
	m := root.ReadAddonManifest(id)
	return m != nil && m.UI.Fullscreen
}

// addonSettingsURL is the settings page the shell frames for an addon: its Config-Url, else the
// one it registered in hm_addons.cfg (AddonFrame.svelte takes them in that order).
func addonSettingsURL(a system.Addon) string {
	if a.ConfigURL != "" {
		return a.ConfigURL
	}
	if a.Settings != nil {
		return a.Settings.ConfigURL
	}
	return ""
}
