//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type orgKey struct{}

// InOrg confines an entity's rows to the context's organization, and fills
// org_id on insert.
type InOrg struct{ mixin.Schema }

func (InOrg) Fields() []schema.Field {
	return []schema.Field{field.Int64("org_id").Immutable()}
}

func (InOrg) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		v, _ := ctx.Value(orgKey{}).(int64)
		return velox.FieldEQ("org_id", v), nil
	}}
}

func (InOrg) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		v, _ := ctx.Value(orgKey{}).(int64)
		return m.Set("org_id", v)
	}, velox.OpInsert)}
}

// Audited records who wrote a row; its By is configuration, so the
// generated code reads the value the schema returns.
type Audited struct {
	mixin.Schema
	By string
}

func (a *Audited) Hooks() []velox.Hook {
	by := a.By
	return []velox.Hook{func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m *velox.Mutation) error {
			_ = by
			return next.Mutate(ctx, m)
		})
	}}
}
