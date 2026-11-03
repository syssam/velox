//go:build ignore

package schema

import (
	"github.com/syssam/velox"

	"example.com/tmp/gen/entity"
)

// Policy returns a pointer, which is not the signature velox binds.
func (Post) Policy() *velox.Policy[entity.Post] { return &velox.Policy[entity.Post]{} }
