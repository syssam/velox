package testmodel

import "github.com/syssam/velox2"

// Item is a line of an Invoice. Invoice.Items finds it by the convention
// "<Owner>ID" (InvoiceID); Item.Invoice belongs to the Invoice whose key
// InvoiceID holds, by the convention "<Relation>ID".
type Item struct {
	ID        int64
	InvoiceID int64
	SKU       string
	Qty       int64
	Invoice   velox.One[Invoice]
}

// Schema names Item's table.
func (x *Item) Schema(s *velox.Schema) { s.Table("items") }
