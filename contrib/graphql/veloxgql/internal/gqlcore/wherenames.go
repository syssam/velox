package gqlcore

import "context"

// WhereNames is what a generated where input tells of itself: its GraphQL
// name, "PostWhereInput", and the GraphQL name of each field of its struct,
// in order.
type WhereNames struct {
	Input  string
	Fields []string
}

// whereNamesKey is the context key of the slot a where input's names are
// put in.
type whereNamesKey struct{}

// AskWhereNames returns ctx with a slot for a where input's names, and the
// slot. A generated input's Predicate, called with the context on a nil
// input, fills it (TellWhereNames): this is how veloxgql learns the names,
// which only gqlgraph's Type knows.
func AskWhereNames(ctx context.Context) (context.Context, *WhereNames) {
	slot := new(WhereNames)
	return context.WithValue(ctx, whereNamesKey{}, slot), slot
}

// TellWhereNames puts names() in the slot of ctx, when it has one.
func TellWhereNames(ctx context.Context, names func() WhereNames) {
	if slot, ok := ctx.Value(whereNamesKey{}).(*WhereNames); ok {
		*slot = names()
	}
}
