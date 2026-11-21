package request_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx/internal/request"
)

// TestCheckLimits names the first negative limit, the Timeout included, and
// passes the defaults.
func TestCheckLimits(t *testing.T) {
	if err := request.CheckLimits("x", veloxgql.DefaultLimits()); err != nil {
		t.Errorf("the defaults: %v", err)
	}
	for _, c := range []struct {
		l    veloxgql.Limits
		want string
	}{
		{veloxgql.Limits{MaxPage: -1}, "x: Limits.MaxPage is -1, and a limit must not be negative (zero is the default)"},
		{veloxgql.Limits{MaxResponseBytes: -2}, "x: Limits.MaxResponseBytes is -2"},
		{veloxgql.Limits{Timeout: -time.Second}, "x: Limits.Timeout is -1s"},
	} {
		if err := request.CheckLimits("x", c.l); err == nil || !strings.HasPrefix(err.Error(), c.want) {
			t.Errorf("CheckLimits(%+v) = %v, want %q", c.l, err, c.want)
		}
	}
}

// TestCostLimits: the cost model reads MaxPage, MaxList and
// MaxIntrospectionCost.
func TestCostLimits(t *testing.T) {
	got := request.CostLimits(veloxgql.Limits{MaxPage: 1, MaxList: 2, MaxIntrospectionCost: 3})
	if got.MaxPage != 1 || got.MaxList != 2 || got.MaxIntrospectionCost != 3 {
		t.Errorf("CostLimits = %+v", got)
	}
}

// TestBoundsOfSchema: a schema's longest name raises MaxNameBytes, and its
// widest input type weighs an object of a query sent by its hash.
func TestBoundsOfSchema(t *testing.T) {
	long := strings.Repeat("n", 200)
	schema := gqlparser.MustLoadSchema(&ast.Source{Input: `
		type Query { ` + long + `(w: W): Int }
		input W { a: Int b: Int c: Int d: Int e: Int f: Int g: Int h: Int i: Int j: Int k: Int l: Int m: Int n: Int o: Int p: Int q: Int }
		enum E { ` + long + `X }
		directive @` + long + `Y on FIELD`})
	l := veloxgql.DefaultLimits()
	b := request.NewBounds(l, schema)
	if err := request.Check("", `{ `+long+` }`, nil, b); err != nil {
		t.Errorf("a field of the schema's own long name: %v", err)
	}
	if err := request.Check("", `{ `+long+`Z1 }`, nil, b); err == nil {
		t.Error("a name longer than the schema's longest passed")
	}
	// A query sent by its hash: its objects weigh as the widest input does.
	l.MaxVariableNodes = 5
	if err := request.Check("", "", map[string]any{"w": map[string]any{}}, request.NewBounds(l, schema)); err == nil || !strings.Contains(err.Message, "over 5 nodes") {
		t.Errorf("an object of unknown type: %v", err)
	}
}
