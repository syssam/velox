package veloxschema

import (
	"context"

	"github.com/syssam/velox"
)

// Policy declares Comment's policy by inheritance: a comment is visible when
// its doc is. Has is an EXISTS on the Doc, and velox applies Doc's own Read
// policy inside it, for this context.
func (Comment) Policy() velox.Policy {
	return velox.Policy{
		Read: func(context.Context) (velox.Predicate, error) { return velox.HasEdge("doc"), nil },
	}
}
