// Package internal_test holds the guard that keeps roadmap ids out of what a user reads.
//
// Task, bug, decision and open-question ids (task 17, B-47, D-36, OQ-3, A-2) are how the code finds
// its history, and they belong in comments. A user who sees "(D-36)" in a help text, an error, a log
// line or a generated file has no way to look it up (task 52). Comments are not string literals, so
// the walk below leaves them alone; test files are skipped because their strings are expectations,
// not something anybody reads on a box.
package internal_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// roadmapID is the same pattern the UI's own guard applies to its texts.
var roadmapID = regexp.MustCompile(`\b(D|B|OQ|A)-[0-9]+\b|\b[Tt]ask [0-9]+`)

// TestNoRoadmapIDsInStringLiterals walks internal/ and cmd/ with go/parser and fails on every string
// literal that carries a roadmap id; the message names file:line so the fix is a one-liner.
func TestNoRoadmapIDsInStringLiterals(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	files := 0
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				// the embedded UI build and anything a package manager leaves behind hold no Go
				if name := d.Name(); name == "dist" || name == "node_modules" || name == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			files++
			offenders = append(offenders, roadmapIDsIn(t, root, path)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if files == 0 {
		t.Fatal("no Go files found; the walk starts from the wrong directory")
	}
	if len(offenders) > 0 {
		t.Errorf("roadmap ids in string literals (a user reads these; keep the id in a comment):\n  %s", strings.Join(offenders, "\n  "))
	}
}

// roadmapIDsIn parses one file without comments and returns "file:line: literal" for every string
// literal that matches. Raw and interpreted strings both go through strconv.Unquote so an escape
// cannot hide an id.
func roadmapIDsIn(t *testing.T, root, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			value = lit.Value
		}
		if m := roadmapID.FindString(value); m != "" {
			rel, _ := filepath.Rel(root, path)
			out = append(out, rel+":"+strconv.Itoa(fset.Position(lit.Pos()).Line)+": "+strconv.Quote(value)+" ("+m+")")
		}
		return true
	})
	return out
}

// TestNoRoadmapIDsInDeployedUnitFiles covers the files the image installs from deploy/systemd: the
// Services page's unit editor shows them through systemctl cat, comments included, so a whole file
// is text a user reads. Every line is checked, not only string literals.
func TestNoRoadmapIDsInDeployedUnitFiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "deploy", "systemd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	files := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files++
		for i, line := range strings.Split(string(data), "\n") {
			if m := roadmapID.FindString(line); m != "" {
				offenders = append(offenders, "deploy/systemd/"+e.Name()+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line)+" ("+m+")")
			}
		}
	}
	if files == 0 {
		t.Fatal("no files in deploy/systemd; the walk starts from the wrong directory")
	}
	if len(offenders) > 0 {
		t.Errorf("roadmap ids in installed unit files (systemctl cat shows them to a user):\n  %s", strings.Join(offenders, "\n  "))
	}
}

// The guard must not be fooled by a working directory other than the package's: go test runs each
// package in its own directory, and that is what ".." relies on.
func TestGuardRunsFromItsPackageDirectory(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(wd) != "internal" {
		t.Fatalf("expected to run in internal/, got %s", wd)
	}
}
