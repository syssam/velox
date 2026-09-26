package gqlrelay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/runtime"
)

// collectQuery is a minimal runtime.FieldCollectable recording what the
// collector asks for: projected columns on Ctx.Fields, eager loads on
// Edges. Children are created per edge name and carry their own metadata,
// like the generated query types, so recursion can be observed.
type collectQuery struct {
	IDColumn string
	Ctx      *runtime.QueryContext
	Meta     *runtime.CollectMeta
	Edges    []collectedEdge
	Children map[string]*collectQuery
	// ChildMeta is the metadata a child created for the edge gets.
	ChildMeta map[string]*runtime.CollectMeta
}

// collectedEdge records one WithEdgeLoad call.
type collectedEdge struct {
	Name string
	Opts []runtime.LoadOption
}

func newCollectQuery(meta *runtime.CollectMeta) *collectQuery {
	return &collectQuery{IDColumn: "id", Ctx: &runtime.QueryContext{}, Meta: meta, Children: map[string]*collectQuery{}}
}

func (q *collectQuery) GetIDColumn() string               { return q.IDColumn }
func (q *collectQuery) GetCtx() *runtime.QueryContext     { return q.Ctx }
func (q *collectQuery) CollectMeta() *runtime.CollectMeta { return q.Meta }

func (q *collectQuery) WithEdgeLoad(name string, opts ...runtime.LoadOption) runtime.FieldCollectable {
	q.Edges = append(q.Edges, collectedEdge{Name: name, Opts: opts})
	child := q.Children[name]
	if child == nil {
		child = newCollectQuery(q.ChildMeta[name])
		child.Ctx.EdgeLoadCreated = true
		q.Children[name] = child
	}
	for _, o := range opts {
		var c runtime.LoadConfig
		o(&c)
		if c.Limit != nil {
			child.Ctx.PartitionLimit = c.Limit
		}
	}
	return child
}

// loadConfig returns the options of the only WithEdgeLoad call for name.
func (q *collectQuery) loadConfig(t *testing.T, name string) *runtime.LoadConfig {
	t.Helper()
	var calls []collectedEdge
	for _, e := range q.Edges {
		if e.Name == name {
			calls = append(calls, e)
		}
	}
	require.Len(t, calls, 1, "edge %q must be loaded exactly once", name)
	return runtime.NewLoadConfig(calls[0].Opts...)
}

// newGQLContext creates a context with gqlgen field and operation contexts
// for the given selection set.
func newGQLContext(t testing.TB, selections ast.SelectionSet) context.Context {
	t.Helper()
	collected := graphql.CollectedField{
		Field:      &ast.Field{Name: "user", Alias: "user", SelectionSet: selections},
		Selections: selections,
	}
	ctx := graphql.WithFieldContext(context.Background(), &graphql.FieldContext{Field: collected})
	return graphql.WithOperationContext(ctx, &graphql.OperationContext{Variables: map[string]any{}})
}

// field builds a selected field with its selection set.
func field(name string, sel ...ast.Selection) *ast.Field {
	return &ast.Field{Name: name, Alias: name, SelectionSet: sel}
}

// connField builds a connection field with the given literal arguments
// (name → raw Int literal, or "null" for an explicit null).
func connField(name string, args map[string]string, sel ...ast.Selection) *ast.Field {
	f := field(name, sel...)
	def := &ast.FieldDefinition{Name: name}
	for _, argName := range []string{"after", "first", "before", "last", "orderBy", "where"} {
		def.Arguments = append(def.Arguments, &ast.ArgumentDefinition{Name: argName, Type: ast.NamedType("Int", nil)})
	}
	for argName, raw := range args {
		v := &ast.Value{Kind: ast.IntValue, Raw: raw}
		switch raw {
		case "null":
			v = &ast.Value{Kind: ast.NullValue, Raw: raw}
		case "{}":
			v = &ast.Value{Kind: ast.ObjectValue}
		}
		f.Arguments = append(f.Arguments, &ast.Argument{Name: argName, Value: v})
	}
	f.Definition = def
	return f
}

