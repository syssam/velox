// Package schema is the schema-shape matrix: one small entity (or a few) per
// shape the generator must handle. See ../README.md.
package schema

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type denyCtxKey struct{}

// DenyContext makes every context-driven policy in this package deny.
func DenyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, denyCtxKey{}, true)
}

// denyOnMarker denies under DenyContext and skips (allows) otherwise.
func denyOnMarker() privacy.QueryMutationRule {
	return privacy.ContextQueryMutationRule(func(ctx context.Context) error {
		if v, _ := ctx.Value(denyCtxKey{}).(bool); v {
			return privacy.Deny
		}
		return privacy.Skip
	})
}

// stampHook writes "<prefix>:<op>" into column on every non-delete mutation
// and counts its runs in counter.
func stampHook(column, prefix string, counter *atomic.Int64) velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			counter.Add(1)
			if !m.Op().Is(velox.OpDelete | velox.OpDeleteOne) {
				if err := m.SetField(column, prefix+":"+m.Op().String()); err != nil {
					return nil, err
				}
			}
			return next.Mutate(ctx, m)
		})
	}
}

// PolicyOnly is shape 1: no defaults, validators or hooks — the schema-level
// Policy() is the only runtime code the entity has.
type PolicyOnly struct{ velox.Schema }

// Fields of the PolicyOnly.
func (PolicyOnly) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// Policy of the PolicyOnly.
func (PolicyOnly) Policy() velox.Policy {
	return privacy.Policy{
		Query:    privacy.QueryPolicy{denyOnMarker()},
		Mutation: privacy.MutationPolicy{denyOnMarker()},
	}
}

// HookOnlyRuns counts runs of HookOnly's schema hook.
var HookOnlyRuns atomic.Int64

// HookOnly is shape 2: schema-level Hooks() is the only runtime code.
type HookOnly struct{ velox.Schema }

// Fields of the HookOnly.
func (HookOnly) Fields() []velox.Field {
	return []velox.Field{
		field.String("name"),
		field.String("stamp").Optional(),
	}
}

// Hooks of the HookOnly.
func (HookOnly) Hooks() []velox.Hook {
	return []velox.Hook{stampHook("stamp", "schema", &HookOnlyRuns)}
}

// MixinRuns counts runs of StampMixin's hook.
var MixinRuns atomic.Int64

// StampMixin carries a column, a hook that stamps it and a policy.
type StampMixin struct{ mixin.Schema }

// Fields of the StampMixin.
func (StampMixin) Fields() []velox.Field {
	return []velox.Field{field.String("stamp").Optional()}
}

// Hooks of the StampMixin.
func (StampMixin) Hooks() []velox.Hook {
	return []velox.Hook{stampHook("stamp", "mixin", &MixinRuns)}
}

// Policy of the StampMixin.
func (StampMixin) Policy() velox.Policy {
	return privacy.Policy{
		Query:    privacy.QueryPolicy{denyOnMarker()},
		Mutation: privacy.MutationPolicy{denyOnMarker()},
	}
}

// MixinOnly is shape 3: its hooks and policy come only from a mixin.
type MixinOnly struct{ velox.Schema }

// Mixin of the MixinOnly.
func (MixinOnly) Mixin() []velox.Mixin { return []velox.Mixin{StampMixin{}} }

// Fields of the MixinOnly.
func (MixinOnly) Fields() []velox.Field {
	return []velox.Field{field.String("name")}
}

// TenantDoc is shape 4: a row filter on reads and writes.
type TenantDoc struct{ velox.Schema }

// Fields of the TenantDoc.
func (TenantDoc) Fields() []velox.Field {
	return []velox.Field{
		field.String("tenant_id"),
		field.String("title"),
	}
}

// Policy of the TenantDoc.
func (TenantDoc) Policy() velox.Policy {
	return privacy.Policy{
		Query:    privacy.QueryPolicy{privacy.TenantFilterRule("tenant_id")},
		Mutation: privacy.MutationPolicy{privacy.TenantFilterRule("tenant_id")},
	}
}
