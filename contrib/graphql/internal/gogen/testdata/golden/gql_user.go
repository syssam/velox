package gen

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// Account is User under its GraphQL type name, so that gqlgen's autobind finds
// the Go type of the GraphQL type Account.
type Account = User

// IsNode marks User as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*User) IsNode() {}

// IDGQL is the global ID of the User, the GraphQL field id: of the type Account
// and the key id.
func (x *User) IDGQL() string { return veloxgql.EncodeID("Account", x.ID) }

// MarshalGQL writes e as its value of the GraphQL enum UserRole.
func (e UserRole) MarshalGQL(w io.Writer) {
	var s string
	switch e {
	case "admin":
		s = "ADMIN"
	case "editor":
		s = "EDITOR"
	case "viewer":
		s = "VIEWER"
	default:
		s = string(e) // not a value of the enum
	}
	b, _ := json.Marshal(s) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum UserRole.
func (e *UserRole) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	switch s {
	case "ADMIN":
		*e = "admin"
	case "EDITOR":
		*e = "editor"
	case "VIEWER":
		*e = "viewer"
	default:
		return errors.New("UserRole must be ADMIN, EDITOR or VIEWER")
	}
	return nil
}

// AccountConnection is the GraphQL type AccountConnection, a connection to a
// list of Account. It holds the query of the whole list; gqlgen calls its
// methods for the fields a query selects, and each runs only then.
type AccountConnection struct {
	query velox.Query[User] // the whole list, before paging
}

// TotalCount is the number of Account nodes in the whole connection, not only
// on this page. It counts the rows when gqlgen asks for it, which is only when
// the field totalCount is selected.
func (c *AccountConnection) TotalCount(ctx context.Context) (int, error) {
	n, err := c.query.Count(ctx)
	return int(n), err
}

// AccountEdge is the GraphQL type AccountEdge, an edge in a connection to a
// list of Account.
type AccountEdge struct {
	// Node is the Account at the end of the edge.
	Node *User
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// AccountWhereInput is the GraphQL input AccountWhereInput, a filter of Account
// nodes: a node matches when every predicate given holds.
type AccountWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *AccountWhereInput
	And []*AccountWhereInput
	Or  []*AccountWhereInput

	// Predicates on the field name.
	DisplayName          *string
	DisplayNameNEQ       *string
	DisplayNameIn        []string
	DisplayNameNotIn     []string
	DisplayNameContains  *string
	DisplayNameHasPrefix *string
	DisplayNameHasSuffix *string

	// The edge posts: whether it has a node, or one that matches.
	HasPosts     *bool
	HasPostsWith []*PostWhereInput
}
