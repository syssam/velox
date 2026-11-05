//go:build ignore

package schema

import "github.com/syssam/velox"

// Bad's Policy returns a pointer, which is not the signature velox binds.
type Bad struct{}

func (Bad) Policy() *velox.Policy { return &velox.Policy{} }
