//go:build ignore

package schema

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// HookRuns counts the inserts of Users that the hook saw.
var HookRuns atomic.Int64

// Hooks gives every inserted User without a nickname the nickname "hooked".
func (User) Hooks() []velox.Hook[gen.User] {
	return []velox.Hook[gen.User]{velox.On(nickname, velox.OpInsert)}
}

func nickname(next velox.Mutator[gen.User]) velox.Mutator[gen.User] {
	return velox.MutateFunc[gen.User](func(ctx context.Context, m *velox.Mutation[gen.User]) error {
		HookRuns.Add(1)
		for _, x := range m.Rows() {
			if x.Nickname == "" {
				x.Nickname = "hooked"
			}
		}
		return next.Mutate(ctx, m)
	})
}
