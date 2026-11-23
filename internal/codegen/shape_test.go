package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// shapeFile records GeneratedCodeVersion with the shape of the golden
// generated code it was set for.
var shapeFile = filepath.Join("testdata", "golden", "shape.txt")

// versionGuard matches the guard every generated file holds, whose number
// is the version, not the shape.
var versionGuard = regexp.MustCompile(`GeneratedCodeIsVersion\d+`)

// blankLines matches the line breaks of one or more blank lines.
var blankLines = regexp.MustCompile(`\n(?:[ \t]*\n)+`)

// spaces matches a run of spaces and tabs.
var spaces = regexp.MustCompile(`[ \t]+`)

// shapeHash is the SHA-256 of the Go files of files (slash path → source),
// in path order, each without its comments, printed by go/printer without
// blank lines and with each run of spaces as one, with the version guard's
// number taken out: a change to a comment or to the spacing does not change
// it, nor does the alignment gofmt gives fields beside a comment; a change to
// the code does.
func shapeHash(files map[string][]byte) (string, error) {
	h := sha256.New()
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for n := range files {
			if strings.HasSuffix(n, ".go") && !yield(n) {
				return
			}
		}
	}) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, files[name], parser.SkipObjectResolution)
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		if err := (&printer.Config{Mode: printer.UseSpaces | printer.TabIndent, Tabwidth: 8}).Fprint(&b, fset, f); err != nil {
			return "", err
		}
		// A comment's line, gone, leaves a blank line, which says nothing.
		code := spaces.ReplaceAll(blankLines.ReplaceAll(b.Bytes(), []byte("\n")), []byte(" "))
		_, _ = fmt.Fprintf(h, "%s\n%s\n", name, versionGuard.ReplaceAll(code, []byte("GeneratedCodeIsVersionN")))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// goldenWant reads the want/ trees of testdata/golden, by slash path below
// testdata/golden.
func goldenWant(t *testing.T) map[string][]byte {
	t.Helper()
	root := filepath.Join("testdata", "golden")
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) < 3 || parts[1] != "want" {
			return nil
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// checkShape fails when the golden generated code changed shape and
// GeneratedCodeVersion did not: a change to the generated code's shape
// bumps the version. -update records the new shape, but only with a new
// version.
func checkShape(t *testing.T) {
	t.Helper()
	got, err := shapeHash(goldenWant(t))
	if err != nil {
		t.Fatal(err)
	}
	var version int
	var hash string
	if b, err := os.ReadFile(shapeFile); err == nil {
		_, _ = fmt.Sscanf(string(b), "version %d\nsha256 %s\n", &version, &hash)
	}
	bump := fmt.Sprintf("the generated code changed shape (%s records version %d, sha256 %s; it is %s now): replace GeneratedCodeVersion %d with %d, add a CHANGELOG.md line, regenerate, and run go test ./internal/codegen -update",
		shapeFile, version, hash, got, GeneratedCodeVersion, GeneratedCodeVersion+1)
	switch {
	case version == GeneratedCodeVersion && hash == got:
	case version == GeneratedCodeVersion:
		t.Error(bump)
	case *update:
		rec := "version " + strconv.Itoa(GeneratedCodeVersion) + "\nsha256 " + got + "\n"
		if err := os.WriteFile(shapeFile, []byte(rec), 0o644); err != nil {
			t.Fatal(err)
		}
	default:
		t.Errorf("%s records version %d, and GeneratedCodeVersion is %d: run go test ./internal/codegen -update", shapeFile, version, GeneratedCodeVersion)
	}
}

// The shape hash ignores comments and layout, and sees code.
func TestShapeHash(t *testing.T) {
	base := map[string][]byte{
		"a/want/x.go":  []byte("package x\n\nconst _ = velox.GeneratedCodeIsVersion3\n\nfunc F() int {\n\tx := 1\n\treturn x\n}\n"),
		"a/want/y.txt": []byte("not Go"),
	}
	h0, err := shapeHash(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, src string
		same      bool
	}{
		{"comments", "// Package x is x.\npackage x\n\nconst _ = velox.GeneratedCodeIsVersion3\n\n// F is one.\n// It returns 1.\nfunc F() int {\n\tx := 1\n\t// x is one.\n\treturn x // one\n}\n", true},
		{"blank lines", "package x\n\n\nconst _ = velox.GeneratedCodeIsVersion3\n\nfunc F() int {\n\tx := 1\n\n\treturn x\n}\n", true},
		{"the version", "package x\n\nconst _ = velox.GeneratedCodeIsVersion4\n\nfunc F() int {\n\tx := 1\n\treturn x\n}\n", true},
		{"the code", "package x\n\nconst _ = velox.GeneratedCodeIsVersion3\n\nfunc F() int {\n\tx := 2\n\treturn x\n}\n", false},
	} {
		h, err := shapeHash(map[string][]byte{"a/want/x.go": []byte(c.src)})
		if err != nil {
			t.Fatal(err)
		}
		if (h == h0) != c.same {
			t.Errorf("%s: same hash %v, want %v", c.name, h == h0, c.same)
		}
	}
	// gofmt aligns the fields on either side of a comment's line apart.
	aligned := "package x\n\ntype T struct {\n\tA  int\n\tBB int\n}\n"
	apart := "package x\n\ntype T struct {\n\tA int\n\t// BB is two.\n\tBB int\n}\n"
	ha, err := shapeHash(map[string][]byte{"a/want/x.go": []byte(aligned)})
	if err != nil {
		t.Fatal(err)
	}
	hb, err := shapeHash(map[string][]byte{"a/want/x.go": []byte(apart)})
	if err != nil {
		t.Fatal(err)
	}
	if ha != hb {
		t.Error("alignment: the hash changed with the fields' alignment")
	}
}

// TestGeneratedCodeShape checks the golden generated code's shape against
// GeneratedCodeVersion. TestGolden checks it again after -update rewrites
// want/.
func TestGeneratedCodeShape(t *testing.T) {
	if *update {
		t.Skip("TestGolden records the shape after it writes want/")
	}
	checkShape(t)
}
