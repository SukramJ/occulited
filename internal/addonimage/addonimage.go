// Package addonimage is what every addon icon and logo passes through before the shell shows it
// (openccu-lite task 100). An addon declares its images in its manifest (docs/manifest-format.md,
// ui.icon, ui.icon_dark, ui.logo, ui.logo_dark: paths relative to the package root); the system
// serves them from its own origin - for an installed addon out of its tree, for a catalogue entry
// out of the copy the check fetched with the manifest - and shows them only through <img>. The
// file itself is the addon's, so nothing about it is trusted: the type comes from the content, not
// the name; a file larger than MaxSize is refused; an SVG goes out with a Content-Security-Policy
// that lets nothing in it run or load, and with nosniff, so a browser that is handed its URL
// directly renders an inert picture and nothing else.
package addonimage

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// MaxSize bounds an image: an icon is a few kilobytes, a logo some tens; the manifest's own limit.
const MaxSize = 256 << 10

// The kinds, as the routes name them: GET …/images/<kind>.
const (
	KindIcon     = "icon"
	KindIconDark = "icon-dark"
	KindLogo     = "logo"
	KindLogoDark = "logo-dark"
)

// Kinds is every kind, in the manifest's order.
var Kinds = []string{KindIcon, KindIconDark, KindLogo, KindLogoDark}

// Declared is the manifest path the UI block declares for a kind, "" for none or an unknown kind.
func Declared(ui manifest.UI, kind string) string {
	switch kind {
	case KindIcon:
		return ui.Icon
	case KindIconDark:
		return ui.IconDark
	case KindLogo:
		return ui.Logo
	case KindLogoDark:
		return ui.LogoDark
	}
	return ""
}

// DeclaredKinds are the kinds the UI block declares, in Kinds' order; nil for none.
func DeclaredKinds(ui manifest.UI) []string {
	var out []string
	for _, k := range Kinds {
		if Declared(ui, k) != "" {
			out = append(out, k)
		}
	}
	return out
}

// TreeRel is where a manifest path lies in the installed addon's tree, /usr/local/addons/<id>/ -
// the fallback for an addon without the install's copies (occulited task 11, FromArchive):
// the path's first segment is the directory the package carries the addon in and the installer
// copies to that tree ("mosquitto/www/icon.svg" is "www/icon.svg" there), so the rest is the path
// in the tree. "" for a path of one segment, which lies beside the manifest at the package root and
// nowhere in the tree.
func TreeRel(path string) string {
	_, rest, ok := strings.Cut(path, "/")
	if !ok || rest == "" || strings.HasSuffix(rest, "/") {
		return ""
	}
	return rest
}

// Sniff is the image type a file's content says it is, by its signature: SVG (an XML document whose
// root element is svg, after a byte-order mark, white space, the XML declaration, comments and a
// doctype), PNG, JPEG, GIF or WebP. ok is false for everything else - an HTML page named .svg, a
// text file, an ICO - and such a file is not served.
func Sniff(b []byte) (contentType string, ok bool) {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", true
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg", true
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "image/gif", true
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp", true
	}
	if isSVG(b) {
		return "image/svg+xml", true
	}
	return "", false
}

// isSVG: the document's first element is <svg ...>, whatever stands before it that an XML parser
// skips. Only the head is looked at; the rest of the file is the browser's to parse, as a picture.
func isSVG(b []byte) bool {
	s := bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF"))
	if len(s) > 4096 {
		s = s[:4096]
	}
	for {
		s = bytes.TrimLeft(s, " \t\r\n")
		switch {
		case bytes.HasPrefix(s, []byte("<?")):
			end := bytes.Index(s, []byte("?>"))
			if end < 0 {
				return false
			}
			s = s[end+2:]
		case bytes.HasPrefix(s, []byte("<!--")):
			end := bytes.Index(s[4:], []byte("-->"))
			if end < 0 {
				return false
			}
			s = s[4+end+3:]
		case bytes.HasPrefix(s, []byte("<!DOCTYPE")), bytes.HasPrefix(s, []byte("<!doctype")):
			// a doctype may carry an internal subset in brackets; the element follows its '>'
			depth, i := 0, 0
			for ; i < len(s); i++ {
				switch s[i] {
				case '[':
					depth++
				case ']':
					depth--
				case '>':
					if depth <= 0 {
						goto doctypeEnd
					}
				}
			}
			return false
		doctypeEnd:
			s = s[i+1:]
		case bytes.HasPrefix(s, []byte("<svg")):
			if len(s) == 4 {
				return false
			}
			switch s[4] {
			case ' ', '\t', '\r', '\n', '>', '/':
				return true
			}
			return false
		default:
			return false
		}
	}
}

// Policy is the Content-Security-Policy every image goes out with: nothing in it may run or load
// anything, inline style excepted, so an SVG with a script or an external reference is a picture
// and nothing more wherever it is opened.
const Policy = "default-src 'none'; style-src 'unsafe-inline'"

// SetHeaders puts the fixed headers of an image answer on h: the type the content says, nosniff
// so the browser does not second-guess it, the policy, same-origin only, and an hour of private
// caching (the URL carries the addon's version, so an update is a new URL).
func SetHeaders(h http.Header, contentType string) {
	h.Set("Content-Type", contentType)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", Policy)
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Cache-Control", "private, max-age=3600")
}
