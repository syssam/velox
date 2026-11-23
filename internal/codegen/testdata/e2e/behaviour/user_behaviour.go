//go:build ignore

package schema

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
)

// posterKey marks a context as a poster's: such a context reads only the users
// who have a post.
type posterKey struct{}

// configuredKey marks a context whose poster rule takes its names from
// configuration, as a shared rule package does.
type configuredKey struct{}

// AsPoster returns ctx acting as a poster.
func AsPoster(ctx context.Context) context.Context {
	return context.WithValue(ctx, posterKey{}, true)
}

// AsConfiguredPoster returns ctx acting as a poster whose rule names the
// entity by its table and the relation by values from configuration, which
// velox-gen cannot read: velox finds both in the entity table when the
// statement runs.
func AsConfiguredPoster(ctx context.Context) context.Context {
	return context.WithValue(ctx, configuredKey{}, true)
}

// ruleConfig is a rule's names, as an application's configuration gives them.
type ruleConfig struct{ column, table, key, edge string }

// posterRule is the poster's condition from cfg: the users with a post, by a
// subquery on the table and by the relation, each under Post's Read policy.
func posterRule(cfg ruleConfig) velox.Predicate {
	return velox.And(velox.FieldInSelect(cfg.column, cfg.table, cfg.key), velox.HasEdge(cfg.edge))
}

// Policy shows a poster the users who have a post, as the Post's own Read
// policy lets that context see it: a subquery on another entity, by name.
func (User) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context, _ *velox.DB) (velox.Predicate, error) {
		if v, _ := ctx.Value(configuredKey{}).(bool); v {
			return posterRule(ruleConfig{column: "id", table: "posts", key: "author_id", edge: "posts"}), nil
		}
		if v, _ := ctx.Value(posterKey{}).(bool); v {
			return velox.FieldInSelect("id", "Post", "author_id"), nil
		}
		return velox.Allow(), nil
	}}
}

// HookRuns counts the inserts of Users that the hook saw.
var HookRuns atomic.Int64

// Hooks gives every inserted User without a nickname the nickname "hooked".
func (User) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(nickname, velox.OpInsert)}
}

func nickname(_ context.Context, m *velox.Mutation) error {
	HookRuns.Add(1)
	return m.Fill("nickname", "hooked")
}
