package request_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/request"
)

// TestCostError: CostError refuses a request over MaxCost, naming a list
// that charges its limit, and one whose introspection is over
// MaxIntrospectionCost, with a coded error of CodeTooCostly that wraps
// ErrTooCostly; a request within both, a nil schema and a nil operation
// pass.
func TestCostError(t *testing.T) {
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: `type Query { items: [Item!]! one: Item } type Item { name: String! tags: [Item!]! }`})
	op := func(q string) *ast.OperationDefinition {
		doc, err := gqlparser.LoadQueryWithRules(schema, q, nil)
		if err != nil {
			t.Fatal(err)
		}
		return doc.Operations[0]
	}
	l := veloxgql.DefaultLimits()
	l.MaxList, l.MaxCost, l.MaxIntrospectionCost = 10, 1000, 5
	for _, c := range []struct {
		query, want string
	}{
		{`{ items { tags { tags { name } } } }`, "request is too costly: it costs"},
		{`{ items { tags { tags { name } } } }`, "a list such as"},
		{`{ __schema { types { name fields { name } } } }`, "its introspection costs"},
		{`{ one { name } }`, ""},
	} {
		err := request.CostError(schema, op(c.query), nil, l)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v, want none", c.query, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || err.Code != veloxgql.CodeTooCostly || !errors.Is(err, veloxgql.ErrTooCostly)):
			t.Errorf("%s: %v, want a CodeTooCostly error holding %q", c.query, err, c.want)
		}
	}
	if request.CostError(nil, op(`{ one { name } }`), nil, l) != nil || request.CostError(schema, nil, nil, l) != nil {
		t.Error("a nil schema or operation is refused")
	}
}
