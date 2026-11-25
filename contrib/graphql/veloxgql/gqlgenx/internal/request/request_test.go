package request_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx/internal/request"
)

// spell is f(0) to f(n-1), joined by a space.
func spell(n int, f func(i int) string) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(f(i))
		b.WriteByte(' ')
	}
	return b.String()
}

// nest is depth open, inner and depth shut.
func nest(depth int, open, inner, shut string) string {
	return strings.Repeat(open, depth) + inner + strings.Repeat(shut, depth)
}

// refusal is what the one check before gqlgen says of a request, "" for none. It
// calls the check and not gqlgen, so a shape that it stops refusing fails here, and
// does not allocate what it would through validation.
func refusal(t *testing.T, l veloxgql.Limits, p *graphql.RawParams) string {
	t.Helper()
	b := request.NewBounds(l, nil)
	if err := request.Check(p.OperationName, p.Query, p.Variables, b); err != nil {
		return err.Message
	}
	return ""
}

// TestDocumentShapes: all a parsed document holds that a later stage's work grows
// with is refused before validation, each under its own limit, with the document
// small enough for the body and token limits.
func TestDocumentShapes(t *testing.T) {
	long := strings.Repeat("a", 129)
	spreads := func(n int) string {
		return `{ ` + spell(n, func(i int) string { return fmt.Sprintf("...F%d", i) }) + ` }`
	}
	for _, c := range []struct {
		name, query, want string
	}{
		{"7000 spreads of no definition", spreads(7000), "501 fragments, and the most is 500"},
		{"501 spreads of no definition", spreads(501), "501 fragments, and the most is 500"},
		{"501 fragments never spread", `{ id } ` + spell(501, func(i int) string { return fmt.Sprintf("fragment F%d on T { id }", i) }), "501 fragments"},
		{"5001 spreads of one fragment", `{ ` + strings.Repeat("...F ", 5001) + ` } fragment F on T { id }`, "5001 spreads"},

		{"selections 65 deep", nest(65, "{ a ", "{ b }", "}"), "nests over 64"},
		{"inline fragments 65 deep", nest(65, "{ ... ", "{ b }", "}"), "nests over 64"},
		{"through fragments", `{ ...F0 } ` + spell(70, func(i int) string { return fmt.Sprintf("fragment F%d on T { a { ...F%d } }", i, i+1) }) + `fragment F70 on T { id }`, "nests over 64"},
		{"a value 65 deep, an object", `{ a(w: ` + nest(65, "{not:", "{}", "}") + `) }`, "nests over 64"},
		{"a value 65 deep, a list", `{ a(w: ` + nest(65, "[", "1", "]") + `) }`, "nests over 64"},
		{"a value 900 deep", `{ a(w: ` + nest(900, "{not:", "{}", "}") + `) }`, "nests over 64"},
		{"a directive's value", `{ a @d(w: ` + nest(65, "[", "1", "]") + `) }`, "nests over 64"},
		{"a variable's default", `query($v: T = ` + nest(65, "[", "1", "]") + `) { a(w: $v) }`, "nests over 64"},
		{"a variable's type", `query($v: ` + nest(65, "[", "Int", "]") + `) { a(w: $v) }`, "nests over 64"},

		{"an alias", `{ ` + long + `: id }`, "a name of 129 bytes"},
		{"a field", `{ ` + long + ` }`, "a name of 129 bytes"},
		{"an argument", `{ a(` + long + `: 1) }`, "a name of 129 bytes"},
		{"an input field", `{ a(w: {` + long + `: 1}) }`, "a name of 129 bytes"},
		{"an enum value", `{ a(w: ` + long + `) }`, "a name of 129 bytes"},
		{"a number", `{ a(w: 1` + strings.Repeat("0", 128) + `) }`, "a name of 129 bytes"},
		{"a directive", `{ a @` + long + ` }`, "a name of 129 bytes"},
		{"a fragment", `{ ...` + long + ` }`, "a name of 129 bytes"},
		{"a type condition", `{ ... on ` + long + ` { id } }`, "a name of 129 bytes"},
		{"an operation", `query ` + long + ` { id }`, "a name of 129 bytes"},
		{"a variable", `query($` + long + `: Int) { id }`, "a name of 129 bytes"},
		{"a variable's use", `{ a(w: $` + long + `) }`, "a name of 129 bytes"},
		{"a fragment's definition", `{ id } fragment ` + long + ` on T { id }`, "a name of 129 bytes"},
		{"an alias of 1 MB", `{ ` + strings.Repeat("a", 1<<20) + `: id }`, "over 1048576 bytes"},

		{"a string", `{ a(w: "` + strings.Repeat("s", 64<<10+1) + `") }`, "a string of 65537 bytes"},
		{"a block string", `{ a(w: """` + strings.Repeat("s", 64<<10+1) + `""") }`, "a string of 65537 bytes"},
		{"a string in a list", `{ a(w: ["` + strings.Repeat("s", 64<<10+1) + `"]) }`, "a string of 65537 bytes"},
	} {
		got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: c.query})
		if !strings.Contains(got, c.want) || !strings.Contains(got, "request is too large") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct{ name, query string }{
		{"selections 64 deep", nest(63, "{ a ", "{ b }", "}")},
		{"a value 64 deep", `{ a(w: ` + nest(63, "[", "1", "]") + `) }`},
		{"500 fragments", spell(1, func(int) string { return `{ id }` }) + spell(500, func(i int) string { return fmt.Sprintf("fragment F%d on T { id }", i) })},
		{"a name of 128 bytes", `{ ` + strings.Repeat("a", 128) + `: id }`},
		{"a string of 64 KB", `{ a(w: "` + strings.Repeat("s", 64<<10) + `") }`},
	} {
		if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: c.query}); got != "" {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: `{ id }`, OperationName: strings.Repeat("a", 129)}); !strings.Contains(got, "a name of 129 bytes") {
		t.Errorf("an operationName: %q", got)
	}
}

