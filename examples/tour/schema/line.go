package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// Line is one item of an Invoice. It carries the tenant too, so a line is
// scoped on its own, without a join to its invoice.
type Line struct{ schema.Schema }

// Fields returns the columns after the ID.
func (Line) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("org_id").Tenant(),
		field.Int64("invoice_id"),
		field.String("description"),
		field.Int64("amount_cents"),
	}
}

// Edges returns Invoice, the inverse of Invoice.Lines, which InvoiceID holds
// the key of.
func (Line) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("invoice", Invoice.Type).Ref("lines").Unique().Required().Field("invoice_id"),
	}
}
