package gqlgraph

import (
	"context"
	"fmt"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns, as gqlcore.Selected says:
// ok is false where the caller must read every column.
func selected(ctx context.Context, typ string) (fields []string, ok bool) {
	return gqlcore.Selected(ctx, typ)
}

// needSource fails with veloxgql.ErrNoSelectionSource, and ErrNoDB, when ctx
// holds no adapter's state: a read of generated code outside an adapter's
// operation would read every column and batch nothing, so it fails instead.
func needSource(ctx context.Context) error {
	if gqlcore.Source(ctx) != nil {
		return nil
	}
	return fmt.Errorf("veloxgql: %w, and %w: serve the schema through gqlgenx.NewExtension or graphqlgo.With, which puts both in each request's context", veloxgql.ErrNoDB, veloxgql.ErrNoSelectionSource)
}

// narrow returns q reading only those of cols, column indexes, that q
// selects; nil cols leave q as it is.
//
// It never adds a column q leaves out (TableQuery.Selects). When q selects
// none of cols, q is left as it is too.
func narrow(q untyped.TableQuery, cols []int) untyped.TableQuery {
	if cols == nil {
		return q
	}
	keep := make([]int, 0, len(cols))
	for _, c := range cols {
		if q.Selects(c) {
			keep = append(keep, c)
		}
	}
	if len(keep) == 0 {
		return q
	}
	return q.Columns(keep...)
}
