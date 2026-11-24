package gen

import (
	"context"
	"io"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	_ "github.com/syssam/velox/contrib/graphql/veloxgql/gqlgenx"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// Devices is the edge devices: a connection to the Device nodes it leads to.
// Its queries batch with the same edge of every row on s's page.
func (s *SLADefinition) Devices(
	ctx context.Context,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *DeviceOrder, where *DeviceWhereInput,
) (*DeviceConnection, error) {
	return newDeviceConnection(ctx, sLADefinitionType.Edge(s, slaDefinitionEdgeDevices), after, first, before, last, orderBy, where)
}

// SLADefinitionConnection is the GraphQL type SLADefinitionConnection. Its
// fields run their queries when gqlgen resolves them.
type SLADefinitionConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *SLADefinitionConnection) Edges(ctx context.Context) ([]*SLADefinitionEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]SLADefinitionEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*SLADefinitionEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = SLADefinitionEdge{Node: p.Node(i).(*SLADefinition), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *SLADefinitionConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of SLADefinition nodes in the whole connection, not
// only on this page.
func (c *SLADefinitionConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// SLADefinitionEdge is the GraphQL type SLADefinitionEdge.
type SLADefinitionEdge struct {
	Node   *SLADefinition
	Cursor veloxgql.Cursor
}

// SLADefinitionPaginateOption is an option of a query's Paginate to
// SLADefinition nodes: WithSLADefinitionOrder or WithSLADefinitionFilter. Of
// two options that set the same thing, the later wins.
type SLADefinitionPaginateOption struct {
	orderBy *SLADefinitionOrder
	where   *SLADefinitionWhereInput
}

// WithSLADefinitionOrder orders the connection by orderBy and then the key.
func WithSLADefinitionOrder(orderBy *SLADefinitionOrder) SLADefinitionPaginateOption {
	return SLADefinitionPaginateOption{orderBy: orderBy}
}

// WithSLADefinitionFilter filters the connection by where.
func WithSLADefinitionFilter(where *SLADefinitionWhereInput) SLADefinitionPaginateOption {
	return SLADefinitionPaginateOption{where: where}
}

// PaginateQuery is the connection to the SLADefinition nodes q returns,
// filtered by where, ordered by orderBy and then the key, and paged by the
// other arguments: velox.Query's Paginate. q must not be ordered, and must
// select the key and the order columns.
func (*SLADefinition) PaginateQuery(
	ctx context.Context, q velox.Query[SLADefinition],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []SLADefinitionPaginateOption,
) (*SLADefinitionConnection, error) {
	var o SLADefinitionPaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
		if x.where != nil {
			o.where = x.where
		}
	}
	return newSLADefinitionConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy, o.where)
}

// newSLADefinitionConnection is PaginateQuery of the nodes from leads to: a
// query's, or an edge's, whose queries batch.
func newSLADefinitionConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *SLADefinitionOrder, where *SLADefinitionWhereInput,
) (*SLADefinitionConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*SLADefinitionWhereInput).Predicate)}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &sLADefinitionType, args)
	if err != nil {
		return nil, err
	}
	return &SLADefinitionConnection{Conn: c}, nil
}

// sLADefinitionType describes the GraphQL type SLADefinition to gqlgraph.
var sLADefinitionType = gqlgraph.Type{
	Name:  "SLADefinition",
	Table: untyped.TableOf[SLADefinition],
	Key:   []gqlgraph.Column{{Name: "id", Index: slaDefinitionColID}},
	Order: []gqlgraph.Column{
		{Name: "NAME", Index: slaDefinitionColName},
	},
	AlwaysRead: []int{slaDefinitionColID},
	Fields:     []string{slaDefinitionColID: "id", slaDefinitionColName: "name"},
	Edges:      []string{slaDefinitionEdgeDevices: "devices"},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality, Col: slaDefinitionColID},
		{Name: "name", Ops: gqlgraph.OpsEquality | gqlgraph.OpsText, Col: slaDefinitionColName},
	},
	Inputs:         []string{slaDefinitionColName: "name"},
	LenientFilters: true,
}

// SLADefinitionOrder is the GraphQL input SLADefinitionOrder, an order of
// connections to SLADefinition.
type SLADefinitionOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field SLADefinitionOrderField
}

