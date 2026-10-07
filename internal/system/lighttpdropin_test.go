package system

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/priv"
)

// B-120: the allowlist against the two fragments the lab's addons ship, and against what an
// attacker would write.
const redmaticFragment = `url.redirect = ("^/addons/red$" => "/addons/red/")
$HTTP["url"] =~ "^/(addons/red/).*" {
  proxy.server = ("/addons/red/" => (( "host" => "127.0.0.1", "port" => 1880 )))
  proxy.header = ( "upgrade" => "enable")
  server.errorfile-prefix  = "/usr/local/addons/redmatic/www/lighttpd-error-"
}
# the Philips Hue emulation of node-red-contrib-amazon-echo
$HTTP["url"] =~ "(^/description.xml)|(^/api/.*/lights)" {
  proxy.server = ( "" => ("localhost" => ("host" => "127.0.0.1", "port" => 6502)))
}
`

const hmmFragment = `$HTTP["url"] == "/addons/hmm" {
    url.redirect = ("^/addons/hmm$" => "/addons/hmm/")
}
$HTTP["url"] =~ "^/addons/hmm/(?!settings\.cgi|service\.cgi|update_check\.cgi)" {
    proxy.server = ("" => (("host" => "127.0.0.1", "port" => 8090)))
    proxy.header = ("upgrade" => "enable")
    server.errorfile-prefix = "/usr/local/addons/hmm/www/lighttpd-error-"
}
`

// B-54: ccu-addon-mui's fragment before its single-line workaround (PR #191): values that span
// lines inside parentheses, which the directive match refused as "not a directive".
const muiFragment = `# The WebSocket and the gzip-compressed assets come from the server
$HTTP["url"] =~ "^/addons/mui/(ws|assets/)" {
  proxy.server = ( "" => (
    ( "host" => "127.0.0.1", "port" => 8088 )
  ))
  proxy.header = (
    "upgrade" => "enable",
    "map-host-request" => ( "-" => "127.0.0.1" ),
    "map-host-response" => ( "127.0.0.1" => "-" )
  )
}

# The app's routes (no dot in the path) are its index.html
$HTTP["url"] !~ "^/addons/mui/(ws|assets/)" {
  url.rewrite-once = ( "^/addons/mui/[^.?]*(\?.*)?$" => "/addons/mui/index.html" )
}
`

