package gen

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/contrib/graphql/veloxgql/gqlgraph"
	"github.com/syssam/velox/untyped"
)

const _ = veloxgql.GeneratedCodeIsVersion31

// UsingPredicateGQL is the field using_predicate as JSON, the GraphQL scalar it
// is: gqlgen binds the field to it.
func (d *Device) UsingPredicateGQL() (veloxgql.JSON, error) {
	b, err := json.Marshal(d.UsingPredicate)
	return veloxgql.JSON(b), err
}

// APIKey is the edge api_key: the APIKey it leads to, or nil. It is read in one
// query with the same edge of every row on d's page.
func (d *Device) APIKey(ctx context.Context) (*APIKey, error) {
	v, err := gqlgraph.EdgeOne(ctx, &aPIKeyType, deviceType.Edge(d, deviceEdgeAPIKey))
	n, _ := v.(*APIKey)
	return n, err
}

// SLADefinition is the edge sla_definition: the SLADefinition it leads to, or
// nil. It is read in one query with the same edge of every row on d's page.
func (d *Device) SLADefinition(ctx context.Context) (*SLADefinition, error) {
	if d.SLADefinitionID == nil {
		return nil, nil
	}
	v, err := gqlgraph.EdgeOne(ctx, &sLADefinitionType, deviceType.Edge(d, deviceEdgeSLADefinition))
	n, _ := v.(*SLADefinition)
	return n, err
}

// DeviceConnection is the GraphQL type DeviceConnection. Its fields run their
// queries when gqlgen resolves them.
type DeviceConnection struct {
	// Conn reads the page and the count. gqlgraph.ConnectionFromRows makes one
	// of rows given, with no query, for a test's fake of a service.
	Conn *gqlgraph.Connection
}

// Edges are the edges of the page, in the connection's order.
func (c *DeviceConnection) Edges(ctx context.Context) ([]*DeviceEdge, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	slab := make([]DeviceEdge, len(p.Rows)) // one allocation for all the edges
	edges := make([]*DeviceEdge, len(p.Rows))
	for i := range p.Rows {
		slab[i] = DeviceEdge{Node: p.Node(i).(*Device), Cursor: p.Cursors[i]}
		edges[i] = &slab[i]
	}
	return edges, nil
}

// PageInfo is the page info of the page.
func (c *DeviceConnection) PageInfo(ctx context.Context) (*veloxgql.PageInfo, error) {
	p, err := c.Conn.Page(ctx)
	if err != nil {
		return nil, err
	}
	return &p.Info, nil
}

// TotalCount is the number of Device nodes in the whole connection, not only on
// this page.
func (c *DeviceConnection) TotalCount(ctx context.Context) (int, error) {
	return c.Conn.TotalCount(ctx)
}

// DeviceEdge is the GraphQL type DeviceEdge.
type DeviceEdge struct {
	Node   *Device
	Cursor veloxgql.Cursor
}

// DevicePaginateOption is an option of a query's Paginate to Device nodes:
// WithDeviceOrder or WithDeviceFilter. Of two options that set the same thing,
// the later wins.
type DevicePaginateOption struct {
	orderBy *DeviceOrder
	where   *DeviceWhereInput
}

// WithDeviceOrder orders the connection by orderBy and then the key.
func WithDeviceOrder(orderBy *DeviceOrder) DevicePaginateOption {
	return DevicePaginateOption{orderBy: orderBy}
}

// WithDeviceFilter filters the connection by where.
func WithDeviceFilter(where *DeviceWhereInput) DevicePaginateOption {
	return DevicePaginateOption{where: where}
}

// PaginateQuery is the connection to the Device nodes q returns, filtered by
// where, ordered by orderBy and then the key, and paged by the other arguments:
// velox.Query's Paginate. q must not be ordered, and must select the key and
// the order columns.
func (*Device) PaginateQuery(
	ctx context.Context, q velox.Query[Device],
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	opts []DevicePaginateOption,
) (*DeviceConnection, error) {
	var o DevicePaginateOption
	for _, x := range opts {
		if x.orderBy != nil {
			o.orderBy = x.orderBy
		}
		if x.where != nil {
			o.where = x.where
		}
	}
	return newDeviceConnection(ctx, gqlgraph.Query(untyped.FromQuery(q)), after, first, before, last, o.orderBy, o.where)
}

