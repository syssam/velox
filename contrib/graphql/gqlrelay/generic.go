package gqlrelay

// Connection is a generic Relay connection type for cursor-based pagination.
//
// Velox's own codegen does NOT use this — it emits concrete per-entity
// Connection/Edge structs to avoid generic monomorphization in the hot path
// and to keep the public Go API explicit (e.g., `*entity.UserConnection`
// rather than `*Connection[User]`).
//
// This is provided as a convenience for users who want to build their own
// pagination scaffolding outside the generator, e.g.:
//
//	type CustomConnection = gqlrelay.Connection[MyDTO]
type Connection[T any] struct {
	Edges      []*Edge[T] `json:"edges"`
	PageInfo   PageInfo   `json:"pageInfo"`
	TotalCount int        `json:"totalCount"`
}

// Edge is a generic Relay edge type wrapping a node with its cursor.
// See Connection for usage notes — velox's codegen does not use this type.
type Edge[T any] struct {
	Node   *T     `json:"node"`
	Cursor Cursor `json:"cursor"`
}