func TestValidateLighttpdDropin(t *testing.T) {
	ok := map[string]string{
		"redmatic": redmaticFragment,
		"hmm":      hmmFragment,
		"mui":      muiFragment,
		"x":        "# only a comment\n",
	}
	okX := []string{
		"$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"host\" => \"[::1]\", \"port\" => 9000 ) ) )\n} else $HTTP[\"url\"] == \"/addons/x\" {\n  url.redirect = ( \"^/addons/x$\" => \"/addons/x/\" )\n}\n",
		"alias.url = ( \"/addons/x/static/\" => \"/usr/local/addons/x/share/static/\" )\nsetenv.add-response-header = ( \"X-Frame-Options\" => \"SAMEORIGIN\" )\nmimetype.assign += ( \".wasm\" => \"application/wasm\" )\n",
		"$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"socket\" => \"/usr/local/addons/x/run/x.sock\" ) ) )\n}\n",
		"$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"host\" => \"127.0.0.1\", \"port\" => 8090 ) ) )\n} else {\n  url.access-deny = ( \"~\" )\n}\n",
		// B-54: a value over several lines, with a comment on one of them, outside a condition
		"alias.url = (\n  \"/addons/x/static/\" => \"/usr/local/addons/x/share/static/\", # the static files\n  \"/addons/x/docs/\" => \"/usr/local/addons/x/share/docs/\"\n)\n",
	}
	for id, conf := range ok {
		if err := ValidateLighttpdDropin(id, []byte(conf)); err != nil {
			t.Errorf("%s: refused a good fragment: %v", id, err)
		}
	}
	for i, conf := range okX {
		if err := ValidateLighttpdDropin("x", []byte(conf)); err != nil {
			t.Errorf("x fragment %d: refused a good fragment: %v", i, err)
		}
	}
	bad := map[string]string{
		"include_shell":     "include_shell \"cat /etc/config/shadow > /tmp/x\"\n",
		"include":           "include \"/etc/lighttpd/lighttpd.conf\"\n",
		"socket":            "$SERVER[\"socket\"] == \":8888\" {\n  proxy.server = ( \"\" => ( ( \"host\" => \"127.0.0.1\", \"port\" => 1 ) ) )\n}\n",
		"external host":     "$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"host\" => \"10.0.0.1\", \"port\" => 80 ) ) )\n}\n",
		"socket outside":    "$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"socket\" => \"/run/occulite/helper.sock\" ) ) )\n}\n",
		"errorfile outside": "server.errorfile-prefix = \"/etc/config/\"\n",
		"alias outside":     "alias.url = ( \"/addons/x/\" => \"/etc/config/\" )\n",
		"docroot":           "server.document-root = \"/\"\n",
		"cgi":               "cgi.assign = ( \".sh\" => \"/bin/sh\" )\n",
		"magnet":            "magnet.attract-raw-url-to = ( \"/usr/local/addons/x/x.lua\" )\n",
		"variable":          "proxy.server = ( \"\" => ( ( \"host\" => \"127.0.0.1\", \"port\" => var.port ) ) )\n",
		"env":               "url.redirect = ( \"^/x\" => env.HOME )\n",
		"unbalanced":        "$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => ( ( \"host\" => \"127.0.0.1\", \"port\" => 1 ) ) )\n",
		"close too many":    "}\n",
		"host condition":    "$HTTP[\"host\"] == \"x\" {\n}\n$HTTP[\"remote-ip\"] == \"1\" {\n}\n",
		"binary":            "proxy.server = \x00\n",
		"modules":           "server.modules += ( \"mod_cgi\" )\n",
		// B-54: what a multi-line value must not smuggle in on its later lines
		"multi-line include":  "proxy.server = ( \"\" => (\n  ( \"host\" => \"127.0.0.1\", \"port\" => 1 )\n)\ninclude_shell \"id\"\n)\n",
		"multi-line host":     "$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => (\n    ( \"host\" => \"10.0.0.1\",\n      \"port\" => 80 )\n  ))\n}\n",
		"multi-line variable": "proxy.server = ( \"\" => (\n  ( \"host\" => \"127.0.0.1\",\n    \"port\" => var.port )\n))\n",
		"multi-line string":   "url.redirect = (\n  \"^/x\" => \"/addons/x/\n\"\n)\n",
		"multi-line alias":    "alias.url = (\n  \"/addons/x/\" => \"/usr/local/addons/x/www/\",\n  \"/addons/x/etc/\" => \"/etc/config/\"\n)\n",
	}
	for name, conf := range bad {
		if err := ValidateLighttpdDropin("x", []byte(conf)); err == nil {
			t.Errorf("%s: a bad fragment passed", name)
		}
	}
}

// occulited task 23: the verdict names the line the refused statement starts on and the statement,
// also for one that spans lines; a verdict on the whole fragment names no line
func TestValidateLighttpdDropinNamesTheLine(t *testing.T) {
	cases := []struct {
		conf   string
		reason string
		line   int
		stmt   string
	}{
		{"# a comment\n\ninclude_shell \"id\"\n", "not a directive", 3, `include_shell "id"`},
		{"\nserver.modules += ( \"mod_cgi\" )\n", "the directive server.modules is not allowed in an addon's fragment", 2, `server.modules += ( "mod_cgi" )`},
		{"url.redirect = ( \"^/x$\" => \"/x/\" )\n$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  proxy.server = ( \"\" => (\n    ( \"host\" => \"10.0.0.1\", \"port\" => 80 )\n  ))\n}\n", `proxy.server may point at this system only, not at "10.0.0.1"`, 3, `proxy.server = ( "" => ( ( "host" => "10.0.0.1", "port" => 80 ) ))`},
		{"$HTTP[\"url\"] =~ \"^/addons/x/\" {\n  url.access-deny = ( \"~\" )\n", "a block is not closed", 0, ""},
		{"proxy.server = \x00\n", "the fragment is not text", 0, ""},
	}
	for i, c := range cases {
		err := ValidateLighttpdDropin("x", []byte(c.conf))
		var rej *LighttpdRejection
		if !errors.As(err, &rej) {
			t.Fatalf("%d: %T %v", i, err, err)
		}
		if rej.Reason != c.reason || rej.Line != c.line || rej.Statement != c.stmt {
			t.Errorf("%d: got %+v", i, *rej)
		}
		if c.line > 0 && !strings.Contains(rej.Error(), fmt.Sprintf("(line %d: ", c.line)) {
			t.Errorf("%d: the one-line form lacks the line: %q", i, rej.Error())
		}
	}
	// the note round-trips, and one an older occulited wrote (the first line alone) still reads
	rej := &LighttpdRejection{Reason: "not a directive", Line: 7, Statement: "proxy.server ( \"\" )"}
	if got := parseLighttpdRejectedNote(lighttpdRejectedNote(rej)); got == nil || *got != *rej {
		t.Errorf("round trip: %+v", got)
	}
	if got := parseLighttpdRejectedNote("# occulited refused this addon's lighttpd fragment: the fragment is not a regular file\n"); got == nil || got.Reason != "the fragment is not a regular file" || got.Line != 0 {
		t.Errorf("old note: %+v", got)
	}
	if got := parseLighttpdRejectedNote("# something else\n"); got != nil {
		t.Errorf("not a note: %+v", got)
	}
}

