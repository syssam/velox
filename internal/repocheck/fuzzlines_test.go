package repocheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
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
	fuzzCommand = regexp.MustCompile(`^(?:run: )?go test -run XXX -fuzz '\^(Fuzz\w+)\$' -fuzztime \w+ (\.[\w./]*)$`)
	workingDir  = regexp.MustCompile(`^working-directory: ([\w./-]+)$`)
	changeDir   = regexp.MustCompile(`^cd ([\w./-]+)$`)
)

// fuzzLines returns the -fuzz commands of a script, each with its package's
// directory: CI's steps name theirs with working-directory, ci-local.sh's
// subshells with cd. A -fuzz line of any other form fails the test, so a changed
// form cannot hide a line from the check.
func fuzzLines(t *testing.T, script string) []fuzzLine {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(), script))
	if err != nil {
		t.Fatal(err)
	}
	var out []fuzzLine
	wd := "."
	for i, raw := range strings.Split(string(data), "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "- name:") || strings.HasPrefix(l, "- run:") || raw == ")" {
			wd = "." // a new step, or the end of a subshell
		}
		if m := workingDir.FindStringSubmatch(l); m != nil {
			wd = m[1]
		}
		if m := changeDir.FindStringSubmatch(l); m != nil {
			wd = m[1]
		}
		if !strings.Contains(l, "-fuzz ") {
			continue
		}
		where := fmt.Sprintf("%s:%d", script, i+1)
		m := fuzzCommand.FindStringSubmatch(l)
		if m == nil {
			t.Errorf("%s: a -fuzz line this test cannot read: %q", where, l)
			continue
		}
		out = append(out, fuzzLine{where, m[1], filepath.ToSlash(filepath.Join(wd, m[2]))})
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
	listed := map[string]string{}
	var targets []string // the first script's, sorted
	for _, script := range []string{".github/workflows/ci.yml", "scripts/ci-local.sh"} {
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
	if listed["FuzzCost"] != "contrib/graphql/veloxgql" {
		t.Errorf("FuzzCost is fuzzed in %q, want contrib/graphql/veloxgql", listed["FuzzCost"])
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
