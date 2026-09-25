package radio

import (
	"regexp"
	"strings"
)

// Interface is one <ipc> entry of InterfacesList.xml.
type Interface struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Info string `json:"info"`
}

// BidCosWired is the entry hs485d gets where a wired interface is configured: the template has
// none (D-96), so the plan appends it at boot, and the Interfaces page's write of the wired
// gateways adds or removes it while the system runs (B-166).
var BidCosWired = Interface{Name: "BidCos-Wired", URL: "xmlrpc_bin://127.0.0.1:32000", Info: "BidCos-Wired"}

var (
	ipcRe = regexp.MustCompile(`(?s)<ipc>(.*?)</ipc>`)
	tagRe = regexp.MustCompile(`<(name|url|info)>\s*(.*?)\s*</(name|url|info)>`)
)

// ParseInterfaces reads the entries.
func ParseInterfaces(xml string) []Interface {
	var out []Interface
	for _, m := range ipcRe.FindAllStringSubmatch(xml, -1) {
		var i Interface
		for _, t := range tagRe.FindAllStringSubmatch(m[1], -1) {
			switch t[1] {
			case "name":
				i.Name = t[2]
			case "url":
				i.URL = t[2]
			case "info":
				i.Info = t[2]
			}
		}
		if i.Name != "" {
			out = append(out, i)
		}
	}
	return out
}

// TemplateInterfaces is the file as it will be before the cuts: the template's entries, as
// upstream's S49hs485d re-copies the template whenever the box's file differs from it. Entries
// other programs add (CCU-Jack) are lost at boot, on OpenCCU and here alike (D-97: the merge by
// name that would have kept them was rejected).
func TemplateInterfaces(tmpl string) []Interface {
	return ParseInterfaces(tmpl)
}

func removeInterface(list []Interface, name string) []Interface {
	var out []Interface
	for _, i := range list {
		if i.Name != name {
			out = append(out, i)
		}
	}
	return out
}

func hasInterface(list []Interface, name string) bool {
	for _, i := range list {
		if i.Name == name {
			return true
		}
	}
	return false
}

// RenderInterfaces writes the file as upstream's S49hs485d left it: the template's own bytes,
// with the <ipc> block of every entry not in the list cut out (S49 deleted the lines from the
// entry's <ipc> to its </ipc>) and an entry the template lacks (BidCos-Wired, D-96) appended
// before the closing tag in the template's layout. Byte-identical with the template when nothing
// is cut, as D-97 wants.
func RenderInterfaces(tmpl string, list []Interface) string {
	keep := map[string]bool{}
	for _, i := range list {
		keep[i.Name] = true
	}
	out := ipcBlockRe.ReplaceAllStringFunc(tmpl, func(block string) string {
		m := tagRe.FindStringSubmatch(block)
		name := ""
		for _, t := range tagRe.FindAllStringSubmatch(block, -1) {
			if t[1] == "name" {
				name = t[2]
			}
		}
		_ = m
		if name != "" && !keep[name] {
			return ""
		}
		return block
	})
	have := map[string]bool{}
	for _, i := range ParseInterfaces(out) {
		have[i.Name] = true
	}
	var extra strings.Builder
	for _, i := range list {
		if !have[i.Name] {
			extra.WriteString("\t<ipc>\n\t \t<name>" + i.Name + "</name>\n\t \t<url>" + i.URL + "</url> \n\t \t<info>" + i.Info + "</info> \n\t</ipc>\n")
		}
	}
	if extra.Len() > 0 {
		if idx := strings.LastIndex(out, "</interfaces>"); idx >= 0 {
			out = out[:idx] + extra.String() + out[idx:]
		} else {
			out += extra.String()
		}
	}
	return out
}

// ipcBlockRe is one entry with the line it sits on: from the <ipc> tag's leading whitespace to
// the newline after </ipc>.
var ipcBlockRe = regexp.MustCompile(`(?s)[ \t]*<ipc>.*?</ipc>[ \t]*\n?`)