// the sync in a fake root: a link becomes a copy, a fragment without a drop-in gets one, a bad one
// is refused with the note beside it, an unchanged copy is kept, a vanished source's copy stays
// only as a validated file
func TestSyncLighttpdDropins(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dropins := root.join(lighttpdDropinDir)
	must(os.MkdirAll(dropins, 0o755))
	write := func(rel, content string) {
		must(os.MkdirAll(filepath.Dir(root.join(rel)), 0o755))
		must(os.WriteFile(root.join(rel), []byte(content), 0o644))
	}
	// redmatic: the installer's link into its tree
	write("/usr/local/addons/redmatic/etc/lighttpd.conf", redmaticFragment)
	must(os.Symlink(root.join("/usr/local/addons/redmatic/etc/lighttpd.conf"), filepath.Join(dropins, "redmatic.conf")))
	// hmm: a template in the tree, and the file its rc.d rendered as root: the file stands
	write("/usr/local/addons/hmm/etc/lighttpd.conf", strings.ReplaceAll(hmmFragment, "8090", "@PORT@"))
	write(lighttpdDropinDir+"/hmm.conf", hmmFragment)
	// tmpl: only a template, no rendered file: refused, and the addon has to render it
	write("/usr/local/addons/tmpl/etc/lighttpd.conf", strings.ReplaceAll(hmmFragment, "8090", "@PORT@"))
	// frag: a fragment and no drop-in at all: copied
	write("/usr/local/addons/frag/etc/lighttpd.conf", "url.redirect = ( \"^/addons/frag$\" => \"/addons/frag/\" )\n")
	// evil: a link that leaves its tree, and a fragment with an include_shell
	write("/etc/shadowish", "include_shell \"id\"\n")
	must(os.Symlink(root.join("/etc/shadowish"), filepath.Join(dropins, "evil.conf")))
	write("/usr/local/addons/evil2/etc/lighttpd.conf", "include_shell \"id > /tmp/pwned\"\n")
	// plain: a root-written drop-in of an addon without a fragment, valid: validated in place
	write("/usr/local/addons/plain/www/index.html", "x")
	write(lighttpdDropinDir+"/plain.conf", "url.redirect = ( \"^/addons/plain$\" => \"/addons/plain/\" )\n")
	// occulited's own drop-in (the Log page's access log, openccu-lite B-190): no addon's fragment, left alone
	write(lighttpdDropinDir+"/occulite-accesslog.conf", lighttpdAccessLine+"\n")
	// ... and the note an older sync left beside it goes
	write(lighttpdDropinDir+"/occulite-accesslog.conf"+LighttpdRejectedSuffix, "# occulited refused this addon's lighttpd fragment: accesslog.filename\n")

	changed, results, err := SyncLighttpdDropins(root)
	must(err)
	if !changed {
		t.Fatal("nothing changed")
	}
	got := map[string]DropinResult{}
	for _, r := range results {
		got[r.ID] = r
	}
	if got["redmatic"].Action != "written" || got["hmm"].Action != "kept" || got["plain"].Action != "kept" || got["frag"].Action != "written" || got["tmpl"].Action != "template" {
		t.Fatalf("%+v", got)
	}
	if _, ok := got["occulite-accesslog"]; ok {
		t.Fatalf("occulited's own drop-in was treated as an addon's: %+v", got["occulite-accesslog"])
	}
	if b, err := os.ReadFile(filepath.Join(dropins, "occulite-accesslog.conf")); err != nil || string(b) != lighttpdAccessLine+"\n" {
		t.Fatalf("occulited's own drop-in was touched: %v %q", err, b)
	}
	if _, err := os.Lstat(filepath.Join(dropins, "occulite-accesslog.conf"+LighttpdRejectedSuffix)); err == nil {
		t.Fatal("occulited's own drop-in got a note")
	}
	if b, _ := os.ReadFile(filepath.Join(dropins, "hmm.conf")); string(b) != hmmFragment {
		t.Fatalf("hmm's rendered file was touched: %q", b)
	}
	if got["evil"].Action != "rejected" || !strings.Contains(got["evil"].Reason, "link") || got["evil2"].Action != "rejected" || !strings.Contains(got["evil2"].Reason, "include_shell") {
		t.Fatalf("%+v %+v", got["evil"], got["evil2"])
	}
	// the copies are files, not links, and carry the source line
	for _, id := range []string{"redmatic", "frag"} {
		p := filepath.Join(dropins, id+".conf")
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() {
			t.Fatalf("%s: not a regular file", id)
		}
		b, _ := os.ReadFile(p)
		if !strings.HasPrefix(string(b), "# written by occulited from ") || !strings.Contains(string(b), "addons/"+id) {
			t.Fatalf("%s: %q", id, b)
		}
	}
	for _, id := range []string{"evil", "evil2"} {
		if _, err := os.Lstat(filepath.Join(dropins, id+".conf")); err == nil {
			t.Fatalf("%s.conf still there", id)
		}
		if b, err := os.ReadFile(filepath.Join(dropins, id+".conf"+LighttpdRejectedSuffix)); err != nil || !strings.Contains(string(b), "refused") {
			t.Fatalf("%s: no note: %v %q", id, err, b)
		}
	}
	if _, err := os.Lstat(filepath.Join(dropins, "tmpl.conf"+LighttpdRejectedSuffix)); err == nil {
		t.Fatal("a template got a note")
	}
	// occulited task 23: the verdict is readable from the note, with the line, and absent for the rest
	if rej := root.LighttpdRejection("evil2"); rej == nil || rej.Line != 1 || rej.Reason != "not a directive" || !strings.HasPrefix(rej.Statement, "include_shell") {
		t.Fatalf("evil2's verdict: %+v", rej)
	}
	if rej := root.LighttpdRejection("evil"); rej == nil || rej.Line != 0 || !strings.Contains(rej.Reason, "link") {
		t.Fatalf("evil's verdict: %+v", rej)
	}
	for _, id := range []string{"redmatic", "frag", "hmm", "tmpl", "plain", "nothing"} {
		if rej := root.LighttpdRejection(id); rej != nil {
			t.Fatalf("%s has a verdict: %+v", id, rej)
		}
	}
	// occulited task 23: the note of an addon that was uninstalled - no fragment, no drop-in, no
	// tree - goes with the next sync, instead of lying there until a reinstall
	must(os.RemoveAll(root.join("/usr/local/addons/evil2")))
	if changed, _, err := SyncLighttpdDropins(root); err != nil || changed {
		t.Fatalf("after evil2's uninstall: changed=%v err=%v", changed, err)
	}
	if _, err := os.Lstat(filepath.Join(dropins, "evil2.conf"+LighttpdRejectedSuffix)); err == nil {
		t.Fatal("evil2's note stayed after its uninstall")
	}
	if root.LighttpdRejection("evil2") != nil {
		t.Fatal("evil2 still has a verdict")
	}
	// a second run changes nothing
	changed, results, err = SyncLighttpdDropins(root)
	must(err)
	if changed {
		t.Fatalf("the second run changed something: %+v", results)
	}
	// a rendered file under a tight umask: the mode is repaired, the content stands
	must(os.Chmod(filepath.Join(dropins, "hmm.conf"), 0o600))
	changed, _, err = SyncLighttpdDropins(root)
	must(err)
	if st, _ := os.Stat(filepath.Join(dropins, "hmm.conf")); !changed || st.Mode().Perm() != 0o644 {
		t.Fatalf("the mode was not repaired: changed=%v mode=%v", changed, st.Mode())
	}
	// the fragment fixed: the note goes, the copy comes
	write("/usr/local/addons/evil2/etc/lighttpd.conf", "url.redirect = ( \"^/addons/evil2$\" => \"/addons/evil2/\" )\n")
	changed, _, err = SyncLighttpdDropins(root)
	must(err)
	if !changed {
		t.Fatal("the fixed fragment was not taken")
	}
	if _, err := os.Stat(filepath.Join(dropins, "evil2.conf")); err != nil {
		t.Fatal("no copy of the fixed fragment")
	}
	if _, err := os.Lstat(filepath.Join(dropins, "evil2.conf"+LighttpdRejectedSuffix)); err == nil {
		t.Fatal("the note stayed")
	}
}

