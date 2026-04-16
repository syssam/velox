package mixin

import (
	"context"

	"github.com/syssam/velox"
)

// AuditHook returns a hook that sets created_by and updated_by fields
// based on the actor extracted from context.
// If actorFromCtx returns empty string, no fields are set.
// On create: sets both created_by and updated_by.
// On update: sets only updated_by.
func AuditHook(actorFromCtx func(context.Context) string) velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			actor := actorFromCtx(ctx)
			if actor == "" {
				return next.Mutate(ctx, m)
			}
			if m.Op().Is(velox.OpCreate) {
				_ = m.SetField("created_by", actor)
			}
			_ = m.SetField("updated_by", actor)
			return next.Mutate(ctx, m)
		})
	}
}