// TestDocumentPairs: pairs that differ cost what their errors do, and are few; pairs
// that agree cost little, and may be many.
func TestDocumentPairs(t *testing.T) {
	differ := func(i int) string { return fmt.Sprintf("a: f(n: %d)", i) }
	for _, c := range []struct {
		name, query, want string
	}{
		{"142 that differ", `{ ` + spell(142, differ) + ` }`, "over 10000 pairs of its fields differ"},
		{"142 that differ, by their names", `{ ` + spell(142, func(i int) string { return fmt.Sprintf("a: f%d", i) }) + ` }`, "over 10000 pairs of its fields differ"},
		{"they differ once in each of 11 fragments", `{ ` + spell(11, func(i int) string { return fmt.Sprintf("...F%d", i) }) + ` } ` + spell(11, func(i int) string {
			return fmt.Sprintf("fragment F%d on T { %s }", i, spell(13, func(j int) string { return differ(i*13 + j) }))
		}), "over 10000 pairs of its fields differ"},
		{"in 8 levels of inline fragments, compared at each", nest(8, "{ ... ", "{ "+spell(50, differ)+" }", "}"), "over 10000 pairs of its fields differ"},
		{"1500 that agree", `{ ` + strings.Repeat("a: f ", 1500) + ` }`, "over 1000000 pairs of its fields share a response name"},
	} {
		if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: c.query}); !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct{ name, query string }{
		{"141 that differ", `{ ` + spell(141, differ) + ` }`},
		{"800 that agree", `{ ` + strings.Repeat("a: f ", 800) + ` }`},
		{"with the same arguments, in any place", `{ ` + strings.Repeat("a: f(n: 1, w: {x: 2}) ", 800) + ` }`},
		{"50 fragments of 10 common fields", `{ ` + spell(50, func(i int) string { return fmt.Sprintf("...F%d", i) }) + ` } ` + spell(50, func(i int) string {
			return fmt.Sprintf("fragment F%d on T { %s }", i, spell(10, func(j int) string { return fmt.Sprintf("f%d", j) }))
		})},
	} {
		if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: c.query}); got != "" {
			t.Errorf("%s: %q", c.name, got)
		}
	}
	// Spreads count as steps: fragments spreading only each other, walked once for
	// each operation, as validation does.
	ops := spell(1000, func(i int) string { return fmt.Sprintf("query Q%d { ...F0 }", i) })
	chain := spell(300, func(i int) string { return fmt.Sprintf("fragment F%d on T { ...F%d }", i, i+1) })
	if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: ops + chain + `fragment F300 on T { id }`}); !strings.Contains(got, "its fragments spread to over 200000") {
		t.Errorf("a chain under many operations: %q", got)
	}
}

// TestVariablesShapes: a request's variables, seen before gqlgen decodes them into
// any input, are bounded by the same check: their depth, their bytes on a transport
// with no body limit, and their nodes, an object weighing what its input type does.
func TestVariablesShapes(t *testing.T) {
	deep := func(n int) any {
		var v any = map[string]any{}
		for range n {
			v = map[string]any{"not": v}
		}
		return v
	}
	list := func(n int, v any) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = v
		}
		return out
	}
	for _, c := range []struct {
		name  string
		query string
		vars  map[string]any
		want  string
	}{
		{"65 deep", `query($w: T) { a(w: $w) }`, map[string]any{"w": deep(65)}, "nest over 64"},
		{"3700 deep", `query($w: T) { a(w: $w) }`, map[string]any{"w": deep(3700)}, "nest over 64"},
		{"a list of 100001", `query($w: [Int]) { a(w: $w) }`, map[string]any{"w": list(100001, 1)}, "over 100000 nodes"},
		{"not declared", `{ id }`, map[string]any{"w": list(100001, 1)}, "over 100000 nodes"},
		{"over its bytes", `{ id }`, map[string]any{"w": strings.Repeat("s", 1<<20+1)}, "over 1048576 bytes"},
		{"over its bytes, in a key", `{ id }`, map[string]any{"w": map[string]any{strings.Repeat("s", 1<<20+1): 1}}, "over 1048576 bytes"},
		{"sent by its hash, no query", "", map[string]any{"w": list(100001, 1)}, "over 100000 nodes"},
	} {
		got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: c.query, Variables: c.vars})
		if !strings.Contains(got, c.want) || !strings.Contains(got, "request is too large") {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name string
		vars map[string]any
	}{
		{"64 deep", map[string]any{"w": deep(63)}},
		{"100000 in a list", map[string]any{"w": list(99999, 1)}},
		{"numbers, nulls and booleans", map[string]any{"a": 1, "b": nil, "c": true, "d": 1.5}},
		{"a value gqlgen gave", map[string]any{"a": graphql.Upload{}}},
	} {
		if got := refusal(t, veloxgql.DefaultLimits(), &graphql.RawParams{Query: `{ id }`, Variables: c.vars}); got != "" {
			t.Errorf("%s: %q", c.name, got)
		}
	}
}
