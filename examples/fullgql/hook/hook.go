// Package hook defines reusable mutation hooks for the fullgql example.
// These are referenced from schema definitions and demonstrate the Ent-style
// pattern of separating hook logic from schema declarations.
package hook

import (
	"context"
	"strings"

	"github.com/syssam/velox"
)

// NormalizeName returns a hook that title-cases the "name" field.
func NormalizeName() velox.Hook {
	return func(next velox.Mutator) velox.Mutator {
		return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
			if name, ok := m.Field("name"); ok {
				if s, ok := name.(string); ok && len(s) > 0 {
					m.SetField("name", strings.ToUpper(s[:1])+s[1:])
				}
			}
			return next.Mutate(ctx, m)
		})
	}
}