// SLADefinitionOrderField is the GraphQL enum SLADefinitionOrderField, the
// fields connections to SLADefinition can be ordered by.
type SLADefinitionOrderField string

// The values of SLADefinitionOrderField.
const (
	// SLADefinitionOrderFieldName orders by the field name.
	SLADefinitionOrderFieldName SLADefinitionOrderField = "NAME"
)

// IsValid reports whether o is a value of SLADefinitionOrderField.
func (o SLADefinitionOrderField) IsValid() bool { return sLADefinitionType.IsOrderField(string(o)) }

// String returns o's value.
func (o SLADefinitionOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum SLADefinitionOrderField.
func (o SLADefinitionOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum SLADefinitionOrderField.
func (o *SLADefinitionOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &sLADefinitionType)
}

// SLADefinitionWhereInput is the GraphQL input SLADefinitionWhereInput, a
// filter of SLADefinition nodes: a node matches when every predicate given
// holds.
type SLADefinitionWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *SLADefinitionWhereInput
	And []*SLADefinitionWhereInput
	Or  []*SLADefinitionWhereInput

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

// Pred is the condition w stands for on SLADefinition nodes, true for a nil or
// empty w. It fails on a filter over the limits of
// veloxgql.LimitsFromContext(ctx).
func (w *SLADefinitionWhereInput) Pred(ctx context.Context) (velox.Pred[SLADefinition], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[SLADefinition](p), err
}

// Predicate is Pred, untyped.
func (w *SLADefinitionWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &sLADefinitionType, w)
}

// CreateSLADefinitionInput is the GraphQL input CreateSLADefinitionInput. A nil
// pointer or list is a field not given, and so is a null.
type CreateSLADefinitionInput struct {
	Name string
	// DeviceIDs are the IDs of the Device nodes the edge devices leads to.
	DeviceIDs []int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateSLADefinitionInput) Setters() ([]velox.Setter[SLADefinition], error) {
	var set []velox.Setter[SLADefinition]
	set = append(set, SLADefinitions.Name.Set(in.Name))
	if len(in.DeviceIDs) > 0 {
		deviceIDs, err := gqlgraph.IDs("CreateSLADefinitionInput.deviceIDs", in.DeviceIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, SLADefinitions.Devices.Add(deviceIDs...))
	}
	return gqlgraph.Validate("CreateSLADefinitionInput", &sLADefinitionType, set)
}

// UpdateSLADefinitionInput is the GraphQL input UpdateSLADefinitionInput. A nil
// pointer or list is a field not given, and so is a null. A true clear field
// clears its field or edge.
type UpdateSLADefinitionInput struct {
	Name *string
	// AddDeviceIDs are the IDs of Device nodes to add to the edge devices.
	AddDeviceIDs []int64
	// RemoveDeviceIDs are the IDs of Device nodes to remove from the edge
	// devices; an ID the edge does not lead to is ignored.
	RemoveDeviceIDs []int64
	// ClearDevices removes every Device from the edge devices, before the IDs
	// to remove and to add.
	ClearDevices bool
}

// Setters returns the input's fields and edges as setters for
// db.Update[SLADefinition]().SetInput. A field not given sets nothing, and a
// clear field of an edge with a list of IDs clears it before they are added.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateSLADefinitionInput) Setters() ([]velox.Setter[SLADefinition], error) {
	var set []velox.Setter[SLADefinition]
	if v := in.Name; v != nil {
		set = append(set, SLADefinitions.Name.Set(*v))
	}
	if in.ClearDevices {
		set = append(set, SLADefinitions.Devices.Clear())
	}
	if len(in.RemoveDeviceIDs) > 0 {
		removeDeviceIDs, err := gqlgraph.IDs("UpdateSLADefinitionInput.removeDeviceIDs", in.RemoveDeviceIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, SLADefinitions.Devices.Remove(removeDeviceIDs...))
	}
	if len(in.AddDeviceIDs) > 0 {
		addDeviceIDs, err := gqlgraph.IDs("UpdateSLADefinitionInput.addDeviceIDs", in.AddDeviceIDs)
		if err != nil {
			return nil, err
		}
		set = append(set, SLADefinitions.Devices.Add(addDeviceIDs...))
	}
	return gqlgraph.Validate("UpdateSLADefinitionInput", &sLADefinitionType, set)
}
