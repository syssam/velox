//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/scope"
)

// Org carries the organisation a request acts for.
var Org = scope.NewKey[int64]("org")

// Policy confines notes to the context's organisation, and refuses a note
// of another one.
func (Note) Policy() velox.Policy {
	return velox.Policy{
		Read: func(ctx context.Context) (velox.Cond, error) {
			org, err := Org.Require(ctx)
			if err != nil {
				return velox.Cond{}, err
			}
			return velox.C("org_id").EQ(org), nil
		},
		Insert: func(ctx context.Context, r velox.Row) error { return Org.Match(ctx, r.Value("org_id")) },
	}
}

// Hooks stamps org_id on inserted notes that leave it zero.
func (Note) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		if org, ok := Org.From(ctx); ok {
			return m.Fill("org_id", org)
		}
		return nil
	}, velox.OpInsert)}
}
