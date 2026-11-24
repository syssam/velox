package gen

import (
	"context"
	"io"

	"example.com/app/schema"
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// SalesOrderEntryStatus is SalesOrderStatus, under the name graphql.Type gives the GraphQL enum.
type SalesOrderEntryStatus = SalesOrderStatus

// MarshalGQL writes e as its value of the GraphQL enum SalesOrderEntryStatus.
func (e SalesOrderStatus) MarshalGQL(w io.Writer) {
	switch e {
	case SalesOrderStatusOpen:
		_, _ = io.WriteString(w, `"OPEN"`)
	case SalesOrderStatusShipped:
		_, _ = io.WriteString(w, `"SHIPPED"`)
	default: // not a value of the enum
		gqlgraph.WriteString(w, string(e))
	}
}

// UnmarshalGQL sets e from v, a value of the GraphQL enum SalesOrderEntryStatus.
func (e *SalesOrderStatus) UnmarshalGQL(v any) error {
	switch v {
	case "OPEN":
		*e = SalesOrderStatusOpen
	case "SHIPPED":
		*e = SalesOrderStatusShipped
	default:
		return gqlgraph.InputErrorf("SalesOrderStatus must be OPEN or SHIPPED, not %v", v)
	}
	return nil
}

// Customer is the edge customer: the Customer it leads to, or nil. It is read
// in one query with the same edge of every row on s's page.
func (s *SalesOrder) Customer(ctx context.Context) (*Customer, error) {
	v, err := gqlgraph.EdgeOne(ctx, &customerType, salesOrderType.Edge(s, salesOrderEdgeCustomer))
	n, _ := v.(*Customer)
	return n, err
}

// SalesOrderConnection is the GraphQL type SalesOrderConnection. Its fields run
// their queries when they are resolved.
type SalesOrderConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *SalesOrderConnection) Edges(ctx context.Context) ([]*SalesOrderEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]SalesOrderEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*SalesOrderEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = SalesOrderEdge{Node: p.Node(i).(*SalesOrder), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *SalesOrderConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of SalesOrder nodes in the whole connection, not
// only on this page.
func (c *SalesOrderConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// SalesOrderEdge is the GraphQL type SalesOrderEdge.
type SalesOrderEdge struct {
	Node   *SalesOrder
	Cursor veloxgql.Cursor
}

// SalesOrderPaginateOption is an option of a query's Paginate to SalesOrder
// nodes: WithSalesOrderOrder or WithSalesOrderFilter. Of two options that set
// the same thing, the later wins.
type SalesOrderPaginateOption struct {
	orderBy *SalesOrderOrder
	where   *SalesOrderWhereInput
}

// WithSalesOrderOrder orders the connection by orderBy and then the key.
func WithSalesOrderOrder(orderBy *SalesOrderOrder) SalesOrderPaginateOption {
	return SalesOrderPaginateOption{orderBy: orderBy}
}

// WithSalesOrderFilter filters the connection by where.
func WithSalesOrderFilter(where *SalesOrderWhereInput) SalesOrderPaginateOption {
	return SalesOrderPaginateOption{where: where}
}

// PaginateQuery is the connection to the SalesOrder nodes q returns, filtered
// by where, ordered by orderBy and then the key, and paged by the other
// arguments: velox.Query's Paginate. q must not be ordered, and must select the
// key and the order columns.
func (*SalesOrder) PaginateQuery(
	ctx context.Context, q velox.Query[SalesOrder],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []SalesOrderPaginateOption,
) (*SalesOrderConnection, error) {
	var o SalesOrderPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
		if x.where != nil {
			o.where = x.where
		}
	}
	return newSalesOrderConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy, o.where)
}

// newSalesOrderConnection is PaginateQuery of the nodes from leads to: a
// query's, or an edge's, whose queries batch.
func newSalesOrderConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *SalesOrderOrder, where *SalesOrderWhereInput,
) (*SalesOrderConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*SalesOrderWhereInput).Predicate)}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &salesOrderType, args)
	if err != nil {
		return nil, err
	}
	return &SalesOrderConnection{Conn: c}, nil
}