// newDeviceConnection is PaginateQuery of the nodes from leads to: a query's,
// or an edge's, whose queries batch.
func newDeviceConnection(
	ctx context.Context, from gqlgraph.Source,
	after *veloxgql.Cursor, first *int, before *veloxgql.Cursor, last *int,
	orderBy *DeviceOrder, where *DeviceWhereInput,
) (*DeviceConnection, error) {
	args := gqlgraph.Args{After: after, First: first, Before: before, Last: last, Where: gqlgraph.WhereOf(where, (*DeviceWhereInput).Predicate)}
	if o := orderBy; o != nil {
		args.Order = []gqlgraph.Order{{Field: string(o.Field), Direction: o.Direction}}
	}
	c, err := gqlgraph.Paginate(ctx, from, &deviceType, args)
	if err != nil {
		return nil, err
	}
	return &DeviceConnection{Conn: c}, nil
}

// deviceType describes the GraphQL type Device to gqlgraph.
var deviceType = gqlgraph.Type{
	Name:  "Device",
	Table: untyped.TableOf[Device],
	Key:   []gqlgraph.Column{{Name: "id", Index: deviceColID}},
	Order: []gqlgraph.Column{
		{Name: "IP_ADDRESS", Index: deviceColIPAddress},
		{Name: "API_NAME", Index: deviceColAPIName},
		{Name: "HAS_API_ACCESS", Index: deviceColHasAPIAccess},
		{Name: "DNS_RECORD_NAME", Index: deviceColDNSRecordName},
		{Name: "SLA_BREACH_AT", Index: deviceColSLABreachAt},
		{Name: "API_KEY_ID", Index: deviceColAPIKeyID},
		{Name: "SLA_DEFINITION_ID", Index: deviceColSLADefinitionID},
	},
	AlwaysRead: []int{deviceColID, deviceColAPIKeyID, deviceColSLADefinitionID},
	Fields: []string{
		deviceColID:              "id",
		deviceColIPAddress:       "ipAddress",
		deviceColAPIName:         "apiName",
		deviceColHasAPIAccess:    "hasApiAccess",
		deviceColDNSRecordName:   "dnsRecordName",
		deviceColSLABreachAt:     "slaBreachAt",
		deviceColUsingPredicate:  "usingPredicate",
		deviceColPayload:         "payload",
		deviceColAPIKeyID:        "apiKeyId",
		deviceColSLADefinitionID: "slaDefinitionId",
	},
	Edges: []string{
		deviceEdgeAPIKey:        "apiKey",
		deviceEdgeSLADefinition: "slaDefinition",
	},
	WhereFields: []gqlgraph.WhereField{
		{Name: "not", Kind: gqlgraph.WhereNot},
		{Name: "and", Kind: gqlgraph.WhereAnd},
		{Name: "or", Kind: gqlgraph.WhereOr},
		{Name: "id", Kind: gqlgraph.WhereID, Ops: gqlgraph.OpsEquality, Col: deviceColID},
		{Name: "ipAddress", Ops: gqlgraph.OpsEquality | gqlgraph.OpsText, Col: deviceColIPAddress},
		{Name: "apiName", Ops: gqlgraph.OpsEquality | gqlgraph.OpsText, Col: deviceColAPIName},
		{Name: "hasApiAccess", Ops: gqlgraph.OpEQ | gqlgraph.OpNEQ, Col: deviceColHasAPIAccess},
		{Name: "dnsRecordName", Ops: gqlgraph.OpsEquality | gqlgraph.OpsText | gqlgraph.OpsNull, Col: deviceColDNSRecordName},
		{Name: "slaBreachAt", Ops: gqlgraph.OpsEquality | gqlgraph.OpsOrder | gqlgraph.OpsNull, Col: deviceColSLABreachAt},
		{Name: "apiKeyId", Kind: gqlgraph.WhereEdgeID, Ops: gqlgraph.OpsEquality, Col: deviceColAPIKeyID, Edge: deviceEdgeAPIKey, Target: untyped.TableOf[APIKey]},
		{Name: "slaDefinitionId", Kind: gqlgraph.WhereEdgeID, Ops: gqlgraph.OpsEquality | gqlgraph.OpsNull, Col: deviceColSLADefinitionID, Edge: deviceEdgeSLADefinition, Target: untyped.TableOf[SLADefinition]},
		{Name: "hasApiKey", Kind: gqlgraph.WhereHas, Edge: deviceEdgeAPIKey, Target: untyped.TableOf[APIKey]},
		{Name: "hasApiKeyWith", Kind: gqlgraph.WhereHasWith, Edge: deviceEdgeAPIKey, Target: untyped.TableOf[APIKey]},
	},
	Inputs: []string{
		deviceColIPAddress:       "ipAddress",
		deviceColAPIName:         "apiName",
		deviceColHasAPIAccess:    "hasApiAccess",
		deviceColDNSRecordName:   "dnsRecordName",
		deviceColSLABreachAt:     "slaBreachAt",
		deviceColUsingPredicate:  "usingPredicate",
		deviceColPayload:         "payload",
		deviceColAPIKeyID:        "apiKeyId",
		deviceColSLADefinitionID: "slaDefinitionId",
	},
	LenientFilters: true,
}

