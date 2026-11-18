package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	forAgentsErr     = regexp.MustCompile(`\bErr[A-Z]\w+`)
	forAgentsBacktic = regexp.MustCompile("`([^`]+)`")
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
// exports and only sections docs/guide.md has, and skills/velox/SKILL.md
// carries its directory's name and points at files that exist.
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
