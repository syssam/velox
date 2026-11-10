package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Line is one item of an Invoice. It carries the organization too, so a
// line is scoped on its own, without a join to its invoice.
type Line struct{ schema.Schema }

// Mixin adds org_id, which InOrg confines.
func (Line) Mixin() []schema.Mixin { return []schema.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Line) Fields() []schema.Field {
	return []schema.Field{
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
