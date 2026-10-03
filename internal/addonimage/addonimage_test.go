package addonimage

import (
	"net/http"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// openccu-lite task 100: the type comes from the content, never from the name. An HTML page is no
// SVG however it is called; an SVG with a script in it is still an SVG - it is the policy on the
// answer that keeps it a picture (TestHeaders).
func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"svg", `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`, "image/svg+xml"},
		{"svg, self-closing", `<svg/>`, "image/svg+xml"},
		{"svg, bare root", `<svg>`, "image/svg+xml"},
		{"svg after the xml declaration and a comment", "\xEF\xBB\xBF<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!-- Inkscape -->\n<svg xmlns=\"http://www.w3.org/2000/svg\"/>", "image/svg+xml"},
		{"svg after a doctype with an internal subset", `<!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd" [ <!ENTITY ns_svg "http://www.w3.org/2000/svg"> ]>` + "\n<svg xmlns=\"&ns_svg;\"/>", "image/svg+xml"},
		{"svg with a script is an svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`, "image/svg+xml"},
		{"png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR", "image/png"},
		{"jpeg", "\xFF\xD8\xFF\xE0\x00\x10JFIF", "image/jpeg"},
		{"gif", "GIF89a\x01\x00\x01\x00", "image/gif"},
		{"webp", "RIFF\x24\x00\x00\x00WEBPVP8 ", "image/webp"},
		{"html named .svg is nothing", `<!DOCTYPE html><html><body><svg></svg></body></html>`, ""},
		{"html without a doctype", `<html><svg/></html>`, ""},
		{"text", `hello`, ""},
		{"empty", ``, ""},
		{"svg as a tag name only", `<svgx/>`, ""},
		{"ico", "\x00\x00\x01\x00\x01\x00", ""},
		{"riff that is not webp", "RIFF\x24\x00\x00\x00WAVEfmt ", ""},
		{"unterminated comment", `<!-- <svg/>`, ""},
		{"an xml declaration alone", `<?xml version="1.0"?>`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := Sniff([]byte(c.in))
			if got != c.want || ok != (c.want != "") {
				t.Errorf("Sniff = %q %v, want %q", got, ok, c.want)
			}
		})
	}
	// a long preamble is looked through only so far
	long := strings.Repeat(" ", 5000) + "<svg/>"
	if _, ok := Sniff([]byte(long)); ok {
		t.Error("an svg after 5000 bytes of white space was accepted")
	}
}

func TestHeaders(t *testing.T) {
	h := http.Header{}
	SetHeaders(h, "image/svg+xml")
	want := map[string]string{
		"Content-Type":                 "image/svg+xml",
		"X-Content-Type-Options":       "nosniff",
		"Content-Security-Policy":      "default-src 'none'; style-src 'unsafe-inline'",
		"Cross-Origin-Resource-Policy": "same-origin",
		"Cache-Control":                "private, max-age=3600",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestDeclared(t *testing.T) {
	ui := manifest.UI{Icon: "x/www/icon.svg", LogoDark: "x/www/logo-dark.png"}
	if got := DeclaredKinds(ui); strings.Join(got, ",") != "icon,logo-dark" {
		t.Errorf("DeclaredKinds = %v", got)
	}
	if Declared(ui, KindIcon) != "x/www/icon.svg" || Declared(ui, KindLogoDark) != "x/www/logo-dark.png" || Declared(ui, KindLogo) != "" || Declared(ui, "other") != "" {
		t.Error("Declared")
	}
	if DeclaredKinds(manifest.UI{}) != nil {
		t.Error("an empty block declares kinds")
	}
}

// The manifest path is relative to the package root; its first segment is the directory that
// becomes /usr/local/addons/<id>, so the rest is the path in the tree.
func TestTreeRel(t *testing.T) {
	for in, want := range map[string]string{
		"mosquitto/www/icon.svg":         "www/icon.svg",
		"redmatic/www/favicon-96x96.png": "www/favicon-96x96.png",
		"a/b":                            "b",
		"icon.svg":                       "",
		"a/":                             "",
		"":                               "",
	} {
		if got := TreeRel(in); got != want {
			t.Errorf("TreeRel(%q) = %q, want %q", in, got, want)
		}
	}
}
