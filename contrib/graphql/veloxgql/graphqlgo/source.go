package graphqlgo

import (
	"context"

	"github.com/syssam/graphql-go"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// source is graphql-go's selection source, which the package registers with
// the core when it is linked (decision 0173): an executor needs no option
// for generated code to find the field it resolves and batch its edges. A
// server links the package through Scalars or ScalarsOnly, or a blank
// import. The field of a context is graphql-go's field context
// (graphql.FieldFrom), which graphql-go gives a Resolve or a ResolveArgs
// function and not a Field accessor: generated code reaches gqlgraph only
// from methods that take a context, which gqlc binds with Resolve. Its
// table is in the operation's values (tableOf), so each operation, and
// each event of a subscription, has its own.
//
// A field's occurrence is its parent type, its name and its sub-selection,
// a graphql.Selection: a struct of one pointer to the plan's selection set,
// which graphql-go makes once per field of a plan, so every row of a list
// shares it and two aliases have two. Two fields that share one set select
// the same, and a batch they share reads what each needs; one whose
// arguments differ from the batch's first caller's reads alone. Its
// selection on a type is Selection.ForType(typ).Fields(), whose fragments,
// abstract ones too, and @skip and @include graphql-go has folded: On's ok
// is always true. SelectedField.Withheld is not consulted: a column read
// for a field authorization withholds costs bytes, not correctness.
//
// Wait parks the waiting goroutine in the operation's wave coordinator
// (OperationContext.Waves), so that a resolver waiting on velox's batch never
// holds back the wave of an application's loader.
type source struct{}

var _ gqlcore.Reader = source{}

// In reports whether ctx is in a graphql-go field.
func (source) In(ctx context.Context) bool {
	fc := graphql.FieldFrom(ctx)
	return fc != nil && fc.Field != nil && fc.Object != nil
}

// occurrence is the Key of a field: its parent type, its name and its
// sub-selection.
type occurrence struct {
	object, name string
	sel          graphql.Selection
}

// selection is a graphql.Selection, read as gqlcore reads selections.
type selection struct{ s graphql.Selection }

// Field is the field of graphql-go's field context in ctx. A leaf's Key
// and Sel are nil, as it has no sub-selection: the caller reads alone, and
// every column.
func (source) Field(ctx context.Context) (gqlcore.Field, bool) {
	fc := graphql.FieldFrom(ctx)
	if fc == nil || fc.Field == nil || fc.Object == nil {
		return gqlcore.Field{}, false
	}
	f := gqlcore.Field{Object: fc.Object.Name, Name: fc.Field.Name, Table: tableOf(graphql.OperationFrom(ctx))}
	if fc.Field.Type != nil {
		f.Type = fc.Field.Type.Name()
	}
	if sel := fc.Selection(); !sel.IsEmpty() {
		f.Key = occurrence{f.Object, f.Name, sel}
		f.Sel = selection{sel}
	}
	return f, true
}

// Wait parks the caller in the wave coordinator of ctx's operation, and
// returns the function that unparks it. Outside an operation's execution
// it does nothing.
func (source) Wait(ctx context.Context) func() {
	w := graphql.OperationFrom(ctx).Waves()
	if w == nil {
		return noWait
	}
	w.Park()
	return w.Unpark
}

// noWait is Wait's done function outside an operation.
func noWait() {}

// tableKey is the key of the table in an operation's values.
type tableKey struct{}

// tableOf is oc's table, kept in its values (OperationContext.GetOrSet),
// which graphql-go makes anew for each operation and each event of a
// subscription: so are the tables. It is nil for a nil oc.
func tableOf(oc *graphql.OperationContext) *gqlcore.Table {
	if oc == nil {
		return nil
	}
	if v, ok := oc.Get(tableKey{}); ok {
		return v.(*gqlcore.Table)
	}
	v, _ := oc.GetOrSet(tableKey{}, gqlcore.NewTable())
	return v.(*gqlcore.Table)
}

// On is the fields s selects on an object of the type typ, each with its
// sub-selection; a leaf's is nil.
func (s selection) On(typ string) ([]gqlcore.SelectedField, bool) {
	var out []gqlcore.SelectedField
	for f := range s.s.ForType(typ).Fields() {
		sf := gqlcore.SelectedField{Name: f.Name}
		if sub := f.Selection(); !sub.IsEmpty() {
			sf.Sel = selection{sub}
		}
		out = append(out, sf)
	}
	return out, true
}
