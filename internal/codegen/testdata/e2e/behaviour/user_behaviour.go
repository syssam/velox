//go:build ignore

package schema

import (
	"context"
	"sync/atomic"

	"github.com/syssam/velox"
)

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
