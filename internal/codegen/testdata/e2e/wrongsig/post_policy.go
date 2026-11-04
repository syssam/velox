//go:build ignore

package schema

import (
	"github.com/syssam/velox"

	"example.com/tmp/gen"
)

// Policy returns a pointer, which is not the signature velox binds.
func (Post) Policy() *velox.Policy[gen.Post] { return &velox.Policy[gen.Post]{} }
