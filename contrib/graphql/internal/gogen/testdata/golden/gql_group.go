package gen

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql"
)

// IsNode marks Group as a GraphQL Node: gqlgen binds the interface Node to
// veloxgql.Noder.
func (*Group) IsNode() {}

// IDGQL is the global ID of the Group, the GraphQL field id: the type Group and
// the key id, encoded.
func (x *Group) IDGQL() string { return veloxgql.EncodeID("Group", x.ID) }

// MarshalGQL writes e as a GraphQL String, its stored value: the field
// visibility of Group is a String, as graphql.Skip(graphql.SkipEnumField) asks.
func (e GroupVisibility) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(e)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets e from v, a String that is one of its stored values.
func (e *GroupVisibility) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	switch s {
	case "public", "private":
		*e = GroupVisibility(s)
		return nil
	}
	return errors.New("GroupVisibility must be public or private")
}

// GroupConnection is the GraphQL type GroupConnection, a connection to a list
// of Group. It holds the query of the whole list; gqlgen calls its methods for
// the fields a query selects, and each runs only then.
type GroupConnection struct {
	query velox.Query[Group] // the whole list, before paging
}

// TotalCount is the number of Group nodes in the whole connection, not only on
// this page. It counts the rows when gqlgen asks for it, which is only when the
// field totalCount is selected.
func (c *GroupConnection) TotalCount(ctx context.Context) (int, error) {
	n, err := c.query.Count(ctx)
	return int(n), err
}

// GroupEdge is the GraphQL type GroupEdge, an edge in a connection to a list of
// Group.
type GroupEdge struct {
	// Node is the Group at the end of the edge.
	Node *Group
	// Cursor is the cursor of the edge, for paging.
	Cursor veloxgql.Cursor
}

// GroupOrder is the GraphQL input GroupOrder, an order of connections to Group.
type GroupOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field GroupOrderField
}

// GroupOrderField is the GraphQL enum GroupOrderField, the fields connections
// to Group can be ordered by.
type GroupOrderField string

// The values of GroupOrderField.
const (
	// GroupOrderFieldName orders by the field name.
	GroupOrderFieldName GroupOrderField = "NAME"
)

// IsValid reports whether o is a value of GroupOrderField.
func (o GroupOrderField) IsValid() bool {
	switch o {
	case "NAME":
		return true
	}
	return false
}

// String returns o's value.
func (o GroupOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum GroupOrderField.
func (o GroupOrderField) MarshalGQL(w io.Writer) {
	b, _ := json.Marshal(string(o)) // never fails for a string
	_, _ = w.Write(b)
}

// UnmarshalGQL sets o from v, a value of the GraphQL enum GroupOrderField.
func (o *GroupOrderField) UnmarshalGQL(v any) error {
	s, _ := v.(string)
	if !GroupOrderField(s).IsValid() {
		return errors.New("GroupOrderField must be NAME")
	}
	*o = GroupOrderField(s)
	return nil
}
