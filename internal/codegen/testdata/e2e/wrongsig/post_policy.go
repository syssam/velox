//go:build ignore

package schema

import "github.com/syssam/velox"

// Policy returns a pointer, which is not the signature velox binds.
func (Post) Policy() *velox.Policy { return &velox.Policy{} }
