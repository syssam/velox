package graphql

import (
	"context"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/ast"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/runtime"
	"github.com/syssam/velox/schema/field"
)

// interfaceFixture mirrors ent/contrib's todo example: Bookmark has two
// to-one edges (todo, project) sharing InterfaceField("item"); Todo and
// Project implement BookmarkItem and both rename their category edge to
// "owner". Category is a plain node.
func interfaceFixture() (*entgen.Graph, map[string]*entgen.Type) {
	ann := func(a Annotation) map[string]any { return map[string]any{AnnotationName: a} }
	str := func(name string) *entgen.Field {
		return &entgen.Field{Name: name, Type: &field.TypeInfo{Type: field.TypeString}}
	}
	mk := func(name string, fields ...*entgen.Field) *entgen.Type {
		return &entgen.Type{
			Name:   name,
			ID:     &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt}},
			Fields: fields,
			Config: &entgen.Config{Package: "example.com/app/velox"},
		}
	}
	category := mk("Category", str("name"))
	todo := mk("Todo", str("text"), str("priority"))
	project := mk("Project", str("text"), str("budget"))
	bookmark := mk("Bookmark", str("name"))
	todo.Annotations = ann(Annotation{Implements: []string{"BookmarkItem"}})
	project.Annotations = ann(Annotation{Implements: []string{"BookmarkItem"}})

	// m2o builds the inverse M2O edge of a pair the way velox does: the
	// foreign key is registered against the ASSOC edge on the target type
	// (Workspace.members owns Member's workspace_members column) and the two
	// edges point at each other through Ref. Matching a key to an edge by
	// name alone fails on this shape, which is the only shape a schema
	// written with edge.From(...).Ref(...) produces.
	m2o := func(owner *entgen.Type, name string, target *entgen.Type, ifield string) *entgen.Edge {
		column := owner.Table() + "_" + name
		inverse := &entgen.Edge{
			Name: name, Type: target, Unique: true, Optional: true, Inverse: "ref",
			Rel: entgen.Relation{Type: entgen.M2O, Table: owner.Table(), Columns: []string{column}},
		}
		assoc := &entgen.Edge{
			Name: owner.Table() + "_ref", Type: owner,
			Rel: entgen.Relation{Type: entgen.O2M, Table: owner.Table(), Columns: []string{column}},
		}
		inverse.Ref, assoc.Ref = assoc, inverse
		if ifield != "" {
			inverse.Annotations = ann(Annotation{InterfaceField: ifield})
		}
		owner.ForeignKeys = append(owner.ForeignKeys, &entgen.ForeignKey{
			Edge:  assoc,
			Field: &entgen.Field{Name: column, Type: &field.TypeInfo{Type: field.TypeInt}, Nillable: true, Optional: true},
		})
		return inverse
	}
	bookmark.Edges = []*entgen.Edge{m2o(bookmark, "todo", todo, "item"), m2o(bookmark, "project", project, "item")}
	todo.Edges = []*entgen.Edge{m2o(todo, "category", category, "owner")}
	project.Edges = []*entgen.Edge{m2o(project, "category", category, "owner")}

	graph := &entgen.Graph{Config: &entgen.Config{Package: "example.com/app/velox"}, Nodes: []*entgen.Type{bookmark, category, project, todo}}
	return graph, map[string]*entgen.Type{"Bookmark": bookmark, "Category": category, "Project": project, "Todo": todo}
}

func interfaceGen(t *testing.T) (*Generator, map[string]*entgen.Type) {
	t.Helper()
	graph, types := interfaceFixture()
	g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox", RelaySpec: true, RelayConnection: true})
	return g, types
}

func TestInterfaceFieldGroups(t *testing.T) {
	g, types := interfaceGen(t)

	groups, err := g.interfaceFieldGroups(types["Bookmark"])
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "item", groups[0].FieldName)
	assert.False(t, groups[0].IsRename)
	assert.Equal(t, "BookmarkItem", groups[0].InterfaceName)
	assert.True(t, groups[0].unique())
	assert.True(t, groups[0].allOwnFK())

	groups, err = g.interfaceFieldGroups(types["Todo"])
	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.True(t, groups[0].IsRename)
	assert.Equal(t, "owner", groups[0].FieldName)

	groups, err = g.interfaceFieldGroups(types["Category"])
	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestInterfaceFieldGroups_Errors(t *testing.T) {
	g, types := interfaceGen(t)

	// No common interface: Category implements nothing.
	types["Bookmark"].Edges[1].Type = types["Category"]
	_, err := g.interfaceFieldGroups(types["Bookmark"])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "share no GraphQL interface")

	// Name collision with an existing edge.
	g, types = interfaceGen(t)
	types["Todo"].Edges[0].Annotations = map[string]any{AnnotationName: Annotation{InterfaceField: "category"}}
	_, err = g.interfaceFieldGroups(types["Todo"])
	require.Error(t, err)
	assert.Contains(t, err.Error(), "collides")
}

