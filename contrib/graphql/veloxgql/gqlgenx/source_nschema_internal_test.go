package gqlgenx

import (
	"context"
	"slices"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

// noSchemaSDL is the schema of the tests of a selection read with no
// schema.
const noSchemaSDL = `
interface Node { id: ID! }
type User implements Node { id: ID! name: String }
type Post { id: ID! }
union Result = User | Post
type Query { user: User }
`

// userSelection loads query against noSchemaSDL and returns its operation
// context and the selections of its user field.
func userSelection(t *testing.T, query string) (*graphql.OperationContext, ast.SelectionSet) {
	t.Helper()
	schema, err := gqlparser.LoadSchema(&ast.Source{Name: "schema", Input: noSchemaSDL})
	if err != nil {
		t.Fatal(err)
	}
	doc, errs := gqlparser.LoadQueryWithRules(schema, query, nil)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	user := doc.Operations[0].SelectionSet[0].(*ast.Field)
	return &graphql.OperationContext{Doc: doc}, user.SelectionSet
}

// onNames is the names of the fields sel selects on typ.
func onNames(t *testing.T, sel *selection, typ string) ([]string, bool) {
	t.Helper()
	fs, ok := sel.On(typ)
	var names []string
	for _, f := range fs {
		names = append(names, f.Name)
	}
	return names, ok
}

// TestSelectionOnNoSchema: with no schema, a selection reads the interfaces
// of a type from the definition the validator gave its fields, takes the
// fields of fragments on the type or those interfaces, and fails a fragment
// on a union, inline or spread.
func TestSelectionOnNoSchema(t *testing.T) {
	ok := []struct {
		name, query string
		want        []string
	}{
		{"fields", `{ user { id name } }`, []string{"id", "name"}},
		{"interface fragments", `{ user { ... on Node { id } ...N ...N name } } fragment N on Node { id }`, []string{"id", "name"}},
		{"fragment on the type", `{ user { ...U } } fragment U on User { name }`, []string{"name"}},
		{"nested in an inline fragment", `{ user { ... on User { ...U } } } fragment U on User { name }`, []string{"name"}},
	}
	for _, tc := range ok {
		oc, sels := userSelection(t, tc.query)
		sel := &selection{src: &source{}, oc: oc, sels: sels}
		got, good := onNames(t, sel, "User")
		slices.Sort(got)
		want := slices.Clone(tc.want)
		slices.Sort(want)
		if !good || !slices.Equal(got, want) {
			t.Errorf("%s: On(User) = %v, %v; want %v", tc.name, got, good, want)
		}
	}
	for name, query := range map[string]string{
		"inline union":  `{ user { id ... on Result { __typename } } }`,
		"spread union":  `{ user { id ...R } } fragment R on Result { __typename }`,
		"nested union":  `{ user { ... on User { ...R } } } fragment R on Result { __typename }`,
		"inline in one": `{ user { ... on Node { ... on Result { __typename } } } }`,
	} {
		oc, sels := userSelection(t, query)
		sel := &selection{src: &source{}, oc: oc, sels: sels}
		if got, good := onNames(t, sel, "User"); good {
			t.Errorf("%s: On(User) = %v, true; want false", name, got)
		}
	}
}

// TestObjectDefAndAbstractFragment: objectDef finds a type's definition
// through fragments, once for each fragment name and not at all without an
// operation's document or a fragment's definition; abstractFragment skips
// a spread twice or one with no definition.
func TestObjectDefAndAbstractFragment(t *testing.T) {
	user := &ast.Definition{Name: "User", Kind: ast.Object, Interfaces: []string{"Node"}}
	spread := &ast.FragmentSpread{Name: "U"}
	named := &ast.FragmentDefinition{Name: "U", SelectionSet: ast.SelectionSet{&ast.Field{Name: "id", ObjectDefinition: user}}}
	oc := &graphql.OperationContext{Doc: &ast.QueryDocument{Fragments: ast.FragmentDefinitionList{named}}}
	sels := ast.SelectionSet{spread, spread}

	if d := objectDef(oc, sels, "User", map[string]bool{}); d != user {
		t.Errorf("through a fragment spread: %v, want User", d)
	}
	if d := objectDef(oc, sels, "Post", map[string]bool{}); d != nil {
		t.Errorf("another type: %v, want nil", d)
	}
	if d := objectDef(nil, sels, "User", map[string]bool{}); d != nil {
		t.Errorf("with no operation context: %v, want nil", d)
	}
	if d := objectDef(&graphql.OperationContext{}, sels, "User", map[string]bool{}); d != nil {
		t.Errorf("with no document: %v, want nil", d)
	}
	missing := ast.SelectionSet{&ast.FragmentSpread{Name: "Gone"}}
	if d := objectDef(oc, missing, "User", map[string]bool{}); d != nil {
		t.Errorf("a spread with no definition: %v, want nil", d)
	}
	if abstractFragment(oc, ast.SelectionSet{spread, spread, &ast.FragmentSpread{Name: "Gone"}}, []string{"User"}, map[string]bool{}) {
		t.Error("spreads of an object fragment, seen twice or missing: abstract, want not")
	}
}

// TestSourceFieldWithoutOperation: a field context with no operation
// context has a key and no selection, and Wait of the source and of the
// reader return a function that does nothing.
func TestSourceFieldWithoutOperation(t *testing.T) {
	fc := &graphql.FieldContext{Object: "Query", Field: graphql.CollectedField{Field: &ast.Field{Name: "x"}}}
	ctx := graphql.WithFieldContext(context.Background(), fc)
	f, ok := (&source{}).Field(ctx)
	if !ok || f.Key == nil || f.Sel != nil {
		t.Errorf("no operation context: %+v, %v; want a key and no selection", f, ok)
	}
	(&source{}).Wait(ctx)()
	reader{}.Wait(ctx)()
}

// TestSourceFieldWithoutASTField: a field context with no *ast.Field gives
// a field with no name, key or selection, read alone, and no panic.
func TestSourceFieldWithoutASTField(t *testing.T) {
	fc := &graphql.FieldContext{Object: "Query"}
	ctx := graphql.WithFieldContext(context.Background(), fc)
	f, ok := (&source{}).Field(ctx)
	if !ok || f.Object != "Query" || f.Name != "" || f.Key != nil || f.Sel != nil {
		t.Errorf("no *ast.Field: %+v, %v; want only the object", f, ok)
	}
}
