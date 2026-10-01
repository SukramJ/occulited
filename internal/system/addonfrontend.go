package system

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Where an addon declares a web frontend of its own. An addon that installs a file here is asking
// lighttpd to map a path that the addon's own server answers; that is what makes it a frontend
// rather than a settings page (task 26's correction).
const lighttpdDropinDir = "/usr/local/etc/config/lighttpd"

var (
	// `$HTTP["url"] =~ "^/(addons/red/).*" {`
	lighttpdURLBlockRe = regexp.MustCompile(`(?s)\$HTTP\s*\[\s*"url"\s*\]\s*=~\s*"([^"]*)"`)
	// `proxy.server = ( "/addons/red/" => (( "host" => …` — the key may sit on the next line
	lighttpdProxyRe = regexp.MustCompile(`(?s)\bproxy\.server\s*=\s*\(\s*"([^"]*)"`)
	// a path we are willing to put in the menu: under /addons/, no traversal, no surprises
	frontendPathRe = regexp.MustCompile(`^/addons/[A-Za-z0-9][A-Za-z0-9._~-]*(?:/[A-Za-z0-9][A-Za-z0-9._~-]*)*/?$`)
)

// AddonFrontends maps addon id → the path its lighttpd drop-in exposes, for every addon that has
// a web frontend of its own.
//
// The distinction matters and used to be conflated (task 26's correction). An addon's `Config-Url`
// is its *settings page* — what OpenCCU reaches through Systemsteuerung → Zusatzsoftware — and most
// addons have one; it belongs on the Addons page, not in the shell's addon menu. A *frontend* is a
// full interface of its own, and an addon gets one onto the box exactly one way: by dropping a
// lighttpd configuration into /usr/local/etc/config/lighttpd/<id>.conf that maps a path to its own
// server. RedMatic is the worked example, and is what the parser was written against:
//
//	url.redirect = ("^/addons/red$" => "/addons/red/")
//	$HTTP["url"] =~ "^/(addons/red/).*" {
//	  proxy.server = ("/addons/red/" => (( "host" => "127.0.0.1", "port" => 1880 )))
//	  …
//	}
//
// so redmatic's entry is /addons/red/ — note that the path is *not* /addons/redmatic/: only the
// file name says which addon a drop-in belongs to.
//
// The parse is deliberately narrow rather than a lighttpd configuration parser. It takes the key of
// a `proxy.server` mapping, falls back to the literal prefix of the enclosing `$HTTP["url"]` regex
// when that key is the catch-all "", and accepts the result only if it is a plain path under
// /addons/. Anything else yields no entry: an addon that is not confidently placed is treated as
// having no frontend, because a wrong link in the menu is worse than a missing one. The same file's
// second block —
//
//	$HTTP["url"] =~ "(^/description.xml)|(^/api/.*/lights)" { proxy.server = ( "" => …
//
// is a Philips-Hue emulation, not a frontend, and falls out by that rule.
//
// Only lighttpd counts here. lighttpd already serves /addons/<id>/ off the filesystem, so an addon
// whose frontend is static needs no drop-in at all and cannot be told apart from its settings page;
// a drop-in exists precisely because something has to be proxied or aliased somewhere else.
func (r Root) AddonFrontends() map[string]string {
	out := map[string]string{}
	files, _ := filepath.Glob(filepath.Join(r.join(lighttpdDropinDir), "*.conf"))
	sort.Strings(files)
	for _, f := range files {
		id := strings.TrimSuffix(filepath.Base(f), ".conf")
		if !navIDRe.MatchString(id) {
			continue
		}
		st, err := os.Stat(f) // follows the symlink an addon usually installs
		if err != nil || st.Size() > 256*1024 {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if p := frontendPath(string(raw)); p != "" {
			out[id] = p
		}
	}
	return out
}

// frontendPath returns the first /addons/… path a lighttpd drop-in maps, or "".
func frontendPath(conf string) string {
	if ps := frontendPaths(conf); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

// frontendPaths returns every distinct /addons/… path a lighttpd drop-in maps, in the order of its
// proxy.server statements. The menu takes the first; the addon ingress scope opens all of them
// (openccu-lite task 307, AddonIngressSegments).
func frontendPaths(conf string) []string {
	conf = stripLighttpdComments(conf)
	blocks := lighttpdURLBlockRe.FindAllStringSubmatchIndex(conf, -1)
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, m := range lighttpdProxyRe.FindAllStringSubmatchIndex(conf, -1) {
		cand := conf[m[2]:m[3]]
		if p := cleanFrontendPath(cand); p != "" {
			add(p)
			continue
		}
		// A catch-all key ("" or "/") proxies whatever the enclosing block matched, so the path is
		// in that block's regex instead. Take the nearest one that opens before this line.
		for i := len(blocks) - 1; i >= 0; i-- {
			if blocks[i][0] >= m[0] {
				continue
			}
			add(cleanFrontendPath(regexLiteralPrefix(conf[blocks[i][2]:blocks[i][3]])))
			break
		}
	}
	return out
}

// cleanFrontendPath accepts a path only if it is an ordinary absolute path under /addons/, and
// normalises it to the directory form the menu links to.
func cleanFrontendPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || !frontendPathRe.MatchString(p) || strings.Contains(p, "..") {
		return ""
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	if p == "/addons/" { // the whole directory is not one addon's frontend
		return ""
	}
	return p
}

// regexLiteralPrefix reduces a lighttpd url regex to the literal path it starts with. Grouping and
// anchors are dropped — `^/(addons/red/).*` is `/addons/red/` — and the run stops at the first
// character that is not plain path material, so a regex whose meaning we cannot read
// (`^/description.xml`, `^/api/.*/lights`) yields a prefix that cleanFrontendPath then rejects.
func regexLiteralPrefix(re string) string {
	var b strings.Builder
	for _, c := range re {
		switch {
		case c == '^' || c == '(':
			continue // anchors and grouping carry no path
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteRune(c)
		case c == '/' || c == '_' || c == '-' || c == '~':
			b.WriteRune(c)
		default:
			return b.String() // `.`, `*`, `|`, `$`, `)`, anything else: the literal ends here
		}
	}
	return b.String()
}

// stripLighttpdComments removes `#` comments, which are only comments outside a quoted string.
func stripLighttpdComments(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, line := range strings.Split(s, "\n") {
		inQuote, esc, cut := false, false, len(line)
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inQuote = !inQuote
			case c == '#' && !inQuote:
				cut = i
			}
			if cut != len(line) {
				break
			}
		}
		b.WriteString(line[:cut])
		b.WriteByte('\n')
	}
	return b.String()
}
