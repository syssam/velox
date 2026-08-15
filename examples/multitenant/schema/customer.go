package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Customer holds the schema definition for the Customer entity.
type Customer struct {
	velox.Schema
}

// Mixin of the Customer.
func (Customer) Mixin() []velox.Mixin {
	return []velox.Mixin{TenantMixin{}}
}

// Fields of the Customer.
func (Customer) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty(),
	}
}

// Edges of the Customer.
func (Customer) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("orders", SalesOrder.Type),
	}
}
