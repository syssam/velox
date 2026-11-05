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

func (Tenant) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context) (velox.Cond, error) { return velox.C("org_id").EQ(orgOf(ctx)), nil }}
}

func (Tenant) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(ctx context.Context, m *velox.Mutation) error {
		Trace = append(Trace, "tenant")
		return m.Set("org_id", orgOf(ctx))
	}, velox.OpInsert)}
}

// Hidden hides the rows whose hidden is set.
type Hidden struct{ mixin.Schema }

func (Hidden) Fields() []schema.Field { return []schema.Field{field.Bool("hidden").Default(false)} }

func (Hidden) Interceptors() []velox.Interceptor {
	return []velox.Interceptor{{Filter: func(context.Context) (velox.Cond, error) { return velox.C("hidden").EQ(false), nil }}}
}

// Stamp records its Prefix: a configured mixin, which the generated code
// reads from the schema's Mixin method.
type Stamp struct {
	mixin.Schema
	Prefix string
}

func (s *Stamp) Hooks() []velox.Hook {
	p := s.Prefix
	return []velox.Hook{velox.Before(func(context.Context, *velox.Mutation) error {
		Trace = append(Trace, p)
		return nil
	}, velox.OpInsert)}
}

// Ticket lists the three mixins.
type Ticket struct{ schema.Schema }

func (Ticket) Mixin() []schema.Mixin {
	return []schema.Mixin{Tenant{}, Hidden{}, &Stamp{Prefix: "stamp"}}
}

func (Ticket) Fields() []schema.Field { return []schema.Field{field.String("title")} }
