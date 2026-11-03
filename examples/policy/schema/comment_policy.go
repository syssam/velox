package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/gen"
)

// Policy declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (Comment) Policy() velox.Policy[gen.Comment] {
	return velox.Policy[gen.Comment]{
		Read: func(context.Context) (velox.Pred[gen.Comment], error) {
			return gen.Comments.Doc.Has(), nil
		},
	}
}
