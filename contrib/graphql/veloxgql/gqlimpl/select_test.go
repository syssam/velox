package gqlimpl_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlimpl"
)

// TestSelectedConnection: through a connection, the fields selected on its
// nodes, with named fragments and every alias of edges, without
// __typename; computed once per field.
func TestSelectedConnection(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, db, `{ owners {
		totalCount
		edges { cursor node { name ...F } }
		more: edges { node { id __typename ... on Node { id } } }
	} }
	fragment F on Owner { age }`)
	ctx = field(t, ctx, "owners")
	got, ok := gqlimpl.Selected(ctx, "Owner")
	if want := []string{"name", "age", "id"}; !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Selected: %q, %v; want %q", got, ok, want)
	}
	again, _ := gqlimpl.Selected(ctx, "Owner")
	if &again[0] != &got[0] {
		t.Error("Selected computed the field's fields again")
	}
}

// TestSelectedObject: a field of the type itself, or of Node, gives its own
// selection, with the fragments on the type or on Node.
func TestSelectedObject(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, db, `{
		owner { name ... on Owner { age } __typename tags { name } children { totalCount } }
		node(id: "1") { id ... on Owner { name } ... on Child { title } ... on Node { id } }
		nodes(ids: ["1"]) { ... on Child { title } }
	}`)
	for _, c := range []struct {
		field, typ string
		want       []string
	}{
		{"owner", "Owner", []string{"name", "age", "tags", "children"}},
		{"node", "Owner", []string{"id", "name"}},
		{"node", "Child", []string{"id", "title"}},
		{"nodes", "Child", []string{"title"}},
		{"nodes", "Owner", []string{}},
	} {
		got, ok := gqlimpl.Selected(field(t, ctx, c.field), c.typ)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s as %s: %q, %v; want %q", c.field, c.typ, got, ok, c.want)
		}
	}
}

// TestSelectedNotOurs: a field that returns another type, or a connection
// to one, and a context outside a field, give ok false.
func TestSelectedNotOurs(t *testing.T) {
	db, _, _ := edgeDB(t)
	ctx := operation(t, db, `{ other { name } owner { name } children { totalCount } }`)
	for _, c := range []struct {
		name string
		ctx  context.Context
		typ  string
	}{
		{"other", field(t, ctx, "other"), "Owner"},
		{"owner as Child", field(t, ctx, "owner"), "Child"},
		{"children as Owner", field(t, ctx, "children"), "Owner"},
		{"no field", ctx, "Owner"},
		{"no operation", context.Background(), "Owner"},
	} {
		if got, ok := gqlimpl.Selected(c.ctx, c.typ); ok {
			t.Errorf("%s: %q, ok; want not ok", c.name, got)
		}
	}
}

// TestSelectedAppFields: a field of the connection or of its edge that
// velox-graphql does not write, which an application's resolver may read
// the nodes for, gives ok false.
func TestSelectedAppFields(t *testing.T) {
	db, _, _ := edgeDB(t)
	for _, c := range []struct {
		query string
		want  []string // nil: not ok
	}{
		{`{ owners { __typename totalCount pageInfo { hasNextPage } edges { __typename cursor node { name } } } }`, []string{"name"}},
		{`{ owners { nodes { name } } }`, nil},
		{`{ owners { edges { extra node { name } } } }`, nil},
	} {
		got, ok := gqlimpl.Selected(field(t, operation(t, db, c.query), "owners"), "Owner")
		if ok != (c.want != nil) || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s: %q, %v; want %q", c.query, got, ok, c.want)
		}
	}
}

// TestSelectedInterfaces: fragments on every interface and union of the
// type count, read from the server's schema; without the schema, a
// fragment on an interface or a union other than Node gives ok false.
func TestSelectedInterfaces(t *testing.T) {
	db, _, _ := edgeDB(t)
	for _, c := range []struct {
		query, field string
		withSchema   []string // nil: not ok
		without      []string
	}{
		{`{ owner { ... on Named { name } } }`, "owner", []string{"name"}, nil},
		{`{ owners { edges { node { ... on Named { name } } } } }`, "owners", []string{"name"}, nil},
		{`{ owner { ...T } } fragment T on Thing { ... on Owner { age } }`, "owner", []string{"age"}, nil},
		{`{ owner { id ... on Node { id } } }`, "owner", []string{"id"}, []string{"id"}},
		{`{ node(id: "1") { ... on Child { title } ... on Owner { name } } }`, "node", []string{"name"}, []string{"name"}},
	} {
		for _, s := range []struct {
			name   string
			schema *ast.Schema
			want   []string
		}{{"with the schema", edgeSchema, c.withSchema}, {"without", nil, c.without}} {
			got, ok := gqlimpl.Selected(field(t, operationIn(t, db, c.query, s.schema), c.field), "Owner")
			if ok != (s.want != nil) || (ok && !reflect.DeepEqual(got, s.want)) {
				t.Errorf("%s %s: %q, %v; want %q", c.query, s.name, got, ok, s.want)
			}
		}
	}
}

// TestNarrow: Narrow keeps the columns the query selects, and nil columns
// keep the query.
func TestNarrow(t *testing.T) {
	db, _, log := edgeDB(t)
	q := db.QueryTable(velox.TableOf[owner]()).Unbounded()
	idName := []int{int(owners.ID.I), int(owners.Name.I)}
	for _, c := range []struct {
		name string
		q    velox.TableQuery
		want string
	}{
		{"every column", gqlimpl.Narrow(q, nil), `SELECT "id", "name", "age" FROM "owners"`},
		{"two", gqlimpl.Narrow(q, idName), `SELECT "id", "name" FROM "owners"`},
		{"never added", gqlimpl.Narrow(q.Columns(int(owners.ID.I), int(owners.Age.I)), idName), `SELECT "id" FROM "owners"`},
		{"none selected", gqlimpl.Narrow(q.Columns(int(owners.ID.I)), []int{int(owners.Age.I)}), `SELECT "id" FROM "owners"`},
	} {
		log.take("")
		if _, err := c.q.All(context.Background()); err != nil {
			t.Fatal(err)
		}
		if sql := log.take("owners"); len(sql) != 1 || sql[0] != c.want {
			t.Errorf("%s: %q, want %s", c.name, sql, c.want)
		}
	}
}
