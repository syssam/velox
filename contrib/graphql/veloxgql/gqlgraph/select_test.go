package gqlgraph_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

// TestSelectedConnection: through a connection, the fields selected on its
// nodes, with named fragments and every alias of edges, without
// __typename; computed once per field.
func TestSelectedConnection(t *testing.T) {
	ctx := operation(t, `{ owners {
		totalCount
		edges { cursor node { name ...F } }
		more: edges { node { id __typename ... on Keyed { id } } }
	} }
	fragment F on Owner { age }`)
	ctx = field(t, ctx, "owners")
	got, ok := gqlgraph.Selected(ctx, "Owner")
	if want := []string{"name", "age", "id"}; !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("Selected: %q, %v; want %q", got, ok, want)
	}
	again, _ := gqlgraph.Selected(ctx, "Owner")
	if &again[0] != &got[0] {
		t.Error("Selected computed the field's fields again")
	}
}

// TestSelectedObject: a field of the type itself gives its own selection,
// with the fragments on the type.
func TestSelectedObject(t *testing.T) {
	ctx := operation(t, `{
		owner { name ... on Owner { age } __typename tags { name } children { totalCount } }
	}`)
	for _, c := range []struct {
		field, typ string
		want       []string
	}{
		{"owner", "Owner", []string{"name", "age", "tags", "children"}},
	} {
		got, ok := gqlgraph.Selected(field(t, ctx, c.field), c.typ)
		if !ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s as %s: %q, %v; want %q", c.field, c.typ, got, ok, c.want)
		}
	}
}

// TestSelectedNotOurs: a field that returns another type, or a connection
// to one, and a context outside a field, give ok false.
func TestSelectedNotOurs(t *testing.T) {
	ctx := operation(t, `{ other { name } owner { name } children { totalCount } }`)
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
		if got, ok := gqlgraph.Selected(c.ctx, c.typ); ok {
			t.Errorf("%s: %q, ok; want not ok", c.name, got)
		}
	}
}

// TestSelectedAppFields: a field of the connection or of its edge that
// velox-graphql does not write, which an application's resolver may read
// the nodes for, gives ok false.
func TestSelectedAppFields(t *testing.T) {
	for _, c := range []struct {
		query string
		want  []string // nil: not ok
	}{
		{`{ owners { __typename totalCount pageInfo { hasNextPage } edges { __typename cursor node { name } } } }`, []string{"name"}},
		{`{ owners { nodes { name } } }`, nil},
		{`{ owners { edges { extra node { name } } } }`, nil},
	} {
		got, ok := gqlgraph.Selected(field(t, operation(t, c.query), "owners"), "Owner")
		if ok != (c.want != nil) || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s: %q, %v; want %q", c.query, got, ok, c.want)
		}
	}
}

// TestSelectedInterfaces: fragments on the interfaces the validator's
// definition of the type lists count, found on a field selected on it; a
// fragment on any other interface or a union gives ok false.
func TestSelectedInterfaces(t *testing.T) {
	for _, c := range []struct {
		query, field string
		want         []string // nil: not ok
	}{
		{`{ owner { ... on Named { name } } }`, "owner", nil},
		{`{ owners { edges { node { ... on Named { name } } } } }`, "owners", nil},
		{`{ owner { ...T } } fragment T on Thing { ... on Owner { age } }`, "owner", nil},
		{`{ owner { id ... on Keyed { id } } }`, "owner", []string{"id"}},
		{`{ owner { ... on Owner { name } } }`, "owner", []string{"name"}},
	} {
		got, ok := gqlgraph.Selected(field(t, operation(t, c.query), c.field), "Owner")
		if ok != (c.want != nil) || (ok && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("%s: %q, %v; want %q", c.query, got, ok, c.want)
		}
	}
}

// TestNarrow: Narrow keeps the columns the query selects, and nil columns
// keep the query.
func TestNarrow(t *testing.T) {
	db, _, log := edgeDB(t)
	q := untyped.Query(db, untyped.TableOf[owner]()).AllowUnbounded()
	idName := []int{int(owners.ID.I), int(owners.Name.I)}
	for _, c := range []struct {
		name string
		q    untyped.TableQuery
		want string
	}{
		{"every column", gqlgraph.Narrow(q, nil), `SELECT "id", "name", "age" FROM "owners"`},
		{"two", gqlgraph.Narrow(q, idName), `SELECT "id", "name" FROM "owners"`},
		{"never added", gqlgraph.Narrow(q.Columns(int(owners.ID.I), int(owners.Age.I)), idName), `SELECT "id" FROM "owners"`},
		{"none selected", gqlgraph.Narrow(q.Columns(int(owners.ID.I)), []int{int(owners.Age.I)}), `SELECT "id" FROM "owners"`},
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