// salesOrderType describes the GraphQL type SalesOrder to gqlgraph.
var salesOrderType = gqlgraph.Type{
	Name:  "SalesOrder",
	Table: untyped.TableOf[SalesOrder],
	Key:   []gqlgraph.Column{{Name: "id", Index: salesOrderColID}},
	Order: []gqlgraph.Column{
		{Name: "ORGANIZATION_ID", Index: salesOrderColOrganizationID},
		{Name: "CUSTOMER_ID", Index: salesOrderColCustomerID},
		{Name: "IMAGE_URL", Index: salesOrderColImageURL},
		{Name: "REFERRER_ID", Index: salesOrderColReferrerID},
		{Name: "STATUS", Index: salesOrderColStatus},
		{Name: "NOTE", Index: salesOrderColNote},
	},
	AlwaysRead: []int{
		salesOrderColID,
		salesOrderColCustomerID,
		salesOrderColReferrerID,
	},
	Fields: []string{
		salesOrderColID:             "id",
		salesOrderColOrganizationID: "organizationId",
		salesOrderColCustomerID:     "customerId",
		salesOrderColImageURL:       "imageUrl",
		salesOrderColReferrerID:     "referrerId",
		salesOrderColTotal:          "total",
		salesOrderColCustomFields:   "customFields",
		salesOrderColStatus:         "status",
		salesOrderColNote:           "note",
	},
	Edges: []string{salesOrderEdgeCustomer: "customer"},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality, Col: salesOrderColID},
		{Name: "hasCustomer", Kind: gqlgraph.WhereHas, Edge: salesOrderEdgeCustomer, Target: untyped.TableOf[Customer]},
	},
	Inputs: []string{
		salesOrderColOrganizationID: "organizationId",
		salesOrderColImageURL:       "imageUrl",
		salesOrderColTotal:          "total",
		salesOrderColCustomFields:   "customFields",
		salesOrderColStatus:         "status",
		salesOrderColNote:           "note",
		salesOrderColCustomerID:     "customerId",
	},
	LenientFilters: true,
}

// SalesOrderOrder is the GraphQL input SalesOrderOrder, an order of connections
// to SalesOrder.
type SalesOrderOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field SalesOrderOrderField
}

// SalesOrderOrderField is the GraphQL enum SalesOrderOrderField, the fields
// connections to SalesOrder can be ordered by.
type SalesOrderOrderField string

// The values of SalesOrderOrderField.
const (
	// SalesOrderOrderFieldOrganizationID orders by the field organization_id.
	SalesOrderOrderFieldOrganizationID SalesOrderOrderField = "ORGANIZATION_ID"
	// SalesOrderOrderFieldCustomerID orders by the field customer_id.
	SalesOrderOrderFieldCustomerID SalesOrderOrderField = "CUSTOMER_ID"
	// SalesOrderOrderFieldImageURL orders by the field image_url.
	SalesOrderOrderFieldImageURL SalesOrderOrderField = "IMAGE_URL"
	// SalesOrderOrderFieldReferrerID orders by the field referrer_id.
	SalesOrderOrderFieldReferrerID SalesOrderOrderField = "REFERRER_ID"
	// SalesOrderOrderFieldStatus orders by the field status.
	SalesOrderOrderFieldStatus SalesOrderOrderField = "STATUS"
	// SalesOrderOrderFieldNote orders by the field note.
	SalesOrderOrderFieldNote SalesOrderOrderField = "NOTE"
)

// IsValid reports whether o is a value of SalesOrderOrderField.
func (o SalesOrderOrderField) IsValid() bool { return salesOrderType.IsOrderField(string(o)) }

// String returns o's value.
func (o SalesOrderOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum SalesOrderOrderField.
func (o SalesOrderOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum SalesOrderOrderField.
func (o *SalesOrderOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &salesOrderType)
}

// SalesOrderWhereInput is the GraphQL input SalesOrderWhereInput, a filter of
// SalesOrder nodes: a node matches when every predicate given holds.
type SalesOrderWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *SalesOrderWhereInput
	And []*SalesOrderWhereInput
	Or  []*SalesOrderWhereInput

	// Predicates on the ID.
	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64

	// The edge customer: whether it has a node, or one that matches.
	HasCustomer *bool
}

// Pred is the condition w stands for on SalesOrder nodes, true for a nil or
// empty w. It fails on a filter over the limits of
// veloxgql.LimitsFromContext(ctx).
func (w *SalesOrderWhereInput) Pred(ctx context.Context) (velox.Pred[SalesOrder], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[SalesOrder](p), err
}

// Predicate is Pred, untyped.
func (w *SalesOrderWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &salesOrderType, w)
}

// CreateSalesOrderInput is the GraphQL input CreateSalesOrderInput. A nil
// pointer or list is a field not given, and so is a null.
type CreateSalesOrderInput struct {
	OrganizationID int64
	ImageURL       string
	Total          schema.Amount
	CustomFields   map[string]any
	Status         SalesOrderStatus
	Note           *string
	// CustomerID is the ID of the Customer the edge customer leads to.
	CustomerID int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateSalesOrderInput) Setters() ([]velox.Setter[SalesOrder], error) {
	var set []velox.Setter[SalesOrder]
	set = append(set, SalesOrders.OrganizationID.Set(in.OrganizationID))
	set = append(set, SalesOrders.ImageURL.Set(in.ImageURL))
	set = append(set, SalesOrders.Total.Set(in.Total))
	set = append(set, SalesOrders.CustomFields.Set(in.CustomFields))
	set = append(set, SalesOrders.Status.Set(in.Status))
	if v := in.Note; v != nil {
		set = append(set, SalesOrders.Note.Set(*v))
	}
	set = append(set, SalesOrders.Customer.Set(in.CustomerID))
	return gqlgraph.Validate("CreateSalesOrderInput", &salesOrderType, set)
}