// nodeSel is edges { node { <sel> } }.
func nodeSel(sel ...ast.Selection) ast.Selection {
	return field("edges", field("node", sel...))
}

var postsMeta = &runtime.CollectMeta{
	FieldColumns: map[string]string{"title": "title", "body": "body"},
}

func userMeta() *runtime.CollectMeta {
	return &runtime.CollectMeta{
		FieldColumns: map[string]string{"name": "name", "email": "email", "age": "age"},
		Edges: map[string]runtime.EdgeMeta{
			"posts":    {Name: "posts", Relay: true, PagesLoaded: true},
			"comments": {Name: "comments"},
			"company":  {Name: "company", Unique: true, FKColumns: []string{"company_users"}},
			"groups":   {Name: "groups", Relay: true},
		},
	}
}

func newUserQuery() *collectQuery {
	q := newCollectQuery(userMeta())
	q.ChildMeta = map[string]*runtime.CollectMeta{"posts": postsMeta, "comments": postsMeta, "company": {FieldColumns: map[string]string{"name": "name"}}}
	return q
}

func TestCollectFields_NoGraphQLContext(t *testing.T) {
	q := newUserQuery()
	require.NoError(t, CollectFields(context.Background(), q, q.Meta))
	assert.Empty(t, q.Ctx.Fields)
	assert.Empty(t, q.Edges)
}

func TestCollectFields_FieldContextWithoutOperationContext(t *testing.T) {
	ctx := graphql.WithFieldContext(context.Background(), &graphql.FieldContext{})
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.Empty(t, q.Ctx.Fields)
}

func TestCollectFields_ScalarProjection(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("id"), field("__typename"), field("name"), field("email")})
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.Equal(t, []string{"id", "name", "email"}, q.Ctx.Fields)
}

func TestCollectFields_UnknownFieldKeepsSelectAll(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("name"), field("customResolver")})
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.Empty(t, q.Ctx.Fields, "unknown fields must prevent column projection")
}

func TestCollectFields_CollectedFor(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("fullName")})
	meta := &runtime.CollectMeta{
		FieldColumns: map[string]string{"age": "age"},
		CollectedFor: map[string][]string{"fullName": {"first_name", "last_name"}},
	}
	q := newCollectQuery(meta)
	require.NoError(t, CollectFields(ctx, q, meta))
	assert.Equal(t, []string{"id", "first_name", "last_name"}, q.Ctx.Fields)
}

func TestCollectFields_CollectedFor_UnknownStillFallsBack(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("fullName"), field("initials")})
	meta := &runtime.CollectMeta{CollectedFor: map[string][]string{"fullName": {"first_name", "last_name"}}}
	q := newCollectQuery(meta)
	require.NoError(t, CollectFields(ctx, q, meta))
	assert.Empty(t, q.Ctx.Fields)
}

// TestCollectFields_UniqueEdgeRecursesAndSelectsOwnKey pins that a to-one
// edge whose key lives on this table adds the key to the projection, and
// that the edge query is projected from the nested selection.
func TestCollectFields_UniqueEdgeRecursesAndSelectsOwnKey(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("name"), field("company", field("name"))})
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.Equal(t, []string{"id", "name", "company_users"}, q.Ctx.Fields)
	assert.Nil(t, q.loadConfig(t, "company").Limit)
	assert.Equal(t, []string{"id", "name"}, q.Children["company"].Ctx.Fields)
}

// TestCollectFields_ListEdgeLoadsWholeEdge pins that a plain list edge is
// loaded unlimited and projected.
func TestCollectFields_ListEdgeLoadsWholeEdge(t *testing.T) {
	ctx := newGQLContext(t, ast.SelectionSet{field("comments", field("body"))})
	q := newUserQuery()
	require.NoError(t, CollectFields(ctx, q, q.Meta))
	assert.Nil(t, q.loadConfig(t, "comments").Limit)
	assert.Equal(t, []string{"id", "body"}, q.Children["comments"].Ctx.Fields)
	assert.Equal(t, []string{"id"}, q.Ctx.Fields, "a key on the other table must not be selected here")
}

