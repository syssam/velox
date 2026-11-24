package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion30

// Devices is the edge devices: a connection to the Device nodes it leads to.
// Its queries batch with the same edge of every row on a's page.
func (a *APIKey) Devices(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *DeviceOrder, where *DeviceWhereInput,
) (*DeviceConnection, error) {
	return newDeviceConnection(ctx, aPIKeyType.Edge(a, apiKeyEdgeDevices), after, first, before, last, orderBy, where)
}

// APIKeyConnection is the GraphQL type APIKeyConnection. Its fields run their
// queries when gqlgen resolves them.
type APIKeyConnection struct {
	c *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *APIKeyConnection) Edges(ctx context.Context) ([]*APIKeyEdge, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]APIKeyEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*APIKeyEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = APIKeyEdge{Node: p.Node(i).(*APIKey), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *APIKeyConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.c.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of APIKey nodes in the whole connection, not only on
// this page.
func (c *APIKeyConnection) TotalCount(ctx context.Context) (int, error) { return c.c.TotalCount(ctx) }

// APIKeyEdge is the GraphQL type APIKeyEdge.
type APIKeyEdge struct {
	Node   *APIKey
	Cursor veloxgql.Cursor
}

// APIKeyPaginateOption is an option of a query's Paginate to APIKey nodes:
// WithAPIKeyOrder or WithAPIKeyFilter. Of two options that set the same thing,
// the later wins.
type APIKeyPaginateOption struct {
	orderBy *APIKeyOrder
	where   *APIKeyWhereInput
}

// WithAPIKeyOrder orders the connection by orderBy and then the key.
func WithAPIKeyOrder(orderBy *APIKeyOrder) APIKeyPaginateOption {
	return APIKeyPaginateOption{orderBy: orderBy}
}

// WithAPIKeyFilter filters the connection by where.
func WithAPIKeyFilter(where *APIKeyWhereInput) APIKeyPaginateOption {
	return APIKeyPaginateOption{where: where}
}

// PaginateQuery is the connection to the APIKey nodes q returns, filtered by
// where, ordered by orderBy and then the key, and paged by the other arguments:
// velox.Query's Paginate. q must not be ordered, and must select the key and
// the order columns.
func (*APIKey) PaginateQuery(
	ctx context.Context, q velox.Query[APIKey],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []APIKeyPaginateOption,
) (*APIKeyConnection, error) {
	var o APIKeyPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
		if x.where != nil {
			o.where = x.where
		}
	}
	return newAPIKeyConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy, o.where)
}

// newAPIKeyConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newAPIKeyConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *APIKeyOrder, where *APIKeyWhereInput,
) (*APIKeyConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*APIKeyWhereInput).Predicate)}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &aPIKeyType, args)
	if err != nil {
		return nil, err
	}
	return &APIKeyConnection{c: c}, nil
}

// aPIKeyType describes the GraphQL type APIKey to gqlgraph.
var aPIKeyType = gqlgraph.Type{
	Name:  "APIKey",
	Table: untyped.TableOf[APIKey],
	Key:   []gqlgraph.Column{{Name: "id", Index: apiKeyColID}},
	Order: []gqlgraph.Column{
		{Name: "NAME", Index: apiKeyColName},
	},
	AlwaysRead: []int{apiKeyColID},
	Fields:     []string{apiKeyColID: "id", apiKeyColName: "name"},
	Edges:      []string{apiKeyEdgeDevices: "devices"},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality, Col: apiKeyColID},
		{Name: "name", Ops: gqlgraph.OpsEquality | gqlgraph.OpsText, Col: apiKeyColName},
	},
	Inputs:         []string{apiKeyColName: "name"},
	LenientFilters: true,
}

// APIKeyOrder is the GraphQL input APIKeyOrder, an order of connections to
// APIKey.
type APIKeyOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field APIKeyOrderField
}

// APIKeyOrderField is the GraphQL enum APIKeyOrderField, the fields connections
// to APIKey can be ordered by.
type APIKeyOrderField string

// The values of APIKeyOrderField.
const (
	// APIKeyOrderFieldName orders by the field name.
	APIKeyOrderFieldName APIKeyOrderField = "NAME"
)

// IsValid reports whether o is a value of APIKeyOrderField.
func (o APIKeyOrderField) IsValid() bool { return aPIKeyType.IsOrderField(string(o)) }

// String returns o's value.
func (o APIKeyOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum APIKeyOrderField.
func (o APIKeyOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum APIKeyOrderField.
func (o *APIKeyOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &aPIKeyType)
}

// APIKeyWhereInput is the GraphQL input APIKeyWhereInput, a filter of APIKey
// nodes: a node matches when every predicate given holds.
type APIKeyWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *APIKeyWhereInput
	And []*APIKeyWhereInput
	Or  []*APIKeyWhereInput

	// Predicates on the ID.
	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64

	// Predicates on the field name.
	Name             *string
	NameNEQ          *string
	NameIn           []string
	NameNotIn        []string
	NameContains     *string
	NameHasPrefix    *string
	NameHasSuffix    *string
	NameEqualFold    *string
	NameContainsFold *string
}

// Pred is the condition w stands for on APIKey nodes, true for a nil or empty
// w. It fails on a filter over the limits of veloxgql.LimitsFromContext(ctx).
func (w *APIKeyWhereInput) Pred(ctx context.Context) (velox.Pred[APIKey], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[APIKey](p), err
}

// Predicate is Pred, untyped.
func (w *APIKeyWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &aPIKeyType, w)
}

// CreateAPIKeyInput is the GraphQL input CreateAPIKeyInput. A nil pointer or
// list is a field not given, and so is a null.
type CreateAPIKeyInput struct {
	Name string
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateAPIKeyInput) Setters() ([]velox.Setter[APIKey], error) {
	var set []velox.Setter[APIKey]
	set = append(set, APIKeys.Name.Set(in.Name))
	return gqlgraph.Validate("CreateAPIKeyInput", &aPIKeyType, set)
}

// UpdateAPIKeyInput is the GraphQL input UpdateAPIKeyInput. A nil pointer or
// list is a field not given, and so is a null.
type UpdateAPIKeyInput struct {
	Name *string
}

// Setters returns the input's fields and edges as setters for
// db.Update[APIKey]().SetInput. A field not given sets nothing.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateAPIKeyInput) Setters() ([]velox.Setter[APIKey], error) {
	var set []velox.Setter[APIKey]
	if v := in.Name; v != nil {
		set = append(set, APIKeys.Name.Set(*v))
	}
	return gqlgraph.Validate("UpdateAPIKeyInput", &aPIKeyType, set)
}
