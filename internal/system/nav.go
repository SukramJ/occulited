package system

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// NavEntry is one menu entry of the shell (D-18, task 10): from a drop-in file in
// /usr/local/etc/config/nav.d/, or synthesised for an addon that brings a web frontend of its own.
type NavEntry struct {
	// ID is the entry's route, /nav/<ID>. For an addon's synthesised frontend it is named after the
	// path the addon's lighttpd drop-in proxies (task 88, D-61): RedMatic's Node-RED at /addons/red/
	// is "red", so the embedded view reads like its standalone URL. A nav.d drop-in keeps its own.
	ID string `json:"id"`
	// Addon is the addon the entry belongs to, when it is one: the id the shell keys the dropdown
	// row, the settings link, the favicon and the kept page by. The shell replaces /nav/<Addon> by
	// /nav/<ID> when the two differ, so old links and bookmarks keep working. Empty for a nav.d
	// entry that is no addon.
	Addon  string            `json:"addon,omitempty"`
	Label  map[string]string `json:"label"`
	Icon   string            `json:"icon,omitempty"`
	Href   string            `json:"href"`
	Target string            `json:"target"` // "iframe" or "blank"
	Order  int               `json:"order"`
	// Source says what the entry *is*, which is what the shell renders it by: "addon" is an
	// addon's own web frontend and goes in the addon dropdown, "nav.d" is a menu entry the box
	// was configured with and becomes a tab of its own. A nav.d drop-in named after an installed
	// addon is that addon's frontend, deliberately declared, and is reported as "addon".
	Source string `json:"source"` // "nav.d" or "addon"
	// KeepAlive asks the shell to keep the page loaded while the user is on other pages, as it
	// does for every addon frontend (task 39): switching back shows it as it was instead of
	// loading it again. A nav.d page is not kept by default - an arbitrary page may be heavy or
	// hostile - so a drop-in opts in with "keep_alive": true. Only an iframe entry can be kept.
	KeepAlive bool `json:"keep_alive,omitempty"`
	// SessionHeader: the addon reads the gate's X-Occulite-Session, so the shell opens Href without
	// ?sid= (task 88, D-67). Set per request by GET /nav (httpapi.SessionHeader), never here.
	SessionHeader bool `json:"session_header,omitempty"`
	// LegacySession: the shell opens Href with the session's ?sid=@..@ alias (task 125): an addon
	// that does not read the header, with the legacy session switched on for it. Set per request
	// by GET /nav (httpapi.LegacySession), never here.
	LegacySession bool `json:"legacy_session,omitempty"`
	// AddonVersion is the installed version of Addon, for that decision; not part of the answer.
	AddonVersion string `json:"-"`
	// Proxied: Href is a path the addon's lighttpd drop-in proxies to the addon's own server (B-133).
	// Such a page never gets the legacy alias: the alias exists for the tclsh CGIs lighttpd serves
	// itself (they ask the tclrega shim), while a server behind the proxy learns the session from
	// the gate's X-Occulite-Session header and can do nothing with an alias - occulited's API
	// refuses it (D-77), so a frontend that checks its ?sid= against the API refuses the page.
	// Not part of the answer.
	Proxied bool `json:"-"`
}

