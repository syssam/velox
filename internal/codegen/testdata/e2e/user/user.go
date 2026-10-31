package gen

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
)

// HookRuns counts the inserts of Users that the hook saw.
var HookRuns atomic.Int64

// Hooks gives every inserted User without a nickname the nickname "hooked".
func (*User) Hooks() []velox.Hook[User] {
	return []velox.Hook[User]{velox.On(nickname, velox.OpInsert)}
}

func nickname(next velox.Mutator[User]) velox.Mutator[User] {
	return velox.MutateFunc[User](func(ctx context.Context, m *velox.Mutation[User]) error {
		HookRuns.Add(1)
		for _, x := range m.Rows() {
			if x.Nickname == "" {
				x.Nickname = "hooked"
			}
		}
		return next.Mutate(ctx, m)
	})
}

// UserDesc is User's metadata as the generated code builds it, for the
// external test to read the column defaults.
func UserDesc() velox.Desc[User] { return userDesc() }