// homematic-manager task 69: the addon's fragment is the source for as long as it exists - a changed
// fragment replaces the copy and an older installer's rendering, a vanished one takes our copy with
// it, and a link out of the addon's tree is refused without being read.
func TestSyncLighttpdDropinsFollowTheFragment(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dropins := root.join(lighttpdDropinDir)
	must(os.MkdirAll(dropins, 0o755))
	write := func(rel, content string) {
		t.Helper()
		must(os.MkdirAll(filepath.Dir(root.join(rel)), 0o755))
		must(os.WriteFile(root.join(rel), []byte(content), 0o644))
	}
	read := func(id string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dropins, id+".conf"))
		if err != nil {
			return ""
		}
		return string(b)
	}
	sync := func() (bool, map[string]DropinResult) {
		t.Helper()
		changed, results, err := SyncLighttpdDropins(root)
		must(err)
		got := map[string]DropinResult{}
		for _, r := range results {
			got[r.ID] = r
		}
		return changed, got
	}
	port := func(p string) string { return strings.ReplaceAll(hmmFragment, "8090", p) }

	// hmm: the rendered fragment in its tree, and the file an older update_script rendered as root
	write("/usr/local/addons/hmm/etc/lighttpd.conf.in", port("@PORT@"))
	write("/usr/local/addons/hmm/etc/lighttpd.conf", port("8091"))
	write(lighttpdDropinDir+"/hmm.conf", port("8090"))
	// frag: a fragment and no drop-in
	write("/usr/local/addons/frag/etc/lighttpd.conf", "url.redirect = ( \"^/addons/frag$\" => \"/addons/frag/\" )\n")
	// inlink: the fragment is a link inside the tree - fine
	write("/usr/local/addons/inlink/share/lighttpd.conf", "url.redirect = ( \"^/addons/inlink$\" => \"/addons/inlink/\" )\n")
	must(os.MkdirAll(root.join("/usr/local/addons/inlink/etc"), 0o755))
	must(os.Symlink("../share/lighttpd.conf", root.join("/usr/local/addons/inlink/etc/lighttpd.conf")))
	// outlink: the fragment is a link to a root-only file, over a valid file an installer wrote
	write("/etc/shadowish", "root:SECRETHASH:19000:0:99999:7:::\n")
	must(os.MkdirAll(root.join("/usr/local/addons/outlink/etc"), 0o755))
	must(os.Symlink(root.join("/etc/shadowish"), root.join("/usr/local/addons/outlink/etc/lighttpd.conf")))
	write(lighttpdDropinDir+"/outlink.conf", "url.redirect = ( \"^/addons/outlink$\" => \"/addons/outlink/\" )\n")
	// outdir: the fragment's directory is a link out of the tree
	must(os.MkdirAll(root.join("/usr/local/addons/outdir"), 0o755))
	must(os.Symlink(root.join("/etc"), root.join("/usr/local/addons/outdir/etc")))
	write("/etc/lighttpd.conf", "url.redirect = ( \"^/addons/outdir$\" => \"/addons/outdir/\" )\n")

	changed, got := sync()
	if !changed {
		t.Fatal("nothing changed")
	}
	if got["hmm"].Action != "written" || !strings.HasPrefix(read("hmm"), lighttpdCopyPrefix) || !strings.Contains(read("hmm"), `"port" => 8091`) {
		t.Fatalf("the rendered fragment did not replace the old rendering: %+v %q", got["hmm"], read("hmm"))
	}
	if !strings.Contains(read("hmm"), "/usr/local/addons/hmm/etc/lighttpd.conf") {
		t.Fatalf("the copy does not name its source: %q", read("hmm"))
	}
	if got["frag"].Action != "written" || got["inlink"].Action != "written" {
		t.Fatalf("%+v %+v", got["frag"], got["inlink"])
	}
	for _, id := range []string{"outlink", "outdir"} {
		if got[id].Action != "rejected" || read(id) != "" {
			t.Fatalf("%s: a link out of the tree was taken: %+v %q", id, got[id], read(id))
		}
		note, _ := os.ReadFile(filepath.Join(dropins, id+".conf"+LighttpdRejectedSuffix))
		if strings.Contains(string(note), "SECRETHASH") || strings.Contains(string(note), "redirect") {
			t.Fatalf("%s: the note quotes the file behind the link: %q", id, note)
		}
	}
	if changed, got = sync(); changed {
		t.Fatalf("the second run changed something: %+v", got)
	}

	// a new port: the addon renders its fragment again, and the next sync (the reload's) takes it
	write("/usr/local/addons/hmm/etc/lighttpd.conf", port("8092"))
	changed, got = sync()
	if !changed || got["hmm"].Action != "written" || !strings.Contains(read("hmm"), `"port" => 8092`) {
		t.Fatalf("the new port was not taken: %v %+v %q", changed, got["hmm"], read("hmm"))
	}
	// an update that changes a fragment without a template
	write("/usr/local/addons/frag/etc/lighttpd.conf", "url.redirect = ( \"^/addons/frag$\" => \"/addons/frag/index.html\" )\n")
	if changed, got = sync(); !changed || got["frag"].Action != "written" || !strings.Contains(read("frag"), "index.html") {
		t.Fatalf("the changed fragment was not taken: %v %+v %q", changed, got["frag"], read("frag"))
	}
	// an older version's template beside our copy: the copy stands, validated in place
	must(os.Remove(root.join("/usr/local/addons/hmm/etc/lighttpd.conf")))
	write("/usr/local/addons/hmm/etc/lighttpd.conf", port("@PORT@"))
	if changed, got = sync(); changed || got["hmm"].Action != "kept" {
		t.Fatalf("a template beside the copy: %v %+v", changed, got["hmm"])
	}
	// uninstalled: the tree goes, and our copy with it; a file we did not write stays
	must(os.RemoveAll(root.join("/usr/local/addons/hmm")))
	must(os.RemoveAll(root.join("/usr/local/addons/frag")))
	write(lighttpdDropinDir+"/own.conf", "url.redirect = ( \"^/addons/own$\" => \"/addons/own/\" )\n")
	changed, got = sync()
	if !changed || got["hmm"].Action != "removed" || got["frag"].Action != "removed" || got["own"].Action != "kept" {
		t.Fatalf("after the uninstall: %v %+v", changed, got)
	}
	if read("hmm") != "" || read("frag") != "" || read("own") == "" {
		t.Fatalf("hmm %q frag %q own %q", read("hmm"), read("frag"), read("own"))
	}
	if changed, got = sync(); changed {
		t.Fatalf("the run after the uninstall changed something: %+v", got)
	}
}