// DeviceOrder is the GraphQL input DeviceOrder, an order of connections to
// Device.
type DeviceOrder struct {
	// Direction is the direction of the order, ASC unless the input says.
	Direction veloxgql.OrderDirection
	// Field is the field to order by.
	Field DeviceOrderField
}

// DeviceOrderField is the GraphQL enum DeviceOrderField, the fields connections
// to Device can be ordered by.
type DeviceOrderField string

// The values of DeviceOrderField.
const (
	// DeviceOrderFieldIPAddress orders by the field ip_address.
	DeviceOrderFieldIPAddress DeviceOrderField = "IP_ADDRESS"
	// DeviceOrderFieldAPIName orders by the field api_name.
	DeviceOrderFieldAPIName DeviceOrderField = "API_NAME"
	// DeviceOrderFieldHasAPIAccess orders by the field has_api_access.
	DeviceOrderFieldHasAPIAccess DeviceOrderField = "HAS_API_ACCESS"
	// DeviceOrderFieldDNSRecordName orders by the field dns_record_name.
	DeviceOrderFieldDNSRecordName DeviceOrderField = "DNS_RECORD_NAME"
	// DeviceOrderFieldSLABreachAt orders by the field sla_breach_at.
	DeviceOrderFieldSLABreachAt DeviceOrderField = "SLA_BREACH_AT"
	// DeviceOrderFieldAPIKeyID orders by the field api_key_id.
	DeviceOrderFieldAPIKeyID DeviceOrderField = "API_KEY_ID"
	// DeviceOrderFieldSLADefinitionID orders by the field sla_definition_id.
	DeviceOrderFieldSLADefinitionID DeviceOrderField = "SLA_DEFINITION_ID"
)

// IsValid reports whether o is a value of DeviceOrderField.
func (o DeviceOrderField) IsValid() bool { return deviceType.IsOrderField(string(o)) }

// String returns o's value.
func (o DeviceOrderField) String() string { return string(o) }

// MarshalGQL writes o as a value of the GraphQL enum DeviceOrderField.
func (o DeviceOrderField) MarshalGQL(w io.Writer) { gqlgraph.WriteString(w, string(o)) }

// UnmarshalGQL sets o from v, a value of the GraphQL enum DeviceOrderField.
func (o *DeviceOrderField) UnmarshalGQL(v any) error {
	return gqlgraph.UnmarshalOrderField(o, v, &deviceType)
}

