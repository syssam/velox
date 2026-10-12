package ormgen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	src := header + `// Package p is documented once.
package p

import (
	_ "example.com/linked"
	orm "example.com/orm"
	fmt "fmt"
	strings "strings"
	time "time"
)

// Shared is in the common file.
var Shared = strings.ToUpper("a")

` + fileMarker + `user

// User mentions fmt.Println in its comment and "time.Now" in a string.
type User struct{ At time.Time }

// Name reads the field orm of its argument, which is not the package.
func (u *User) Name(x struct{ orm int }) string { return "time.Now" + string(rune(x.orm)) }

` + fileMarker + `post

type Post struct{}

func (*Post) String() string { return fmt.Sprint(orm.Version) }

` + fileMarker + `user

func init() { _ = orm.Version }

` + fileMarker + `

// Late is after an empty marker: the common file again.
const Late = 1
`
	files, err := split([]byte(src), "p")
	if err != nil {
		t.Fatal(err)
	}
	// The imports of a file are the standard library's, then the rest.
	want := map[string]struct {
		Imports  []string
		contains []string
		absent   []string
	}{
		"p.go":    {[]string{"strings", "example.com/linked"}, []string{"// Package p is documented once.", "var Shared", "const Late"}, []string{"type User", "type Post"}},
		"user.go": {[]string{"time", "example.com/orm"}, []string{"type User struct", "func init()", "// User mentions fmt.Println"}, []string{"Package p is documented", "type Post"}},
		"post.go": {[]string{"fmt", "example.com/orm"}, []string{"type Post struct", "func (*Post) String()"}, []string{"type User", "Shared"}},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d", len(files), len(want))
	}
	for name, w := range want {
		got, ok := files[name]
		if !ok {
			t.Fatalf("no file %s", name)
		}
		if !strings.HasPrefix(string(got), header) {
			t.Errorf("%s does not start with the generated-code header", name)
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, got, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v\n%s", name, err, got)
		}
		if f.Name.Name != "p" {
			t.Errorf("%s is in package %s", name, f.Name.Name)
		}
		var Imports []string
		for _, s := range f.Imports {
			Imports = append(Imports, strings.Trim(s.Path.Value, `"`))
		}
		if strings.Join(Imports, " ") != strings.Join(w.Imports, " ") {
			t.Errorf("%s imports %v, want %v", name, Imports, w.Imports)
		}
		for _, s := range w.contains {
			if !strings.Contains(string(got), s) {
				t.Errorf("%s lacks %q", name, s)
			}
		}
		for _, s := range append(w.absent, fileMarker) {
			if strings.Contains(string(got), s) {
				t.Errorf("%s has %q", name, s)
			}
		}
	}
}

// A file name must not be one the go command reads a build constraint from.
func TestFileName(t *testing.T) {
	for pkg, want := range map[string]string{"user": "user", "linux": "linux", "user_linux": "user_linux_gen", "note_test": "note_test_gen"} {
		if got := fileName(&Entity{Pkg: pkg}); got != want {
			t.Errorf("fileName(%q) = %q, want %q", pkg, got, want)
		}
	}
}
