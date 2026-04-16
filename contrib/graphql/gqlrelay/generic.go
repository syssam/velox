package gqlrelay

// Connection is a generic Relay connection type for cursor-based pagination.
// Use as a type alias in generated code: type UserConnection = Connection[User]
type Connection[T any] struct {
	Edges      []*Edge[T] `json:"edges"`
	PageInfo   PageInfo   `json:"pageInfo"`
	TotalCount int        `json:"totalCount"`
}

// Edge is a generic Relay edge type wrapping a node with its cursor.
type Edge[T any] struct {
	Node   *T     `json:"node"`
	Cursor Cursor `json:"cursor"`
}
