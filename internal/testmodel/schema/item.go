package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
)

// Item is a line of an Invoice: Invoice.Items finds it by its InvoiceID, and
// Item.Invoice belongs to the Invoice whose key InvoiceID holds.
type Item struct{ schema.Schema }

// Fields returns the columns after the ID. The field is named SKU, not sku,
// because the generator writes the Go name sku as Sku.
func (Item) Fields() []schema.Field {
	return []schema.Field{
		field.Int64("invoice_id"),
		field.String("SKU").StorageKey("sku"),
		field.Int64("qty"),
	}
}

// Edges returns Invoice, which InvoiceID holds the key of.
func (Item) Edges() []schema.Edge {
	return []schema.Edge{
		edge.From("invoice", Invoice.Type).Ref("items").Unique().Required().Field("invoice_id"),
	}
}
