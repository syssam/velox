package veloxschema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

// Line is one item of an Invoice. It carries the organization too, so a
// line is scoped on its own, without a join to its invoice.
type Line struct{ velox.Schema }

// Mixin adds org_id, which InOrg confines.
func (Line) Mixin() []velox.Mixin { return []velox.Mixin{InOrg{}} }

// Fields returns the columns after the ID and the mixin's.
func (Line) Fields() []velox.Field {
	return []velox.Field{
		field.Int64("invoice_id"),
		field.String("description"),
		field.Int64("amount_cents"),
	}
}

// Edges returns Invoice, the inverse of Invoice.Lines, which InvoiceID holds
// the key of.
func (Line) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("invoice", Invoice.Type).Ref("lines").Unique().Required().Field("invoice_id"),
	}
}
