package gqlrelay

import (
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
)

// BenchmarkCollectFields_Feed is the collector's cost per resolver call on a
// feed-shaped selection under gqlgen: columns, a to-one edge, a list edge
// and two aliased pages of one connection with a nested edge.
func BenchmarkCollectFields_Feed(b *testing.B) {
	recent := connField("posts", map[string]string{"first": "2"}, nodeSel(field("title")), field("totalCount"))
	recent.Alias = "recent"
	more := connField("posts", map[string]string{"first": "5"}, nodeSel(field("body")))
	more.Alias = "more"
	sel := ast.SelectionSet{
		field("name"), field("email"),
		field("company", field("name")),
		recent, more,
		field("comments", field("title")),
	}
	ctx := newGQLContext(b, sel)
	b.ReportAllocs()
	for b.Loop() {
		q := newUserQuery()
		if err := CollectFields(ctx, q, q.Meta); err != nil {
			b.Fatal(err)
		}
	}
}
