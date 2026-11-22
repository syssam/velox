package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	forAgentsErr     = regexp.MustCompile(`\bErr[A-Z]\w+`)
	forAgentsBacktic = regexp.MustCompile("`([^`]+)`")
	forAgentsTest    = regexp.MustCompile("`(Test\\w+)`")
)

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestForAgentsDoc: docs/for-agents.md names only errors the root package
// exports and only sections docs/guide.md has, each of its rules cites a
// test that exists, and skills/velox/SKILL.md carries its directory's name
// and points at files that exist.
func TestForAgentsDoc(t *testing.T) {
	t.Parallel()
	doc := readRepoFile(t, "docs/for-agents.md")
	api := readRepoFile(t, "internal/repocheck/testdata/api.txt")
	guide := readRepoFile(t, "docs/guide.md")

	errs := forAgentsErr.FindAllString(doc, -1)
	if len(errs) < 3 {
		t.Fatalf("docs/for-agents.md names %d errors, want at least 3", len(errs))
	}
	for _, name := range errs {
		if !strings.Contains(api, "\nvar "+name+"\n") {
			t.Errorf("docs/for-agents.md names %s, which the public API does not export", name)
		}
	}

	_, para, ok := strings.Cut(doc, "Its sections are")
	if !ok {
		t.Fatal("docs/for-agents.md does not list the guide's sections")
	}
	para, _, _ = strings.Cut(para, "Open the one")
	para = strings.Join(strings.Fields(para), " ")
	sections := forAgentsBacktic.FindAllStringSubmatch(para, -1)
	if len(sections) < 5 {
		t.Fatalf("docs/for-agents.md lists %d guide sections, want at least 5", len(sections))
	}
	for _, m := range sections {
		if !strings.Contains(guide, "\n## "+m[1]+"\n") {
			t.Errorf("docs/for-agents.md lists the guide section %q, which docs/guide.md lacks", m[1])
		}
	}

	checkForAgentsRules(t, doc)

	skill := readRepoFile(t, "skills/velox/SKILL.md")
	if !strings.HasPrefix(skill, "---\nname: velox\ndescription: ") {
		t.Error("skills/velox/SKILL.md must open with the name velox, then a description")
	}
	for _, rel := range []string{"docs/for-agents.md", "docs/guide.md", "docs/coming-from-ent.md"} {
		if !strings.Contains(skill, "$VELOX/"+rel) {
			t.Errorf("skills/velox/SKILL.md does not point at %s", rel)
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRoot(), rel)); err != nil {
			t.Error(err)
		}
	}
}

// checkForAgentsRules fails for a rule of docs/for-agents.md that cites no
// test, and for a cited test that no _test.go file of the repository
// declares.
func checkForAgentsRules(t *testing.T, doc string) {
	t.Helper()
	_, rules, ok := strings.Cut(doc, "\n## Rules that differ from intuition\n")
	if !ok {
		t.Fatal("docs/for-agents.md has no rules section")
	}
	rules, _, _ = strings.Cut(rules, "\n## ")
	var bullets []string
	for line := range strings.SplitSeq(rules, "\n") {
		switch {
		case strings.HasPrefix(line, "- "):
			bullets = append(bullets, line)
		case strings.HasPrefix(line, "  ") && len(bullets) > 0:
			bullets[len(bullets)-1] += " " + strings.TrimSpace(line)
		}
	}
	if len(bullets) < 5 {
		t.Fatalf("docs/for-agents.md has %d rules, want at least 5", len(bullets))
	}
	declared := testFuncs(t)
	for _, b := range bullets {
		cited := forAgentsTest.FindAllStringSubmatch(b, -1)
		if len(cited) == 0 {
			t.Errorf("docs/for-agents.md: a rule cites no test: %s", b)
		}
		for _, m := range cited {
			if !declared[m[1]] {
				t.Errorf("docs/for-agents.md cites %s, which no _test.go file declares", m[1])
			}
		}
	}
}

var testFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

// testFuncs returns the names of the test functions the repository's
// _test.go files declare, outside dot directories (agents' worktrees).
func testFuncs(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	err := filepath.WalkDir(repoRoot(), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != repoRoot() {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range testFunc.FindAllSubmatch(data, -1) {
			out[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
