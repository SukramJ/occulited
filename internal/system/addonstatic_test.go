package system

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// B-120: the static files of an addon through os.Root - a link inside the addon's tree is fine, a
// link out of it is 404, so is a dotfile, a CGI and a directory without index.html.
func TestAddonStatic(t *testing.T) {
	dir := t.TempDir()
	root := Root(dir)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	tree := root.join("/usr/local/addons/x")
	must(os.MkdirAll(filepath.Join(tree, "www", "sub"), 0o755))
	must(os.MkdirAll(filepath.Join(tree, "lib"), 0o755))
	must(os.MkdirAll(root.join(AddonWWW), 0o755))
	must(os.Symlink(filepath.Join(tree, "www"), root.join(filepath.Join(AddonWWW, "x"))))
	must(os.WriteFile(filepath.Join(tree, "www", "index.html"), []byte("<p>hi</p>"), 0o644))
	must(os.WriteFile(filepath.Join(tree, "www", "app.js"), []byte("1"), 0o644))
	must(os.WriteFile(filepath.Join(tree, "www", "sub", "index.html"), []byte("sub"), 0o644))
	must(os.WriteFile(filepath.Join(tree, "www", ".secret"), []byte("s"), 0o644))
	must(os.WriteFile(filepath.Join(tree, "www", "run.cgi"), []byte("puts x"), 0o755))
	must(os.WriteFile(filepath.Join(tree, "lib", "inside.txt"), []byte("in"), 0o644))
	must(os.MkdirAll(root.join("/etc"), 0o755))
	must(os.WriteFile(root.join("/etc/secret"), []byte("root-only"), 0o644))
	must(os.Symlink(filepath.Join(tree, "lib", "inside.txt"), filepath.Join(tree, "www", "inside.txt")))
	must(os.Symlink(root.join("/etc/secret"), filepath.Join(tree, "www", "outside.txt")))
	must(os.Symlink("../../../../../etc/secret", filepath.Join(tree, "www", "relout.txt")))
	srv := httptest.NewServer(CGIRunner{Root: root, Static: AddonStatic{Root: root}})
	t.Cleanup(srv.Close)
	cases := []struct {
		path string
		want int
		body string
		ct   string
	}{
		{"/addons/x/", 200, "<p>hi</p>", "text/html; charset=utf-8"},
		{"/addons/x/index.html", 200, "<p>hi</p>", ""},
		{"/addons/x/app.js", 200, "1", "text/javascript; charset=utf-8"},
		{"/addons/x/sub", 301, "", ""},
		{"/addons/x/sub/", 200, "sub", ""},
		{"/addons/x/inside.txt", 200, "in", ""},
		{"/addons/x/outside.txt", 404, "", ""},
		{"/addons/x/relout.txt", 404, "", ""},
		{"/addons/x/.secret", 404, "", ""},
		{"/addons/x/missing.js", 404, "", ""},
		{"/addons/x/../../etc/secret", 404, "", ""},
		{"/addons/x/run.cgi", 502, "", ""}, // the CGI path, which has no tclsh in this root - never the file itself
		{"/addons/y/index.html", 404, "", ""},
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, c := range cases {
		res, err := client.Get(srv.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		b := make([]byte, 64)
		n, _ := res.Body.Read(b)
		res.Body.Close()
		if res.StatusCode != c.want {
			t.Errorf("%s: %d, want %d", c.path, res.StatusCode, c.want)
			continue
		}
		if c.body != "" && string(b[:n]) != c.body {
			t.Errorf("%s: body %q", c.path, b[:n])
		}
		if c.ct != "" && res.Header.Get("Content-Type") != c.ct {
			t.Errorf("%s: content type %q", c.path, res.Header.Get("Content-Type"))
		}
	}
}