func TestInterfaceField_SDL(t *testing.T) {
	g, types := interfaceGen(t)

	bookmark := g.genEntityType(types["Bookmark"])
	assert.Contains(t, bookmark, "  todo: Todo\n", "the edge keeps its own field")
	assert.Contains(t, bookmark, "  item: BookmarkItem\n", "polymorphic to-one field typed as the interface")

	todo := g.genEntityType(types["Todo"])
	assert.Contains(t, todo, "type Todo implements Node & BookmarkItem")
	assert.Contains(t, todo, "  category: Category\n")
	assert.Contains(t, todo, "  owner: Category\n", "rename adds the field under the new name")

	defs, err := g.genInterfaceDefsSchema()
	require.NoError(t, err)
	assert.Contains(t, defs, `interface BookmarkItem @goModel(model: "example.com/app/velox/entity.BookmarkItem") {`)
	for _, f := range []string{"  category: Category\n", "  id: ID!\n", "  owner: Category\n", "  text: String!\n"} {
		assert.Contains(t, defs, f, "shared field must be on the interface")
	}
	assert.NotContains(t, defs, "priority", "a field only Todo has is not shared")
	assert.NotContains(t, defs, "budget", "a field only Project has is not shared")
	assert.NotContains(t, defs, "BookmarkItemConnection", "no to-many polymorphic field, no connection types")

	full := g.genFullSchema()
	assert.Contains(t, full, "interface BookmarkItem", "full schema carries the generated interface")
}

func TestInterfaceField_NotGeneratedWithoutSharedRename(t *testing.T) {
	g, types := interfaceGen(t)
	// Drop the rename from Project: BookmarkItem is now only declared via
	// Implements, so it is the application's to define.
	types["Project"].Edges[0].Annotations = nil
	defs, err := g.genInterfaceDefsSchema()
	require.NoError(t, err)
	assert.Empty(t, defs)
	f, err := g.genInterfacesShared()
	require.NoError(t, err)
	assert.Nil(t, f)
}

func TestInterfaceField_GoInterfaceAndMarkers(t *testing.T) {
	g, _ := interfaceGen(t)
	f, err := g.genInterfacesShared()
	require.NoError(t, err)
	require.NotNil(t, f)
	code := f.GoString()
	assert.Contains(t, code, "type BookmarkItem interface {\n\tIsBookmarkItem()\n}")
	assert.Regexp(t, `func \(\*Project\) IsBookmarkItem\(\) +\{\}`, code)
	assert.Regexp(t, `func \(\*Todo\) IsBookmarkItem\(\) +\{\}`, code)
	assert.NotContains(t, code, "func (*Category)")
}

func TestInterfaceField_ResolverMethods(t *testing.T) {
	g, types := interfaceGen(t)

	bookmark := g.genEntityEdge(types["Bookmark"]).GoString()
	assert.Contains(t, bookmark, "func (m *Bookmark) Item(ctx context.Context) (BookmarkItem, error)")
	// Fast path: the populated foreign key names the concrete type.
	assert.Contains(t, bookmark, "case m.bookmarks_todo != nil:")
	assert.Contains(t, bookmark, `gqlrelay.InterfaceFieldCoveredByID(fc.Field, graphql.GetOperationContext(ctx), "BookmarkItem", "Todo", "Project")`)
	assert.Contains(t, bookmark, "return &Todo{ID: *m.bookmarks_todo}, nil")
	assert.Contains(t, bookmark, "return &Project{ID: *m.bookmarks_project}, nil")
	// Foreign keys are NOT selected unless the query asks for them, so with
	// every key nil the resolver must probe the edges rather than conclude
	// that the field is empty — without this arm the field resolved to null
	// on any query that did not go through field collection.
	assert.Contains(t, bookmark, "default:", "fast path needs a fallback arm when no key was loaded")
	assert.Contains(t, bookmark, "m.QueryTodo().Only(ctx)")
	assert.Contains(t, bookmark, "m.QueryProject().Only(ctx)")

	todo := g.genEntityEdge(types["Todo"]).GoString()
	assert.Contains(t, todo, "func (m *Todo) Owner(ctx context.Context) (*Category, error) {\n\treturn m.Category(ctx)\n}")
}

func TestInterfaceField_ResolverWithoutFKFastPath(t *testing.T) {
	g, types := interfaceGen(t)
	// A required edge has a non-pointer key: no fast path, sequential probe.
	for _, fk := range types["Bookmark"].ForeignKeys {
		fk.Field.Nillable = false
	}
	code := g.genEntityEdge(types["Bookmark"]).GoString()
	assert.Contains(t, code, "func (m *Bookmark) Item(ctx context.Context) (BookmarkItem, error)")
	assert.NotContains(t, code, "InterfaceFieldCoveredByID")
	assert.Contains(t, code, "m.QueryTodo().Only(ctx)")
	assert.Contains(t, code, "m.QueryProject().Only(ctx)")
	assert.Contains(t, code, "!runtime.IsNotFound(err)")
}

