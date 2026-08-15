package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// SalesOrder holds the schema definition for the SalesOrder entity.
type SalesOrder struct {
	velox.Schema
}

// Mixin of the SalesOrder.
func (SalesOrder) Mixin() []velox.Mixin {
	return []velox.Mixin{TenantMixin{}}
}

// Fields of the SalesOrder.
func (SalesOrder) Fields() []velox.Field {
	return []velox.Field{
		field.String("reference").NotEmpty(),
		field.Int("customer_id"),
		field.Bool("active").Default(true),
	}
}

// Edges of the SalesOrder.
func (SalesOrder) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("customer", Customer.Type).
			Ref("orders").
			Unique().
			Field("customer_id"),
	}
}
