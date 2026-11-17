package repocheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// fuzzLine is one `go test -fuzz` command of a CI script.
type fuzzLine struct {
	where  string // the script and line, for a message
	target string // the anchored target's name
	dir    string // the package, relative to the repository's root
}

var (
	fuzzCommand = regexp.MustCompile(`^go test -run XXX -fuzz '\^(Fuzz\w+)\$' -fuzztime \w+ (\.[\w./]*)$`)
)

// fuzzLines returns the -fuzz commands of a script, each with its package's
// directory. A -fuzz line of any other form fails the test, so a changed form
// cannot hide a line from the check.
func fuzzLines(t *testing.T, script string) []fuzzLine {
	t.Helper()
	var out []fuzzLine
	for _, l := range scriptLines(t, script) {
		if !strings.Contains(l.text, "-fuzz ") {
			continue
		}
		m := fuzzCommand.FindStringSubmatch(l.text)
		if m == nil {
			t.Errorf("%s: a -fuzz line this test cannot read: %q", l.where, l.text)
			continue
		}
		out = append(out, fuzzLine{l.where, m[1], filepath.ToSlash(filepath.Join(l.dir, m[2]))})
	}
	return out
}

// fuzzTargets returns the names of the fuzz targets declared in the test files
// directly in dir.
func fuzzTargets(t *testing.T, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, name := range files {
		f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && strings.HasPrefix(fd.Name.Name, "Fuzz") {
				out = append(out, fd.Name.Name)
			}
		}
	}
	return out
}

// TestFuzzLinesNameRealTargets: go test -fuzz exits 0 when the pattern matches no
// target, so a line left behind by a moved target fuzzes nothing and CI stays
// green. Each -fuzz line of ci.yml and ci-local.sh must name a target of its
// package, the two scripts must list the same targets in the same packages, and
// every target in the repository must be listed.
func TestFuzzLinesNameRealTargets(t *testing.T) {
	t.Parallel()
	listed := map[string]string{}
	var targets []string // the first script's, sorted
	for _, script := range []string{ciWorkflow, ciLocal} {
		lines := fuzzLines(t, script)
		if len(lines) == 0 {
			t.Fatalf("%s: no -fuzz lines", script)
		}
		var names []string
		for _, l := range lines {
			names = append(names, l.target)
			found := fuzzTargets(t, filepath.Join(repoRoot(), l.dir))
			if !slices.Contains(found, l.target) {
				t.Errorf("%s: %s is not in %s, which has %v", l.where, l.target, l.dir, found)
			}
			if dir, ok := listed[l.target]; ok && dir != l.dir {
				t.Errorf("%s: %s is fuzzed in %s, and in %s by the other script", l.where, l.target, l.dir, dir)
			}
			listed[l.target] = l.dir
		}
		slices.Sort(names)
		if targets != nil && !slices.Equal(names, targets) {
			t.Errorf("%s lists %v, the other script %v", script, names, targets)
		}
		targets = names
	}
	if listed["FuzzCost"] != "contrib/graphql/veloxgql/internal/cost" {
		t.Errorf("FuzzCost is fuzzed in %q, want contrib/graphql/veloxgql/internal/cost", listed["FuzzCost"])
	}
	err := filepath.WalkDir(repoRoot(), func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && (d.Name() == "testdata" || d.Name() == "docs" || strings.HasPrefix(d.Name(), ".")):
			return filepath.SkipDir
		case !d.IsDir():
			return nil
		}
		for _, target := range fuzzTargets(t, p) {
			if _, ok := listed[target]; !ok {
				t.Errorf("%s in %s has no -fuzz line in ci.yml and ci-local.sh", target, p)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