func TestCollectFields_Connection(t *testing.T) {
	tests := []struct {
		name      string
		sel       ast.SelectionSet
		wantLoad  bool
		wantLimit *int
	}{
		{
			name:      "first limits per parent to first+1",
			sel:       ast.SelectionSet{connField("posts", map[string]string{"first": "2"}, nodeSel(field("title")))},
			wantLoad:  true,
			wantLimit: intp(3),
		},
		{
			name:     "no first loads the whole edge",
			sel:      ast.SelectionSet{connField("posts", nil, nodeSel(field("title")))},
			wantLoad: true,
		},
		{
			name:     "last loads the whole edge",
			sel:      ast.SelectionSet{connField("posts", map[string]string{"last": "2"}, nodeSel(field("title")))},
			wantLoad: true,
		},
		{
			name:     "totalCount needs every row",
			sel:      ast.SelectionSet{connField("posts", map[string]string{"first": "2"}, nodeSel(field("title")), field("totalCount"))},
			wantLoad: true,
		},
		{
			name: "cursor resolves through Paginate",
			sel:  ast.SelectionSet{connField("posts", map[string]string{"first": "2", "after": "1"}, nodeSel(field("title")))},
		},
		{
			name: "where resolves through Paginate",
			sel:  ast.SelectionSet{connField("posts", map[string]string{"where": "{}"}, nodeSel(field("title")))},
		},
		{
			name: "orderBy resolves through Paginate",
			sel:  ast.SelectionSet{connField("posts", map[string]string{"orderBy": "1"}, nodeSel(field("title")))},
		},
		{
			name:      "explicit null where still loads",
			sel:       ast.SelectionSet{connField("posts", map[string]string{"where": "null", "first": "1"}, nodeSel(field("title")))},
			wantLoad:  true,
			wantLimit: intp(2),
		},
		{
			name: "nothing read loads nothing",
			sel:  ast.SelectionSet{connField("posts", map[string]string{"first": "1"}, field("__typename"))},
		},
		{
			name: "connection without an in-memory pager is left to Paginate",
			sel:  ast.SelectionSet{connField("groups", map[string]string{"first": "1"}, nodeSel(field("name")))},
		},
		{
			name: "aliases merge: the largest first wins",
			sel: ast.SelectionSet{
				connField("posts", map[string]string{"first": "2"}, nodeSel(field("title"))),
				&ast.Field{Name: "posts", Alias: "more", SelectionSet: ast.SelectionSet{nodeSel(field("body"))},
					Arguments:  ast.ArgumentList{{Name: "first", Value: &ast.Value{Kind: ast.IntValue, Raw: "5"}}},
					Definition: connField("posts", nil).Definition},
			},
			wantLoad:  true,
			wantLimit: intp(6),
		},
		{
			name: "aliases merge: one unlimited occurrence loads everything",
			sel: ast.SelectionSet{
				connField("posts", map[string]string{"first": "2"}, nodeSel(field("title"))),
				&ast.Field{Name: "posts", Alias: "all", SelectionSet: ast.SelectionSet{nodeSel(field("body"))},
					Definition: connField("posts", nil).Definition},
			},
			wantLoad: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := newGQLContext(t, tt.sel)
			q := newUserQuery()
			require.NoError(t, CollectFields(ctx, q, q.Meta))
			if !tt.wantLoad {
				assert.Empty(t, q.Edges)
				return
			}
			name := "posts"
			assert.Equal(t, tt.wantLimit, q.loadConfig(t, name).Limit)
		})
	}
}

