package gqlrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/runtime"
)

// treeField is SelectedField as an engine other than gqlgen presents it: a
// plain tree whose type-conditioned fields are already split out, and whose
// numbers are json.Number, as graphql-go gives them.
type treeField struct {
	name     string
	args     map[string]any
	children []*treeField
	byType   map[string][]*treeField
}

func (f *treeField) FieldName() string         { return f.name }
func (f *treeField) Arguments() map[string]any { return f.args }

func (f *treeField) Fields(satisfies []string) []SelectedField {
	out := make([]SelectedField, 0, len(f.children))
	for _, c := range f.children {
		out = append(out, c)
	}
	for typ, fields := range f.byType {
		if satisfies != nil && !slices.Contains(satisfies, typ) {
			continue
		}
		for _, c := range fields {
			out = append(out, c)
		}
	}
	return out
}

// toTree converts a gqlparser selection into a treeField without going
// through gqlgen, so the two sources can be compared: an inline fragment's
// fields go under its type condition, and an Int literal becomes a
// json.Number.
func toTree(name string, args ast.ArgumentList, sel ast.SelectionSet) *treeField {
	f := &treeField{name: name}
	for _, a := range args {
		if f.args == nil {
			f.args = map[string]any{}
		}
		switch a.Value.Kind {
		case ast.IntValue:
			f.args[a.Name] = json.Number(a.Value.Raw)
		case ast.NullValue:
			f.args[a.Name] = nil
		default:
			f.args[a.Name] = map[string]any{}
		}
	}
	for _, s := range sel {
		switch s := s.(type) {
		case *ast.Field:
			f.children = append(f.children, toTree(s.Name, s.Arguments, s.SelectionSet))
		case *ast.InlineFragment:
			if f.byType == nil {
				f.byType = map[string][]*treeField{}
			}
			f.byType[s.TypeCondition] = append(f.byType[s.TypeCondition], toTree("", nil, s.SelectionSet).children...)
		}
	}
	return f
}

func treeContext(root *treeField) context.Context {
	return WithSelectionSource(context.Background(), func(context.Context) (SelectedField, bool) { return root, true })
}

// plan renders what the collector asked of q, recursively, in a form two
// runs can be compared by: sorted projected columns, then each eager load
// with its per-parent limit and its child's plan.
func plan(q *collectQuery) string {
	var b strings.Builder
	var walk func(q *collectQuery, depth int)
	walk = func(q *collectQuery, depth int) {
		cols := slices.Clone(q.Ctx.Fields)
		slices.Sort(cols)
		fmt.Fprintf(&b, "%*scolumns %v\n", depth*2, "", cols)
		edges := slices.Clone(q.Edges)
		slices.SortFunc(edges, func(a, b collectedEdge) int { return strings.Compare(a.Name, b.Name) })
		for _, e := range edges {
			limit := "all"
			if l := runtime.NewLoadConfig(e.Opts...).Limit; l != nil {
				limit = fmt.Sprint(*l)
			}
			fmt.Fprintf(&b, "%*sload %s limit %s\n", depth*2, "", e.Name, limit)
			walk(q.Children[e.Name], depth+1)
		}
	}
	walk(q, 0)
	return b.String()
}

// The selections a large client sends -- aliases of one connection at
// different page sizes, a to-one edge, a list edge, fragments, a connection
// asked only for its count -- must plan the same loads whichever engine
// describes them. A divergence here is a query per row under one engine
// and not the other.
func TestSelectionSource_PlansWhatGqlgenPlans(t *testing.T) {
	cases := map[string]ast.SelectionSet{
		"feed page with two aliased page sizes": {
			field("name"),
			field("company", field("name")),
			aliased(connField("posts", map[string]string{"first": "2"}, nodeSel(field("title")), field("totalCount")), "recent"),
			aliased(connField("posts", map[string]string{"first": "5"}, nodeSel(field("body"))), "more"),
			field("comments", field("title")),
		},
		"fragment on the concrete type": {
			field("email"),
			&ast.InlineFragment{TypeCondition: "User", SelectionSet: ast.SelectionSet{field("name"), field("company", field("name"))}},
		},
		"count only": {
			connField("posts", map[string]string{"first": "10"}, field("totalCount")),
		},
		"paged through Paginate": {
			connField("posts", map[string]string{"first": "2", "after": "1"}, nodeSel(field("title"))),
			connField("groups", map[string]string{"first": "3"}, nodeSel(field("name"))),
		},
		"a custom resolver keeps SELECT *": {
			field("name"), field("avatarURL"), field("company", field("name")),
		},
	}
	for name, sel := range cases {
		t.Run(name, func(t *testing.T) {
			viaGqlgen := newUserQuery()
			require.NoError(t, CollectFields(newGQLContext(t, sel), viaGqlgen, viaGqlgen.Meta))
			viaSource := newUserQuery()
			require.NoError(t, CollectFields(treeContext(toTree("user", nil, sel)), viaSource, viaSource.Meta))
			require.True(t, len(viaGqlgen.Ctx.Fields)+len(viaGqlgen.Edges) > 0,
				"the gqlgen run must plan something for the comparison to mean anything")
			assert.Equal(t, plan(viaGqlgen), plan(viaSource))
		})
	}
}

func TestSelectionSource_ConnectionAndCount(t *testing.T) {
	conn := func(sel ...ast.Selection) context.Context {
		return treeContext(toTree("posts", nil, sel))
	}
	assert.False(t, TotalCountSelected(conn(nodeSel(field("title")))), "no totalCount selected: Paginate skips COUNT")
	assert.True(t, TotalCountSelected(conn(field("totalCount"))))
	assert.True(t, TotalCountSelected(context.Background()), "a direct call has no selection and must count")

	q := newCollectQuery(postsMeta)
	require.NoError(t, CollectConnectionFields(conn(nodeSel(field("title"))), q, postsMeta))
	assert.ElementsMatch(t, []string{"id", "title"}, q.Ctx.Fields)

	q = newCollectQuery(postsMeta)
	require.NoError(t, CollectConnectionFields(conn(field("totalCount")), q, postsMeta))
	assert.Equal(t, []string{"id"}, q.Ctx.Fields, "a count needs only the key")
}

// A source that reports no field (ctx is not one of its resolvers) must
// fall back to gqlgen, not hide it: one process can serve both.
func TestSelectionSource_FallsBackToGqlgen(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("name")})
	ctx = WithSelectionSource(ctx, func(context.Context) (SelectedField, bool) { return nil, false })
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.ElementsMatch(t, []string{"id", "name"}, q.Ctx.Fields)

	q = newUserQuery()
	require.NoError(t, CollectFields(WithSelectionSource(context.Background(), nil), q, q.Meta))
	assert.Empty(t, q.Ctx.Fields, "a nil source is no source")
}

func aliased(f *ast.Field, alias string) *ast.Field {
	f.Alias = alias
	return f
}
