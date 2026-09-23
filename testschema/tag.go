package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type (
	denyTagQueryCtxKey         struct{}
	filterTagQueryPrefixCtxKey struct{}
)

// DenyTagQueryContext makes the Tag query policy deny. Tag is the target of
// a many-to-many edge (Post.tags) with named-edge variants, so this is how
// the integration tests reach a policy Deny through an M2M and a named
// eager load. Without the marker the policy skips.
func DenyTagQueryContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, denyTagQueryCtxKey{}, true)
}

// FilterTagQueryToPrefixContext makes the Tag query policy narrow every
// Tag read to names starting with prefix: a row filter on the target of a
// many-to-many edge, which the M2M eager loader must apply exactly as the
// entity-level edge query does.
func FilterTagQueryToPrefixContext(ctx context.Context, prefix string) context.Context {
	return context.WithValue(ctx, filterTagQueryPrefixCtxKey{}, prefix)
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

// Policy denies Tag reads under DenyTagQueryContext, narrows them under
// FilterTagQueryToPrefixContext, and skips otherwise.
func (Tag) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if v, _ := ctx.Value(denyTagQueryCtxKey{}).(bool); v {
					return privacy.Deny
				}
				return privacy.Skip
			}),
			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
				prefix, ok := ctx.Value(filterTagQueryPrefixCtxKey{}).(string)
				if !ok {
					return privacy.Skip
				}
				f.WhereP(func(s *sql.Selector) {
					s.Where(sql.HasPrefix(s.C("name"), prefix))
				})
				return privacy.Skip
			}),
		},
	}
}
