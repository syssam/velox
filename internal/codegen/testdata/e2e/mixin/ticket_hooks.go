//go:build ignore

package schema

import (
	"context"

	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// Hooks is Ticket's own, which runs inside its mixins'.
func (Ticket) Hooks() []velox.Hook[gen.Ticket] {
	return []velox.Hook[gen.Ticket]{velox.On(func(next velox.Mutator[gen.Ticket]) velox.Mutator[gen.Ticket] {
		return velox.MutateFunc[gen.Ticket](func(ctx context.Context, m *velox.Mutation[gen.Ticket]) error {
			Trace = append(Trace, "own")
			return next.Mutate(ctx, m)
		})
	}, velox.OpInsert)}
}
