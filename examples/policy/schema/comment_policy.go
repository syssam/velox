package schema

import (
	"context"

	"github.com/syssam/velox"
	"github.com/syssam/velox/examples/policy/gen/entity"
)

// Policy declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (Comment) Policy() velox.Policy[entity.Comment] {
	return velox.Policy[entity.Comment]{
		Read: func(context.Context) (velox.Pred[entity.Comment], error) {
			return entity.Comments.Doc.Has(), nil
		},
	}
}
