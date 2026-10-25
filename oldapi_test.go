package velox

import (
	"os/exec"
	"regexp"
	"testing"
)

// TestOldAPIIsGone: the struct-scanning path is gone. None of its exported names is declared any more, and the
// only way to give an entity metadata is a Desc (MetaOnce.Load). The check
// reads the package documentation, which lists every exported declaration at
// the start of a line.
func TestOldAPIIsGone(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go doc; run without -short")
	}
	cmd := exec.Command("go", "doc", "-all", ".")
	cmd.Dir = repoRoot()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go doc -all: %v", err)
	}
	doc := string(out)
	// Not vacuous: the new API is listed.
	for _, kept := range []string{`(?m)^type Desc\[E any\] struct`, `(?m)^func \(o \*MetaOnce\[E\]\) Load\[`, `(?m)^const GeneratedCodeIsVersion6 `, `(?m)^func EdgeTo\[`, `(?m)^func \(m \*Mutation\[E\]\) Conds\[`} {
		if !regexp.MustCompile(kept).MatchString(doc) {
			t.Errorf("go doc does not list %s: the check reads nothing", kept)
		}
	}
	for _, old := range []string{
		`^type Schema\b`,
		`^type Schemer\b`,
		`^type Mixer\b`,
		`^type ColumnDef\b`,
		`^type RelDef\b`,
		`^type Shape\b`,
		`^func RelationTo\b`,
		`^func \(o \*MetaOnce\[E\]\) Get\b`,
		`^const GeneratedCodeIsVersion2\b`,
		`^const GeneratedCodeIsVersion4\b`,
		`^const GeneratedCodeIsVersion5\b`,
		`^type Relation\b`,
		`^func AllTenants\b`,
		`^func SkipPolicy\b`,
		`^func ReadPrimary\b`,
		`^func OnShard\b`,
		`^func RowScopeOf\b`,
		`^func \(m \*Mutation\[E\]\) Value\[`,
		`^func \(m \*Mutation\[E\]\) Result\[`,
	} {
		if m := regexp.MustCompile(`(?m)` + old).FindString(doc); m != "" {
			t.Errorf("the old API is still declared: %q", m)
		}
	}
}
