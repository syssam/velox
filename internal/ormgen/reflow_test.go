package ormgen

import (
	"strings"
	"testing"
)

// A doc comment is wrapped once the names of the schema are in it, and what
// is not running text is left as written.
func TestReflow(t *testing.T) {
	long := "// ApprovedByEmployeeOrErr returns the approved_by_employee edge: an error when it was not loaded (velox.IsNotLoaded), velox.ErrNotFound when it was loaded and is empty.\n"
	src := header + "package p\n\n" + long + "//\n//go:noinline\nfunc f() {}\n\n" +
		"// Short stays as written,\n// on its two lines.\nfunc g() {}\n\n" +
		"// A table is not text, however wide, and neither is what follows a paragraph that is itself too long to fit on the line it was written on:\n" +
		"//\n//\tEQ, NEQ, In, NotIn, GT, GTE, LT, LTE  a condition on the column's value; In of no values matches no row at all\n" +
		"//\n// - a list item that goes on for longer than the width of a comment would otherwise allow it to\n// 1. and a numbered one that goes on for longer than the width of a comment would otherwise allow\nfunc h() {\n\t// A comment inside a function is indented by gofmt and is not a doc comment, whatever its length is here.\n}\n"
	got := string(reflow([]byte(src)))
	for i, line := range strings.Split(got, "\n") {
		prose := strings.HasPrefix(line, "// ") && !strings.HasPrefix(line, "// -") && !strings.HasPrefix(line, "// 1.")
		if prose && len(line) > docWidth {
			t.Errorf("line %d is %d wide: %s", i+1, len(line), line)
		}
	}
	for _, want := range []string{
		header,
		"// ApprovedByEmployeeOrErr returns the approved_by_employee edge: an error when\n// it was not loaded (velox.IsNotLoaded), velox.ErrNotFound when it was loaded\n// and is empty.\n//\n//go:noinline\nfunc f() {}\n",
		"// Short stays as written,\n// on its two lines.\n",
		"//\tEQ, NEQ, In, NotIn, GT, GTE, LT, LTE  a condition on the column's value; In of no values matches no row at all\n",
		"// - a list item that goes on for longer than the width of a comment would otherwise allow it to\n",
		"// 1. and a numbered one that goes on for longer than the width of a comment would otherwise allow\n",
		"\t// A comment inside a function is indented by gofmt and is not a doc comment, whatever its length is here.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the result does not have:\n%s\ngot:\n%s", want, got)
		}
	}
	// Wrapping changes where lines break and nothing else.
	if a, b := strings.Join(strings.Fields(strings.ReplaceAll(src, "//", " ")), " "), strings.Join(strings.Fields(strings.ReplaceAll(got, "//", " ")), " "); a != b {
		t.Errorf("the words changed:\n%s\n%s", a, b)
	}
	// What fits is returned as it was.
	if again := string(reflow([]byte(got))); again != got {
		t.Errorf("a second pass changed the text")
	}
}
