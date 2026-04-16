// Package schema defines the entity schemas for the basic example.
package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"
)

// User privacy enforcement is opt-in via context to keep the existing
// integration tests working without threading auth through every call.
//
// EnforceUserPrivacyContext flips the policy into "deny by default" mode.
// Within that mode, AllowWriteContext grants permission. Tests that do not
// set the enforce flag bypass the rule (Skip → policy chain returns nil
// → mutation allowed).
type (
	enforceUserPrivacyCtxKey   struct{}
	allowWriteCtxKey           struct{}
	filterToNameCtxKey         struct{}
	filterMutationToNameCtxKey struct{}
)

// EnforceUserPrivacyContext opts the request into User privacy enforcement.
// Without this, the policy rule short-circuits to Skip.
func EnforceUserPrivacyContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, enforceUserPrivacyCtxKey{}, true)
}

// AllowWriteContext authorizes User mutations under enforced privacy.
func AllowWriteContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, allowWriteCtxKey{}, true)
}

// FilterUserQueryToNameContext returns a context that the query
// filter reads to narrow subsequent User queries to a single name.
// Demonstrates the "current user only" pattern without needing an
// owner-ID column on the testschema User.
func FilterUserQueryToNameContext(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, filterToNameCtxKey{}, name)
}

// FilterUserMutationToNameContext returns a context that the mutation
// filter reads to narrow subsequent User updates/deletes to a single
// name. Counterpart of FilterUserQueryToNameContext — pins that
// privacy.FilterFunc works on mutations too, not just queries.
func FilterUserMutationToNameContext(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, filterMutationToNameCtxKey{}, name)
}

// User holds the schema definition for the User entity.
type User struct {
	velox.Schema
}

// Mixin of the User.
func (User) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{}, // Adds created_at and updated_at
	}
}

// Fields of the User.
func (User) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").
			NotEmpty().
			MaxLen(100),
		field.String("email").
			Unique().
			NotEmpty(),
		field.Int("age").
			Optional().
			Positive(),
		field.Enum("role").
			Values("admin", "user", "guest").
			Default("user"),
	}
}

// Edges of the User.
func (User) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("posts", Post.Type).
			Comment("Posts written by this user"),
		edge.To("comments", Comment.Type).
			Comment("Comments written by this user"),
	}
}

// Policy enforces User mutation auth, but only when the request context
// has opted in via EnforceUserPrivacyContext. This keeps existing tests
// (which do not thread auth through every call) working while still
// exercising the full privacy chain from the e2e privacy tests.
func (User) Policy() velox.Policy {
	return privacy.Policy{
		Query: privacy.QueryPolicy{
			// Row-level filter demo: when the request carries a
			// filter-to-name value, inject a WHERE name = <value>
			// on the query. Uses FilterFunc so the rule doesn't
			// know about the concrete query type — velox's
			// generated Filter() method satisfies privacy.Filter.
			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
				name, ok := ctx.Value(filterToNameCtxKey{}).(string)
				if !ok {
					return privacy.Skip
				}
				f.WhereP(func(s *sql.Selector) {
					s.Where(sql.EQ(s.C("name"), name))
				})
				return privacy.Skip
			}),
			// Deny-by-default under enforcement, mirroring the
			// mutation rule: reads under EnforceUserPrivacyContext
			// require an AllowWriteContext marker, otherwise the
			// read fails with privacy.Deny.
			privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if v, _ := ctx.Value(enforceUserPrivacyCtxKey{}).(bool); !v {
					return privacy.Skip
				}
				if a, _ := ctx.Value(allowWriteCtxKey{}).(bool); a {
					return privacy.Allow
				}
				return privacy.Deny
			}),
		},
		Mutation: privacy.MutationPolicy{
			// Row-level filter on mutations: when the request carries
			// a filter-to-name value, inject a WHERE name = <value>
			// into the UPDATE/DELETE via the generated Filter()
			// method on *UserMutation. Pins that privacy.FilterFunc
			// works on mutations, not just queries.
			privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
				name, ok := ctx.Value(filterMutationToNameCtxKey{}).(string)
				if !ok {
					return privacy.Skip
				}
				f.WhereP(func(s *sql.Selector) {
					s.Where(sql.EQ(s.C("name"), name))
				})
				return privacy.Skip
			}),
			privacy.ContextQueryMutationRule(func(ctx context.Context) error {
				if v, _ := ctx.Value(enforceUserPrivacyCtxKey{}).(bool); !v {
					return privacy.Skip
				}
				if a, _ := ctx.Value(allowWriteCtxKey{}).(bool); a {
					return privacy.Allow
				}
				return privacy.Deny
			}),
		},
	}
}

// Indexes of the User.
func (User) Indexes() []velox.Index {
	return []velox.Index{
		index.Fields("email").
			Unique(),
		index.Fields("role", "created_at"),
	}
}