func TestCollectFields_ConnectionNodeProjection(t *testing.T) {
	sel := ast.SelectionSet{
		connField("posts", map[string]string{"first": "2"}, nodeSel(field("title"))),
		&ast.Field{Name: "posts", Alias: "more", SelectionSet: ast.SelectionSet{nodeSel(field("body"))},
			Definition: connField("posts", nil).Definition},
	}
	q := newUserQuery()
	require.NoError(t, CollectFields(newGQLContext(t, sel), q, q.Meta))
	assert.Equal(t, []string{"id", "title", "body"}, q.Children["posts"].Ctx.Fields,
		"the edge query must read what every alias selects")

	// Only pageInfo: rows are needed, columns are not.
	q = newUserQuery()
	require.NoError(t, CollectFields(newGQLContext(t, ast.SelectionSet{connField("posts", map[string]string{"first": "1"}, field("pageInfo", field("hasNextPage")))}), q, q.Meta))
	assert.Equal(t, []string{"id"}, q.Children["posts"].Ctx.Fields)
}

func TestCollectConnectionFields(t *testing.T) {
	sel := ast.SelectionSet{field("totalCount"), nodeSel(field("title"), connField("posts", map[string]string{"first": "3"}, nodeSel(field("body"))))}
	meta := &runtime.CollectMeta{
		FieldColumns: map[string]string{"title": "title"},
		Edges:        map[string]runtime.EdgeMeta{"posts": {Name: "posts", Relay: true, PagesLoaded: true}},
	}
	q := newCollectQuery(meta)
	q.ChildMeta = map[string]*runtime.CollectMeta{"posts": postsMeta}
	require.NoError(t, CollectConnectionFields(newGQLContext(t, sel), q, meta))
	assert.Equal(t, []string{"id", "title"}, q.Ctx.Fields)
	assert.Equal(t, intp(4), q.loadConfig(t, "posts").Limit)
	assert.Equal(t, []string{"id", "body"}, q.Children["posts"].Ctx.Fields)

	// No edges selected: only the key is read.
	q = newCollectQuery(meta)
	require.NoError(t, CollectConnectionFields(newGQLContext(t, ast.SelectionSet{field("totalCount")}), q, meta))
	assert.Equal(t, []string{"id"}, q.Ctx.Fields)
	assert.Empty(t, q.Edges)
}

func TestTotalCountSelected(t *testing.T) {
	assert.True(t, TotalCountSelected(context.Background()), "outside a resolver there is no selection to consult: count")
	assert.True(t, TotalCountSelected(graphql.WithFieldContext(context.Background(), &graphql.FieldContext{})),
		"a field context without an operation context: count")
	assert.True(t, TotalCountSelected(newGQLContext(t, ast.SelectionSet{field("totalCount"), nodeSel(field("title"))})))
	assert.True(t, TotalCountSelected(newGQLContext(t, ast.SelectionSet{
		&ast.InlineFragment{SelectionSet: ast.SelectionSet{field("totalCount")}},
	})), "a totalCount inside a fragment counts")
	assert.False(t, TotalCountSelected(newGQLContext(t, ast.SelectionSet{field("pageInfo", field("hasNextPage")), nodeSel(field("title"))})),
		"pageInfo is computed from the extra row, not the count")
	assert.False(t, TotalCountSelected(newGQLContext(t, ast.SelectionSet{nodeSel(field("title"))})))
}

