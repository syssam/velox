package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type denyTagQueryCtxKey struct{}

// DenyTagQueryContext makes the Tag query policy deny. Tag is the target of
// a many-to-many edge (Post.tags) with named-edge variants, so this is how
// the integration tests reach a policy Deny through an M2M and a named
// eager load. Without the marker the policy skips.
func DenyTagQueryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, denyTagQueryCtxKey{}, true)
}

// Tag holds the schema definition for the Tag entity.
type Tag struct {
	velox.Schema
}

// Fields of the Tag.
func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			Unique().
			NotEmpty().
			MaxLen(50),
	}
}

// Edges of the Tag.
func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("posts", Post.Type).
			Ref("tags"),
	}
}

// Policy denies Tag reads under DenyTagQueryContext and skips otherwise.
func (Tag) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if v, _ := ctx.Value(denyTagQueryCtxKey{}).(bool); v {
					return privacy.Deny
				}
				return privacy.Skip
			}),
		},
	}
}
