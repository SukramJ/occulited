package addonimage

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

type entry struct {
	name, body, link string
	typ              byte
}

func archive(t *testing.T, gz bool, entries ...entry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	var tw *tar.Writer
	var zw *gzip.Writer
	if gz {
		zw = gzip.NewWriter(&buf)
		tw = tar.NewWriter(zw)
	} else {
		tw = tar.NewWriter(&buf)
	}
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		h := &tar.Header{Name: e.name, Mode: 0o644, Typeflag: typ, Linkname: e.link}
		if typ == tar.TypeReg {
			h.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if zw != nil {
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return &buf
}

const svg = `<svg xmlns="http://www.w3.org/2000/svg"/>`

// occulited task 11: the declared images come out of the archive at their path from the package
// root, with or without "./", compressed or not; one file may serve two kinds; what is no regular
// file, too large or no image is not taken, and each kind not taken says why.
func TestFromArchive(t *testing.T) {
	ui := manifest.UI{Icon: "www/icon.svg", IconDark: "www/icon.svg", Logo: "www/logo.png", LogoDark: "www/dark.png"}
	for _, gz := range []bool{true, false} {
		got, why, err := FromArchive(archive(t, gz,
			entry{name: "./www/icon.svg", body: svg},
			entry{name: "www/logo.png", link: "/etc/shadow", typ: tar.TypeSymlink},
			entry{name: "www/dark.png", body: "\x89PNG\r\n\x1a\n" + strings.Repeat("x", MaxSize)},
		), ui)
		if err != nil {
			t.Fatal(err)
		}
		if string(got[KindIcon]) != svg || string(got[KindIconDark]) != svg || len(got) != 2 {
			t.Errorf("gz=%v got %v", gz, got)
		}
		if why[KindLogo] != "not a regular file in the package" || !strings.HasPrefix(why[KindLogoDark], "larger than") || len(why) != 2 {
			t.Errorf("gz=%v why %v", gz, why)
		}
	}
	got, why, err := FromArchive(archive(t, true, entry{name: "www/icon.svg", body: "<html><script>x</script></html>"}), manifest.UI{Icon: "www/icon.svg", Logo: "www/none.png"})
	if err != nil || len(got) != 0 || !strings.HasPrefix(why[KindIcon], "not an image") || why[KindLogo] != "not in the package" {
		t.Errorf("%v %v %v", got, why, err)
	}
	// nothing declared: the archive is not even read
	if got, why, err := FromArchive(strings.NewReader("not an archive"), manifest.UI{}); err != nil || len(got) != 0 || len(why) != 0 {
		t.Errorf("%v %v %v", got, why, err)
	}
	if _, _, err := FromArchive(strings.NewReader("\x1f\x8bnot gzip"), ui); err == nil {
		t.Error("a broken archive")
	}
}

// A path that is not plainly inside the package is never looked up: a traversal is refused even
// when the archive carries an entry of that name.
func TestArchivePathRefusesTraversal(t *testing.T) {
	for _, p := range []string{"", "/etc/passwd", "../x.svg", "www/../../x.svg", "www/./i.svg", "www//i.svg", "www/", ".", `www\i.svg`, "a/.."} {
		if ArchivePath(p) != "" {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"www/icon.svg", "icon.svg", "a/b/c.png", "a..b/x.svg"} {
		if ArchivePath(p) != p {
			t.Errorf("%q refused", p)
		}
	}
	got, why, err := FromArchive(archive(t, true, entry{name: "../x.svg", body: svg}), manifest.UI{Icon: "../x.svg"})
	if err != nil || len(got) != 0 || !strings.Contains(why[KindIcon], "not a path inside the package") {
		t.Errorf("%v %v %v", got, why, err)
	}
}
