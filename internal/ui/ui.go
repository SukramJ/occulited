// Package ui serves the built Svelte admin UI embedded into the binary (D-21): it is the box's
// shell (D-18). A build without ui/dist (a bare `go build` in development) serves a placeholder.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// The shell's Content-Security-Policy and Permissions-Policy (openccu-lite task 259, D-78's F-5):
// one file, header lines, embedded here and read by the UI's Playwright stub (ui/test/stub), so
// the suite runs every page under exactly this policy (ui/test/e2e/csp.spec.ts fails on a
// violation). They go on the shell's own answers alone - the page, its assets, the client-side
// routes - and not on the API, not on an addon's pages under /addons/ (the CCU's conventions:
// inline scripts, their own styles) and not on a path an addon's lighttpd drop-in claims outside
// /addons/, which is why occulited sets them rather than lighttpd's fragment: a header added to
// "everything but /api and /addons" there would reach those too. What the shell needs: scripts and
// styles from its origin, style attributes ('unsafe-inline' for styles alone - Svelte writes
// style="" into its templates; no inline script anywhere), images from itself and data: (the
// favicon), connections to itself (fetch, EventSource), frames from itself (the addon pages),
// framed by itself alone (X-Frame-Options' successor), forms to itself, no plugins, no <base>. The
// camera is the shell's own (the QR scanner for device keys, task 154); microphone and geolocation
// nobody's.
//
//go:embed csp.txt
var policyFile string

// Policy is the shell's security headers, by name, as csp.txt has them.
var Policy = parsePolicy(policyFile)

func parsePolicy(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if name, value, ok := strings.Cut(line, ": "); ok && name != "" {
			out[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
	}
	return out
}

func setPolicy(w http.ResponseWriter) {
	for name, value := range Policy {
		w.Header().Set(name, value)
	}
}

// Handler returns the SPA handler: static files from dist, index.html for every other path so
// the client-side router owns navigation, and the placeholder when nothing was built.
func Handler() http.Handler {
	sub, _ := fs.Sub(dist, "dist")
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			setPolicy(w)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
		})
	}
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setPolicy(w) // on every answer of the shell, the 404 for a missing file included
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if _, err := fs.Stat(sub, p); err != nil {
			// B-150: a missing file is a 404, never the app shell - a client asking for a device
			// picture under /config/img took the HTML page for the picture
			if isStaticFile(p) {
				http.NotFound(w, r)
				return
			}
			// client-side route: serve the app shell, never cached
			r.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-store")
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
		// task 193: the manifest's own type (Go's table has none), and the worker's scope is the root
		switch path.Ext(p) {
		case ".webmanifest":
			w.Header().Set("Content-Type", "application/manifest+json")
		case ".js":
			if p == "sw.js" {
				w.Header().Set("Service-Worker-Allowed", "/")
			}
		}
		files.ServeHTTP(w, r)
	})
}

// staticExt are the extensions of files, never of a client-side route (B-150).
var staticExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".svg": true, ".ico": true,
	".css": true, ".js": true, ".mjs": true, ".map": true, ".json": true, ".xml": true, ".txt": true,
	".woff": true, ".woff2": true, ".ttf": true, ".webmanifest": true,
	// the WebUI's DEVDB.tcl (openccu-lite task 331): lighttpd serves it, never the shell
	".tcl": true,
}

// spaPrefixes are the client-side routes that carry an id in their path - an addon's id may end
// in ".js" - so they keep the app shell whatever their extension.
var spaPrefixes = []string{"addon-settings/", "addons/", "catalog/", "system/", "account/", "settings/"}

func isStaticFile(p string) bool {
	for _, pre := range spaPrefixes {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	return staticExt[strings.ToLower(path.Ext(p))]
}

const placeholder = `<!doctype html><meta charset="utf-8"><title>occulited</title>
<style>body{font:15px system-ui,sans-serif;margin:3rem auto;max-width:36rem;color:#222;background:#fafafa}code{background:#eee;padding:.1em .3em;border-radius:3px}</style>
<h1>occulited</h1><p>The web UI is not part of this build. Build it with <code>cd ui &amp;&amp; npm ci &amp;&amp; npm run build</code> and rebuild the binary, or use the API directly: <code>/api/meta/v1/version</code>, <code>/api/system/v1/status</code>.</p>`
