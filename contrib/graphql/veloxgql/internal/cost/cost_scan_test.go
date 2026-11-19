package cost_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
)

// TestCostScans: a where predicate no index serves, Contains, HasPrefix and
// HasSuffix of a <T>WhereInput, and a has<Edge>With, which runs a subquery for
// each row, add 2000 each to the statements part, so that 700 aliases of
// items(first: 1, where: {nameContains: "x"}), which took 12.5 s over 90000 rows,
// are refused at the default MaxCost, and a query with a few of them passes.
func TestCostScans(t *testing.T) {
	const scan = 2000
	const sdl = costSDL + `
input UserWhereInput {
	not: UserWhereInput and: [UserWhereInput!] or: [UserWhereInput!]
	name: String nameContains: String nameHasPrefix: String nameHasSuffix: String nameEqualFold: String nameContainsFold: String
	tagsHas: String tagsHasSome: [String!] tagsHasEvery: [String!] tagsIsEmpty: Boolean
	hasPosts: Boolean hasPostsWith: [PostWhereInput!]
}
input PostWhereInput { title: String titleContains: String }
input Plain { nameContains: String }
extend type Query { filtered(first: Int, where: UserWhereInput): UserConnection! plain(first: Int, where: Plain): UserConnection! }`
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: sdl})
	l := veloxgql.DefaultLimits()
	cost := func(query string, vars map[string]any) int {
		t.Helper()
		doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
		if len(errs) > 0 {
			t.Fatalf("%s: %v", query, errs)
		}
		if vars != nil {
			var err error
			if vars, err = validator.VariableValues(schema, doc.Operations[0], vars); err != nil {
				t.Fatal(err)
			}
		}
		return veloxgql.Cost(schema, doc.Operations[0], vars, l)
	}
	const count = stmt + 1 + 1 + stmt // filtered(first: 0) { totalCount }
	for _, c := range []struct {
		name, where string
		want        int
	}{
		{"an equality", `{name: "x"}`, count + 2},
		{"contains", `{nameContains: "x"}`, count + 2 + scan},
		{"a prefix", `{nameHasPrefix: "x"}`, count + 2 + scan},
		{"a suffix", `{nameHasSuffix: "x"}`, count + 2 + scan},
		{"equal fold", `{nameEqualFold: "x"}`, count + 2 + scan},
		{"contains fold", `{nameContainsFold: "x"}`, count + 2 + scan},
		{"array has", `{tagsHas: "x"}`, count + 2 + scan},
		{"array has some", `{tagsHasSome: ["x", "y"]}`, count + 3 + scan}, // the second value adds one, as an In list does
		{"array has every", `{tagsHasEvery: ["x"]}`, count + 2 + scan},
		{"array is empty", `{tagsIsEmpty: true}`, count + 2 + scan},
		{"two", `{or: [{nameContains: "x"}, {nameHasSuffix: "y"}]}`, count + 1 + 2*(1+1+scan)},
		{"a null", `{nameContains: null}`, count + 2},
		{"has", `{hasPosts: true}`, count + 2},
		{"has with", `{hasPostsWith: [{title: "x"}]}`, count + 1 + scan + 2},
		{"has with, empty", `{hasPostsWith: []}`, count + 1},
		{"has with contains", `{not: {hasPostsWith: [{titleContains: "x"}]}}`, count + 1 + 1 + scan + 1 + 1 + scan},
	} {
		if got := cost(`{ filtered(first: 0, where: `+c.where+`) { totalCount } }`, nil); got != c.want {
			t.Errorf("%s: cost %d, want %d", c.name, got, c.want)
		}
	}
	// The same from a variable.
	w := map[string]any{"or": []any{map[string]any{"nameContains": "x"}, map[string]any{"hasPostsWith": []any{map[string]any{"titleContains": "y"}}}}}
	if got, want := cost(`query ($w: UserWhereInput) { filtered(first: 0, where: $w) { totalCount } }`, map[string]any{"w": w}), count+1+(1+1+scan)+(1+scan+(1+1+scan)); got != want {
		t.Errorf("a variable: cost %d, want %d", got, want)
	}
	// Only a where input's predicates are scans.
	if got, want := cost(`{ plain(first: 0, where: {nameContains: "x"}) { totalCount } }`, nil), count+2; got != want {
		t.Errorf("another input: cost %d, want %d", got, want)
	}
	// The flood is refused, and a page with a few such filters passes.
	var flood strings.Builder
	for i := range 700 {
		fmt.Fprintf(&flood, "a%d: filtered(first: 1, where: {nameContains: \"x\"}) { edges { node { id } } } ", i)
	}
	if got := cost(`{ `+flood.String()+` }`, nil); got <= l.MaxCost {
		t.Errorf("700 scans cost %d, under MaxCost %d", got, l.MaxCost)
	}
	if got := cost(`{ filtered(first: 100, where: {or: [{nameContains: "a"}, {nameHasSuffix: "b"}, {hasPostsWith: [{titleContains: "c"}]}]}) { edges { node { name posts(first: 10) { edges { node { title } } } } } } }`, nil); got > l.MaxCost/10 {
		t.Errorf("an ordinary search costs %d, over a tenth of MaxCost %d", got, l.MaxCost)
	}
}