var (
	navIDRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	navIconRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// NavEntries reads the nav.d drop-ins and synthesises an entry for every addon that brings a web
// frontend of its own without one. Strictly sanitised: labels are text, hrefs must be same-origin
// paths unless the target is "blank", no HTML anywhere. Invalid files are skipped, never fatal.
func (b AddonScripts) NavEntries(ctx context.Context) []NavEntry {
	byID := map[string]NavEntry{}
	dir := b.Root.join("/usr/local/etc/config/nav.d")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(files)
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil || len(raw) > 16*1024 {
			continue
		}
		// label may be a {lang: text} map or a plain string (taken for both languages)
		var probe struct {
			ID        string          `json:"id"`
			Label     json.RawMessage `json:"label"`
			Icon      string          `json:"icon"`
			Href      string          `json:"href"`
			Target    string          `json:"target"`
			Order     int             `json:"order"`
			KeepAlive bool            `json:"keep_alive"`
		}
		if err := json.Unmarshal(raw, &probe); err != nil {
			continue
		}
		e := NavEntry{ID: probe.ID, Icon: probe.Icon, Href: probe.Href, Target: probe.Target, Order: probe.Order, KeepAlive: probe.KeepAlive, Label: map[string]string{}}
		var text string
		if json.Unmarshal(probe.Label, &e.Label) != nil {
			if json.Unmarshal(probe.Label, &text) == nil && text != "" {
				e.Label = map[string]string{"de": text, "en": text}
			}
		}
		e.Source = "nav.d"
		if ok := sanitizeNav(&e); ok {
			byID[e.ID] = e
		}
	}
	// An addon belongs in the shell's addon menu when it brings a *frontend* - an interface of its
	// own, which on this box means a lighttpd drop-in mapping a path to its server. Its Config-Url
	// is a different thing: the settings page the Addons page already links to, which most addons
	// have and which is not a frontend (task 26's correction). Synthesising from Config-Url put
	// mosquitto in the menu, which has only settings, and sent redmatic's entry to settings.cgi
	// instead of the Node-RED at /addons/red/ that is its whole point.
	frontends := b.Root.AddonFrontends()
	addons, _ := b.ListAddons(ctx)
	installed := map[string]bool{}
	for _, a := range addons {
		installed[a.ID] = true
	}
	var synthesised []NavEntry
	for _, a := range addons {
		if e, taken := byID[a.ID]; taken {
			// The addon's own drop-in wins over the synthesised entry, and is a deliberate
			// declaration that this addon has a frontend - so it is an addon entry, wherever the
			// path came from, and shows in the dropdown with the others. It keeps its own id.
			e.Source = "addon"
			e.Addon, e.AddonVersion = a.ID, a.Version
			// a drop-in that points into the proxied path is the proxied frontend too (B-133)
			e.Proxied = frontends[a.ID] != "" && strings.HasPrefix(e.Href, frontends[a.ID])
			byID[a.ID] = e
			continue
		}
		href := frontends[a.ID]
		if href == "" {
			continue // settings page only, or nothing: the Addons page, not the menu
		}
		name := a.Name
		if a.Settings != nil && a.Settings.Name != "" {
			name = a.Settings.Name
		}
		if name == "" {
			name = a.ID
		}
		e := NavEntry{ID: a.ID, Addon: a.ID, AddonVersion: a.Version, Label: map[string]string{"de": name, "en": name}, Href: href, Target: "iframe", Order: 500, Source: "addon", Proxied: true}
		if sanitizeNav(&e) {
			synthesised = append(synthesised, e)
		}
	}
	// Task 88 (D-61): a frontend's route is named after its proxied path - /nav/red for /addons/red/
	// - where that name is unambiguous: no other frontend's path gives it, no nav.d entry has it, and
	// it is no other installed addon's id (that id is the old route of that addon's own frontend).
	// Otherwise the addon id stays, as before.
	byPath := map[string]int{}
	for _, e := range synthesised {
		if id := frontendNavID(e.Href); id != "" {
			byPath[id]++
		}
	}
	for _, e := range synthesised {
		if id := frontendNavID(e.Href); id != "" && byPath[id] == 1 && (id == e.Addon || !installed[id]) {
			if _, taken := byID[id]; !taken {
				e.ID = id
			}
		}
		byID[e.ID] = e
	}
	out := make([]NavEntry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Order != out[j].Order {
			return out[i].Order < out[j].Order
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// frontendNavID is the route id a frontend's path gives: its first segment after /addons/, lower
// case, anything but letters, digits and dashes turned into a dash, at most 32 characters - "" when
// nothing usable is left.
func frontendNavID(href string) string {
	rest, ok := strings.CutPrefix(href, "/addons/")
	if !ok {
		return ""
	}
	seg, _, _ := strings.Cut(rest, "/")
	id := strings.Map(func(c rune) rune {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
			return c
		case c >= 'A' && c <= 'Z':
			return c + ('a' - 'A')
		default:
			return '-'
		}
	}, seg)
	id = strings.Trim(id, "-")
	if len(id) > 32 {
		id = strings.TrimRight(id[:32], "-")
	}
	if !navIDRe.MatchString(id) {
		return ""
	}
	return id
}

func sanitizeNav(e *NavEntry) bool {
	if !navIDRe.MatchString(e.ID) {
		return false
	}
	if e.Target != "blank" {
		e.Target = "iframe"
	}
	if e.Target != "iframe" {
		e.KeepAlive = false // a new tab is the browser's to keep
	}
	e.Href = strings.TrimSpace(e.Href)
	if e.Href == "" {
		return false
	}
	if e.Target == "iframe" && (!strings.HasPrefix(e.Href, "/") || strings.HasPrefix(e.Href, "//")) {
		return false // an iframe pane must be same-origin
	}
	if e.Target == "blank" && !(strings.HasPrefix(e.Href, "/") || strings.HasPrefix(e.Href, "http://") || strings.HasPrefix(e.Href, "https://")) {
		return false
	}
	if strings.ContainsAny(e.Href, "<>\"'\n\r") {
		return false
	}
	clean := map[string]string{}
	for lang, text := range e.Label {
		text = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(text, "<", ""), ">", ""))
		if text != "" && len(text) <= 64 && regexp.MustCompile(`^[a-z]{2}$`).MatchString(lang) {
			clean[lang] = text
		}
	}
	if len(clean) == 0 {
		return false
	}
	if _, ok := clean["en"]; !ok {
		for _, v := range clean {
			clean["en"] = v
			break
		}
	}
	e.Label = clean
	if !navIconRe.MatchString(e.Icon) {
		e.Icon = ""
	}
	return true
}
