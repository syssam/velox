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

// AsPoster returns ctx acting as a poster.
func AsPoster(ctx context.Context) context.Context {
	return context.WithValue(ctx, posterKey{}, true)
}

// Policy shows a poster the users who have a post, as the Post's own Read
// policy lets that context see it: a subquery on another entity, by name.
func (User) Policy() velox.Policy {
	return velox.Policy{Read: func(ctx context.Context) (velox.Predicate, error) {
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
