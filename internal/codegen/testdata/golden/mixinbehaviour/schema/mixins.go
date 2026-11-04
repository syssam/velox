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

func (InOrg) Policy[E any]() velox.Policy[E] {
	org := velox.Field[E, int64]("org_id")
	return velox.Policy[E]{Read: func(ctx context.Context) (velox.Pred[E], error) {
		v, _ := ctx.Value(orgKey{}).(int64)
		return org.EQ(v), nil
	}}
}

func (InOrg) Hooks[E any]() []velox.Hook[E] {
	org := velox.Field[E, int64]("org_id")
	return []velox.Hook[E]{velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			v, _ := ctx.Value(orgKey{}).(int64)
			for _, x := range m.Rows() {
				*org.Ptr(x) = v
			}
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)}
}

// Audited records who wrote a row; its By is configuration, so the
// generated code reads the value the schema returns.
type Audited struct {
	mixin.Schema
	By string
}

func (a *Audited) Hooks[E any]() []velox.Hook[E] {
	by := a.By
	return []velox.Hook[E]{func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			_ = by
			return next.Mutate(ctx, m)
		})
	}}
}