// DeviceWhereInput is the GraphQL input DeviceWhereInput, a filter of Device
// nodes: a node matches when every predicate given holds.
type DeviceWhereInput struct {
	// Not, And and Or combine filters: a node matches when it does not match
	// Not, matches each of And, and matches one of Or.
	Not *DeviceWhereInput
	And []*DeviceWhereInput
	Or  []*DeviceWhereInput

	// Predicates on the ID.
	ID      *int64
	IDNEQ   *int64
	IDIn    []int64
	IDNotIn []int64

	// Predicates on the field ip_address (ipAddress).
	IPAddress             *string
	IPAddressNEQ          *string
	IPAddressIn           []string
	IPAddressNotIn        []string
	IPAddressContains     *string
	IPAddressHasPrefix    *string
	IPAddressHasSuffix    *string
	IPAddressEqualFold    *string
	IPAddressContainsFold *string

	// Predicates on the field api_name (apiName).
	APIName             *string
	APINameNEQ          *string
	APINameIn           []string
	APINameNotIn        []string
	APINameContains     *string
	APINameHasPrefix    *string
	APINameHasSuffix    *string
	APINameEqualFold    *string
	APINameContainsFold *string

	// Predicates on the field has_api_access (hasApiAccess).
	HasAPIAccess    *bool
	HasAPIAccessNEQ *bool

	// Predicates on the field dns_record_name (dnsRecordName).
	DNSRecordName             *string
	DNSRecordNameNEQ          *string
	DNSRecordNameIn           []string
	DNSRecordNameNotIn        []string
	DNSRecordNameContains     *string
	DNSRecordNameHasPrefix    *string
	DNSRecordNameHasSuffix    *string
	DNSRecordNameEqualFold    *string
	DNSRecordNameContainsFold *string
	DNSRecordNameIsNil        *bool
	DNSRecordNameNotNil       *bool

	// Predicates on the field sla_breach_at (slaBreachAt).
	SLABreachAt       *time.Time
	SLABreachAtNEQ    *time.Time
	SLABreachAtIn     []time.Time
	SLABreachAtNotIn  []time.Time
	SLABreachAtGT     *time.Time
	SLABreachAtGTE    *time.Time
	SLABreachAtLT     *time.Time
	SLABreachAtLTE    *time.Time
	SLABreachAtIsNil  *bool
	SLABreachAtNotNil *bool

	// Predicates on the edge api_key (apiKeyId), by the ID of its node.
	APIKeyID      *int64
	APIKeyIDNEQ   *int64
	APIKeyIDIn    []int64
	APIKeyIDNotIn []int64

	// Predicates on the edge sla_definition (slaDefinitionId), by the ID of its
	// node.
	SLADefinitionID       *int64
	SLADefinitionIDNEQ    *int64
	SLADefinitionIDIn     []int64
	SLADefinitionIDNotIn  []int64
	SLADefinitionIDIsNil  *bool
	SLADefinitionIDNotNil *bool

	// The edge api_key (apiKey): whether it has a node, or one that matches.
	HasAPIKey     *bool
	HasAPIKeyWith []*APIKeyWhereInput
}

// Pred is the condition w stands for on Device nodes, true for a nil or empty
// w. It fails on a filter over the limits of veloxgql.LimitsFromContext(ctx).
func (w *DeviceWhereInput) Pred(ctx context.Context) (velox.Pred[Device], error) {
	p, err := w.Predicate(ctx)
	return velox.Pred[Device](p), err
}

// Predicate is Pred, untyped.
func (w *DeviceWhereInput) Predicate(ctx context.Context) (velox.Predicate, error) {
	return gqlgraph.Predicate(ctx, &deviceType, w)
}

// CreateDeviceInput is the GraphQL input CreateDeviceInput. A nil pointer or
// list is a field not given, and so is a null.
type CreateDeviceInput struct {
	IPAddress      string
	APIName        string
	HasAPIAccess   bool
	DNSRecordName  *string
	SLABreachAt    *time.Time
	UsingPredicate veloxgql.JSON
	Payload        *[]byte
	// APIKeyID is the ID of the APIKey the edge api_key leads to.
	APIKeyID int64
	// SLADefinitionID is the ID of the SLADefinition the edge sla_definition
	// leads to.
	SLADefinitionID *int64
}

