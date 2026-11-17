package cost

import (
	"encoding/json"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"
)

// argSDL is a connection whose first is read from literals and variables.
const argSDL = `
type PageInfo { hasNextPage: Boolean! }
type User { id: ID! }
type UserEdge { node: User! cursor: String! }
type UserConnection { edges: [UserEdge!]! pageInfo: PageInfo! totalCount: Int! }
type Query { users(first: Int): UserConnection! }
type Subscription { users(first: Int): UserConnection! }
`

// raw is the first field of q, parsed and not validated.
func raw(t *testing.T, q string) *ast.Field {
	t.Helper()
	doc, err := parser.ParseQuery(&ast.Source{Input: q})
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return doc.Operations[0].SelectionSet[0].(*ast.Field)
}

// TestIntArg: first is read from a literal, past int64 too, and from a
// variable of every type gqlgen decodes one to; what is not an integer
// counts as no first.
func TestIntArg(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: argSDL})
	parse := func(q string) *ast.Field {
		t.Helper()
		doc, err := gqlparser.LoadQueryWithRules(schema, q, nil)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return doc.Operations[0].SelectionSet[0].(*ast.Field)
	}
	byVar := parse(`query ($n: Int) { users(first: $n) { totalCount } }`)
	for _, c := range []struct {
		name string
		f    *ast.Field
		vars map[string]any
		n    int64
		ok   bool
	}{
		{"a literal", parse(`{ users(first: 7) { totalCount } }`), nil, 7, true},
		{"over int64", raw(t, `{ users(first: 99999999999999999999) { totalCount } }`), nil, maxCost, true},
		{"below int64", raw(t, `{ users(first: -99999999999999999999) { totalCount } }`), nil, 0, true},
		{"not a number", raw(t, `{ users(first: "x") { totalCount } }`), nil, 0, false},
		{"no first", parse(`{ users { totalCount } }`), nil, 0, false},
		{"int", byVar, map[string]any{"n": 3}, 3, true},
		{"int32", byVar, map[string]any{"n": int32(4)}, 4, true},
		{"int64", byVar, map[string]any{"n": int64(5)}, 5, true},
		{"negative", byVar, map[string]any{"n": -5}, 0, true},
		{"float64", byVar, map[string]any{"n": 6.0}, 6, true},
		{"float64 past the most", byVar, map[string]any{"n": 1e300}, maxCost, true},
		{"json.Number", byVar, map[string]any{"n": json.Number("8")}, 8, true},
		{"json.Number past int64", byVar, map[string]any{"n": json.Number("99999999999999999999")}, maxCost, true},
		{"json.Number not an integer", byVar, map[string]any{"n": json.Number("1.5")}, 0, false},
		{"a string", byVar, map[string]any{"n": "9"}, 0, false},
		{"null", byVar, map[string]any{"n": nil}, 0, false},
	} {
		c0 := &coster{schema: schema, vars: c.vars}
		if n, ok := c0.intArg(c.f, "first"); n != c.n || ok != c.ok {
			t.Errorf("%s: %d %v, want %d %v", c.name, n, ok, c.n, c.ok)
		}
	}
}

// TestOfRoots: no schema or no operation costs nothing, and a subscription
// is costed on its own root.
func TestOfRoots(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: argSDL})
	if data, meta, list := Of(nil, nil, nil, Limits{}); data != 0 || meta != 0 || list != "" {
		t.Errorf("nothing: %d %d %q", data, meta, list)
	}
	doc, err := gqlparser.LoadQueryWithRules(schema, `subscription { users(first: 2) { totalCount } }`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _, _ := Of(schema, doc.Operations[0], nil, Limits{MaxPage: 10}); data != 2*(statementCost+1) {
		t.Errorf("a subscription costs %d, want %d", data, 2*(statementCost+1))
	}
}

// TestIntrospectionType: __type costs its type's selection; a missing, null
// or unknown name costs 1, and a variable not given the sum over every type.
func TestIntrospectionType(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: argSDL})
	meta := func(q string, vars map[string]any) int64 {
		t.Helper()
		doc, err := gqlparser.LoadQueryWithRules(schema, q, nil)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		_, m, _ := Of(schema, doc.Operations[0], vars, Limits{MaxIntrospectionCost: 1 << 20})
		return m
	}
	one := meta(`{ __type(name: "User") { fields { name } } }`, nil)
	if one <= 1 {
		t.Errorf("__type(User) costs %d", one)
	}
	if got := meta(`{ __type(name: "Nope") { name } }`, nil); got != 1 {
		t.Errorf("an unknown type costs %d, want 1", got)
	}
	if got := meta(`query ($n: String!) { __type(name: $n) { name } }`, map[string]any{"n": nil}); got != 1 {
		t.Errorf("a null name costs %d, want 1", got)
	}
	// A name no String holds, which validation refuses: the sum over the
	// types bounds any one of them.
	f := raw(t, `{ __type(name: 99999999999999999999) { fields { name } } }`)
	c := &coster{schema: schema, l: Limits{MaxIntrospectionCost: 1 << 20}}
	if got := c.introspection(f); got <= one {
		t.Errorf("a name no String holds costs %d, want more than one type's %d", got, one)
	}
}
