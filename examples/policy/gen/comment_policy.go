package gen

import (
	"context"

	"github.com/syssam/velox2"
)

// Policy declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (*Comment) Policy() velox.Policy[Comment] {
	return velox.Policy[Comment]{
		Read: func(context.Context) (velox.Pred[Comment], error) {
			return Comments.Doc.Has(), nil
		},
	}
}
