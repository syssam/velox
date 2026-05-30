package model

import "velox.test/parity/op"

// paginate is implemented in Task 4. This placeholder keeps the package
// compiling for the CRUD/JSON tasks; it is replaced with the real Relay
// in-memory slicing algorithm before any PaginatePosts op is exercised.
func paginate(_ []*post, _ op.PaginatePosts) Result {
	panic("model: paginate not yet implemented")
}
