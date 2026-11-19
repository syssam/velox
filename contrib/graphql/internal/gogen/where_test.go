package gogen

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/sdl"
	"github.com/syssam/velox/plugin"
)

// TestWhereFieldRefusesBugs: a run of where fields that sdl does not write,
// which only a bug in sdl gives, is refused, not rendered as a wrong
// descriptor.
func TestWhereFieldRefusesBugs(t *testing.T) {
	e := &plugin.Entity{
		Name:   "Thing",
		Fields: []*plugin.Field{{Name: "title", Type: "string"}},
		Edges:  []*plugin.Edge{{Name: "owner", Target: "Thing"}},
	}
	typ := &sdl.Type{Name: "Thing", Entity: e}
	r := &renderer{types: map[string]*sdl.Type{"Thing": typ}}
	pred := func(name string, op graphql.WhereOp) sdl.WhereField {
		return sdl.WhereField{Name: name, GoName: name, Kind: sdl.WherePred, Op: op, Field: 0, Edge: -1}
	}
	for name, c := range map[string]struct {
		ws   []sdl.WhereField
		want string
	}{
		"an unknown op": {[]sdl.WhereField{pred("title", 0)}, "no predicate WhereOp(0x0)"},
		"another field": {[]sdl.WhereField{pred("title", graphql.OpEQ), pred("nameNEQ", graphql.OpNEQ)}, "nameNEQ is not titleNEQ"},
		"IDs of no ID":  {[]sdl.WhereField{func() sdl.WhereField { w := pred("id", graphql.OpEQ); w.Target = "Thing"; return w }()}, "a type that has none, which is a bug"},
		"out of order":  {[]sdl.WhereField{pred("titleNEQ", graphql.OpNEQ), pred("title", graphql.OpEQ)}, "not in gqlgraph's order"},
		"nil before":    {[]sdl.WhereField{pred("titleIsNil", graphql.OpIsNil), pred("titleContainsFold", graphql.OpContainsFold)}, "not in gqlgraph's order"},
		"twice":         {[]sdl.WhereField{pred("title", graphql.OpEQ), pred("title", graphql.OpEQ)}, "not in gqlgraph's order"},
		"an FK's value": {[]sdl.WhereField{func() sdl.WhereField { w := pred("ownerID", graphql.OpEQ); w.Edge = 0; return w }()}, "takes no IDs"},
	} {
		if _, err := r.whereField(newFile(), typ, c.ws); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