// Setters returns the input's fields and edges as setters for
// db.Insert(x).SetInput. A field not given sets nothing: x keeps its value, or
// the field's Default.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in CreateDeviceInput) Setters() ([]velox.Setter[Device], error) {
	var set []velox.Setter[Device]
	set = append(set, Devices.IPAddress.Set(in.IPAddress))
	set = append(set, Devices.APIName.Set(in.APIName))
	set = append(set, Devices.HasAPIAccess.Set(in.HasAPIAccess))
	if v := in.DNSRecordName; v != nil {
		set = append(set, Devices.DNSRecordName.Set(*v))
	}
	if v := in.SLABreachAt; v != nil {
		set = append(set, Devices.SLABreachAt.Set(*v))
	}
	usingPredicate, err := gqlgraph.FromJSON[map[string]any]("CreateDeviceInput.usingPredicate", in.UsingPredicate)
	if err != nil {
		return nil, err
	}
	set = append(set, Devices.UsingPredicate.Set(usingPredicate))
	if v := in.Payload; v != nil {
		set = append(set, Devices.Payload.Set(*v))
	}
	set = append(set, Devices.APIKey.Set(in.APIKeyID))
	if v := in.SLADefinitionID; v != nil {
		set = append(set, Devices.SLADefinition.Set(*v))
	}
	return gqlgraph.Validate("CreateDeviceInput", &deviceType, set)
}

// UpdateDeviceInput is the GraphQL input UpdateDeviceInput. A nil pointer or
// list is a field not given, and so is a null. A true clear field clears its
// field or edge.
type UpdateDeviceInput struct {
	IPAddress     *string
	APIName       *string
	HasAPIAccess  *bool
	DNSRecordName *string
	// ClearDNSRecordName sets the field dns_record_name to NULL.
	ClearDNSRecordName bool
	SLABreachAt        *time.Time
	// ClearSLABreachAt sets the field sla_breach_at to NULL.
	ClearSLABreachAt bool
	UsingPredicate   *veloxgql.JSON
	Payload          *[]byte
	// ClearPayload sets the field payload to NULL.
	ClearPayload bool
	// APIKeyID is the ID of the APIKey the edge api_key leads to.
	APIKeyID *int64
	// SLADefinitionID is the ID of the SLADefinition the edge sla_definition
	// leads to.
	SLADefinitionID *int64
	// ClearSLADefinition unlinks the edge sla_definition: it leads to no
	// SLADefinition afterwards.
	ClearSLADefinition bool
}

// Setters returns the input's fields and edges as setters for
// db.Update[Device]().SetInput. A field not given sets nothing, and a clear
// field wins over its field.
//
// It does no I/O. A value a validator refuses, or a list of more than
// gqlgraph.MaxIDs IDs, is BAD_USER_INPUT naming the field. See "Mutations" in
// the documentation of package github.com/syssam/velox/contrib/graphql.
func (in UpdateDeviceInput) Setters() ([]velox.Setter[Device], error) {
	var set []velox.Setter[Device]
	if v := in.IPAddress; v != nil {
		set = append(set, Devices.IPAddress.Set(*v))
	}
	if v := in.APIName; v != nil {
		set = append(set, Devices.APIName.Set(*v))
	}
	if v := in.HasAPIAccess; v != nil {
		set = append(set, Devices.HasAPIAccess.Set(*v))
	}
	if in.ClearDNSRecordName {
		set = append(set, Devices.DNSRecordName.SetNull())
	} else if v := in.DNSRecordName; v != nil {
		set = append(set, Devices.DNSRecordName.Set(*v))
	}
	if in.ClearSLABreachAt {
		set = append(set, Devices.SLABreachAt.SetNull())
	} else if v := in.SLABreachAt; v != nil {
		set = append(set, Devices.SLABreachAt.Set(*v))
	}
	if v := in.UsingPredicate; v != nil {
		usingPredicate, err := gqlgraph.FromJSON[map[string]any]("UpdateDeviceInput.usingPredicate", *v)
		if err != nil {
			return nil, err
		}
		set = append(set, Devices.UsingPredicate.Set(usingPredicate))
	}
	if in.ClearPayload {
		set = append(set, Devices.Payload.SetNull())
	} else if v := in.Payload; v != nil {
		set = append(set, Devices.Payload.Set(*v))
	}
	if v := in.APIKeyID; v != nil {
		set = append(set, Devices.APIKey.Set(*v))
	}
	if in.ClearSLADefinition {
		set = append(set, Devices.SLADefinitionID.SetNull())
	} else if v := in.SLADefinitionID; v != nil {
		set = append(set, Devices.SLADefinition.Set(*v))
	}
	return gqlgraph.Validate("UpdateDeviceInput", &deviceType, set)
}