func TestInterfaceField_CollectMeta(t *testing.T) {
	g, types := interfaceGen(t)
	code := g.genEntityCollection(types["Bookmark"]).GoString()
	assert.Contains(t, code, `.InterfaceFields = map[string]runtime.InterfaceFieldMeta{`)
	assert.Contains(t, code, `"item": {`)
	assert.Contains(t, code, `Edges:     []string{"todo", "project"},`)
	assert.Contains(t, code, `Satisfies: []string{"BookmarkItem", "Todo", "Project"},`)
	assert.Contains(t, code, "FastPath:  true")
}

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
	q := newCollectQuery("id", "Bookmark")
	require.NoError(t, runtime.CollectFields(ctx, q, meta))
	assert.ElementsMatch(t, []string{"id", "bookmark_todo", "bookmark_project"}, q.Ctx.Fields)
	assert.Empty(t, q.Edges, "covered by id: no edge load")

	ctx = newGQLContext(t, ast.SelectionSet{item(&ast.Field{Name: "id"}, &ast.InlineFragment{
		TypeCondition: "Todo",
		SelectionSet:  ast.SelectionSet{&ast.Field{Name: "text"}},
	})})
	q = newCollectQuery("id", "Bookmark")
	require.NoError(t, runtime.CollectFields(ctx, q, meta))
	names := make([]string, 0, 2)
	for _, e := range q.Edges {
		names = append(names, e.Name)
	}
	assert.ElementsMatch(t, []string{"todo", "project"}, names, "a real selection loads every contributing edge")
	_ = context.Background
	_ = graphql.CollectedField{}
	_ = strings.Contains
}

// TestInterfaceField_RenameHasNoFastPath pins that a rename is reported as
// having no foreign-key fast path. Its resolver delegates to the ordinary
// edge method, so a collector that skipped the edge load for a
// __typename/id selection would turn one join into a query per row.
func TestInterfaceField_RenameHasNoFastPath(t *testing.T) {
	g, types := interfaceGen(t)

	groups, err := g.interfaceFieldGroups(types["Todo"])
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.True(t, groups[0].IsRename)
	assert.False(t, g.hasFKFastPath(types["Todo"], groups[0]),
		"a rename resolves through the edge method and must not claim the fast path")

	code := g.genEntityCollection(types["Todo"]).GoString()
	assert.Contains(t, code, "FastPath:  false")

	bookmark, err := g.interfaceFieldGroups(types["Bookmark"])
	require.NoError(t, err)
	assert.True(t, g.hasFKFastPath(types["Bookmark"], bookmark[0]),
		"a polymorphic group over nullable owner-side keys has the fast path")
}

// TestInterfaceField_FastPathMasksNotFound pins that the fast path treats a
// loaded-but-absent target as "no value" rather than an error, matching the
// probe path. A privacy policy hiding the row must yield a null field, not
// a GraphQL error.
func TestInterfaceField_FastPathMasksNotFound(t *testing.T) {
	g, types := interfaceGen(t)
	code := g.genEntityEdge(types["Bookmark"]).GoString()
	assert.Contains(t, code, "return nil, runtime.MaskNotFound(err)")
	assert.NotContains(t, code, "if !runtime.IsNotLoaded(err) {\n\t\t\treturn nil, err\n\t\t}",
		"an unmasked NotFound would surface as a GraphQL error")
}

// TestInterfaceField_RejectsToManyOverUndefinedInterface pins that grouping
// to-many edges under an interface velox does not generate is rejected: the
// field would render as <Interface>Connection with no such type in the SDL,
// and gqlgen would fail without naming the annotation.
func TestInterfaceField_RejectsToManyOverUndefinedInterface(t *testing.T) {
	g, types := interfaceGen(t)
	// Drop the shared rename so BookmarkItem is no longer velox-generated,
	// and make the group's edges to-many.
	types["Todo"].Edges[0].Annotations = nil
	types["Project"].Edges[0].Annotations = nil
	for _, e := range types["Bookmark"].Edges {
		e.Unique = false
	}
	err := g.validateInterfaceFields()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "BookmarkItemConnection")
	assert.Contains(t, err.Error(), "graphql.InterfaceField rename")
}

// TestInterfaceField_RejectsReservedGoName pins that a name whose pascal
// form collides with a member velox already emits on the entity is rejected
// at generation time, instead of producing an entity package that does not
// compile.
func TestInterfaceField_RejectsReservedGoName(t *testing.T) {
	for _, name := range []string{"edges", "config", "unwrap", "string"} {
		g, types := interfaceGen(t)
		types["Todo"].Edges[0].Annotations = map[string]any{AnnotationName: Annotation{InterfaceField: name}}
		_, err := g.interfaceFieldGroups(types["Todo"])
		require.Errorf(t, err, "interface field %q must be rejected", name)
		assert.Contains(t, err.Error(), "already exists on the entity")
	}
}
