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

// WithOrg is ctx acting for organization id.
func WithOrg(ctx context.Context, id int64) context.Context {
	return velox.WithScopeValue(context.WithValue(ctx, orgKey{}, id), "org", id)
}

func orgOf(ctx context.Context) int64 { v, _ := ctx.Value(orgKey{}).(int64); return v }

// Trace records the hooks that ran, in order.
var Trace []string

// Tenant confines rows to the context's organization and fills org_id.
type Tenant struct{ mixin.Schema }

func (Tenant) Fields() []schema.Field { return []schema.Field{field.Int64("org_id").Immutable()} }

func (Tenant) Policy[E any]() velox.Policy[E] {
	org := velox.Field[E, int64]("org_id")
	return velox.Policy[E]{Read: func(ctx context.Context) (velox.Pred[E], error) { return org.EQ(orgOf(ctx)), nil }}
}

func (Tenant) Hooks[E any]() []velox.Hook[E] {
	org := velox.Field[E, int64]("org_id")
	return []velox.Hook[E]{velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			Trace = append(Trace, "tenant")
			for _, x := range m.Rows() {
				*org.Ptr(x) = orgOf(ctx)
			}
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)}
}

// Hidden hides the rows whose hidden is set.
type Hidden struct{ mixin.Schema }

func (Hidden) Fields() []schema.Field { return []schema.Field{field.Bool("hidden").Default(false)} }

func (Hidden) Interceptors[E any]() []velox.Interceptor[E] {
	hidden := velox.Field[E, bool]("hidden")
	return []velox.Interceptor[E]{{Filter: func(context.Context) (velox.Pred[E], error) { return hidden.EQ(false), nil }}}
}

// Stamp records its Prefix: a configured mixin, which the generated code
// reads from the schema's Mixin method.
type Stamp struct {
	mixin.Schema
	Prefix string
}

func (s *Stamp) Hooks[E any]() []velox.Hook[E] {
	p := s.Prefix
	return []velox.Hook[E]{velox.On(func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			Trace = append(Trace, p)
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)}
}

// Ticket lists the three mixins.
type Ticket struct{ schema.Schema }

func (Ticket) Mixin() []schema.Mixin {
	return []schema.Mixin{Tenant{}, Hidden{}, &Stamp{Prefix: "stamp"}}
}

func (Ticket) Fields() []schema.Field { return []schema.Field{field.String("title")} }
