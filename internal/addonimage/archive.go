package addonimage

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/hobbyquaker/occulited/internal/manifest"
)

// occulited task 11: the install keeps the images a manifest declares, taken out of the package
// archive at their path from the package root - the archive's root, where openccu-lite.json lies -
// so the installed addon shows them however its update_script lays out its files. OpenCCU-Loom
// (B-39) declares www/icon.svg, and its update_script copies www/ to the CCU's addon web tree, not
// into /usr/local/addons/openccu-loom: read from the tree, the icon was not there.

// ArchivePath is the archive entry a declared path names, "" when the path is not one the archive
// may be asked for: relative, clean (no "./", "//" or trailing "/"), without a ".." segment. The
// manifest's own check already refuses most of that; this is the reader's.
func ArchivePath(p string) string {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p || p == "." {
		return ""
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return ""
		}
	}
	return p
}

// FromArchive reads the images ui declares out of a package archive (tar, gzip-compressed or not).
// It answers the image of every kind it found, and for every declared kind it did not take why: not
// in the package, not a regular file (a link is never followed), larger than MaxSize, or no image by
// its content. Two kinds may name the same file. An archive that cannot be read is an error; a
// missing image is not.
func FromArchive(r io.Reader, ui manifest.UI) (map[string][]byte, map[string]string, error) {
	want := map[string][]string{} // archive path → kinds
	why := map[string]string{}
	for _, kind := range DeclaredKinds(ui) {
		p := ArchivePath(Declared(ui, kind))
		if p == "" {
			why[kind] = fmt.Sprintf("%q is not a path inside the package", Declared(ui, kind))
			continue
		}
		want[p] = append(want[p], kind)
		why[kind] = "not in the package"
	}
	got := map[string][]byte{}
	if len(want) == 0 {
		return got, why, nil
	}
	br := bufio.NewReader(r)
	var tr *tar.Reader
	if head, _ := br.Peek(2); len(head) == 2 && head[0] == 0x1f && head[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return nil, nil, err
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(br)
	}
	left := len(want)
	for left > 0 {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		kinds, ok := want[name]
		if !ok {
			continue
		}
		left--
		delete(want, name)
		set := func(reason string) {
			for _, k := range kinds {
				why[k] = reason
			}
		}
		if h.Typeflag != tar.TypeReg {
			set("not a regular file in the package")
			continue
		}
		if h.Size > MaxSize {
			set(fmt.Sprintf("larger than %d bytes", MaxSize))
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, MaxSize+1))
		if err != nil {
			return nil, nil, err
		}
		if len(b) > MaxSize {
			set(fmt.Sprintf("larger than %d bytes", MaxSize))
			continue
		}
		if _, ok := Sniff(b); !ok {
			set("not an image (SVG, PNG, JPEG, GIF or WebP)")
			continue
		}
		for _, k := range kinds {
			got[k] = b
			delete(why, k)
		}
	}
	return got, why, nil
}

// FromArchiveFile is FromArchive on a file.
func FromArchiveFile(name string, ui manifest.UI) (map[string][]byte, map[string]string, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	return FromArchive(f, ui)
}