// fragmentHelper stands in for the helper's addon-fragment read, which reads as root: it opens the
// closed tree for the moment of the read and counts the calls.
type fragmentHelper struct {
	priv.Local
	calls *int
}

func (h fragmentHelper) ReadAddonFragment(path string) ([]byte, error) {
	*h.calls++
	etc := filepath.Dir(path)
	if err := os.Chmod(etc, 0o755); err != nil {
		return nil, err
	}
	defer func() { _ = os.Chmod(etc, 0o311) }()
	if err := os.Chmod(path, 0o644); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	defer func() { _ = os.Chmod(path, 0o200) }()
	return priv.Local{}.ReadAddonFragment(path)
}

// occulited B-35: a confined addon's tree is closed to occulited's user since openccu-lite B-252 -
// its etc directory searchable only, the fragment not readable. The sync reads the fragment through
// the helper then: the copy is written, and an update that changes the fragment replaces it. An etc
// directory without a fragment is no refusal either.
func TestSyncLighttpdDropinsClosedTree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a closed tree cannot be produced")
	}
	root := Root(t.TempDir())
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	dropins := root.join(lighttpdDropinDir)
	must(os.MkdirAll(dropins, 0o755))
	etc := root.join("/usr/local/addons/hmm/etc")
	frag := filepath.Join(etc, "lighttpd.conf")
	must(os.MkdirAll(etc, 0o755))
	must(os.MkdirAll(root.join("/usr/local/addons/mosquitto/etc"), 0o755))
	write := func(content string) {
		t.Helper()
		must(os.Chmod(etc, 0o755))
		_ = os.Chmod(frag, 0o644)
		must(os.WriteFile(frag, []byte(content), 0o644))
		must(os.Chmod(frag, 0o200))
		must(os.Chmod(etc, 0o311))
	}
	t.Cleanup(func() { _ = os.Chmod(etc, 0o755); _ = os.Chmod(frag, 0o644) })
	must(os.Chmod(root.join("/usr/local/addons/mosquitto/etc"), 0o311))
	t.Cleanup(func() { _ = os.Chmod(root.join("/usr/local/addons/mosquitto/etc"), 0o755) })
	write(hmmFragment)

	sync := func() (bool, map[string]DropinResult) {
		t.Helper()
		changed, results, err := SyncLighttpdDropins(root)
		must(err)
		got := map[string]DropinResult{}
		for _, r := range results {
			got[r.ID] = r
		}
		return changed, got
	}
	read := func() string {
		b, _ := os.ReadFile(filepath.Join(dropins, "hmm.conf"))
		return string(b)
	}

	// without a helper that reads as root the closed tree is what B-35 saw: the fragment refused
	old := Priv
	Priv = priv.Local{}
	_, got := sync()
	Priv = old
	if got["hmm"].Action != "rejected" || !strings.Contains(got["hmm"].Reason, "cannot be read") {
		t.Fatalf("a closed tree without the helper: %+v", got["hmm"])
	}
	must(os.Remove(filepath.Join(dropins, "hmm.conf"+LighttpdRejectedSuffix)))

	calls := 0
	withPriv(t, fragmentHelper{calls: &calls})
	changed, got := sync()
	if !changed || got["hmm"].Action != "written" || !strings.Contains(read(), `"port" => 8090`) {
		t.Fatalf("the closed fragment was not taken: %v %+v %q", changed, got["hmm"], read())
	}
	if r, ok := got["mosquitto"]; ok {
		t.Fatalf("an etc directory without a fragment: %+v", r)
	}
	if calls != 2 {
		t.Fatalf("helper reads: %d, want 2 (hmm and mosquitto)", calls)
	}
	if _, err := os.Lstat(filepath.Join(dropins, "hmm.conf"+LighttpdRejectedSuffix)); err == nil {
		t.Fatal("a refusal note was left")
	}
	// the update that changes the fragment: the next sync takes it
	write(strings.ReplaceAll(hmmFragment, "8090", "8093"))
	changed, got = sync()
	if !changed || got["hmm"].Action != "written" || !strings.Contains(read(), `"port" => 8093`) {
		t.Fatalf("the changed fragment was not taken: %v %+v %q", changed, got["hmm"], read())
	}
	if changed, got = sync(); changed || got["hmm"].Action != "kept" {
		t.Fatalf("the second run: %v %+v", changed, got["hmm"])
	}
	// an installer's link straight to the closed fragment is read the same way and becomes a copy
	must(os.Remove(filepath.Join(dropins, "hmm.conf")))
	must(os.Symlink(frag, filepath.Join(dropins, "hmm.conf")))
	changed, got = sync()
	if st, err := os.Lstat(filepath.Join(dropins, "hmm.conf")); err != nil || !st.Mode().IsRegular() || !changed || got["hmm"].Action != "written" {
		t.Fatalf("the link to the closed fragment: %v %+v %v", changed, got["hmm"], err)
	}
}
