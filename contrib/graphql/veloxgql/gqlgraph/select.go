package gqlgraph

import (
	"context"

	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
	"github.com/syssam/velox/untyped"
)

// selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns, as gqlcore.Selected says:
// ok is false where the caller must read every column.
func selected(ctx context.Context, typ string) (fields []string, ok bool) {
	return gqlcore.Selected(ctx, typ)
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