func TestGqlToInt(t *testing.T) {
	tests := []struct {
		name   string
		input  any
		want   int
		wantOk bool
	}{
		{"int", 10, 10, true},
		{"int64", int64(42), 42, true},
		{"int32", int32(7), 7, true},
		{"json.Number", json.Number("9"), 9, true},
		{"json.Number fraction", json.Number("9.5"), 0, false},
		{"float64 whole number", float64(10), 10, true},
		{"float64 with fraction returns false", 3.14, 0, false},
		{"string returns false", "10", 0, false},
		{"nil returns false", nil, 0, false},
		{"negative int", -5, -5, true},
		{"int64 max int", int64(maxInt), maxInt, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := gqlToInt(tt.input)
			assert.Equal(t, tt.wantOk, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func intp(n int) *int { return &n }

// TestCollectFields_InterfaceField pins the collector: a selection covered
// by __typename/id on an all-own-FK field selects only the key columns and
// schedules no edge load; any other selection eager-loads every edge.
func TestCollectFields_InterfaceField(t *testing.T) {
	meta := &runtime.CollectMeta{
		FieldColumns: map[string]string{"name": "name"},
		Edges: map[string]runtime.EdgeMeta{
			"todo":    {Name: "todo", Target: "todos", Unique: true, FKColumns: []string{"bookmark_todo"}},
			"project": {Name: "project", Target: "projects", Unique: true, FKColumns: []string{"bookmark_project"}},
		},
		InterfaceFields: map[string]runtime.InterfaceFieldMeta{
			"item": {Edges: []string{"todo", "project"}, Satisfies: []string{"BookmarkItem", "Todo", "Project"}, FastPath: true},
		},
	}
	item := func(sel ...ast.Selection) *ast.Field {
		return &ast.Field{Name: "item", Alias: "item", SelectionSet: sel}
	}

	ctx := newGQLContext(t, ast.SelectionSet{item(&ast.Field{Name: "__typename"}, &ast.Field{Name: "id"})})
	q := newCollectQuery(meta)
	require.NoError(t, CollectFields(ctx, q, meta))
	assert.ElementsMatch(t, []string{"id", "bookmark_todo", "bookmark_project"}, q.Ctx.Fields)
	assert.Empty(t, q.Edges, "covered by id: no edge load")

	ctx = newGQLContext(t, ast.SelectionSet{item(&ast.Field{Name: "id"}, &ast.InlineFragment{
		TypeCondition: "Todo",
		SelectionSet:  ast.SelectionSet{&ast.Field{Name: "text"}},
	})})
	q = newCollectQuery(meta)
	require.NoError(t, CollectFields(ctx, q, meta))
	names := make([]string, 0, 2)
	for _, e := range q.Edges {
		names = append(names, e.Name)
	}
	assert.ElementsMatch(t, []string{"todo", "project"}, names, "a real selection loads every contributing edge")
}

// An interface field and a direct selection of the same edge share one
// eager-loaded child query. The child's projection must cover BOTH: the
// interface resolver answers from the loaded row, so a projection narrowed
// to the direct selection alone returned the interface's other fields as
// zero values (description null, role "").
func TestCollectFields_InterfaceFieldSharesEdgeWithDirectSelection(t *testing.T) {
	workspaceMeta := &runtime.CollectMeta{FieldColumns: map[string]string{"name": "name", "description": "description"}}
	memberMeta := &runtime.CollectMeta{FieldColumns: map[string]string{"role": "role", "accepted": "accepted"}}
	meta := &runtime.CollectMeta{
		Edges: map[string]runtime.EdgeMeta{
			"workspace":   {Name: "workspace", Unique: true, FKColumns: []string{"workspace_members"}},
			"user":        {Name: "user", Unique: true, FKColumns: []string{"user_memberships"}},
			"memberships": {Name: "memberships", Relay: true, PagesLoaded: true},
		},
		InterfaceFields: map[string]runtime.InterfaceFieldMeta{
			"principal": {Edges: []string{"workspace", "user"}, Satisfies: []string{"Principal", "Workspace", "User"}, FastPath: true},
			"relations": {Edges: []string{"memberships"}, Satisfies: []string{"Member"}},
		},
	}
	newQ := func() *collectQuery {
		q := newCollectQuery(meta)
		q.ChildMeta = map[string]*runtime.CollectMeta{"workspace": workspaceMeta, "user": {FieldColumns: map[string]string{"name": "name"}}, "memberships": memberMeta}
		return q
	}

	t.Run("to-one", func(t *testing.T) {
		ctx := newGQLContext(t, ast.SelectionSet{
			field("workspace", field("name")),
			field("principal", &ast.InlineFragment{
				TypeCondition: "Workspace",
				SelectionSet:  ast.SelectionSet{field("name"), field("description")},
			}),
		})
		q := newQ()
		require.NoError(t, CollectFields(ctx, q, meta))
		q.loadConfig(t, "workspace")
		child := q.Children["workspace"]
		if len(child.Ctx.Fields) > 0 {
			assert.Subset(t, child.Ctx.Fields, []string{"id", "name", "description"})
		}
	})

	t.Run("to-many connection with first", func(t *testing.T) {
		ctx := newGQLContext(t, ast.SelectionSet{
			connField("memberships", map[string]string{"first": "1"}, nodeSel(field("accepted"))),
			field("relations", field("role")),
		})
		q := newQ()
		require.NoError(t, CollectFields(ctx, q, meta))
		cfg := q.loadConfig(t, "memberships")
		assert.Nil(t, cfg.Limit, "the interface resolver reads every loaded row: no per-parent limit")
		child := q.Children["memberships"]
		if len(child.Ctx.Fields) > 0 {
			assert.Subset(t, child.Ctx.Fields, []string{"id", "accepted", "role"})
		}
	})

	t.Run("nested edge under the interface selection", func(t *testing.T) {
		// principal { ... on Workspace { owner { name } } } next to a direct
		// workspace { name }: the nested edge is loaded too.
		wsMeta := &runtime.CollectMeta{
			FieldColumns: map[string]string{"name": "name"},
			Edges:        map[string]runtime.EdgeMeta{"owner": {Name: "owner", Unique: true, FKColumns: []string{"owner_id"}}},
		}
		q := newCollectQuery(meta)
		q.ChildMeta = map[string]*runtime.CollectMeta{"workspace": wsMeta, "user": {}}
		ctx := newGQLContext(t, ast.SelectionSet{
			field("workspace", field("name")),
			field("principal", &ast.InlineFragment{
				TypeCondition: "Workspace",
				SelectionSet:  ast.SelectionSet{field("owner", field("name"))},
			}),
		})
		require.NoError(t, CollectFields(ctx, q, meta))
		child := q.Children["workspace"]
		child.loadConfig(t, "owner")
		if len(child.Ctx.Fields) > 0 {
			assert.Contains(t, child.Ctx.Fields, "owner_id")
		}
	})
}

// An edge the resolver loaded itself (WithItems before Paginate) is read by
// code the collector cannot see -- a computed total over every item, say.
// Narrowing it to the columns the client selected, or capping it to the
// first page, answered that total from zero values and partial rows. The
// collector may still load edges beneath it.
func TestCollectFields_CallerConfiguredEdgeIsLoadedWhole(t *testing.T) {
	q := newUserQuery()
	q.ChildMeta["posts"] = &runtime.CollectMeta{
		FieldColumns: map[string]string{"title": "title", "body": "body"},
		Edges:        map[string]runtime.EdgeMeta{"author": {Name: "author", Unique: true, FKColumns: []string{"post_author"}}},
	}
	explicit := newCollectQuery(q.ChildMeta["posts"])
	explicit.ChildMeta = map[string]*runtime.CollectMeta{"author": {FieldColumns: map[string]string{"name": "name"}}}
	q.Children["posts"] = explicit // as WithPosts() left it: EdgeLoadCreated false

	sel := ast.SelectionSet{connField("posts", map[string]string{"first": "2"},
		nodeSel(field("title"), field("author", field("name"))))}
	require.NoError(t, CollectFields(newGQLContext(t, sel), q, q.Meta))

	assert.Empty(t, explicit.Ctx.Fields, "the caller's edge query keeps every column")
	assert.Nil(t, explicit.Ctx.PartitionLimit, "the caller's edge query keeps every row")
	author := explicit.Children["author"]
	require.NotNil(t, author, "edges beneath the caller's query are still collected")
	assert.ElementsMatch(t, []string{"id", "name"}, author.Ctx.Fields, "and a query the collector created is projected")

	// The same selection without the caller's load is narrowed and capped.
	q = newUserQuery()
	q.ChildMeta["posts"] = explicit.Meta
	require.NoError(t, CollectFields(newGQLContext(t, sel), q, q.Meta))
	created := q.Children["posts"]
	assert.ElementsMatch(t, []string{"id", "title", "post_author"}, created.Ctx.Fields)
	require.NotNil(t, created.Ctx.PartitionLimit)
	assert.Equal(t, 3, *created.Ctx.PartitionLimit)
}
