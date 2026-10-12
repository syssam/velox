package gqlinput

// OrderDirection is the direction of an ordering that arrives in a request:
// the Direction of a generated order input.
type OrderDirection string

// The two directions, spelled as GraphQL spells them.
const (
	OrderDirectionAsc  OrderDirection = "ASC"
	OrderDirectionDesc OrderDirection = "DESC"
)
