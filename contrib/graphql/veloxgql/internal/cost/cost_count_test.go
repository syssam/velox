package cost_test

import (
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/validator"

	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
)

// TestCostCountOrder: an order by the number of an edge's nodes, a value
// velox-graphql describes with sdl.CountOrderDoc, costs what has<Edge>With
// costs, 20 statements, from a literal and from a variable; an order by a
// column costs its size alone.
func TestCostCountOrder(t *testing.T) {
	sdlText := costSDL + `
enum OrderDirection { ASC DESC }
enum UserOrderField {
	NAME
	"""` + sdl.CountOrderDoc + `posts of each User, counting only those the reader may see."""
	POSTS_COUNT
}
input UserOrder { direction: OrderDirection! = ASC field: UserOrderField! }
extend type Query { ordered(first: Int, orderBy: UserOrder): UserConnection! }`
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: sdlText})
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
		return gqlgenx.Cost(schema, doc.Operations[0], vars, veloxgql.DefaultLimits())
	}
	byName := cost(`{ ordered(first: 0, orderBy: {field: NAME}) { totalCount } }`, nil)
	byCount := cost(`{ ordered(first: 0, orderBy: {field: POSTS_COUNT}) { totalCount } }`, nil)
	if byCount-byName != 20*stmt {
		t.Errorf("a literal: by count %d, by name %d; want 20 statements more", byCount, byName)
	}
	const q = `query ($o: UserOrder) { ordered(first: 0, orderBy: $o) { totalCount } }`
	vName := cost(q, map[string]any{"o": map[string]any{"field": "NAME"}})
	vCount := cost(q, map[string]any{"o": map[string]any{"field": "POSTS_COUNT"}})
	if vCount-vName != 20*stmt {
		t.Errorf("a variable: by count %d, by name %d; want 20 statements more", vCount, vName)
	}
}
