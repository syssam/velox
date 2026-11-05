//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"
)

// Hooks is Ticket's own, which runs inside its mixins'.
func (Ticket) Hooks() []velox.Hook {
	return []velox.Hook{velox.Before(func(context.Context, *velox.Mutation) error {
		Trace = append(Trace, "own")
		return nil
	}, velox.OpInsert)}
}
